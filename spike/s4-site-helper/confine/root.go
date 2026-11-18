package confine

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"sort"

	"golang.org/x/sys/unix"
)

// Limits protect the helper and the disk from absurd requests.
const (
	MaxDirEntries  = 10000
	MaxRemoveDepth = 128
)

// Root is a directory file descriptor plus the resolution policy. All methods
// are safe for concurrent use because they only use *at calls on the fd.
type Root struct {
	fd      int
	resolve uint64
}

// Options select the symlink policy.
type Options struct {
	// NoSymlinks refuses every symlink on the way (RESOLVE_NO_SYMLINKS).
	// Default: symlinks are followed as long as they stay beneath the root
	// (relative links such as public/storage -> ../storage/app/public work).
	NoSymlinks bool
}

// NewRoot takes ownership of fd, which must be an open directory.
func NewRoot(fd int, opt Options) (*Root, error) {
	var st unix.Stat_t
	if err := unix.Fstat(fd, &st); err != nil {
		return nil, err
	}
	if st.Mode&unix.S_IFMT != unix.S_IFDIR {
		return nil, errf(CodeWrongType, "root is not a directory")
	}
	// NO_XDEV: never walk into another mount (bind mounts, pools nested in a site).
	res := uint64(unix.RESOLVE_BENEATH | unix.RESOLVE_NO_MAGICLINKS | unix.RESOLVE_NO_XDEV)
	if opt.NoSymlinks {
		res |= unix.RESOLVE_NO_SYMLINKS
	}
	return &Root{fd: fd, resolve: res}, nil
}

func (r *Root) Close() error { return unix.Close(r.fd) }

// Stat of the root itself is allowed and returns Info{Name: "."}.
type Info struct {
	Name    string `json:"name"`
	Type    string `json:"type"` // file, dir, symlink, other
	Size    int64  `json:"size"`
	Mode    uint32 `json:"mode"` // permission bits only
	ModUnix int64  `json:"mtime"`
}

func infoOf(name string, st *unix.Stat_t) Info {
	t := "other"
	switch st.Mode & unix.S_IFMT {
	case unix.S_IFREG:
		t = "file"
	case unix.S_IFDIR:
		t = "dir"
	case unix.S_IFLNK:
		t = "symlink"
	}
	return Info{Name: name, Type: t, Size: st.Size, Mode: st.Mode & 0o777, ModUnix: st.Mtim.Sec}
}

func (r *Root) open(rel string, flags int, mode uint32) (int, error) {
	return openat2(r.fd, rel, flags, mode, r.resolve)
}

// openParent returns an O_PATH fd for the directory containing p, and the last name.
func (r *Root) openParent(p Rel) (int, string, error) {
	if p.IsRoot() {
		return -1, "", errf(CodeInvalid, "operation needs a path below the root")
	}
	dir, name := p.split()
	fd, err := r.open(dir, unix.O_PATH|unix.O_DIRECTORY, 0)
	if err != nil {
		return -1, "", err
	}
	return fd, name, nil
}

// Stat does not follow a final symlink (lstat semantics).
func (r *Root) Stat(p Rel) (Info, error) {
	var st unix.Stat_t
	if p.IsRoot() {
		if err := unix.Fstat(r.fd, &st); err != nil {
			return Info{}, wrap("stat", p, err)
		}
		return infoOf(".", &st), nil
	}
	pfd, name, err := r.openParent(p)
	if err != nil {
		return Info{}, wrap("stat", p, err)
	}
	defer unix.Close(pfd)
	if err := unix.Fstatat(pfd, name, &st, unix.AT_SYMLINK_NOFOLLOW); err != nil {
		return Info{}, wrap("stat", p, err)
	}
	return infoOf(name, &st), nil
}

// List returns entries sorted by name, only names greater than after, at most
// limit of them. truncated reports that more entries follow.
func (r *Root) List(p Rel, after string, limit int) (out []Info, truncated bool, err error) {
	if limit <= 0 || limit > MaxDirEntries {
		limit = MaxDirEntries
	}
	dfd, err := r.open(orDot(string(p)), unix.O_RDONLY|unix.O_DIRECTORY, 0)
	if err != nil {
		return nil, false, wrap("list", p, err)
	}
	f := os.NewFile(uintptr(dfd), "dir") // closes dfd
	defer f.Close()
	names, err := f.Readdirnames(-1)
	if err != nil {
		return nil, false, wrap("list", p, err)
	}
	sort.Strings(names)
	for _, n := range names {
		if n <= after || len(n) > MaxNameLen {
			continue
		}
		if len(out) == limit {
			return out, true, nil
		}
		var st unix.Stat_t
		// fstatat on the directory fd: a rename of the directory cannot redirect it.
		if err := unix.Fstatat(dfd, n, &st, unix.AT_SYMLINK_NOFOLLOW); err != nil {
			continue // vanished between readdir and stat
		}
		out = append(out, infoOf(n, &st))
	}
	return out, false, nil
}

func orDot(s string) string {
	if s == "" {
		return "."
	}
	return s
}

// OpenRead opens a regular file for reading. Final symlinks are followed only
// if they stay beneath the root. FIFOs and other non-regular files are refused
// without blocking (O_NONBLOCK, then fstat).
func (r *Root) OpenRead(p Rel) (*os.File, int64, error) {
	if p.IsRoot() {
		return nil, 0, errf(CodeWrongType, "read %q: is a directory", "")
	}
	fd, err := r.open(string(p), unix.O_RDONLY|unix.O_NONBLOCK|unix.O_NOCTTY, 0)
	if err != nil {
		return nil, 0, wrap("read", p, err)
	}
	var st unix.Stat_t
	if err := unix.Fstat(fd, &st); err != nil {
		unix.Close(fd)
		return nil, 0, wrap("read", p, err)
	}
	if st.Mode&unix.S_IFMT != unix.S_IFREG {
		unix.Close(fd)
		return nil, 0, errf(CodeWrongType, "read %q: not a regular file", string(p))
	}
	return os.NewFile(uintptr(fd), "file"), st.Size, nil
}

// WriteFile writes exactly size bytes from src to p atomically: data goes to a
// temp file in the same directory, then it is renamed into place. A failed
// write (quota, short input, crash) never leaves a partial file under the real
// name and the temp file is removed. If overwrite is false an existing name
// makes the call fail (RENAME_NOREPLACE, atomic). A final symlink is replaced,
// never followed.
func (r *Root) WriteFile(p Rel, mode uint32, src io.Reader, size int64, overwrite bool) error {
	if err := checkMode(mode); err != nil {
		return err
	}
	pfd, name, err := r.openParent(p)
	if err != nil {
		return wrap("write", p, err)
	}
	defer unix.Close(pfd)

	tmp := TempPrefix + randHex(8)
	fd, err := unix.Openat(pfd, tmp, unix.O_WRONLY|unix.O_CREAT|unix.O_EXCL|unix.O_NOFOLLOW|unix.O_CLOEXEC, mode)
	if err != nil {
		return wrap("write", p, err)
	}
	f := os.NewFile(uintptr(fd), "tmp")
	cleanup := func() { _ = unix.Unlinkat(pfd, tmp, 0) }
	n, err := io.Copy(f, io.LimitReader(src, size+1))
	if err == nil && n != size {
		err = errf(CodeInvalid, "write %q: body has %d bytes, expected %d", string(p), n, size)
	}
	if err == nil {
		err = f.Sync() // surfaces EDQUOT/ENOSPC from delayed allocation
	}
	if err == nil {
		err = unix.Fchmod(int(f.Fd()), mode) // exact mode, independent of umask
	}
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		cleanup()
		return wrap("write", p, err)
	}
	var flags uint = 0
	if !overwrite {
		flags = unix.RENAME_NOREPLACE
	}
	if err := unix.Renameat2(pfd, tmp, pfd, name, flags); err != nil {
		cleanup()
		return wrap("write", p, err)
	}
	return nil
}

func randHex(n int) string {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		panic(err) // crypto/rand failing is not recoverable
	}
	return hex.EncodeToString(b)
}

// checkMode accepts permission bits only: setuid, setgid and sticky are never
// settable through this package.
func checkMode(m uint32) error {
	if m&^0o777 != 0 {
		return errf(CodeInvalid, "mode %#o: only permission bits are allowed", m)
	}
	return nil
}

// Mkdir creates a directory. With parents it creates missing ancestors and
// walks them with NO_SYMLINKS semantics (an existing symlink in the chain is an error).
func (r *Root) Mkdir(p Rel, mode uint32, parents bool) error {
	if err := checkMode(mode); err != nil {
		return err
	}
	if p.IsRoot() {
		return errf(CodeExists, "mkdir %q", "")
	}
	if !parents {
		pfd, name, err := r.openParent(p)
		if err != nil {
			return wrap("mkdir", p, err)
		}
		defer unix.Close(pfd)
		return wrap("mkdir", p, mkdirExact(pfd, name, mode))
	}
	cur, err := unix.Dup(r.fd)
	if err != nil {
		return wrap("mkdir", p, err)
	}
	comps := p.components()
	for _, c := range comps {
		if err := mkdirExact(cur, c, mode); err != nil && err != unix.EEXIST {
			unix.Close(cur)
			return wrap("mkdir", p, err)
		}
		next, err := openat2(cur, c, unix.O_PATH|unix.O_DIRECTORY, 0,
			unix.RESOLVE_BENEATH|unix.RESOLVE_NO_MAGICLINKS|unix.RESOLVE_NO_XDEV|unix.RESOLVE_NO_SYMLINKS)
		unix.Close(cur)
		if err != nil {
			return wrap("mkdir", p, err)
		}
		cur = next
	}
	unix.Close(cur)
	return nil
}

// mkdirExact creates name with exactly mode (not masked by umask).
func mkdirExact(dirfd int, name string, mode uint32) error {
	if err := unix.Mkdirat(dirfd, name, mode); err != nil {
		return err
	}
	nfd, err := unix.Openat(dirfd, name, unix.O_PATH|unix.O_DIRECTORY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
	if err != nil {
		return err
	}
	defer unix.Close(nfd)
	return chmodFD(nfd, mode)
}

// chmodFD changes the mode of the object an O_PATH fd refers to, never of a symlink.
func chmodFD(fd int, mode uint32) error {
	var st unix.Stat_t
	if err := unix.Fstat(fd, &st); err != nil {
		return err
	}
	if st.Mode&unix.S_IFMT == unix.S_IFLNK {
		return unix.EOPNOTSUPP
	}
	// O_PATH fds cannot be used with fchmod; /proc/self/fd/N names the same inode.
	return unix.Chmod(fmt.Sprintf("/proc/self/fd/%d", fd), mode)
}

// Chmod changes permission bits of a file or directory (not of a symlink).
func (r *Root) Chmod(p Rel, mode uint32) error {
	if err := checkMode(mode); err != nil {
		return err
	}
	pfd, name, err := r.openParent(p)
	if err != nil {
		return wrap("chmod", p, err)
	}
	defer unix.Close(pfd)
	fd, err := unix.Openat(pfd, name, unix.O_PATH|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
	if err != nil {
		return wrap("chmod", p, err)
	}
	defer unix.Close(fd)
	return wrap("chmod", p, chmodFD(fd, mode))
}

// Rename moves from to to inside the root. It never replaces an existing target.
func (r *Root) Rename(from, to Rel) error {
	ffd, fname, err := r.openParent(from)
	if err != nil {
		return wrap("rename", from, err)
	}
	defer unix.Close(ffd)
	tfd, tname, err := r.openParent(to)
	if err != nil {
		return wrap("rename", to, err)
	}
	defer unix.Close(tfd)
	return wrap("rename", from, unix.Renameat2(ffd, fname, tfd, tname, unix.RENAME_NOREPLACE))
}

// Remove deletes a file, symlink (the link itself) or empty directory, or with
// recursive a whole tree. The recursive walk uses only fds and O_NOFOLLOW, so
// swapping a directory for a symlink during the walk cannot redirect it.
func (r *Root) Remove(p Rel, recursive bool) error {
	pfd, name, err := r.openParent(p)
	if err != nil {
		return wrap("remove", p, err)
	}
	defer unix.Close(pfd)
	if !recursive {
		err := unix.Unlinkat(pfd, name, 0)
		if err == unix.EISDIR || err == unix.EPERM { // directory
			err = unix.Unlinkat(pfd, name, unix.AT_REMOVEDIR)
		}
		return wrap("remove", p, err)
	}
	return wrap("remove", p, removeAll(pfd, name, 0))
}

func removeAll(parent int, name string, depth int) error {
	if depth > MaxRemoveDepth {
		return errf(CodeTooLarge, "tree deeper than %d", MaxRemoveDepth)
	}
	err := unix.Unlinkat(parent, name, 0)
	if err == nil || err == unix.ENOENT {
		return nil
	}
	if err != unix.EISDIR && err != unix.EPERM {
		return err
	}
	// Open as directory without following: if it was swapped for a symlink
	// meanwhile, this fails with ELOOP/ENOTDIR and we unlink the link itself.
	// NO_XDEV matters: st_dev is NOT enough, a bind mount of the same file
	// system has the same st_dev (found by TestNoXdevRefusesMountInsideTree).
	dfd, err := openat2(parent, name, unix.O_RDONLY|unix.O_DIRECTORY, 0,
		unix.RESOLVE_NO_SYMLINKS|unix.RESOLVE_NO_MAGICLINKS|unix.RESOLVE_NO_XDEV|unix.RESOLVE_BENEATH)
	if err != nil {
		if err == unix.ELOOP || err == unix.ENOTDIR {
			if e := unix.Unlinkat(parent, name, 0); e != nil && e != unix.ENOENT {
				return e
			}
			return nil
		}
		if err == unix.ENOENT {
			return nil
		}
		return err
	}
	f := os.NewFile(uintptr(dfd), "dir")
	names, err := f.Readdirnames(-1)
	if err != nil {
		f.Close()
		return err
	}
	for _, n := range names {
		if err := removeAll(dfd, n, depth+1); err != nil {
			f.Close()
			return err
		}
	}
	f.Close()
	err = unix.Unlinkat(parent, name, unix.AT_REMOVEDIR)
	if err == unix.ENOENT {
		return nil
	}
	return err
}
