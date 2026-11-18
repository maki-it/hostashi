// SPDX-License-Identifier: AGPL-3.0-or-later

// Package statefile reads and writes the agent's small JSON state files in a
// root-owned directory. Reads refuse symlinks, foreign owners and loose
// modes; writes are atomic (temp file, fsync, rename, fsync of the
// directory), so a crash leaves either the old or the new file.
//
// The site registry predates this package and keeps its own copy of the
// same rules; new state files use this one.
package statefile

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"golang.org/x/sys/unix"
)

// Errors of the package.
var (
	ErrUnsafe   = errors.New("statefile: directory or file is not safe")
	ErrTooLarge = errors.New("statefile: file is too large")
)

// CheckDir verifies that dir is an absolute clean path without symlinks,
// owned by ownerUID and closed to group and others.
func CheckDir(dir string, ownerUID uint32) error {
	if !filepath.IsAbs(dir) || filepath.Clean(dir) != dir {
		return fmt.Errorf("%w: directory must be an absolute clean path", ErrUnsafe)
	}

	fd, err := unix.Openat2(unix.AT_FDCWD, dir, &unix.OpenHow{
		Flags:   unix.O_RDONLY | unix.O_DIRECTORY | unix.O_CLOEXEC,
		Resolve: unix.RESOLVE_NO_SYMLINKS | unix.RESOLVE_NO_MAGICLINKS,
	})
	if err != nil {
		return fmt.Errorf("%w: open directory: %w", ErrUnsafe, err)
	}

	defer unix.Close(fd)

	var st unix.Stat_t
	if err := unix.Fstat(fd, &st); err != nil {
		return fmt.Errorf("%w: stat directory: %w", ErrUnsafe, err)
	}

	if st.Uid != ownerUID || st.Mode&0o077 != 0 {
		return fmt.Errorf("%w: directory must be owned by uid %d with mode 0700", ErrUnsafe, ownerUID)
	}

	return nil
}

// Read returns the content of dir/name. A missing file is os.ErrNotExist.
// The file must be a regular file owned by ownerUID with mode 0600 or
// stricter and at most maxSize bytes long.
func Read(dir, name string, ownerUID uint32, maxSize int) ([]byte, error) {
	if err := CheckDir(dir, ownerUID); err != nil {
		return nil, err
	}

	path := filepath.Join(dir, name)

	fd, err := unix.Openat2(unix.AT_FDCWD, path, &unix.OpenHow{
		Flags:   unix.O_RDONLY | unix.O_CLOEXEC | unix.O_NONBLOCK,
		Resolve: unix.RESOLVE_NO_SYMLINKS | unix.RESOLVE_NO_MAGICLINKS,
	})
	if err != nil {
		if errors.Is(err, unix.ENOENT) {
			return nil, os.ErrNotExist
		}

		return nil, fmt.Errorf("%w: open %s: %w", ErrUnsafe, name, err)
	}

	f := os.NewFile(uintptr(fd), path)
	defer f.Close()

	var st unix.Stat_t
	if err := unix.Fstat(fd, &st); err != nil {
		return nil, fmt.Errorf("%w: stat %s: %w", ErrUnsafe, name, err)
	}

	if st.Mode&unix.S_IFMT != unix.S_IFREG || st.Uid != ownerUID || st.Mode&0o077 != 0 {
		return nil, fmt.Errorf("%w: %s must be a regular file owned by uid %d with mode 0600", ErrUnsafe, name, ownerUID)
	}

	raw, err := io.ReadAll(io.LimitReader(f, int64(maxSize)+1))
	if err != nil {
		return nil, fmt.Errorf("statefile: read %s: %w", name, err)
	}

	if len(raw) > maxSize {
		return nil, fmt.Errorf("%w: %s is larger than %d bytes", ErrTooLarge, name, maxSize)
	}

	return raw, nil
}

// Write replaces dir/name atomically with data, mode 0600.
func Write(dir, name string, data []byte) error {
	path := filepath.Join(dir, name)
	tmp := filepath.Join(dir, "."+name+".tmp")

	// A stale temp file from a crash is replaced, never reused or followed.
	_ = os.Remove(tmp)

	f, err := os.OpenFile(tmp, os.O_WRONLY|os.O_CREATE|os.O_EXCL|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0o600)
	if err != nil {
		return fmt.Errorf("statefile: write %s: %w", name, err)
	}

	if _, err := f.Write(data); err != nil {
		_ = f.Close()
		_ = os.Remove(tmp)

		return fmt.Errorf("statefile: write %s: %w", name, err)
	}

	if err := f.Sync(); err != nil {
		_ = f.Close()
		_ = os.Remove(tmp)

		return fmt.Errorf("statefile: sync %s: %w", name, err)
	}

	if err := f.Close(); err != nil {
		_ = os.Remove(tmp)

		return fmt.Errorf("statefile: close %s: %w", name, err)
	}

	if err := os.Rename(tmp, path); err != nil {
		_ = os.Remove(tmp)

		return fmt.Errorf("statefile: replace %s: %w", name, err)
	}

	d, err := os.Open(dir)
	if err != nil {
		return fmt.Errorf("statefile: open directory: %w", err)
	}

	defer d.Close()

	if err := d.Sync(); err != nil {
		return fmt.Errorf("statefile: sync directory: %w", err)
	}

	return nil
}
