// SPDX-License-Identifier: AGPL-3.0-or-later

package files

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"sort"
	"strconv"
	"time"

	"golang.org/x/sys/unix"
)

// Limits of one request.
const (
	// MaxListEntries caps one folder listing; a bigger folder is listed in
	// part and marked as truncated.
	MaxListEntries = 20000
	// MaxSelection caps the entries of one move, copy, delete or mode change.
	MaxSelection = 1000
	// MaxEditBytes is the largest file the editor opens and saves.
	MaxEditBytes = 1 << 20
	// maxDepth caps the folder depth of a copy or delete.
	maxDepth = 128
)

// Errors of the file operations. The HTTP layer maps them to status codes.
var (
	ErrNotFound    = errors.New("files: not found")
	ErrExists      = errors.New("files: already exists")
	ErrNotDir      = errors.New("files: not a folder")
	ErrNotRegular  = errors.New("files: not a regular file")
	ErrSymlink     = errors.New("files: symlinks are not followed")
	ErrChanged     = errors.New("files: changed since it was opened")
	ErrTooLarge    = errors.New("files: too large")
	ErrDenied      = errors.New("files: permission denied")
	ErrNoSpace     = errors.New("files: no space left or quota exceeded")
	ErrInvalidMode = errors.New("files: invalid mode")
	ErrIntoItself  = errors.New("files: cannot move or copy a folder into itself")
	ErrHome        = errors.New("files: the home folder itself cannot be changed")
	ErrUnsafeHome  = errors.New("files: home folder failed safety checks")
)

const (
	dirFlags  = unix.O_RDONLY | unix.O_DIRECTORY | unix.O_CLOEXEC
	fileFlags = unix.O_RDONLY | unix.O_NOFOLLOW | unix.O_NONBLOCK | unix.O_NOCTTY | unix.O_CLOEXEC
	noEscape  = unix.RESOLVE_BENEATH | unix.RESOLVE_NO_SYMLINKS | unix.RESOLVE_NO_MAGICLINKS | unix.RESOLVE_NO_XDEV
	tmpPrefix = ".hostashi-"
)

// Root is an open site home. All paths are relative to it.
type Root struct {
	fd int
}

// Open opens the home at path. It must be a directory owned by the calling
// user; the path itself is resolved without following symlinks.
func Open(path string) (*Root, error) {
	fd, err := unix.Openat2(unix.AT_FDCWD, path, &unix.OpenHow{Flags: dirFlags, Resolve: unix.RESOLVE_NO_SYMLINKS | unix.RESOLVE_NO_MAGICLINKS})
	if err != nil {
		return nil, fmt.Errorf("%w: open %s: %w", ErrUnsafeHome, path, err)
	}

	var st unix.Stat_t
	if err := unix.Fstat(fd, &st); err != nil {
		_ = unix.Close(fd)

		return nil, fmt.Errorf("%w: stat: %w", ErrUnsafeHome, err)
	}

	if int(st.Uid) != os.Getuid() {
		_ = unix.Close(fd)

		return nil, fmt.Errorf("%w: %s is owned by uid %d, not %d", ErrUnsafeHome, path, st.Uid, os.Getuid())
	}

	return &Root{fd: fd}, nil
}

// Close releases the home.
func (r *Root) Close() error { return unix.Close(r.fd) }

// Entry is one item of a folder.
type Entry struct {
	Name     string    `json:"name"`
	Type     string    `json:"type"` // file, dir, symlink or other
	Size     int64     `json:"size"`
	Modified time.Time `json:"modified"`
	// Mode is the permission bits in octal, for example "0644".
	Mode string `json:"mode"`
	// Target is a symlink's target, shown only.
	Target string `json:"target,omitempty"`
	// Version changes whenever the file changes; the editor sends it back
	// when it saves.
	Version string `json:"version,omitempty"`
}

// Listing is the content of one folder.
type Listing struct {
	Path    string  `json:"path"`
	Entries []Entry `json:"entries"`
	// Truncated is set when the folder has more than MaxListEntries items.
	Truncated bool `json:"truncated"`
	// Unnamed counts items whose names are not valid UTF-8; they cannot be
	// addressed here (use SFTP).
	Unnamed int `json:"unnamed"`
}

func typeOf(mode uint32) string {
	switch mode & unix.S_IFMT {
	case unix.S_IFREG:
		return "file"
	case unix.S_IFDIR:
		return "dir"
	case unix.S_IFLNK:
		return "symlink"
	default:
		return "other"
	}
}

func version(st *unix.Stat_t) string {
	return strconv.FormatUint(st.Ino, 16) + "-" + strconv.FormatInt(st.Size, 16) + "-" +
		strconv.FormatInt(st.Mtim.Sec, 16) + "." + strconv.FormatInt(st.Mtim.Nsec, 16)
}

func entryOf(name string, st *unix.Stat_t) Entry {
	e := Entry{
		Name:     name,
		Type:     typeOf(st.Mode),
		Size:     st.Size,
		Modified: time.Unix(st.Mtim.Sec, st.Mtim.Nsec).UTC(),
		Mode:     fmt.Sprintf("%04o", st.Mode&0o7777),
	}
	if e.Type == "file" {
		e.Version = version(st)
	}

	return e
}

// mapErr turns an errno into the package's errors, keeping the original.
func mapErr(op, p string, err error) error {
	var kind error

	switch {
	case errors.Is(err, unix.ENOENT):
		kind = ErrNotFound
	case errors.Is(err, unix.EEXIST), errors.Is(err, unix.ENOTEMPTY):
		kind = ErrExists
	case errors.Is(err, unix.ELOOP), errors.Is(err, unix.EXDEV):
		kind = ErrSymlink
	case errors.Is(err, unix.ENOTDIR):
		kind = ErrNotDir
	case errors.Is(err, unix.EACCES), errors.Is(err, unix.EPERM):
		kind = ErrDenied
	case errors.Is(err, unix.ENOSPC), errors.Is(err, unix.EDQUOT):
		kind = ErrNoSpace
	case errors.Is(err, unix.ENAMETOOLONG):
		kind = ErrInvalidPath
	default:
		return fmt.Errorf("files: %s %q: %w", op, p, err)
	}

	return fmt.Errorf("%w: %s %q: %w", kind, op, p, err)
}

// openDir opens the folder p ("" is the home).
func (r *Root) openDir(p string) (int, error) {
	if p == "" {
		fd, err := unix.Dup(r.fd)
		if err != nil {
			return -1, fmt.Errorf("files: dup: %w", err)
		}

		unix.CloseOnExec(fd)

		return fd, nil
	}

	fd, err := unix.Openat2(r.fd, p, &unix.OpenHow{Flags: dirFlags, Resolve: noEscape})
	if err != nil {
		return -1, mapErr("open folder", p, err)
	}

	return fd, nil
}

// openParent opens the folder that holds p and returns p's last component.
// p must not be the home.
func (r *Root) openParent(p string) (int, string, error) {
	p, err := CleanPath(p)
	if err != nil {
		return -1, "", err
	}

	if p == "" {
		return -1, "", ErrHome
	}

	parent, name := split(p)

	fd, err := r.openDir(parent)
	if err != nil {
		return -1, "", err
	}

	return fd, name, nil
}

func lstatAt(dir int, name string) (unix.Stat_t, error) {
	var st unix.Stat_t

	err := unix.Fstatat(dir, name, &st, unix.AT_SYMLINK_NOFOLLOW)

	return st, err
}

// Stat describes p without following a symlink.
func (r *Root) Stat(p string) (Entry, error) {
	p, err := CleanPath(p)
	if err != nil {
		return Entry{}, err
	}

	if p == "" {
		var st unix.Stat_t
		if err := unix.Fstat(r.fd, &st); err != nil {
			return Entry{}, mapErr("stat", p, err)
		}

		return entryOf("", &st), nil
	}

	dir, name, err := r.openParent(p)
	if err != nil {
		return Entry{}, err
	}
	defer unix.Close(dir)

	st, err := lstatAt(dir, name)
	if err != nil {
		return Entry{}, mapErr("stat", p, err)
	}

	return entryOf(name, &st), nil
}

// List returns the folder p, sorted by name.
func (r *Root) List(p string) (Listing, error) {
	p, err := CleanPath(p)
	if err != nil {
		return Listing{}, err
	}

	fd, err := r.openDir(p)
	if err != nil {
		return Listing{}, err
	}

	f := os.NewFile(uintptr(fd), "dir")
	defer f.Close()

	out := Listing{Path: p, Entries: []Entry{}}

	for {
		names, err := f.Readdirnames(1024)
		for _, n := range names {
			if ValidName(n) != nil {
				out.Unnamed++

				continue
			}

			if len(out.Entries) >= MaxListEntries {
				out.Truncated = true

				continue
			}

			st, err := lstatAt(fd, n)
			if err != nil {
				continue // removed meanwhile
			}

			e := entryOf(n, &st)
			if e.Type == "symlink" {
				e.Target = readlink(fd, n)
			}

			out.Entries = append(out.Entries, e)
		}

		if errors.Is(err, io.EOF) {
			break
		}

		if err != nil {
			return Listing{}, mapErr("list", p, err)
		}
	}

	sort.Slice(out.Entries, func(i, j int) bool { return out.Entries[i].Name < out.Entries[j].Name })

	return out, nil
}

func readlink(dir int, name string) string {
	buf := make([]byte, MaxPathBytes)

	n, err := unix.Readlinkat(dir, name, buf)
	if err != nil {
		return ""
	}

	return string(buf[:n])
}

// OpenFile opens the regular file p for reading. The caller closes it.
func (r *Root) OpenFile(p string) (*os.File, Entry, error) {
	p, err := CleanPath(p)
	if err != nil {
		return nil, Entry{}, err
	}

	if p == "" {
		return nil, Entry{}, ErrNotRegular
	}

	fd, err := unix.Openat2(r.fd, p, &unix.OpenHow{Flags: fileFlags, Resolve: noEscape})
	if err != nil {
		return nil, Entry{}, mapErr("open", p, err)
	}

	var st unix.Stat_t
	if err := unix.Fstat(fd, &st); err != nil {
		_ = unix.Close(fd)

		return nil, Entry{}, mapErr("stat", p, err)
	}

	if st.Mode&unix.S_IFMT != unix.S_IFREG {
		_ = unix.Close(fd)

		return nil, Entry{}, fmt.Errorf("%w: %q", ErrNotRegular, p)
	}

	// The file was opened non-blocking so that a FIFO could not hang the
	// open; reads of a regular file do not block either way.
	_, name := split(p)

	return os.NewFile(uintptr(fd), name), entryOf(name, &st), nil
}

func tmpName() string {
	b := make([]byte, 8)
	_, _ = rand.Read(b)

	return tmpPrefix + hex.EncodeToString(b) + ".tmp"
}

// WriteOptions control WriteFile.
type WriteOptions struct {
	// Version must match the file's current version; empty means the file
	// must not exist yet.
	Version string
	// Max is the largest accepted content; 0 means MaxEditBytes.
	Max int64
}

// WriteFile replaces or creates the regular file p with the content of src.
// It writes a temporary file next to it and renames it at the end, so a
// reader never sees half a file. A replaced file keeps its mode.
func (r *Root) WriteFile(p string, src io.Reader, o WriteOptions) (Entry, error) {
	dir, name, err := r.openParent(p)
	if err != nil {
		return Entry{}, err
	}
	defer unix.Close(dir)

	limit := o.Max
	if limit <= 0 {
		limit = MaxEditBytes
	}

	mode := uint32(0o644)

	old, err := lstatAt(dir, name)

	switch {
	case err == nil && old.Mode&unix.S_IFMT != unix.S_IFREG:
		return Entry{}, fmt.Errorf("%w: %q", ErrNotRegular, p)
	case err == nil && o.Version == "":
		return Entry{}, fmt.Errorf("%w: %q", ErrExists, p)
	case err == nil && version(&old) != o.Version:
		return Entry{}, fmt.Errorf("%w: %q", ErrChanged, p)
	case err == nil:
		mode = old.Mode & 0o777
	case !errors.Is(err, unix.ENOENT):
		return Entry{}, mapErr("stat", p, err)
	case o.Version != "":
		return Entry{}, fmt.Errorf("%w: %q was removed", ErrChanged, p)
	}

	tmp := tmpName()

	fd, err := unix.Openat(dir, tmp, unix.O_WRONLY|unix.O_CREAT|unix.O_EXCL|unix.O_NOFOLLOW|unix.O_CLOEXEC, mode)
	if err != nil {
		return Entry{}, mapErr("create", p, err)
	}

	f := os.NewFile(uintptr(fd), tmp)

	fail := func(err error) (Entry, error) {
		_ = f.Close()
		_ = unix.Unlinkat(dir, tmp, 0)

		return Entry{}, err
	}

	n, err := io.Copy(f, io.LimitReader(src, limit+1))
	if err != nil {
		return fail(mapErr("write", p, err))
	}

	if n > limit {
		return fail(fmt.Errorf("%w: more than %d bytes", ErrTooLarge, limit))
	}

	// The umask may have cleared bits of the old mode.
	if err := unix.Fchmod(fd, mode); err != nil {
		return fail(mapErr("chmod", p, err))
	}

	if err := f.Sync(); err != nil {
		return fail(mapErr("sync", p, err))
	}

	var st unix.Stat_t
	if err := unix.Fstat(fd, &st); err != nil {
		return fail(mapErr("stat", p, err))
	}

	if err := f.Close(); err != nil {
		_ = unix.Unlinkat(dir, tmp, 0)

		return Entry{}, mapErr("close", p, err)
	}

	flags := uint(0)
	if o.Version == "" {
		flags = unix.RENAME_NOREPLACE
	}

	if err := unix.Renameat2(dir, tmp, dir, name, flags); err != nil {
		_ = unix.Unlinkat(dir, tmp, 0)

		return Entry{}, mapErr("rename", p, err)
	}

	return entryOf(name, &st), nil
}

// Mkdir creates the folder p.
func (r *Root) Mkdir(p string) error {
	dir, name, err := r.openParent(p)
	if err != nil {
		return err
	}
	defer unix.Close(dir)

	if err := unix.Mkdirat(dir, name, 0o755); err != nil {
		return mapErr("create folder", p, err)
	}

	return nil
}

// CreateFile creates the empty file p.
func (r *Root) CreateFile(p string) error {
	dir, name, err := r.openParent(p)
	if err != nil {
		return err
	}
	defer unix.Close(dir)

	fd, err := unix.Openat(dir, name, unix.O_WRONLY|unix.O_CREAT|unix.O_EXCL|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0o644)
	if err != nil {
		return mapErr("create file", p, err)
	}

	return unix.Close(fd)
}

// Rename gives the entry from the path to. It never replaces an entry.
func (r *Root) Rename(from, to string) error {
	cf, err := CleanPath(from)
	if err != nil {
		return err
	}

	ct, err := CleanPath(to)
	if err != nil {
		return err
	}

	if cf != "" && ct != "" && within(ct, cf) && ct != cf {
		return ErrIntoItself
	}

	sd, sn, err := r.openParent(cf)
	if err != nil {
		return err
	}
	defer unix.Close(sd)

	dd, dn, err := r.openParent(ct)
	if err != nil {
		return err
	}
	defer unix.Close(dd)

	if err := unix.Renameat2(sd, sn, dd, dn, unix.RENAME_NOREPLACE); err != nil {
		return mapErr("rename", cf, err)
	}

	return nil
}

// cleanSelection checks a selection: 1 to MaxSelection paths, none of them
// the home, no duplicates.
func cleanSelection(paths []string) ([]string, error) {
	if len(paths) == 0 || len(paths) > MaxSelection {
		return nil, fmt.Errorf("%w: select 1 to %d entries", ErrInvalidPath, MaxSelection)
	}

	out := make([]string, 0, len(paths))
	seen := make(map[string]bool, len(paths))

	for _, p := range paths {
		c, err := CleanPath(p)
		if err != nil {
			return nil, err
		}

		if c == "" {
			return nil, ErrHome
		}

		if !seen[c] {
			seen[c] = true

			out = append(out, c)
		}
	}

	return out, nil
}

// Move moves each entry of paths into the folder to, keeping its name. It
// stops at the first failure; entries before it have moved.
func (r *Root) Move(paths []string, to string) error {
	sel, err := cleanSelection(paths)
	if err != nil {
		return err
	}

	ct, err := CleanPath(to)
	if err != nil {
		return err
	}

	for _, p := range sel {
		_, name := split(p)
		if err := r.Rename(p, joinPath(ct, name)); err != nil {
			return err
		}
	}

	return nil
}

func joinPath(dir, name string) string {
	if dir == "" {
		return name
	}

	return dir + "/" + name
}

// CopyResult tells what a copy left out.
type CopyResult struct {
	// Skipped counts device files, sockets and FIFOs, which are not copied.
	Skipped int `json:"skipped"`
}

// Copy copies each entry of paths into the folder to, keeping its name.
// Folders are copied with their content, symlinks as symlinks. It stops at
// the first failure and leaves what it copied so far.
func (r *Root) Copy(paths []string, to string) (CopyResult, error) {
	var res CopyResult

	sel, err := cleanSelection(paths)
	if err != nil {
		return res, err
	}

	ct, err := CleanPath(to)
	if err != nil {
		return res, err
	}

	for _, p := range sel {
		if within(ct, p) {
			return res, ErrIntoItself
		}
	}

	dst, err := r.openDir(ct)
	if err != nil {
		return res, err
	}
	defer unix.Close(dst)

	for _, p := range sel {
		sd, name, err := r.openParent(p)
		if err != nil {
			return res, err
		}

		err = copyEntry(sd, name, dst, name, 1, &res)

		_ = unix.Close(sd)

		if err != nil {
			return res, err
		}
	}

	return res, nil
}

func copyEntry(sd int, name string, dd int, dname string, depth int, res *CopyResult) error {
	if depth > maxDepth {
		return fmt.Errorf("%w: deeper than %d folders", ErrTooLarge, maxDepth)
	}

	st, err := lstatAt(sd, name)
	if err != nil {
		return mapErr("stat", name, err)
	}

	perm := st.Mode & 0o777

	switch st.Mode & unix.S_IFMT {
	case unix.S_IFREG:
		return copyFile(sd, name, dd, dname, perm)
	case unix.S_IFLNK:
		target := readlink(sd, name)
		if target == "" {
			return fmt.Errorf("files: read symlink %q", name)
		}

		if err := unix.Symlinkat(target, dd, dname); err != nil {
			return mapErr("copy symlink", dname, err)
		}

		return nil
	case unix.S_IFDIR:
		return copyDir(sd, name, dd, dname, perm, depth, res)
	default:
		res.Skipped++

		return nil
	}
}

func copyFile(sd int, name string, dd int, dname string, perm uint32) error {
	in, err := unix.Openat2(sd, name, &unix.OpenHow{Flags: fileFlags, Resolve: noEscape})
	if err != nil {
		return mapErr("open", name, err)
	}

	src := os.NewFile(uintptr(in), name)
	defer src.Close()

	var st unix.Stat_t
	if err := unix.Fstat(in, &st); err != nil || st.Mode&unix.S_IFMT != unix.S_IFREG {
		return fmt.Errorf("%w: %q changed during the copy", ErrNotRegular, name)
	}

	out, err := unix.Openat(dd, dname, unix.O_WRONLY|unix.O_CREAT|unix.O_EXCL|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0o600)
	if err != nil {
		return mapErr("create", dname, err)
	}

	dst := os.NewFile(uintptr(out), dname)

	if _, err := io.Copy(dst, src); err != nil {
		_ = dst.Close()

		return mapErr("copy", dname, err)
	}

	if err := unix.Fchmod(out, perm); err != nil {
		_ = dst.Close()

		return mapErr("chmod", dname, err)
	}

	if err := dst.Close(); err != nil {
		return mapErr("close", dname, err)
	}

	return nil
}

func copyDir(sd int, name string, dd int, dname string, perm uint32, depth int, res *CopyResult) error {
	in, err := unix.Openat2(sd, name, &unix.OpenHow{Flags: dirFlags, Resolve: noEscape})
	if err != nil {
		return mapErr("open folder", name, err)
	}

	src := os.NewFile(uintptr(in), name)
	defer src.Close()

	if err := unix.Mkdirat(dd, dname, 0o700); err != nil {
		return mapErr("create folder", dname, err)
	}

	out, err := unix.Openat2(dd, dname, &unix.OpenHow{Flags: dirFlags, Resolve: noEscape})
	if err != nil {
		return mapErr("open folder", dname, err)
	}
	defer unix.Close(out)

	names, err := src.Readdirnames(-1)
	if err != nil {
		return mapErr("list", name, err)
	}

	for _, n := range names {
		if err := copyEntry(in, n, out, n, depth+1, res); err != nil {
			return err
		}
	}

	if err := unix.Fchmod(out, perm); err != nil {
		return mapErr("chmod", dname, err)
	}

	return nil
}

// Delete removes each entry of paths, folders with their content. There is
// no recycle bin (ADR-0040). It stops at the first failure.
func (r *Root) Delete(paths []string) error {
	sel, err := cleanSelection(paths)
	if err != nil {
		return err
	}

	for _, p := range sel {
		dir, name, err := r.openParent(p)
		if err != nil {
			return err
		}

		if _, err := lstatAt(dir, name); err != nil {
			_ = unix.Close(dir)

			return mapErr("delete", p, err)
		}

		err = removeAll(dir, name, 1)

		_ = unix.Close(dir)

		if err != nil {
			return err
		}
	}

	return nil
}

// removeAll deletes name below parent using only fds and without following
// symlinks, so swapping a folder for a symlink during the walk cannot lead
// it elsewhere (same walk as internal/sites).
func removeAll(parent int, name string, depth int) error {
	if depth > maxDepth {
		return fmt.Errorf("%w: deeper than %d folders", ErrTooLarge, maxDepth)
	}

	err := unix.Unlinkat(parent, name, 0)
	if err == nil || errors.Is(err, unix.ENOENT) {
		return nil
	}

	if !errors.Is(err, unix.EISDIR) && !errors.Is(err, unix.EPERM) {
		return mapErr("delete", name, err)
	}

	dfd, err := unix.Openat2(parent, name, &unix.OpenHow{Flags: dirFlags, Resolve: noEscape})
	if err != nil {
		switch {
		case errors.Is(err, unix.ELOOP), errors.Is(err, unix.ENOTDIR):
			if e := unix.Unlinkat(parent, name, 0); e != nil && !errors.Is(e, unix.ENOENT) {
				return mapErr("delete", name, e)
			}

			return nil
		case errors.Is(err, unix.ENOENT):
			return nil
		default:
			return mapErr("open folder", name, err)
		}
	}

	f := os.NewFile(uintptr(dfd), name)

	names, err := f.Readdirnames(-1)
	if err != nil {
		_ = f.Close()

		return mapErr("list", name, err)
	}

	for _, n := range names {
		if err := removeAll(dfd, n, depth+1); err != nil {
			_ = f.Close()

			return err
		}
	}

	_ = f.Close()

	if err := unix.Unlinkat(parent, name, unix.AT_REMOVEDIR); err != nil && !errors.Is(err, unix.ENOENT) {
		return mapErr("delete", name, err)
	}

	return nil
}

// ParseMode reads permission bits in octal ("644" or "0644"). setuid,
// setgid and the sticky bit are refused (ADR-0040).
func ParseMode(s string) (uint32, error) {
	if s == "" || len(s) > 4 {
		return 0, fmt.Errorf("%w: %q", ErrInvalidMode, s)
	}

	m, err := strconv.ParseUint(s, 8, 32)
	if err != nil || m&^0o777 != 0 {
		return 0, fmt.Errorf("%w: %q (only permission bits 0000 to 0777)", ErrInvalidMode, s)
	}

	return uint32(m), nil
}

// Chmod sets the permission bits of each entry of paths. Symlinks have no
// mode of their own and are refused. It stops at the first failure.
func (r *Root) Chmod(paths []string, mode uint32) error {
	if mode&^0o777 != 0 {
		return ErrInvalidMode
	}

	sel, err := cleanSelection(paths)
	if err != nil {
		return err
	}

	for _, p := range sel {
		dir, name, err := r.openParent(p)
		if err != nil {
			return err
		}

		// fchmodat2 with AT_SYMLINK_NOFOLLOW (Linux 6.6) changes the entry
		// itself and fails on a symlink, even one swapped in meanwhile.
		err = unix.Fchmodat(dir, name, mode, unix.AT_SYMLINK_NOFOLLOW)

		_ = unix.Close(dir)

		if errors.Is(err, unix.EOPNOTSUPP) {
			return fmt.Errorf("%w: %q", ErrSymlink, p)
		}

		if err != nil {
			return mapErr("chmod", p, err)
		}
	}

	return nil
}
