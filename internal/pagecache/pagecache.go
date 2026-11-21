// SPDX-License-Identifier: AGPL-3.0-or-later

// Package pagecache keeps the directories of the sites' page caches
// (ADR-0046): <pool>/.hostashi-cache/<site>, owned by the web server user
// with mode 0700 and tagged with the site's quota project, so cached pages
// count against the site's disk limit. The site user can neither write nor
// list it. Purging swaps the directory for an empty one; nginx treats the
// missing files as misses.
//
// Everything works on file descriptors without following symlinks.
package pagecache

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path"
	"strings"

	"golang.org/x/sys/unix"
)

// DirName is the directory in every storage pool that holds the caches. A
// site user never starts with a dot, so it cannot clash with a home.
const DirName = ".hostashi-cache"

// trashPrefix marks a purged cache that is still being deleted.
const trashPrefix = ".purged-"

// Modes of the directories.
const (
	rootMode = 0o711
	siteMode = 0o700
)

const (
	dirFlags = unix.O_RDONLY | unix.O_DIRECTORY | unix.O_CLOEXEC | unix.O_NOFOLLOW
	noFollow = unix.RESOLVE_NO_SYMLINKS | unix.RESOLVE_NO_MAGICLINKS
	beneath  = unix.RESOLVE_BENEATH | noFollow | unix.RESOLVE_NO_XDEV
	maxDepth = 16 // nginx uses levels=1:2, so three levels are enough
)

// ErrUnsafe means a directory failed the ownership or type checks.
var ErrUnsafe = errors.New("pagecache: cache directory failed safety checks")

// Dirs creates, purges and removes the cache directories.
type Dirs struct {
	// UID and GID own every site's cache directory: nginx's workers.
	UID, GID int
	// Assign puts a directory into a quota project (package quota). Nil
	// leaves the project alone, for file systems without project quota.
	Assign func(dir string, project uint32) error
	// RootUID owns <pool>/.hostashi-cache; 0 except in tests.
	RootUID int
}

// Path is the cache directory of user in pool.
func Path(pool, user string) string { return path.Join(pool, DirName, user) }

// Ensure creates the cache directory of user in pool or fixes its owner and
// mode, and puts it into project. It returns the directory.
func (d Dirs) Ensure(pool, user string, project uint32) (string, error) {
	if err := checkName(user); err != nil {
		return "", err
	}

	cfd, err := d.openRoot(pool, true)
	if err != nil {
		return "", err
	}
	defer unix.Close(cfd) // read-only fd

	if err := d.ensureSite(cfd, user); err != nil {
		return "", err
	}

	dir := Path(pool, user)
	if d.Assign != nil {
		if err := d.Assign(dir, project); err != nil {
			return "", fmt.Errorf("pagecache: quota project of %s: %w", user, err)
		}
	}

	return dir, nil
}

// Purge swaps the cache directory of user for an empty one. A missing
// directory is created. The old contents keep a trash name until Sweep
// deletes them, which may take a while for a large cache.
func (d Dirs) Purge(pool, user string, project uint32) error {
	if err := checkName(user); err != nil {
		return err
	}

	cfd, err := d.openRoot(pool, true)
	if err != nil {
		return err
	}

	defer unix.Close(cfd) // read-only fd

	// Sweep deletes the old contents.
	if _, err := d.moveAway(cfd, user); err != nil {
		return err
	}

	_, err = d.Ensure(pool, user, project)

	return err
}

// Sweep deletes what Purge moved away in pool.
func (d Dirs) Sweep(pool string) error {
	cfd, err := d.openRoot(pool, false)
	if errors.Is(err, unix.ENOENT) {
		return nil
	}

	if err != nil {
		return err
	}

	defer unix.Close(cfd) // read-only fd

	names, err := readNames(cfd)
	if err != nil {
		return err
	}

	for _, n := range names {
		if strings.HasPrefix(n, trashPrefix) {
			if err := removeAll(cfd, n, 0); err != nil {
				return err
			}
		}
	}

	return nil
}

// Remove deletes the cache directory of user. A missing one is fine.
func (d Dirs) Remove(pool, user string) error {
	if err := checkName(user); err != nil {
		return err
	}

	cfd, err := d.openRoot(pool, false)
	if errors.Is(err, unix.ENOENT) {
		return nil
	}

	if err != nil {
		return err
	}

	defer unix.Close(cfd) // read-only fd

	trash, err := d.moveAway(cfd, user)
	if err != nil || trash == "" {
		return err
	}

	return removeAll(cfd, trash, 0)
}

// List returns the sites that have a cache directory in pool, without the
// leftovers of a purge.
func (d Dirs) List(pool string) ([]string, error) {
	cfd, err := d.openRoot(pool, false)
	if errors.Is(err, unix.ENOENT) {
		return nil, nil
	}

	if err != nil {
		return nil, err
	}

	defer unix.Close(cfd) // read-only fd

	names, err := readNames(cfd)
	if err != nil {
		return nil, err
	}

	out := make([]string, 0, len(names))

	for _, n := range names {
		if !strings.HasPrefix(n, trashPrefix) {
			out = append(out, n)
		}
	}

	return out, nil
}

// Check reports whether the cache directory of user exists with the right
// owner and mode.
func (d Dirs) Check(pool, user string) error {
	if err := checkName(user); err != nil {
		return err
	}

	cfd, err := d.openRoot(pool, false)
	if err != nil {
		return fmt.Errorf("pagecache: %w", err)
	}
	defer unix.Close(cfd) // read-only fd

	fd, err := unix.Openat2(cfd, user, &unix.OpenHow{Flags: dirFlags, Resolve: beneath})
	if err != nil {
		return fmt.Errorf("pagecache: open %s: %w", user, err)
	}
	defer unix.Close(fd) // read-only fd

	return d.checkSite(fd)
}

func (d Dirs) checkSite(fd int) error {
	var st unix.Stat_t
	if err := unix.Fstat(fd, &st); err != nil {
		return fmt.Errorf("pagecache: stat: %w", err)
	}

	if int(st.Uid) != d.UID || int(st.Gid) != d.GID || st.Mode&0o7777 != siteMode {
		return fmt.Errorf("%w: owner %d:%d mode %o, want %d:%d %o", ErrUnsafe, st.Uid, st.Gid, st.Mode&0o7777, d.UID, d.GID, siteMode)
	}

	return nil
}

// openRoot opens <pool>/.hostashi-cache, creating it if create is set, and
// checks that it belongs to RootUID and that nobody else may write to it.
func (d Dirs) openRoot(pool string, create bool) (int, error) {
	if !path.IsAbs(pool) || path.Clean(pool) != pool {
		return -1, fmt.Errorf("pagecache: invalid pool %q", pool)
	}

	pfd, err := unix.Openat2(unix.AT_FDCWD, pool, &unix.OpenHow{Flags: dirFlags, Resolve: noFollow})
	if err != nil {
		return -1, fmt.Errorf("pagecache: open pool: %w", err)
	}
	defer unix.Close(pfd) // read-only fd

	if create {
		if err := unix.Mkdirat(pfd, DirName, rootMode); err != nil && !errors.Is(err, unix.EEXIST) {
			return -1, fmt.Errorf("pagecache: create %s: %w", DirName, err)
		}
	}

	cfd, err := unix.Openat2(pfd, DirName, &unix.OpenHow{Flags: dirFlags, Resolve: beneath})
	if err != nil {
		return -1, fmt.Errorf("pagecache: open %s: %w", DirName, err)
	}

	var st unix.Stat_t
	if err := unix.Fstat(cfd, &st); err != nil {
		_ = unix.Close(cfd)

		return -1, fmt.Errorf("pagecache: stat %s: %w", DirName, err)
	}

	if int(st.Uid) != d.RootUID {
		_ = unix.Close(cfd)

		return -1, fmt.Errorf("%w: %s belongs to uid %d", ErrUnsafe, DirName, st.Uid)
	}

	if st.Mode&0o7777 != rootMode {
		if err := unix.Fchmod(cfd, rootMode); err != nil {
			_ = unix.Close(cfd)

			return -1, fmt.Errorf("pagecache: chmod %s: %w", DirName, err)
		}
	}

	return cfd, nil
}

// ensureSite creates the site's directory below cfd and sets owner and
// mode on the open fd. Only RootUID can create entries in cfd, so an
// existing entry was made by us, but it is checked to be a directory.
func (d Dirs) ensureSite(cfd int, user string) error {
	if err := unix.Mkdirat(cfd, user, siteMode); err != nil && !errors.Is(err, unix.EEXIST) {
		return fmt.Errorf("pagecache: create %s: %w", user, err)
	}

	fd, err := unix.Openat2(cfd, user, &unix.OpenHow{Flags: dirFlags, Resolve: beneath})
	if err != nil {
		return fmt.Errorf("%w: open %s: %w", ErrUnsafe, user, err)
	}
	defer unix.Close(fd) // read-only fd

	if err := unix.Fchown(fd, d.UID, d.GID); err != nil {
		return fmt.Errorf("pagecache: chown %s: %w", user, err)
	}

	if err := unix.Fchmod(fd, siteMode); err != nil {
		return fmt.Errorf("pagecache: chmod %s: %w", user, err)
	}

	return nil
}

// moveAway renames the site's directory to a trash name and returns it, or
// "" when there was none.
func (d Dirs) moveAway(cfd int, user string) (string, error) {
	b := make([]byte, 8)
	_, _ = rand.Read(b) // never fails (crypto/rand)

	trash := trashPrefix + user + "-" + hex.EncodeToString(b)

	err := unix.Renameat2(cfd, user, cfd, trash, unix.RENAME_NOREPLACE)
	if errors.Is(err, unix.ENOENT) {
		return "", nil
	}

	if err != nil {
		return "", fmt.Errorf("pagecache: move %s away: %w", user, err)
	}

	return trash, nil
}

func checkName(user string) error {
	if user == "" || user == "." || user == ".." || strings.ContainsAny(user, "/\x00") || strings.HasPrefix(user, ".") {
		return fmt.Errorf("pagecache: invalid site %q", user)
	}

	return nil
}

func readNames(fd int) ([]string, error) {
	dfd, err := unix.Dup(fd)
	if err != nil {
		return nil, fmt.Errorf("pagecache: %w", err)
	}

	f := os.NewFile(uintptr(dfd), "dir") // closes dfd
	defer f.Close()

	names, err := f.Readdirnames(-1)
	if err != nil {
		return nil, fmt.Errorf("pagecache: list: %w", err)
	}

	return names, nil
}

// removeAll deletes name below parent with fds only. nginx's workers own
// the tree; a symlink in it is removed itself and never followed.
func removeAll(parent int, name string, depth int) error {
	if depth > maxDepth {
		return fmt.Errorf("%w: tree deeper than %d", ErrUnsafe, maxDepth)
	}

	err := unix.Unlinkat(parent, name, 0)
	if err == nil || errors.Is(err, unix.ENOENT) {
		return nil
	}

	if !errors.Is(err, unix.EISDIR) && !errors.Is(err, unix.EPERM) {
		return fmt.Errorf("pagecache: remove %q: %w", name, err)
	}

	fd, err := unix.Openat2(parent, name, &unix.OpenHow{Flags: dirFlags, Resolve: beneath})
	if err != nil {
		if errors.Is(err, unix.ELOOP) || errors.Is(err, unix.ENOTDIR) {
			if e := unix.Unlinkat(parent, name, 0); e != nil && !errors.Is(e, unix.ENOENT) {
				return fmt.Errorf("pagecache: remove %q: %w", name, e)
			}

			return nil
		}

		if errors.Is(err, unix.ENOENT) {
			return nil
		}

		return fmt.Errorf("pagecache: open %q: %w", name, err)
	}

	names, err := readNames(fd)
	if err != nil {
		_ = unix.Close(fd)

		return err
	}

	for _, n := range names {
		if err := removeAll(fd, n, depth+1); err != nil {
			_ = unix.Close(fd)

			return err
		}
	}

	_ = unix.Close(fd)

	if err := unix.Unlinkat(parent, name, unix.AT_REMOVEDIR); err != nil && !errors.Is(err, unix.ENOENT) {
		return fmt.Errorf("pagecache: remove %q: %w", name, err)
	}

	return nil
}
