// SPDX-License-Identifier: AGPL-3.0-or-later

package files

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"regexp"
	"strings"
	"time"

	"golang.org/x/sys/unix"
)

// Uploads come in chunks (ADR-0040), so large files fit through nginx's
// body limit and an interrupted upload can go on. The helper keeps no
// state: the part file next to the target holds what arrived, and the
// client sends the target, the id, the offset and the total size with
// every chunk. The part becomes the target with the last chunk.

// MaxChunk is the largest chunk of an upload.
const MaxChunk = 8 << 20

// partMaxAge is how old an abandoned part file gets before the next upload
// into the same folder removes it.
const partMaxAge = 24 * time.Hour

const partPrefix = tmpPrefix + "upload-"

var uploadIDRe = regexp.MustCompile(`^[0-9a-f]{16}$`)

// UploadOptions control StartUpload.
type UploadOptions struct {
	// Size is the file's total size in bytes.
	Size int64
	// Overwrite allows replacing an existing file.
	Overwrite bool
	// Parents creates missing folders on the way (folder uploads).
	Parents bool
}

// Upload is the state of an upload after a call.
type Upload struct {
	ID       string `json:"id"`
	Received int64  `json:"received"`
	Done     bool   `json:"done"`
	Entry    *Entry `json:"entry,omitempty"`
}

func partName(id string) string { return partPrefix + id + ".part" }

// StartUpload checks the target p and creates an empty part file next to
// it. With Parents it creates the missing folders first.
func (r *Root) StartUpload(p string, o UploadOptions) (Upload, error) {
	if o.Size < 0 {
		return Upload{}, fmt.Errorf("%w: negative size", ErrInvalidPath)
	}

	cp, err := CleanPath(p)
	if err != nil {
		return Upload{}, err
	}

	if cp == "" {
		return Upload{}, ErrHome
	}

	if o.Parents {
		if err := r.mkdirAll(parentOf(cp)); err != nil {
			return Upload{}, err
		}
	}

	dir, name, err := r.openParent(cp)
	if err != nil {
		return Upload{}, err
	}
	defer unix.Close(dir)

	if err := checkTarget(dir, name, o.Overwrite); err != nil {
		return Upload{}, fmt.Errorf("%w: %q", err, cp)
	}

	removeOldParts(dir)

	b := make([]byte, 8)
	_, _ = rand.Read(b)
	id := hex.EncodeToString(b)

	fd, err := unix.Openat(dir, partName(id), unix.O_WRONLY|unix.O_CREAT|unix.O_EXCL|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0o600)
	if err != nil {
		return Upload{}, mapErr("create", cp, err)
	}

	_ = unix.Close(fd)

	if o.Size == 0 {
		return r.finish(dir, cp, name, id, o.Overwrite)
	}

	return Upload{ID: id}, nil
}

func parentOf(p string) string {
	d, _ := split(p)

	return d
}

// checkTarget allows a missing target, or an existing regular file with
// overwrite.
func checkTarget(dir int, name string, overwrite bool) error {
	st, err := lstatAt(dir, name)

	switch {
	case errors.Is(err, unix.ENOENT):
		return nil
	case err != nil:
		return mapErr("stat", name, err)
	case st.Mode&unix.S_IFMT != unix.S_IFREG:
		return ErrNotRegular
	case !overwrite:
		return ErrExists
	}

	return nil
}

// mkdirAll creates the folder p and its missing parents beneath the home.
func (r *Root) mkdirAll(p string) error {
	fd, err := r.openDir("")
	if err != nil {
		return err
	}

	if p == "" {
		return unix.Close(fd)
	}

	for _, c := range strings.Split(p, "/") {
		if err := unix.Mkdirat(fd, c, 0o755); err != nil && !errors.Is(err, unix.EEXIST) {
			_ = unix.Close(fd)

			return mapErr("create folder", p, err)
		}

		next, err := unix.Openat2(fd, c, &unix.OpenHow{Flags: dirFlags, Resolve: noEscape})

		_ = unix.Close(fd)

		if err != nil {
			return mapErr("open folder", p, err)
		}

		fd = next
	}

	return unix.Close(fd)
}

// removeOldParts deletes abandoned part files of the folder.
func removeOldParts(dir int) {
	d, err := unix.Dup(dir)
	if err != nil {
		return
	}

	f := os.NewFile(uintptr(d), "dir")
	defer f.Close()

	names, _ := f.Readdirnames(-1)
	cutoff := time.Now().Add(-partMaxAge).Unix()

	for _, n := range names {
		if !strings.HasPrefix(n, partPrefix) || !strings.HasSuffix(n, ".part") {
			continue
		}

		if st, err := lstatAt(dir, n); err == nil && st.Mode&unix.S_IFMT == unix.S_IFREG && st.Mtim.Sec < cutoff {
			_ = unix.Unlinkat(dir, n, 0)
		}
	}
}

// ChunkOptions name the upload a chunk belongs to.
type ChunkOptions struct {
	ID        string
	Offset    int64
	Size      int64
	Overwrite bool
}

// WriteChunk appends the chunk in src to the upload of p at Offset. The
// offset must be what arrived so far; otherwise ErrChanged tells the
// client to ask again (Received holds the part's size). With the last
// chunk the part becomes p.
func (r *Root) WriteChunk(p string, src io.Reader, o ChunkOptions) (Upload, error) {
	if !uploadIDRe.MatchString(o.ID) {
		return Upload{}, fmt.Errorf("%w: upload id", ErrInvalidPath)
	}

	if o.Offset < 0 || o.Size < 0 || o.Offset > o.Size {
		return Upload{}, fmt.Errorf("%w: offset %d of %d", ErrInvalidPath, o.Offset, o.Size)
	}

	cp, err := CleanPath(p)
	if err != nil {
		return Upload{}, err
	}

	dir, name, err := r.openParent(cp)
	if err != nil {
		return Upload{}, err
	}
	defer unix.Close(dir)

	fd, err := unix.Openat2(dir, partName(o.ID), &unix.OpenHow{Flags: unix.O_WRONLY | unix.O_NOFOLLOW | unix.O_CLOEXEC, Resolve: noEscape})
	if err != nil {
		if errors.Is(err, unix.ENOENT) {
			return Upload{}, fmt.Errorf("%w: upload %s is gone, start again", ErrNotFound, o.ID)
		}

		return Upload{}, mapErr("open upload", cp, err)
	}

	f := os.NewFile(uintptr(fd), partName(o.ID))

	var st unix.Stat_t
	if err := unix.Fstat(fd, &st); err != nil || st.Mode&unix.S_IFMT != unix.S_IFREG {
		_ = f.Close()

		return Upload{}, fmt.Errorf("%w: upload part", ErrNotRegular)
	}

	if st.Size != o.Offset {
		_ = f.Close()

		return Upload{ID: o.ID, Received: st.Size}, fmt.Errorf("%w: upload has %d bytes, chunk starts at %d", ErrChanged, st.Size, o.Offset)
	}

	limit := min(int64(MaxChunk), o.Size-o.Offset)

	if _, err := unix.Seek(fd, o.Offset, io.SeekStart); err != nil {
		_ = f.Close()

		return Upload{}, mapErr("seek", cp, err)
	}

	n, err := io.Copy(f, io.LimitReader(src, limit+1))
	if err == nil && n > limit {
		err = fmt.Errorf("%w: chunk larger than %d bytes or beyond the size", ErrTooLarge, limit)

		_ = unix.Ftruncate(fd, o.Offset)
	}

	if err != nil {
		_ = f.Close()

		if errors.Is(err, ErrTooLarge) {
			return Upload{}, err
		}

		return Upload{}, mapErr("write", cp, err)
	}

	received := o.Offset + n

	if received < o.Size {
		if err := f.Close(); err != nil {
			return Upload{}, mapErr("close", cp, err)
		}

		return Upload{ID: o.ID, Received: received}, nil
	}

	if err := f.Sync(); err != nil {
		_ = f.Close()

		return Upload{}, mapErr("sync", cp, err)
	}

	if err := f.Close(); err != nil {
		return Upload{}, mapErr("close", cp, err)
	}

	return r.finish(dir, cp, name, o.ID, o.Overwrite)
}

// finish gives the complete part its mode and renames it to the target.
func (r *Root) finish(dir int, cp, name, id string, overwrite bool) (Upload, error) {
	part := partName(id)

	if err := checkTarget(dir, name, overwrite); err != nil {
		_ = unix.Unlinkat(dir, part, 0)

		return Upload{}, fmt.Errorf("%w: %q", err, cp)
	}

	mode := uint32(0o644)
	if old, err := lstatAt(dir, name); err == nil {
		mode = old.Mode & 0o777
	}

	if err := unix.Fchmodat(dir, part, mode, unix.AT_SYMLINK_NOFOLLOW); err != nil {
		return Upload{}, mapErr("chmod", cp, err)
	}

	flags := uint(unix.RENAME_NOREPLACE)
	if overwrite {
		flags = 0
	}

	if err := unix.Renameat2(dir, part, dir, name, flags); err != nil {
		return Upload{}, mapErr("rename", cp, err)
	}

	st, err := lstatAt(dir, name)
	if err != nil {
		return Upload{}, mapErr("stat", cp, err)
	}

	e := entryOf(name, &st)

	return Upload{ID: id, Received: st.Size, Done: true, Entry: &e}, nil
}

// CancelUpload removes the part file of an upload.
func (r *Root) CancelUpload(p, id string) error {
	if !uploadIDRe.MatchString(id) {
		return fmt.Errorf("%w: upload id", ErrInvalidPath)
	}

	dir, _, err := r.openParent(p)
	if err != nil {
		return err
	}
	defer unix.Close(dir)

	if err := unix.Unlinkat(dir, partName(id), 0); err != nil && !errors.Is(err, unix.ENOENT) {
		return mapErr("cancel upload", p, err)
	}

	return nil
}
