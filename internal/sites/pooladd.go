// SPDX-License-Identifier: AGPL-3.0-or-later

package sites

import (
	"bufio"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path"
	"strings"

	"golang.org/x/sys/unix"

	"github.com/maki-it/hostashi/internal/agentapi"
)

// Errors of adding and removing pools.
var (
	ErrPoolRefused = errors.New("sites: the directory cannot become a storage pool")
	ErrPoolExists  = errors.New("sites: storage pool already exists")
	ErrPoolInUse   = errors.New("sites: storage pool still has sites")
)

// File system magic numbers of the pools Hostashi supports (ADR-0016).
const (
	magicExt4 = 0xEF53
	magicXFS  = 0x58465342
)

// DefaultFstab is where AddPool looks for the volume's mount entry.
const DefaultFstab = "/etc/fstab"

// systemTrees can never hold a pool: the system itself and the directories
// other services own (ADR-0016 finding 3).
var systemTrees = []string{
	"/bin", "/boot", "/dev", "/etc", "/lib", "/lib32", "/lib64", "/libx32", "/proc",
	"/root", "/run", "/sbin", "/sys", "/tmp", "/usr", "/var",
}

// AddPool registers the mounted volume at dir as pool name (ADR-0016, spike
// S9). Hostashi never formats, partitions or mounts: the admin mounts the
// volume and adds it to /etc/fstab first. dir must be the root of a mount
// of ext4 or XFS with nosuid and nodev, owned by root, and empty apart from
// lost+found. AddPool then closes the root to 0711 and writes the marker.
func (m *Manager) AddPool(name, dir string) (Pool, error) {
	if err := agentapi.ValidatePoolName(name); err != nil {
		return Pool{}, fmt.Errorf("%w: %w", ErrPoolRefused, err)
	}

	p := Pool{Name: name, Path: dir, RequireMount: true}

	if err := validatePool(p); err != nil {
		return Pool{}, fmt.Errorf("%w: %w", ErrPoolRefused, err)
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	if _, ok := m.reg.Pool(name); ok {
		return Pool{}, fmt.Errorf("%w: %s", ErrPoolExists, name)
	}

	trees := m.trees
	if trees == nil {
		trees = systemTrees
	}

	if err := placeAllowed(dir, trees, m.reg.Pools()); err != nil {
		return Pool{}, err
	}

	fd, err := unix.Openat2(unix.AT_FDCWD, dir, &unix.OpenHow{Flags: dirFlags, Resolve: openPoolRes})
	if err != nil {
		return Pool{}, fmt.Errorf("%w: open %s: %w", ErrPoolRefused, dir, err)
	}
	defer unix.Close(fd) // read-only fd

	if err := m.checkNewPool(fd, dir); err != nil {
		return Pool{}, err
	}

	if err := unix.Fchown(fd, int(m.opt.OwnerUID), -1); err != nil {
		return Pool{}, fmt.Errorf("sites: pool %s: %w", name, err)
	}

	// Site users may enter their own home but not list the others
	// (ADR-0032), as on /home.
	if err := unix.Fchmod(fd, 0o711); err != nil {
		return Pool{}, fmt.Errorf("sites: pool %s: %w", name, err)
	}

	if p.ID, err = writeMarker(fd); err != nil {
		return Pool{}, fmt.Errorf("sites: pool %s: %w", name, err)
	}

	if err := m.reg.addPool(p); err != nil {
		_ = unix.Unlinkat(fd, MarkerName, 0)

		return Pool{}, err
	}

	m.opt.Log.Info("storage pool added", "pool", name, "path", dir)

	return p, nil
}

// RemovePool unregisters a pool no site uses and deletes its marker, so the
// volume can be unmounted or added again. The default pool stays.
func (m *Manager) RemovePool(name string) error {
	if err := agentapi.ValidatePoolName(name); err != nil {
		return fmt.Errorf("%w: %w", ErrPoolRefused, err)
	}

	if name == agentapi.DefaultPool {
		return fmt.Errorf("%w: the default pool cannot be removed", ErrPoolRefused)
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	p, ok := m.reg.Pool(name)
	if !ok {
		return ErrNotFound
	}

	for _, s := range m.reg.List() {
		if s.Pool == name {
			return fmt.Errorf("%w: %s is used by site %s", ErrPoolInUse, name, s.User)
		}
	}

	if err := m.reg.removePool(name); err != nil {
		return err
	}

	// The marker goes last: a pool left registered keeps working. A volume
	// that is gone leaves nothing to delete.
	// Without the mount demand: the marker check still makes sure it is
	// this pool's volume.
	unmounted := p
	unmounted.RequireMount = false

	if fd, err := openPool(unmounted, m.opt.OwnerUID); err == nil {
		if err := unix.Unlinkat(fd, MarkerName, 0); err != nil && !errors.Is(err, unix.ENOENT) {
			m.opt.Log.Warn("remove pool marker", "pool", name, "err", err)
		}

		_ = unix.Close(fd)
	}

	m.opt.Log.Info("storage pool removed", "pool", name, "path", p.Path)

	return nil
}

// placeAllowed refuses system trees and paths inside or around another
// pool: one file system per pool, and the homes of one never below another.
func placeAllowed(dir string, trees []string, pools []Pool) error {
	for _, t := range trees {
		if dir == t || strings.HasPrefix(dir, t+"/") {
			return fmt.Errorf("%w: %s is inside the system tree %s", ErrPoolRefused, dir, t)
		}
	}

	for _, p := range pools {
		if dir == p.Path || strings.HasPrefix(dir, p.Path+"/") || strings.HasPrefix(p.Path, dir+"/") {
			return fmt.Errorf("%w: %s overlaps pool %s (%s)", ErrPoolRefused, dir, p.Name, p.Path)
		}
	}

	return nil
}

// checkNewPool runs the checks on the open root of a would-be pool. Tests
// replace the file system part (fsCheck), which needs a real mount.
func (m *Manager) checkNewPool(fd int, dir string) error {
	var st unix.Stat_t
	if err := unix.Fstat(fd, &st); err != nil {
		return fmt.Errorf("%w: %s: %w", ErrPoolRefused, dir, err)
	}

	if st.Uid != m.opt.OwnerUID {
		return fmt.Errorf("%w: %s is not owned by uid %d", ErrPoolRefused, dir, m.opt.OwnerUID)
	}

	check := m.fsCheck
	if check == nil {
		check = mountCheck
	}

	if err := check(fd, dir); err != nil {
		return err
	}

	if !m.inFstab(dir) {
		return fmt.Errorf("%w: %s has no entry in %s; add one (with nofail) so the sites wait for the volume at boot",
			ErrPoolRefused, dir, m.fstab())
	}

	return checkEmpty(fd, dir)
}

// mountCheck demands the root of a mount of ext4 or XFS with nosuid and
// nodev: a missing volume then shows, quota works per pool, and nothing on
// the volume can raise privileges.
func mountCheck(fd int, dir string) error {
	var sx unix.Statx_t
	if err := unix.Statx(fd, "", unix.AT_EMPTY_PATH, 0, &sx); err != nil {
		return fmt.Errorf("%w: %s: %w", ErrPoolRefused, dir, err)
	}

	if sx.Attributes_mask&unix.STATX_ATTR_MOUNT_ROOT == 0 || sx.Attributes&unix.STATX_ATTR_MOUNT_ROOT == 0 {
		return fmt.Errorf("%w: %s is not a mount point; mount the volume there first", ErrPoolRefused, dir)
	}

	var fs unix.Statfs_t
	if err := unix.Fstatfs(fd, &fs); err != nil {
		return fmt.Errorf("%w: %s: %w", ErrPoolRefused, dir, err)
	}

	if fs.Type != magicExt4 && fs.Type != magicXFS {
		return fmt.Errorf("%w: %s is not ext4 or XFS (type %#x)", ErrPoolRefused, dir, fs.Type)
	}

	if fs.Flags&unix.ST_NOSUID == 0 || fs.Flags&unix.ST_NODEV == 0 {
		return fmt.Errorf("%w: %s must be mounted with nosuid,nodev", ErrPoolRefused, dir)
	}

	if fs.Flags&unix.ST_RDONLY != 0 {
		return fmt.Errorf("%w: %s is mounted read-only", ErrPoolRefused, dir)
	}

	return nil
}

// checkEmpty allows only lost+found: an old marker or files from somewhere
// else would mix up two pools or two servers.
func checkEmpty(fd int, dir string) error {
	dfd, err := unix.Dup(fd)
	if err != nil {
		return err
	}

	f := os.NewFile(uintptr(dfd), dir)
	defer f.Close()

	names, err := f.Readdirnames(-1)
	if err != nil {
		return fmt.Errorf("%w: read %s: %w", ErrPoolRefused, dir, err)
	}

	for _, n := range names {
		if n != "lost+found" {
			return fmt.Errorf("%w: %s is not empty (%s); a pool starts on an empty volume", ErrPoolRefused, dir, n)
		}
	}

	return nil
}

func (m *Manager) fstab() string {
	if m.opt.Fstab != "" {
		return m.opt.Fstab
	}

	return DefaultFstab
}

// inFstab reports whether the fstab has an active entry with mount point
// dir. Only then does systemd have a mount unit, and only then does
// RequiresMountsFor= keep the site units from starting without the volume
// (spike S9).
func (m *Manager) inFstab(dir string) bool {
	f, err := os.Open(m.fstab())
	if err != nil {
		return false
	}
	defer f.Close()

	return fstabHas(f, dir)
}

// PoolInFstab reports whether the fstab has an active entry for the mount
// point dir, for doctor.
func (m *Manager) PoolInFstab(dir string) bool { return m.inFstab(dir) }

func fstabHas(r io.Reader, dir string) bool {
	sc := bufio.NewScanner(r)
	for sc.Scan() {
		fields := strings.Fields(sc.Text())
		if len(fields) < 2 || strings.HasPrefix(fields[0], "#") {
			continue
		}

		// fstab escapes blanks as \040; pool paths cannot hold any.
		if path.Clean(fields[1]) == dir {
			return true
		}
	}

	return false
}

// writeMarker creates the marker with a new random pool id.
func writeMarker(poolFD int) (string, error) {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", err
	}

	id := hex.EncodeToString(b[:])

	mfd, err := unix.Openat(poolFD, MarkerName, unix.O_WRONLY|unix.O_CREAT|unix.O_EXCL|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0o644)
	if err != nil {
		return "", err
	}

	f := os.NewFile(uintptr(mfd), MarkerName)

	// 0644 whatever the umask: doctor and admins may read it.
	err = f.Chmod(0o644)
	if err == nil {
		_, err = f.WriteString(id + "\n")
	}

	if err == nil {
		err = f.Sync()
	}

	if cerr := f.Close(); err == nil {
		err = cerr
	}

	if err != nil {
		_ = unix.Unlinkat(poolFD, MarkerName, 0)

		return "", err
	}

	return id, nil
}
