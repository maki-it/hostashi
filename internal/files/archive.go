// SPDX-License-Identifier: AGPL-3.0-or-later

package files

import (
	"archive/zip"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"golang.org/x/sys/unix"
)

// Limits of ZIP extraction (ADR-0040).
const (
	MaxZipEntries  = 100000
	MaxZipUnpacked = 10 << 30
)

// ZipResult tells what a ZIP download left out.
type ZipResult struct {
	Files   int `json:"files"`
	Skipped int `json:"skipped"`
}

// CheckSelection checks that every entry of paths exists, so a download can
// fail with a status before the first byte is sent.
func (r *Root) CheckSelection(paths []string) ([]string, error) {
	sel, err := cleanSelection(paths)
	if err != nil {
		return nil, err
	}

	for _, p := range sel {
		if _, err := r.Stat(p); err != nil {
			return nil, err
		}
	}

	return sel, nil
}

// WriteZip writes the entries of paths as one ZIP to w, folders with their
// content, while it reads them: nothing is stored in between, so the
// download uses no quota. Names in the archive are relative to each entry's
// folder. Symlinks, devices, sockets and FIFOs are left out. An error after
// the first byte leaves a broken archive; the caller can only stop.
func (r *Root) WriteZip(w io.Writer, paths []string) (ZipResult, error) {
	var res ZipResult

	sel, err := cleanSelection(paths)
	if err != nil {
		return res, err
	}

	zw := zip.NewWriter(w)

	for _, p := range sel {
		dir, name, err := r.openParent(p)
		if err != nil {
			return res, err
		}

		err = zipEntry(zw, dir, name, name, 1, &res)

		_ = unix.Close(dir)

		if err != nil {
			return res, err
		}
	}

	if err := zw.Close(); err != nil {
		return res, fmt.Errorf("files: zip: %w", err)
	}

	return res, nil
}

func zipEntry(zw *zip.Writer, dir int, name, arcName string, depth int, res *ZipResult) error {
	if depth > maxDepth {
		return fmt.Errorf("%w: deeper than %d folders", ErrTooLarge, maxDepth)
	}

	st, err := lstatAt(dir, name)
	if err != nil {
		return mapErr("stat", arcName, err)
	}

	mtime := time.Unix(st.Mtim.Sec, st.Mtim.Nsec)

	switch st.Mode & unix.S_IFMT {
	case unix.S_IFDIR:
		h := &zip.FileHeader{Name: arcName + "/", Modified: mtime}
		h.SetMode(os.ModeDir | os.FileMode(st.Mode&0o777))

		if _, err := zw.CreateHeader(h); err != nil {
			return fmt.Errorf("files: zip: %w", err)
		}

		fd, err := unix.Openat2(dir, name, &unix.OpenHow{Flags: dirFlags, Resolve: noEscape})
		if err != nil {
			return mapErr("open folder", arcName, err)
		}

		f := os.NewFile(uintptr(fd), name)
		defer f.Close()

		names, err := f.Readdirnames(-1)
		if err != nil {
			return mapErr("list", arcName, err)
		}

		for _, n := range names {
			if ValidName(n) != nil {
				res.Skipped++

				continue
			}

			if err := zipEntry(zw, fd, n, arcName+"/"+n, depth+1, res); err != nil {
				return err
			}
		}

		return nil
	case unix.S_IFREG:
		fd, err := unix.Openat2(dir, name, &unix.OpenHow{Flags: fileFlags, Resolve: noEscape})
		if err != nil {
			return mapErr("open", arcName, err)
		}

		f := os.NewFile(uintptr(fd), name)
		defer f.Close()

		h := &zip.FileHeader{Name: arcName, Modified: mtime, Method: zip.Deflate}
		h.SetMode(os.FileMode(st.Mode & 0o777))

		zf, err := zw.CreateHeader(h)
		if err != nil {
			return fmt.Errorf("files: zip: %w", err)
		}

		if _, err := io.Copy(zf, f); err != nil {
			return mapErr("read", arcName, err)
		}

		res.Files++

		return nil
	default:
		res.Skipped++

		return nil
	}
}

// ExtractOptions control Extract.
type ExtractOptions struct {
	// Overwrite replaces existing files; without it they are kept.
	Overwrite bool
	// Progress, if set, is called now and then with the entries done.
	Progress func(done, total int)
}

// ExtractResult counts what an extraction did.
type ExtractResult struct {
	Files int `json:"files"`
	Dirs  int `json:"dirs"`
	// Kept counts existing files that were not overwritten.
	Kept int `json:"kept"`
	// Skipped counts symlinks and other entries that are not files or
	// folders.
	Skipped int `json:"skipped"`
}

// zipName checks the name of an archive entry. Absolute names and names
// with ".." are refused for the whole archive, not skipped.
func zipName(n string) (string, error) {
	if strings.HasPrefix(n, "/") {
		return "", fmt.Errorf("%w: archive entry %q has an absolute path", ErrInvalidPath, n)
	}

	c, err := CleanPath(n)
	if err != nil {
		return "", fmt.Errorf("archive entry %q: %w", n, err)
	}

	if c == "" {
		return "", fmt.Errorf("%w: archive entry %q has no name", ErrInvalidPath, n)
	}

	return c, nil
}

// Extract unpacks the ZIP file p into the folder to. The archive is checked
// first: at most MaxZipEntries entries and MaxZipUnpacked bytes, and no
// absolute or ".." names. Symlink entries are skipped. Every file is
// written to a temporary name and renamed, beneath to only.
func (r *Root) Extract(p, to string, o ExtractOptions) (ExtractResult, error) {
	var res ExtractResult

	f, e, err := r.OpenFile(p)
	if err != nil {
		return res, err
	}
	defer f.Close()

	zr, err := zip.NewReader(f, e.Size)
	if err != nil {
		return res, fmt.Errorf("%w: %q is not a ZIP file: %w", ErrNotRegular, p, err)
	}

	if len(zr.File) > MaxZipEntries {
		return res, fmt.Errorf("%w: more than %d entries", ErrTooLarge, MaxZipEntries)
	}

	var total uint64

	names := make([]string, len(zr.File))

	for i, zf := range zr.File {
		n, err := zipName(zf.Name)
		if err != nil {
			return res, err
		}

		names[i] = n
		total += zf.UncompressedSize64

		if total > MaxZipUnpacked {
			return res, fmt.Errorf("%w: more than %d bytes unpacked", ErrTooLarge, MaxZipUnpacked)
		}
	}

	ct, err := CleanPath(to)
	if err != nil {
		return res, err
	}

	base, err := r.openDir(ct)
	if err != nil {
		return res, err
	}
	defer unix.Close(base)

	x := &extractor{base: base, overwrite: o.Overwrite, budget: MaxZipUnpacked}
	last := time.Now()

	for i, zf := range zr.File {
		if err := x.entry(zf, names[i], &res); err != nil {
			return res, err
		}

		if o.Progress != nil && time.Since(last) >= time.Second {
			o.Progress(i+1, len(zr.File))

			last = time.Now()
		}
	}

	return res, nil
}

type extractor struct {
	base      int
	overwrite bool
	budget    int64
}

// mkdirs makes the folders of rel beneath base and returns the last one.
func (x *extractor) mkdirs(rel string, res *ExtractResult) (int, error) {
	fd, err := unix.Dup(x.base)
	if err != nil {
		return -1, fmt.Errorf("files: dup: %w", err)
	}

	if rel == "" {
		return fd, nil
	}

	for _, c := range strings.Split(rel, "/") {
		err := unix.Mkdirat(fd, c, 0o755)

		switch {
		case err == nil:
			res.Dirs++
		case errors.Is(err, unix.EEXIST):
		default:
			_ = unix.Close(fd)

			return -1, mapErr("create folder", rel, err)
		}

		next, err := unix.Openat2(fd, c, &unix.OpenHow{Flags: dirFlags, Resolve: noEscape})

		_ = unix.Close(fd)

		if err != nil {
			return -1, mapErr("open folder", rel, err)
		}

		fd = next
	}

	return fd, nil
}

func (x *extractor) entry(zf *zip.File, name string, res *ExtractResult) error {
	mode := zf.Mode()

	switch {
	case mode.IsDir():
		fd, err := x.mkdirs(name, res)
		if err == nil {
			_ = unix.Close(fd)
		}

		return err
	case !mode.IsRegular():
		res.Skipped++

		return nil
	}

	parent, base := split(name)

	dir, err := x.mkdirs(parent, res)
	if err != nil {
		return err
	}
	defer unix.Close(dir)

	if old, err := lstatAt(dir, base); err == nil {
		if old.Mode&unix.S_IFMT != unix.S_IFREG {
			return fmt.Errorf("%w: %q exists and is not a file", ErrExists, name)
		}

		if !x.overwrite {
			res.Kept++

			return nil
		}
	}

	perm := uint32(mode.Perm())
	if perm == 0 {
		perm = 0o644
	}

	rc, err := zf.Open()
	if err != nil {
		return fmt.Errorf("%w: archive entry %q: %w", ErrNotRegular, name, err)
	}
	defer rc.Close()

	tmp := tmpName()

	fd, err := unix.Openat(dir, tmp, unix.O_WRONLY|unix.O_CREAT|unix.O_EXCL|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0o600)
	if err != nil {
		return mapErr("create", name, err)
	}

	out := os.NewFile(uintptr(fd), tmp)

	fail := func(err error) error {
		_ = out.Close()
		_ = unix.Unlinkat(dir, tmp, 0)

		return err
	}

	// The budget holds even if an entry's header lies about its size.
	n, err := io.Copy(out, io.LimitReader(rc, x.budget+1))
	if err != nil {
		if errors.Is(err, zip.ErrChecksum) || errors.Is(err, zip.ErrFormat) {
			return fail(fmt.Errorf("%w: archive entry %q is damaged: %w", ErrNotRegular, name, err))
		}

		return fail(mapErr("write", name, err))
	}

	x.budget -= n
	if x.budget < 0 {
		return fail(fmt.Errorf("%w: more than %d bytes unpacked", ErrTooLarge, MaxZipUnpacked))
	}

	if err := unix.Fchmod(fd, perm); err != nil {
		return fail(mapErr("chmod", name, err))
	}

	if err := out.Close(); err != nil {
		_ = unix.Unlinkat(dir, tmp, 0)

		return mapErr("close", name, err)
	}

	flags := uint(unix.RENAME_NOREPLACE)
	if x.overwrite {
		flags = 0
	}

	if err := unix.Renameat2(dir, tmp, dir, base, flags); err != nil {
		_ = unix.Unlinkat(dir, tmp, 0)

		if errors.Is(err, unix.EEXIST) {
			res.Kept++

			return nil
		}

		return mapErr("rename", name, err)
	}

	res.Files++

	return nil
}
