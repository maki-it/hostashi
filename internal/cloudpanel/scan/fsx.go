// SPDX-License-Identifier: AGPL-3.0-or-later

package scan

import (
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path"
	"sort"
	"strings"
	"syscall"

	"golang.org/x/sys/unix"
)

// reader reads files below one root directory. Every path is resolved with
// openat2(RESOLVE_IN_ROOT | RESOLVE_NO_MAGICLINKS): ".." stops at the root and
// absolute symlinks are resolved relative to the root, like inside a chroot.
// So a symlink can never lead out of the tree, and a tree mounted from an old
// disk behaves like the live system. (os.Root, used first, refuses every
// absolute symlink, see REPORT.md.) The scanner never writes.
type reader struct {
	rootFD           int
	requireRootOwned bool
}

func newReader(dir string, requireRootOwned bool) (*reader, error) {
	fd, err := unix.Open(dir, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_CLOEXEC, 0)
	if err != nil {
		return nil, &os.PathError{Op: "open", Path: dir, Err: err}
	}

	return &reader{rootFD: fd, requireRootOwned: requireRootOwned}, nil
}

func (r *reader) Close() error { return unix.Close(r.rootFD) }

const resolveFlags = unix.RESOLVE_IN_ROOT | unix.RESOLVE_NO_MAGICLINKS

// open resolves p inside the root. flags are extra open flags.
func (r *reader) open(abs string, flags int) (*os.File, error) {
	p, err := rel(abs)
	if err != nil {
		return nil, err
	}

	flags |= unix.O_CLOEXEC
	if flags&unix.O_PATH == 0 {
		// O_NONBLOCK: opening a FIFO planted in the tree must not hang.
		// openat2 rejects it together with O_PATH (EINVAL).
		flags |= unix.O_NONBLOCK
	}

	for {
		fd, err := unix.Openat2(r.rootFD, p, &unix.OpenHow{
			Flags:   uint64(flags), //nolint:gosec // flags are non-negative open(2) constants
			Resolve: resolveFlags,
		})
		if err == unix.EINTR || err == unix.EAGAIN {
			continue
		}

		if err != nil {
			return nil, &os.PathError{Op: "openat2", Path: abs, Err: err}
		}

		return os.NewFile(uintptr(fd), abs), nil
	}
}

// rel turns an absolute path from a config file into a path below the root.
func rel(abs string) (string, error) {
	if !strings.HasPrefix(abs, "/") {
		return "", fmt.Errorf("path %q is not absolute", abs)
	}

	if strings.ContainsRune(abs, 0) {
		return "", errors.New("NUL in path")
	}

	c := path.Clean(abs)
	if c == "/" {
		return ".", nil
	}

	return c[1:], nil
}

// ErrUntrusted is returned for a config file with the wrong owner or mode.
var ErrUntrusted = errors.New("file is not root-owned or is writable by others")

// ReadFile reads a root-trusted configuration file of at most limit bytes.
func (r *reader) ReadFile(abs string, limit int64) ([]byte, error) {
	f, err := r.open(abs, unix.O_RDONLY|unix.O_NOCTTY)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	st, err := f.Stat() // on the open handle: no race between check and read
	if err != nil {
		return nil, err
	}

	if !st.Mode().IsRegular() {
		return nil, fmt.Errorf("%s: not a regular file", abs)
	}

	if err := r.trusted(st); err != nil {
		return nil, fmt.Errorf("%s: %w", abs, err)
	}

	b, err := io.ReadAll(io.LimitReader(f, limit+1))
	if err != nil {
		return nil, err
	}

	if int64(len(b)) > limit {
		return nil, fmt.Errorf("%s: larger than %d bytes", abs, limit)
	}

	return b, nil
}

func (r *reader) trusted(st fs.FileInfo) error {
	if !r.requireRootOwned {
		return nil
	}

	sys, ok := st.Sys().(*syscall.Stat_t)
	if !ok {
		return errors.New("cannot determine owner")
	}

	if sys.Uid != 0 || st.Mode().Perm()&0o022 != 0 {
		return ErrUntrusted
	}

	return nil
}

// Exists reports whether the path exists, without following a final symlink
// and without reading content. Used for site-controlled trees (home directory).
func (r *reader) Exists(abs string) bool {
	_, err := r.Mode(abs)
	return err == nil
}

// Mode returns the permission bits of the path (no final symlink following).
func (r *reader) Mode(abs string) (fs.FileMode, error) {
	// O_PATH|O_NOFOLLOW gives a handle on a final symlink itself, so this
	// is an lstat; intermediate symlinks resolve inside the root.
	f, err := r.open(abs, unix.O_PATH|unix.O_NOFOLLOW)
	if err != nil {
		return 0, err
	}
	defer f.Close()

	st, err := f.Stat()
	if err != nil {
		return 0, err
	}

	return st.Mode(), nil
}

// ReadDir lists names of a root-trusted directory, sorted, at most limit entries.
func (r *reader) ReadDir(abs string, limit int) ([]string, error) {
	f, err := r.open(abs, unix.O_RDONLY|unix.O_DIRECTORY)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	ents, err := f.ReadDir(limit + 1)
	if err != nil && !errors.Is(err, io.EOF) {
		return nil, err
	}

	if len(ents) > limit {
		return nil, fmt.Errorf("%s: more than %d entries", abs, limit)
	}

	names := make([]string, 0, len(ents))
	for _, e := range ents {
		names = append(names, e.Name())
	}

	sort.Strings(names)

	return names, nil
}

// ReadOwned reads a file that belongs to a site user (a crontab). The file
// must be regular and owned by uid; the content is untrusted data.
func (r *reader) ReadOwned(abs string, uid int, limit int64) ([]byte, error) {
	f, err := r.open(abs, unix.O_RDONLY|unix.O_NOCTTY)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	st, err := f.Stat()
	if err != nil {
		return nil, err
	}

	if !st.Mode().IsRegular() {
		return nil, fmt.Errorf("%s: not a regular file", abs)
	}

	if r.requireRootOwned {
		sys, ok := st.Sys().(*syscall.Stat_t)
		if !ok || int(sys.Uid) != uid {
			return nil, fmt.Errorf("%s: not owned by uid %d", abs, uid)
		}
	}

	b, err := io.ReadAll(io.LimitReader(f, limit+1))
	if err != nil {
		return nil, err
	}

	if int64(len(b)) > limit {
		return nil, fmt.Errorf("%s: larger than %d bytes", abs, limit)
	}

	return b, nil
}
