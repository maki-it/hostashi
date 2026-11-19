// SPDX-License-Identifier: AGPL-3.0-or-later

package backup

import (
	"archive/tar"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"strings"
	"time"

	"golang.org/x/sys/unix"

	"github.com/maki-it/hostashi/internal/agentapi"
	"github.com/maki-it/hostashi/internal/resticctl"
	"github.com/maki-it/hostashi/internal/statefile"
)

// RestoreOnce is hostashi backup restore-run ID: one restore as root,
// started by the agent in hostashi-restore@ID.service. It writes the
// outcome to the restore's record whatever happens after the lock.
func RestoreOnce(ctx context.Context, cfg RunConfig, id string) error {
	if os.Geteuid() != 0 {
		return errors.New("backup: must run as root")
	}

	if !resticctl.ValidRestoreID(id) {
		return fmt.Errorf("%w: restore id", ErrInvalid)
	}

	cfg = cfg.withDefaults()

	if err := statefile.CheckDir(cfg.StatusDir, 0); err != nil {
		return fmt.Errorf("status directory: %w", err)
	}

	unlock, err := lock(filepath.Join(cfg.StatusDir, restoreLock))
	if err != nil {
		if errors.Is(err, ErrBusy) {
			return ErrRestoreBusy
		}

		return err
	}
	defer unlock()

	r, err := readRestore(cfg.StatusDir, id)
	if err != nil {
		return err
	}

	if r.State != agentapi.RestoreQueued {
		return fmt.Errorf("backup: restore %s is %s, not queued", id, r.State)
	}

	r.State, r.Started = agentapi.RestoreRunning, time.Now().UTC()
	if err := writeJSON(cfg.StatusDir, restoreFile(id), r); err != nil {
		return err
	}

	res, err := restore(ctx, cfg, r)

	r.Files, r.Bytes, r.Skipped = res.files, res.bytes, res.skipped
	r.Finished = time.Now().UTC()
	r.State = agentapi.RestoreDone

	if err != nil {
		r.State, r.Error = agentapi.RestoreFailed, err.Error()
		cfg.Log.Error("restore failed", "id", id, "site", r.Site, "err", err)
	}

	if werr := writeJSON(cfg.StatusDir, restoreFile(id), r); werr != nil {
		return fmt.Errorf("write restore record: %w", werr)
	}

	if err != nil {
		return ErrFailed
	}

	return nil
}

func restore(ctx context.Context, cfg RunConfig, r Restore) (extracted, error) {
	settings, err := ReadSettings(cfg.ConfigDir, 0)
	if err != nil {
		return extracted{}, fmt.Errorf("settings: %w", err)
	}

	repo, err := NewRepo(settings, cfg.ConfigDir, cfg.CacheDir, cfg.Host)
	if err != nil {
		return extracted{}, err
	}

	repo.Log = cfg.Log

	all, err := Sites(cfg.StateDir, 0)
	if err != nil {
		return extracted{}, err
	}

	var site *Site

	for i := range all {
		if all[i].Name == r.Site {
			site = &all[i]
		}
	}

	if site == nil {
		return extracted{}, fmt.Errorf("site %s no longer exists", r.Site)
	}

	// The snapshot must still be this host's, of this site and kind; its
	// single path is what the tar names start with.
	tags := []string{"site:" + r.Site, "kind:" + r.Kind}
	if r.Kind == KindDB {
		tags = append(tags, "db:"+r.Database)
	}

	snaps, err := repo.Snapshots(ctx, tags)
	if err != nil {
		return extracted{}, err
	}

	var prefix string

	found := false

	for _, s := range snaps {
		if s.ID == r.Snapshot && len(s.Paths) == 1 {
			prefix, found = strings.TrimPrefix(s.Paths[0], "/"), true
		}
	}

	if !found || prefix == "" {
		return extracted{}, ErrSnapshotNotFound
	}

	target, err := createRestoreDir(site.Home, r.Dir, site.UID)
	if err != nil {
		return extracted{}, err
	}
	defer unix.Close(target)

	// Whatever happens, the directory ends up the site user's, so they
	// can look at or delete a partial restore.
	defer func() {
		_ = unix.Fchown(target, int(site.UID), int(site.GID))
		_ = unix.Fchmod(target, 0o700)
	}()

	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	pr, pw := io.Pipe()
	dumpErr := make(chan error, 1)

	go func() {
		err := repo.Dump(ctx, r.Snapshot, pw)

		_ = pw.CloseWithError(err)
		dumpErr <- err
	}()

	x := &extractor{root: target, prefix: prefix, uid: int(site.UID), gid: int(site.GID)}

	err = x.extract(pr)
	if err != nil {
		// Stop restic; its error is the cancellation, not the cause.
		cancel()

		_ = pr.CloseWithError(errors.New("extraction ended"))
	} else {
		// The tar end marker came; let restic finish writing and exit.
		_, _ = io.Copy(io.Discard, pr)
	}

	derr := <-dumpErr

	switch {
	case err != nil:
		return x.n, err
	case derr != nil:
		return x.n, derr
	}

	return x.n, nil
}

// createRestoreDir makes home/name, root-owned and mode 0700, and returns
// it open. The home is opened without following any symlink and must be
// the site user's. Until the restore ends the site user cannot enter the
// new directory, so nothing inside can be swapped for a symlink.
func createRestoreDir(home, name string, uid uint32) (int, error) {
	how := &unix.OpenHow{
		Flags:   unix.O_RDONLY | unix.O_DIRECTORY | unix.O_NOFOLLOW | unix.O_CLOEXEC,
		Resolve: unix.RESOLVE_NO_SYMLINKS | unix.RESOLVE_NO_MAGICLINKS,
	}

	hfd, err := unix.Openat2(unix.AT_FDCWD, home, how)
	if err != nil {
		return -1, fmt.Errorf("open home %s: %w", home, err)
	}
	defer unix.Close(hfd)

	var st unix.Stat_t
	if err := unix.Fstat(hfd, &st); err != nil || st.Uid != uid {
		return -1, fmt.Errorf("home %s is not the site user's", home)
	}

	if err := unix.Mkdirat(hfd, name, 0o700); err != nil {
		return -1, fmt.Errorf("create %s: %w", name, err)
	}

	how.Resolve |= unix.RESOLVE_BENEATH

	fd, err := unix.Openat2(hfd, name, how)
	if err != nil {
		return -1, fmt.Errorf("open %s: %w", name, err)
	}

	if err := unix.Fstat(fd, &st); err != nil || st.Uid != owner() {
		_ = unix.Close(fd)

		return -1, fmt.Errorf("%s changed while it was created", name)
	}

	return fd, nil
}

type extracted struct{ files, bytes, skipped int64 }

// extractor writes a tar stream from restic dump below root. Every path is
// resolved beneath root without following symlinks, files are created
// exclusively, and everything is owned by uid:gid with setuid, setgid and
// sticky bits dropped.
type extractor struct {
	root     int
	prefix   string // the snapshot's path without the leading slash
	uid, gid int
	n        extracted
}

const (
	maxNameLen = 4096
	// beneath resolves a relative path inside root, refusing symlinks,
	// magic links and "..".
	beneath = unix.RESOLVE_BENEATH | unix.RESOLVE_NO_SYMLINKS | unix.RESOLVE_NO_MAGICLINKS
)

func (x *extractor) extract(r io.Reader) error {
	tr := tar.NewReader(r)

	for {
		h, err := tr.Next()
		if errors.Is(err, io.EOF) {
			return nil
		}

		if err != nil {
			return fmt.Errorf("read the snapshot: %w", err)
		}

		rel, ok, err := x.relative(h)
		if err != nil {
			return err
		}

		if !ok {
			continue
		}

		if err := x.entry(h, rel, tr); err != nil {
			return fmt.Errorf("%s: %w", rel, err)
		}
	}
}

// relative maps a tar name to a path below root. Entries outside the
// snapshot's path (its parent directories) and the path itself, when it is
// a directory, are skipped; a single file (a database dump) keeps its name.
func (x *extractor) relative(h *tar.Header) (string, bool, error) {
	name := strings.TrimSuffix(h.Name, "/")

	if len(name) > maxNameLen || name == "" || strings.HasPrefix(name, "/") || path.Clean(name) != name {
		return "", false, fmt.Errorf("unsafe name %q in the snapshot", h.Name)
	}

	switch {
	case name == x.prefix:
		if h.Typeflag == tar.TypeDir {
			return "", false, nil
		}

		return path.Base(name), true, nil
	case strings.HasPrefix(name, x.prefix+"/"):
		rel := strings.TrimPrefix(name, x.prefix+"/")
		for _, c := range strings.Split(rel, "/") {
			if c == ".." || c == "." || c == "" {
				return "", false, fmt.Errorf("unsafe name %q in the snapshot", h.Name)
			}
		}

		return rel, true, nil
	}

	return "", false, nil
}

func (x *extractor) entry(h *tar.Header, rel string, body io.Reader) error {
	dir, base := path.Split(rel)

	parent, err := x.mkdirs(strings.TrimSuffix(dir, "/"))
	if err != nil {
		return err
	}
	defer unix.Close(parent)

	mode := uint32(h.Mode) & 0o777 //nolint:gosec // tar modes are small; the mask drops the rest

	switch h.Typeflag {
	case tar.TypeDir:
		if err := mkdirOwned(parent, base, x.uid, x.gid); err != nil && !errors.Is(err, unix.EEXIST) {
			return err
		}

		// fchmodat would follow a symlink of that name; the open fd cannot.
		fd, err := unix.Openat2(parent, base, &unix.OpenHow{Flags: unix.O_RDONLY | unix.O_DIRECTORY | unix.O_CLOEXEC, Resolve: beneath})
		if err != nil {
			return err
		}
		defer unix.Close(fd)

		return unix.Fchmod(fd, mode|0o700)
	case tar.TypeReg:
		fd, err := unix.Openat(parent, base, unix.O_WRONLY|unix.O_CREAT|unix.O_EXCL|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0o600)
		if err != nil {
			return err
		}

		f := os.NewFile(uintptr(fd), base)
		n, err := io.Copy(f, body)
		x.n.bytes += n

		if err == nil {
			err = f.Chown(x.uid, x.gid)
		}

		if err == nil {
			err = f.Chmod(os.FileMode(mode))
		}

		if cerr := f.Close(); err == nil {
			err = cerr
		}

		if err != nil {
			return err
		}

		x.n.files++
	case tar.TypeSymlink:
		// The link is created, never followed; it points where it pointed
		// in the backup.
		if err := unix.Symlinkat(h.Linkname, parent, base); err != nil {
			return err
		}

		if err := unix.Fchownat(parent, base, x.uid, x.gid, unix.AT_SYMLINK_NOFOLLOW); err != nil {
			return err
		}

		x.n.files++
	default:
		x.n.skipped++

		return nil
	}

	if !h.ModTime.IsZero() && h.Typeflag != tar.TypeDir {
		ts := []unix.Timespec{unix.NsecToTimespec(h.ModTime.UnixNano()), unix.NsecToTimespec(h.ModTime.UnixNano())}
		if err := unix.UtimesNanoAt(parent, base, ts, unix.AT_SYMLINK_NOFOLLOW); err != nil {
			return err
		}
	}

	return nil
}

// mkdirs opens dir below root, creating missing directories.
func (x *extractor) mkdirs(dir string) (int, error) {
	how := &unix.OpenHow{Flags: unix.O_PATH | unix.O_DIRECTORY | unix.O_CLOEXEC, Resolve: beneath}

	fd, err := unix.Openat2(x.root, orDot(dir), how)
	if err == nil || !errors.Is(err, unix.ENOENT) {
		return fd, err
	}

	parent, err := x.mkdirs(path.Dir(dir))
	if err != nil {
		return -1, err
	}
	defer unix.Close(parent)

	if err := mkdirOwned(parent, path.Base(dir), x.uid, x.gid); err != nil && !errors.Is(err, unix.EEXIST) {
		return -1, err
	}

	return unix.Openat2(parent, path.Base(dir), how)
}

func mkdirOwned(parent int, name string, uid, gid int) error {
	if err := unix.Mkdirat(parent, name, 0o700); err != nil {
		return err
	}

	return unix.Fchownat(parent, name, uid, gid, unix.AT_SYMLINK_NOFOLLOW)
}

func orDot(dir string) string {
	if dir == "" {
		return "."
	}

	return dir
}
