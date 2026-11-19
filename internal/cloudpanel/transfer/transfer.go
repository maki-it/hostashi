// SPDX-License-Identifier: AGPL-3.0-or-later

// Package transfer copies one site's files from the old CloudPanel server
// into a directory on this server (ADR-0026 Part 1 and Part 3).
//
// The old side is read over SFTP; the key there is locked to a read-only
// sftp-server. The tree that comes back is untrusted: names, types, modes,
// link targets and owners are checked here before anything is written, and
// every local path is opened beneath the target directory without following
// symlinks. The caller runs this as the site user (the import job drops its
// root rights first), so files land owned by that user without a chown.
//
// A run mirrors the old tree: files with the same size and modification
// time are kept, changed files are replaced through a temporary name and a
// rename, and local entries the old tree no longer has are removed. The
// first copy and the final sync before the DNS switch are the same call.
package transfer

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path"
	"strings"
	"time"

	"github.com/pkg/sftp"
	"golang.org/x/sys/unix"
)

// Remote is the read side, an SFTP session in production.
type Remote interface {
	ReadDir(p string) ([]fs.FileInfo, error)
	Lstat(p string) (fs.FileInfo, error)
	ReadLink(p string) (string, error)
	Open(p string) (File, error)
}

// File is an open remote file. Stat reports the opened file, not the name.
type File interface {
	io.ReadCloser
	Stat() (fs.FileInfo, error)
}

// SFTP adapts an SFTP client to Remote.
func SFTP(c *sftp.Client) Remote { return sftpRemote{c} }

type sftpRemote struct{ c *sftp.Client }

func (r sftpRemote) ReadDir(p string) ([]fs.FileInfo, error) { return r.c.ReadDir(p) }
func (r sftpRemote) Lstat(p string) (fs.FileInfo, error)     { return r.c.Lstat(p) }
func (r sftpRemote) ReadLink(p string) (string, error)       { return r.c.ReadLink(p) }
func (r sftpRemote) Open(p string) (File, error)             { return r.c.Open(p) }

// Limits bound one run. Zero values take the defaults.
type Limits struct {
	MaxEntries int // files, directories and links together
	MaxDepth   int
}

// Defaults. web2's largest site has a few hundred thousand files [E].
const (
	DefaultMaxEntries = 5_000_000
	DefaultMaxDepth   = 128
	maxName           = 255
	maxPath           = 4096
	maxSkippedListed  = 100
	tmpPrefix         = ".hostashi-import-"
)

// Stats counts what one run did.
type Stats struct {
	Files     int64 `json:"files"`     // copied files
	Bytes     int64 `json:"bytes"`     // bytes of copied files
	Unchanged int64 `json:"unchanged"` // files kept because size and time match
	Dirs      int64 `json:"dirs"`
	Links     int64 `json:"links"`
	Deleted   int64 `json:"deleted"` // local entries the old tree no longer has
	Skipped   int64 `json:"skipped"`
	// SkippedPaths lists the first skipped entries with the reason.
	SkippedPaths []string `json:"skippedPaths"`
}

// ErrUnsafe marks a remote tree that cannot be copied safely at all.
var ErrUnsafe = errors.New("transfer: unsafe remote tree")

// Copy mirrors the remote directory src into the open directory dst. Every
// entry must belong to the owner of src on the old server; anything else
// (a file of another user, a symlink swapped in for a file, a device, a FIFO
// or a socket) is skipped and listed in Stats.
func Copy(ctx context.Context, r Remote, src string, dst int, lim Limits) (Stats, error) {
	if lim.MaxEntries <= 0 {
		lim.MaxEntries = DefaultMaxEntries
	}

	if lim.MaxDepth <= 0 {
		lim.MaxDepth = DefaultMaxDepth
	}

	if !path.IsAbs(src) || path.Clean(src) != src || len(src) > maxPath || strings.ContainsRune(src, 0) {
		return Stats{}, fmt.Errorf("%w: source %q", ErrUnsafe, src)
	}

	root, err := r.Lstat(src)
	if err != nil {
		return Stats{}, fmt.Errorf("transfer: %s: %w", src, err)
	}

	owner, ok := ownerOf(root)
	if !ok || !root.IsDir() {
		return Stats{}, fmt.Errorf("%w: %s is not a directory with a known owner", ErrUnsafe, src)
	}

	c := &copier{ctx: ctx, r: r, owner: owner, lim: lim, st: Stats{SkippedPaths: []string{}}}

	err = c.dir(src, "", dst, 0)

	return c.st, err
}

type copier struct {
	ctx     context.Context //nolint:containedctx // one run, one context
	r       Remote
	owner   uint32
	lim     Limits
	entries int
	st      Stats
}

// beneath resolves one name inside a directory fd, refusing symlinks,
// magic links and "..".
const beneath = unix.RESOLVE_BENEATH | unix.RESOLVE_NO_SYMLINKS | unix.RESOLVE_NO_MAGICLINKS

func (c *copier) skip(rel, why string) {
	c.st.Skipped++
	if len(c.st.SkippedPaths) < maxSkippedListed {
		c.st.SkippedPaths = append(c.st.SkippedPaths, orDot(rel)+": "+why)
	}
}

// dir mirrors the remote directory rpath (rel below src) into the open
// local directory fd.
func (c *copier) dir(rpath, rel string, fd, depth int) error {
	if depth > c.lim.MaxDepth {
		return fmt.Errorf("%w: deeper than %d directories at %s", ErrUnsafe, c.lim.MaxDepth, orDot(rel))
	}

	list, err := c.r.ReadDir(rpath)
	if err != nil {
		return fmt.Errorf("read %s: %w", rpath, err)
	}

	keep := make(map[string]bool, len(list))

	for _, fi := range list {
		if err := c.ctx.Err(); err != nil {
			return err
		}

		name := fi.Name()
		if name == "." || name == ".." {
			continue
		}

		if !validName(name) {
			return fmt.Errorf("%w: name %q in %s", ErrUnsafe, name, rpath)
		}

		child := path.Join(rel, name)
		if len(rpath)+1+len(name) > maxPath {
			return fmt.Errorf("%w: path longer than %d bytes at %s", ErrUnsafe, maxPath, child)
		}

		// A skipped entry keeps whatever is here locally under that name.
		keep[name] = true

		c.entries++
		if c.entries > c.lim.MaxEntries {
			return fmt.Errorf("%w: more than %d entries", ErrUnsafe, c.lim.MaxEntries)
		}

		if err := c.entry(path.Join(rpath, name), child, fd, fi, depth); err != nil {
			return err
		}
	}

	return c.prune(rel, fd, keep)
}

func (c *copier) entry(rpath, rel string, parent int, fi fs.FileInfo, depth int) error {
	name := path.Base(rel)

	if uid, ok := ownerOf(fi); !ok || uid != c.owner {
		c.skip(rel, "not owned by the site user")

		return nil
	}

	// Only these three types are copied; every other one is skipped.
	//exhaustive:ignore
	switch fi.Mode().Type() {
	case fs.ModeDir:
		fd, err := c.mkdir(parent, name)
		if err != nil {
			return fmt.Errorf("%s: %w", rel, err)
		}
		defer unix.Close(fd)

		c.st.Dirs++

		if err := c.dir(rpath, rel, fd, depth+1); err != nil {
			return err
		}

		// Set at the end, so a read-only directory can still be filled.
		return unix.Fchmod(fd, perm(fi)|0o700)
	case 0:
		return c.file(rpath, rel, parent, fi)
	case fs.ModeSymlink:
		return c.link(rpath, rel, parent, fi)
	default:
		c.skip(rel, "not a file, directory or symlink")

		return nil
	}
}

// mkdir opens the directory name in parent, replacing anything else of
// that name.
func (c *copier) mkdir(parent int, name string) (int, error) {
	how := &unix.OpenHow{Flags: unix.O_RDONLY | unix.O_DIRECTORY | unix.O_NOFOLLOW | unix.O_CLOEXEC, Resolve: beneath}

	var st unix.Stat_t

	err := unix.Fstatat(parent, name, &st, unix.AT_SYMLINK_NOFOLLOW)

	switch {
	case err == nil && st.Mode&unix.S_IFMT == unix.S_IFDIR:
	case err == nil:
		if err := removeAll(parent, name, 0); err != nil {
			return -1, err
		}

		fallthrough
	case errors.Is(err, unix.ENOENT):
		if err := unix.Mkdirat(parent, name, 0o700); err != nil {
			return -1, err
		}
	default:
		return -1, err
	}

	return unix.Openat2(parent, name, how)
}

func (c *copier) file(rpath, rel string, parent int, fi fs.FileInfo) error {
	name := path.Base(rel)
	mtime := fi.ModTime()

	var st unix.Stat_t
	if err := unix.Fstatat(parent, name, &st, unix.AT_SYMLINK_NOFOLLOW); err == nil &&
		st.Mode&unix.S_IFMT == unix.S_IFREG && st.Size == fi.Size() && st.Mtim.Sec == mtime.Unix() {
		c.st.Unchanged++

		return nil
	}

	f, err := c.r.Open(rpath)
	if err != nil {
		return fmt.Errorf("open %s: %w", rpath, err)
	}
	defer f.Close()

	// The name may have been swapped for a symlink to another user's file
	// since it was listed: the opened file must still be the site user's
	// regular file.
	ofi, err := f.Stat()
	if err != nil {
		return fmt.Errorf("stat %s: %w", rpath, err)
	}

	if uid, ok := ownerOf(ofi); !ok || uid != c.owner || !ofi.Mode().IsRegular() {
		c.skip(rel, "changed into another user's file or a non-file while it was read")

		return nil
	}

	n, err := c.replace(parent, name, func(tmp int) (int64, error) {
		// The fd stays owned by replace, which closes it.
		n, err := io.Copy(fdWriter(tmp), ctxReader{c.ctx, f})
		if err == nil {
			err = unix.Fchmod(tmp, perm(fi))
		}

		return n, err
	}, mtime)
	if err != nil {
		return fmt.Errorf("%s: %w", rel, err)
	}

	c.st.Files++
	c.st.Bytes += n

	return nil
}

func (c *copier) link(rpath, rel string, parent int, fi fs.FileInfo) error {
	name := path.Base(rel)

	target, err := c.r.ReadLink(rpath)
	if err != nil {
		return fmt.Errorf("readlink %s: %w", rpath, err)
	}

	if target == "" || len(target) > maxPath || strings.ContainsRune(target, 0) {
		c.skip(rel, "symlink target is empty or too long")

		return nil
	}

	buf := make([]byte, maxPath+1)
	if n, err := unix.Readlinkat(parent, name, buf); err == nil && string(buf[:n]) == target {
		c.st.Unchanged++

		return nil
	}

	// The link is created, never followed: it points where it pointed on
	// the old server.
	tmp := tmpName()
	if err := unix.Symlinkat(target, parent, tmp); err != nil {
		return fmt.Errorf("%s: %w", rel, err)
	}

	ts := times(fi.ModTime())
	if err := unix.UtimesNanoAt(parent, tmp, ts, unix.AT_SYMLINK_NOFOLLOW); err != nil {
		_ = unix.Unlinkat(parent, tmp, 0)

		return fmt.Errorf("%s: %w", rel, err)
	}

	if err := c.put(parent, tmp, name); err != nil {
		return fmt.Errorf("%s: %w", rel, err)
	}

	c.st.Links++

	return nil
}

// replace writes a new file under a temporary name and renames it over
// name, so a reader of the live web root sees the old or the new file.
func (c *copier) replace(parent int, name string, write func(fd int) (int64, error), mtime time.Time) (int64, error) {
	tmp := tmpName()

	fd, err := unix.Openat(parent, tmp, unix.O_WRONLY|unix.O_CREAT|unix.O_EXCL|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0o600)
	if err != nil {
		return 0, err
	}

	n, err := write(fd)
	if err == nil {
		err = unix.UtimesNanoAt(parent, tmp, times(mtime), unix.AT_SYMLINK_NOFOLLOW)
	}

	if cerr := unix.Close(fd); err == nil {
		err = cerr
	}

	if err == nil {
		err = c.put(parent, tmp, name)
	}

	if err != nil {
		_ = unix.Unlinkat(parent, tmp, 0)

		return n, err
	}

	return n, nil
}

// put renames tmp over name. A directory of that name is removed first,
// since a rename cannot replace a directory with a file.
func (c *copier) put(parent int, tmp, name string) error {
	var st unix.Stat_t
	if err := unix.Fstatat(parent, name, &st, unix.AT_SYMLINK_NOFOLLOW); err == nil && st.Mode&unix.S_IFMT == unix.S_IFDIR {
		if err := removeAll(parent, name, 0); err != nil {
			return err
		}
	}

	return unix.Renameat(parent, tmp, parent, name)
}

// prune removes the local entries of fd that keep does not list.
func (c *copier) prune(rel string, fd int, keep map[string]bool) error {
	names, err := readNames(fd)
	if err != nil {
		return fmt.Errorf("list %s: %w", orDot(rel), err)
	}

	for _, name := range names {
		if keep[name] {
			continue
		}

		if err := removeAll(fd, name, 0); err != nil {
			return fmt.Errorf("remove %s: %w", path.Join(rel, name), err)
		}

		c.st.Deleted++
	}

	return nil
}

// removeAll removes name in parent and everything below it, without
// following symlinks.
func removeAll(parent int, name string, depth int) error {
	if depth > DefaultMaxDepth*2 {
		return errors.New("tree too deep to remove")
	}

	var st unix.Stat_t
	if err := unix.Fstatat(parent, name, &st, unix.AT_SYMLINK_NOFOLLOW); err != nil {
		if errors.Is(err, unix.ENOENT) {
			return nil
		}

		return err
	}

	if st.Mode&unix.S_IFMT != unix.S_IFDIR {
		return unix.Unlinkat(parent, name, 0)
	}

	fd, err := unix.Openat2(parent, name, &unix.OpenHow{Flags: unix.O_RDONLY | unix.O_DIRECTORY | unix.O_NOFOLLOW | unix.O_CLOEXEC, Resolve: beneath})
	if err != nil {
		return err
	}

	// A directory without write permission (copied from a read-only one)
	// must become writable before its entries can go.
	_ = unix.Fchmod(fd, 0o700)

	names, err := readNames(fd)
	if err == nil {
		for _, n := range names {
			if err = removeAll(fd, n, depth+1); err != nil {
				break
			}
		}
	}

	_ = unix.Close(fd)

	if err != nil {
		return err
	}

	return unix.Unlinkat(parent, name, unix.AT_REMOVEDIR)
}

// readNames lists a directory fd without moving its offset for others.
func readNames(fd int) ([]string, error) {
	dfd, err := unix.Openat2(fd, ".", &unix.OpenHow{Flags: unix.O_RDONLY | unix.O_DIRECTORY | unix.O_CLOEXEC, Resolve: beneath})
	if err != nil {
		return nil, err
	}

	f := os.NewFile(uintptr(dfd), ".")
	defer f.Close()

	return f.Readdirnames(-1)
}

// ownerOf returns the owner of a remote entry. SFTP attributes carry the
// numeric uid; a server that does not send it gives no owner.
func ownerOf(fi fs.FileInfo) (uint32, bool) {
	st, ok := fi.Sys().(*sftp.FileStat)
	if !ok {
		return 0, false
	}

	return st.UID, true
}

// perm keeps the permission bits only: setuid, setgid and sticky are
// dropped.
func perm(fi fs.FileInfo) uint32 { return uint32(fi.Mode().Perm()) }

func validName(n string) bool {
	return n != "" && len(n) <= maxName && !strings.ContainsAny(n, "/\x00")
}

func tmpName() string {
	b := make([]byte, 8)
	_, _ = rand.Read(b)

	return tmpPrefix + hex.EncodeToString(b)
}

func times(t time.Time) []unix.Timespec {
	ts := unix.NsecToTimespec(t.UnixNano())

	return []unix.Timespec{ts, ts}
}

func orDot(rel string) string {
	if rel == "" {
		return "."
	}

	return rel
}

// ctxReader stops a long copy when the run is canceled.
type ctxReader struct {
	ctx context.Context //nolint:containedctx // per read
	r   io.Reader
}

func (r ctxReader) Read(p []byte) (int, error) {
	if err := r.ctx.Err(); err != nil {
		return 0, err
	}

	return r.r.Read(p)
}

// fdWriter writes to a file descriptor it does not own.
type fdWriter int

func (w fdWriter) Write(p []byte) (int, error) {
	n := 0

	for n < len(p) {
		m, err := unix.Write(int(w), p[n:])
		if err != nil {
			if errors.Is(err, unix.EINTR) {
				continue
			}

			return n, err
		}

		n += m
	}

	return n, nil
}
