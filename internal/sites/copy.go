// SPDX-License-Identifier: AGPL-3.0-or-later

package sites

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"sync"
	"sync/atomic"

	"golang.org/x/sys/unix"

	"github.com/maki-it/hostashi/internal/agentapi"
)

// ErrCopyTargetExists means an entry of the source exists in the target
// already and is not a folder of the target site.
var ErrCopyTargetExists = errors.New("sites: entry exists in the target home")

// Reasons a copy leaves an entry out, next to the ones of the reset.
const skipSocket = "socket"

// copyChunk is how much of a file is copied between two checks of the
// job's context.
const copyChunk = 64 << 20

// copyWalk copies one home into another. Counters are atomic, so a job
// can report them while the walk runs.
type copyWalk struct {
	ctx      context.Context
	srcUID   uint32
	uid, gid uint32 // the target site's
	dev      uint64 // the source home's file system
	// srcHome and dstHome rewrite absolute link targets into the source
	// home, so a shared path of a Git deploy points into the clone.
	srcHome, dstHome string

	dirs, files, links, bytes, skipped atomic.Int64

	mu    sync.Mutex
	paths []agentapi.PermissionsSkip
	buf   []byte
}

func (w *copyWalk) skip(rel, reason string) {
	w.skipped.Add(1)

	w.mu.Lock()
	defer w.mu.Unlock()

	if len(w.paths) < agentapi.MaxPermissionsSkipped {
		w.paths = append(w.paths, agentapi.PermissionsSkip{Path: rel, Reason: reason})
	}
}

// fill writes the counters into a job.
func (w *copyWalk) fill(j *agentapi.SiteHomeCopyJob) {
	j.Dirs, j.Files, j.Links, j.Bytes, j.Skipped = w.dirs.Load(), w.files.Load(), w.links.Load(), w.bytes.Load(), w.skipped.Load()

	w.mu.Lock()
	j.SkippedPaths = append([]agentapi.PermissionsSkip(nil), w.paths...)
	w.mu.Unlock()
}

// copySkipped reports whether rel (relative to the home) is left out.
func copySkipped(rel string) bool {
	for _, s := range agentapi.HomeCopySkippedDirs {
		if rel == s {
			return true
		}
	}

	return false
}

// CopyHome copies the home of site src into the home of site dst, both
// registered with matching accounts (ADR-0070). With replace, the target
// home is emptied first, apart from the skipped paths. It returns the
// counters of the walk, also on error.
func (m *Manager) CopyHome(ctx context.Context, src, dst string, replace bool) (agentapi.SiteHomeCopyJob, error) {
	w, err := m.copyHome(ctx, src, dst, replace, nil)

	j := agentapi.SiteHomeCopyJob{Source: src, Target: dst, Replace: replace}
	w.fill(&j)

	return j, err
}

// copyHome is CopyHome; onWalk, if set, gets the walk before it starts.
func (m *Manager) copyHome(ctx context.Context, src, dst string, replace bool, onWalk func(*copyWalk)) (*copyWalk, error) {
	w := &copyWalk{ctx: ctx}
	if onWalk != nil {
		onWalk(w)
	}

	if err := (agentapi.SiteHomeCopyRequest{Source: src, Target: dst, Replace: replace}).Validate(); err != nil {
		return w, err
	}

	sfd, ssite, _, err := m.openHomeForReset(src)
	if err != nil {
		return w, fmt.Errorf("source: %w", err)
	}

	defer unix.Close(sfd)

	dfd, dsite, _, err := m.openHomeForReset(dst)
	if err != nil {
		return w, fmt.Errorf("target: %w", err)
	}

	defer unix.Close(dfd)

	srcHome, _ := m.Home(src)
	dstHome, _ := m.Home(dst)

	var st unix.Stat_t
	if err := unix.Fstat(sfd, &st); err != nil {
		return w, fmt.Errorf("stat source home: %w", err)
	}

	w.srcUID, w.uid, w.gid, w.dev = ssite.UID, dsite.UID, dsite.GID, st.Dev
	w.srcHome, w.dstHome = srcHome, dstHome

	if replace {
		if err := emptyHome(ctx, dfd); err != nil {
			return w, err
		}
	}

	if err := w.dir(sfd, dfd, "", 1); err != nil {
		return w, err
	}

	m.opt.Log.InfoContext(ctx, "site home copied", "source", src, "target", dst, "files", w.files.Load(), "bytes", w.bytes.Load(), "skipped", w.skipped.Load())

	return w, nil
}

// emptyHome removes every entry of the open home except the skipped
// paths, which belong to the target itself.
func emptyHome(ctx context.Context, hfd int) error {
	names, err := readNames(hfd)
	if err != nil {
		return fmt.Errorf("list target home: %w", err)
	}

	for _, n := range names {
		if err := ctx.Err(); err != nil {
			return err
		}

		switch {
		case copySkipped(n):
			continue
		case n == ".deploy":
			// Keep the target's own deploy cache and results.
			if err := emptyDeploy(hfd); err != nil {
				return err
			}

			continue
		}

		if err := removeAll(hfd, n, 1); err != nil {
			return err
		}
	}

	return nil
}

func emptyDeploy(hfd int) error {
	fd, err := unix.Openat2(hfd, ".deploy", &unix.OpenHow{Flags: dirFlags, Resolve: noEscape})
	if err != nil {
		if errors.Is(err, unix.ELOOP) || errors.Is(err, unix.ENOTDIR) {
			return removeAll(hfd, ".deploy", 1)
		}

		return fmt.Errorf("%w: open .deploy: %w", ErrHomeUnsafe, err)
	}

	defer unix.Close(fd)

	names, err := readNames(fd)
	if err != nil {
		return fmt.Errorf("list .deploy: %w", err)
	}

	for _, n := range names {
		if copySkipped(".deploy/" + n) {
			continue
		}

		if err := removeAll(fd, n, 2); err != nil {
			return err
		}
	}

	return nil
}

// readNames lists a directory through a second fd, so dfd stays usable.
func readNames(dfd int) ([]string, error) {
	rfd, err := unix.Openat2(dfd, ".", &unix.OpenHow{Flags: dirFlags, Resolve: noEscape})
	if err != nil {
		return nil, err
	}

	f := os.NewFile(uintptr(rfd), "dir")
	defer f.Close()

	return f.Readdirnames(-1)
}

// dir copies the entries of the source directory sfd into the target
// directory dfd. rel is the path below the home, "" for the home.
func (w *copyWalk) dir(sfd, dfd int, rel string, depth int) error {
	names, err := readNames(sfd)
	if err != nil {
		return fmt.Errorf("list %q: %w", rel, err)
	}

	for _, n := range names {
		p := n
		if rel != "" {
			p = rel + "/" + n
		}

		if copySkipped(p) {
			continue
		}

		if err := w.entry(sfd, dfd, n, p, depth); err != nil {
			return err
		}
	}

	return nil
}

func (w *copyWalk) entry(sparent, dparent int, name, rel string, depth int) error {
	if err := w.ctx.Err(); err != nil {
		return err
	}

	fd, err := unix.Openat2(sparent, name, &unix.OpenHow{Flags: unix.O_PATH | unix.O_NOFOLLOW | unix.O_CLOEXEC, Resolve: noEscape})
	if err != nil {
		switch {
		case errors.Is(err, unix.ENOENT):
			return nil // removed meanwhile
		case errors.Is(err, unix.EXDEV):
			w.skip(rel, skipOtherFS)

			return nil
		default:
			return fmt.Errorf("%w: open %q: %w", ErrHomeUnsafe, rel, err)
		}
	}

	defer unix.Close(fd)

	var st unix.Stat_t
	if err := unix.Fstat(fd, &st); err != nil {
		return fmt.Errorf("stat %q: %w", rel, err)
	}

	if st.Dev != w.dev {
		w.skip(rel, skipOtherFS)

		return nil
	}

	switch st.Mode & unix.S_IFMT {
	case unix.S_IFDIR:
		if depth >= maxRemoveDepth {
			w.skip(rel, skipTooDeep)

			return nil
		}

		return w.copyDir(sparent, dparent, name, rel, &st, depth)
	case unix.S_IFREG:
		if st.Nlink > 1 && st.Uid != w.srcUID {
			w.skip(rel, skipHardLink)

			return nil
		}

		return w.copyFile(sparent, dparent, name, rel, &st)
	case unix.S_IFLNK:
		return w.copyLink(fd, dparent, name, rel)
	case unix.S_IFIFO:
		if err := unix.Mknodat(dparent, name, unix.S_IFIFO|0o600, 0); err != nil {
			return w.createErr(rel, err)
		}

		return w.own(dparent, name, rel, &st)
	case unix.S_IFSOCK:
		w.skip(rel, skipSocket)
	default:
		w.skip(rel, skipDevice)
	}

	return nil
}

func (w *copyWalk) createErr(rel string, err error) error {
	if errors.Is(err, unix.EEXIST) {
		return fmt.Errorf("%w: %q", ErrCopyTargetExists, rel)
	}

	return fmt.Errorf("create %q: %w", rel, err)
}

func (w *copyWalk) copyDir(sparent, dparent int, name, rel string, st *unix.Stat_t, depth int) error {
	sfd, err := unix.Openat2(sparent, name, &unix.OpenHow{Flags: dirFlags, Resolve: noEscape})
	if err != nil {
		if errors.Is(err, unix.ENOENT) {
			return nil
		}

		return fmt.Errorf("%w: open %q: %w", ErrHomeUnsafe, rel, err)
	}

	defer unix.Close(sfd)

	if err := sameObject(sfd, st); err != nil {
		return fmt.Errorf("%w: %q: %w", ErrHomeUnsafe, rel, err)
	}

	created := true

	if err := unix.Mkdirat(dparent, name, 0o700); err != nil {
		if !errors.Is(err, unix.EEXIST) {
			return fmt.Errorf("create %q: %w", rel, err)
		}

		created = false
	}

	dfd, err := unix.Openat2(dparent, name, &unix.OpenHow{Flags: dirFlags, Resolve: noEscape})
	if err != nil {
		if errors.Is(err, unix.ENOTDIR) || errors.Is(err, unix.ELOOP) {
			return fmt.Errorf("%w: %q", ErrCopyTargetExists, rel)
		}

		return fmt.Errorf("%w: open target %q: %w", ErrHomeUnsafe, rel, err)
	}

	defer unix.Close(dfd)

	if !created {
		// An existing folder (the web root of a new site) must be the
		// target's own.
		var dst unix.Stat_t
		if err := unix.Fstat(dfd, &dst); err != nil {
			return fmt.Errorf("stat target %q: %w", rel, err)
		}

		if dst.Uid != w.uid {
			return fmt.Errorf("%w: %q belongs to uid %d", ErrCopyTargetExists, rel, dst.Uid)
		}
	}

	if err := unix.Fchown(dfd, int(w.uid), int(w.gid)); err != nil {
		return fmt.Errorf("chown %q: %w", rel, err)
	}

	if err := unix.Fchmod(dfd, st.Mode&0o777); err != nil {
		return fmt.Errorf("chmod %q: %w", rel, err)
	}

	w.dirs.Add(1)

	if err := w.dir(sfd, dfd, rel, depth+1); err != nil {
		return err
	}

	// Times last: creating the entries changed the folder's mtime.
	return setTimes(dfd, st)
}

func (w *copyWalk) copyFile(sparent, dparent int, name, rel string, st *unix.Stat_t) error {
	// O_NONBLOCK keeps a FIFO swapped in meanwhile from blocking the open;
	// sameObject then refuses it.
	sfd, err := unix.Openat2(sparent, name, &unix.OpenHow{Flags: unix.O_RDONLY | unix.O_NOFOLLOW | unix.O_NONBLOCK | unix.O_CLOEXEC, Resolve: noEscape})
	if err != nil {
		if errors.Is(err, unix.ENOENT) {
			return nil
		}

		return fmt.Errorf("%w: open %q: %w", ErrHomeUnsafe, rel, err)
	}

	in := os.NewFile(uintptr(sfd), "src")
	defer in.Close()

	if err := sameObject(sfd, st); err != nil {
		return fmt.Errorf("%w: %q: %w", ErrHomeUnsafe, rel, err)
	}

	dfd, err := unix.Openat2(dparent, name, &unix.OpenHow{Flags: unix.O_WRONLY | unix.O_CREAT | unix.O_EXCL | unix.O_NOFOLLOW | unix.O_CLOEXEC, Mode: 0o600, Resolve: noEscape})
	if err != nil {
		return w.createErr(rel, err)
	}

	out := os.NewFile(uintptr(dfd), "dst")
	defer out.Close()

	if err := unix.Fchown(dfd, int(w.uid), int(w.gid)); err != nil {
		return fmt.Errorf("chown %q: %w", rel, err)
	}

	// ReadFrom uses copy_file_range where the file systems allow it; the
	// chunks let a canceled job stop inside a large file.
	for {
		if err := w.ctx.Err(); err != nil {
			return err
		}

		n, err := out.ReadFrom(&io.LimitedReader{R: in, N: copyChunk})
		w.bytes.Add(n)

		if err != nil {
			return fmt.Errorf("copy %q: %w", rel, err)
		}

		if n < copyChunk {
			break
		}
	}

	// chown cleared setuid and setgid; they are not copied anyway.
	if err := unix.Fchmod(dfd, st.Mode&0o777); err != nil {
		return fmt.Errorf("chmod %q: %w", rel, err)
	}

	if err := setTimes(dfd, st); err != nil {
		return fmt.Errorf("%q: %w", rel, err)
	}

	if err := out.Close(); err != nil {
		return fmt.Errorf("close %q: %w", rel, err)
	}

	w.files.Add(1)

	return nil
}

func (w *copyWalk) copyLink(fd, dparent int, name, rel string) error {
	if w.buf == nil {
		w.buf = make([]byte, unix.PathMax)
	}

	n, err := unix.Readlinkat(fd, "", w.buf)
	if err != nil {
		return fmt.Errorf("read link %q: %w", rel, err)
	}

	target := string(w.buf[:n])
	if w.srcHome != "" && (target == w.srcHome || strings.HasPrefix(target, w.srcHome+"/")) {
		target = w.dstHome + strings.TrimPrefix(target, w.srcHome)
	}

	if err := unix.Symlinkat(target, dparent, name); err != nil {
		return w.createErr(rel, err)
	}

	if err := unix.Fchownat(dparent, name, int(w.uid), int(w.gid), unix.AT_SYMLINK_NOFOLLOW); err != nil {
		return fmt.Errorf("chown %q: %w", rel, err)
	}

	w.links.Add(1)

	return nil
}

// own gives a just created FIFO to the target and copies its mode, on
// an O_PATH fd, so a link swapped in meanwhile is never followed.
func (w *copyWalk) own(dparent int, name, rel string, st *unix.Stat_t) error {
	fd, err := unix.Openat2(dparent, name, &unix.OpenHow{Flags: unix.O_PATH | unix.O_NOFOLLOW | unix.O_CLOEXEC, Resolve: noEscape})
	if err != nil {
		return fmt.Errorf("open new %q: %w", rel, err)
	}

	defer unix.Close(fd)

	var got unix.Stat_t
	if err := unix.Fstat(fd, &got); err != nil || got.Mode&unix.S_IFMT != unix.S_IFIFO {
		return fmt.Errorf("%w: %q changed after it was created", ErrHomeUnsafe, rel)
	}

	if err := unix.Fchownat(fd, "", int(w.uid), int(w.gid), unix.AT_EMPTY_PATH|unix.AT_SYMLINK_NOFOLLOW); err != nil {
		return fmt.Errorf("chown %q: %w", rel, err)
	}

	if err := fchmodFD(fd, st.Mode&0o777); err != nil {
		return fmt.Errorf("chmod %q: %w", rel, err)
	}

	w.files.Add(1)

	return nil
}

// sameObject checks that an fd opened for reading names the object that
// was looked at through the O_PATH fd, so a swap in between is refused.
func sameObject(fd int, want *unix.Stat_t) error {
	var st unix.Stat_t
	if err := unix.Fstat(fd, &st); err != nil {
		return err
	}

	if st.Dev != want.Dev || st.Ino != want.Ino || st.Mode&unix.S_IFMT != want.Mode&unix.S_IFMT {
		return errors.New("changed while being copied")
	}

	return nil
}

func setTimes(fd int, st *unix.Stat_t) error {
	ts := []unix.Timespec{st.Atim, st.Mtim}
	if err := unix.UtimesNanoAt(fd, "", ts, unix.AT_EMPTY_PATH); err != nil {
		return fmt.Errorf("set times: %w", err)
	}

	return nil
}
