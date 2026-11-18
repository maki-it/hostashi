// SPDX-License-Identifier: AGPL-3.0-or-later

package sites

import (
	"errors"
	"fmt"
	"os"
	"strings"

	"golang.org/x/sys/unix"
)

// MarkerName is the file that identifies a registered pool (ADR-0016).
const MarkerName = ".hostashi-pool"

// HomeMode is the mode of a new site home: closed to other site users. How
// nginx and PHP-FPM reach the web root is decided by the reconciler.
const HomeMode = 0o750

const maxRemoveDepth = 128

// Errors of the file system part.
var (
	ErrPoolUnavailable = errors.New("sites: storage pool unavailable")
	ErrHomeUnsafe      = errors.New("sites: site home directory failed safety checks")
	ErrHomeExists      = errors.New("sites: home directory already exists")
)

const (
	dirFlags    = unix.O_RDONLY | unix.O_DIRECTORY | unix.O_CLOEXEC
	noEscape    = unix.RESOLVE_BENEATH | unix.RESOLVE_NO_SYMLINKS | unix.RESOLVE_NO_MAGICLINKS | unix.RESOLVE_NO_XDEV
	openPoolRes = unix.RESOLVE_NO_SYMLINKS | unix.RESOLVE_NO_MAGICLINKS
)

// openPool opens the pool root and checks it on the open fd: root-owned (or
// ownerUID in tests), closed to group and others for writing, a mount root if
// the pool demands it, and carrying the registered marker.
func openPool(p Pool, ownerUID uint32) (int, error) {
	fd, err := unix.Openat2(unix.AT_FDCWD, p.Path, &unix.OpenHow{Flags: dirFlags, Resolve: openPoolRes})
	if err != nil {
		return -1, fmt.Errorf("%w: open %s: %w", ErrPoolUnavailable, p.Name, err)
	}

	fail := func(format string, a ...any) (int, error) {
		_ = unix.Close(fd)

		return -1, fmt.Errorf("%w: %s: %s", ErrPoolUnavailable, p.Name, fmt.Sprintf(format, a...))
	}

	var st unix.Stat_t
	if err := unix.Fstat(fd, &st); err != nil {
		return fail("fstat: %v", err)
	}

	if st.Uid != ownerUID || st.Mode&0o022 != 0 {
		return fail("pool directory must be owned by uid %d and not group- or world-writable", ownerUID)
	}

	if p.RequireMount {
		var sx unix.Statx_t
		if err := unix.Statx(fd, "", unix.AT_EMPTY_PATH, unix.STATX_BASIC_STATS, &sx); err != nil {
			return fail("statx: %v", err)
		}

		if sx.Attributes_mask&unix.STATX_ATTR_MOUNT_ROOT == 0 {
			return fail("kernel cannot report mount roots")
		}

		if sx.Attributes&unix.STATX_ATTR_MOUNT_ROOT == 0 {
			return fail("not a mount point (volume missing or not mounted)")
		}
	}

	if p.ID != "" {
		if err := checkMarker(fd, p, ownerUID); err != nil {
			_ = unix.Close(fd)

			return -1, err
		}
	}

	return fd, nil
}

func checkMarker(poolFD int, p Pool, ownerUID uint32) error {
	mfd, err := unix.Openat2(poolFD, MarkerName, &unix.OpenHow{Flags: unix.O_RDONLY | unix.O_CLOEXEC, Resolve: noEscape})
	if err != nil {
		return fmt.Errorf("%w: %s: marker missing: %w", ErrPoolUnavailable, p.Name, err)
	}

	f := os.NewFile(uintptr(mfd), "marker")
	defer f.Close()

	var st unix.Stat_t
	if err := unix.Fstat(mfd, &st); err != nil || st.Uid != ownerUID || st.Mode&0o022 != 0 {
		return fmt.Errorf("%w: %s: marker not owned by uid %d or writable by others", ErrPoolUnavailable, p.Name, ownerUID)
	}

	buf := make([]byte, 129)
	n, _ := f.Read(buf)

	if strings.TrimSpace(string(buf[:n])) != p.ID {
		return fmt.Errorf("%w: %s: marker does not match the registered pool id", ErrPoolUnavailable, p.Name)
	}

	return nil
}

// createHome makes the home directory user below the pool and gives it to the
// site. The directory is created root-owned and closed (0700), then handed
// over on the open fd, so no moment exists where another user could enter it.
// The name is a validated single path component.
func createHome(poolFD int, user string, uid, gid uint32) error {
	if err := unix.Mkdirat(poolFD, user, 0o700); err != nil {
		if errors.Is(err, unix.EEXIST) {
			return ErrHomeExists
		}

		return fmt.Errorf("mkdir home: %w", err)
	}

	fd, err := unix.Openat2(poolFD, user, &unix.OpenHow{Flags: dirFlags, Resolve: noEscape})
	if err != nil {
		_ = unix.Unlinkat(poolFD, user, unix.AT_REMOVEDIR)

		return fmt.Errorf("%w: open new home: %w", ErrHomeUnsafe, err)
	}

	defer unix.Close(fd)

	if err := unix.Fchown(fd, int(uid), int(gid)); err != nil {
		_ = unix.Unlinkat(poolFD, user, unix.AT_REMOVEDIR)

		return fmt.Errorf("chown home: %w", err)
	}

	if err := unix.Fchmod(fd, HomeMode); err != nil {
		_ = unix.Unlinkat(poolFD, user, unix.AT_REMOVEDIR)

		return fmt.Errorf("chmod home: %w", err)
	}

	return nil
}

// removeHome deletes the home of a site completely. It refuses a directory
// that the registry does not know as owned by the site user, so a wrong
// registry entry or a swapped directory cannot make the agent delete foreign
// data. A missing home counts as done (retry after a partial delete).
func removeHome(poolFD int, user string, uid, gid uint32) error {
	fd, err := unix.Openat2(poolFD, user, &unix.OpenHow{Flags: dirFlags, Resolve: noEscape})
	if err != nil {
		if errors.Is(err, unix.ENOENT) {
			return nil
		}

		return fmt.Errorf("%w: open home: %w", ErrHomeUnsafe, err)
	}

	var st unix.Stat_t
	if err := unix.Fstat(fd, &st); err != nil {
		_ = unix.Close(fd)

		return fmt.Errorf("stat home: %w", err)
	}

	if st.Uid != uid || st.Gid != gid {
		_ = unix.Close(fd)

		return fmt.Errorf("%w: owner is %d:%d, registry says %d:%d", ErrHomeUnsafe, st.Uid, st.Gid, uid, gid)
	}

	f := os.NewFile(uintptr(fd), "home") // closes fd

	names, err := f.Readdirnames(-1)
	if err != nil {
		_ = f.Close()

		return fmt.Errorf("list home: %w", err)
	}

	for _, n := range names {
		if err := removeAll(fd, n, 1); err != nil {
			_ = f.Close()

			return err
		}
	}

	if err := f.Close(); err != nil {
		return fmt.Errorf("close home: %w", err)
	}

	if err := unix.Unlinkat(poolFD, user, unix.AT_REMOVEDIR); err != nil && !errors.Is(err, unix.ENOENT) {
		return fmt.Errorf("remove home: %w", err)
	}

	return nil
}

// removeAll deletes name below parent using only fds and no symlink
// following, so swapping a directory for a symlink during the walk cannot
// redirect it (spike S4). NO_XDEV keeps it out of bind mounts, which have the
// same st_dev as their source.
func removeAll(parent int, name string, depth int) error {
	if depth > maxRemoveDepth {
		return fmt.Errorf("%w: tree deeper than %d", ErrHomeUnsafe, maxRemoveDepth)
	}

	err := unix.Unlinkat(parent, name, 0)
	if err == nil || errors.Is(err, unix.ENOENT) {
		return nil
	}

	if !errors.Is(err, unix.EISDIR) && !errors.Is(err, unix.EPERM) {
		return fmt.Errorf("remove %q: %w", name, err)
	}

	dfd, err := unix.Openat2(parent, name, &unix.OpenHow{Flags: dirFlags, Resolve: noEscape})
	if err != nil {
		switch {
		case errors.Is(err, unix.ELOOP) || errors.Is(err, unix.ENOTDIR):
			// Swapped for a symlink or file meanwhile: remove that object itself.
			if e := unix.Unlinkat(parent, name, 0); e != nil && !errors.Is(e, unix.ENOENT) {
				return fmt.Errorf("remove %q: %w", name, e)
			}

			return nil
		case errors.Is(err, unix.ENOENT):
			return nil
		default:
			return fmt.Errorf("%w: open %q: %w", ErrHomeUnsafe, name, err)
		}
	}

	f := os.NewFile(uintptr(dfd), "dir")

	names, err := f.Readdirnames(-1)
	if err != nil {
		_ = f.Close()

		return fmt.Errorf("list %q: %w", name, err)
	}

	for _, n := range names {
		if err := removeAll(dfd, n, depth+1); err != nil {
			_ = f.Close()

			return err
		}
	}

	_ = f.Close()

	if err := unix.Unlinkat(parent, name, unix.AT_REMOVEDIR); err != nil && !errors.Is(err, unix.ENOENT) {
		return fmt.Errorf("remove %q: %w", name, err)
	}

	return nil
}
