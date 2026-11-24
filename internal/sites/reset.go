// SPDX-License-Identifier: AGPL-3.0-or-later

package sites

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"strconv"

	"golang.org/x/sys/unix"

	"github.com/maki-it/hostashi/internal/agentapi"
)

// Modes a permissions reset sets (ADR-0067). The home keeps HomeMode and
// the web server's group; everything below it gets the site's own group.
const (
	ResetDirMode     = 0o755
	ResetFileMode    = 0o644
	ResetExecMode    = 0o755
	ResetPrivateDir  = 0o700
	ResetPrivateFile = 0o600
	ResetPrivateExec = 0o700
)

// privateDirs are top-level directories of a home whose content stays
// readable by the site user only: ssh refuses private keys others can read.
var privateDirs = map[string]bool{".ssh": true, ".gnupg": true}

// Reasons an entry is left alone.
const (
	skipOtherFS    = "on another file system"
	skipDevice     = "device file"
	skipHardLink   = "hard link to a file of another owner"
	skipTooDeep    = "deeper than 128 levels"
	resetCheckRate = 1024
)

// ResetPermissions gives every entry below the home of a site back to the
// site user and sets the modes of ADR-0067. It works on file descriptors
// only and never follows a symbolic link: a link gets the site's owner
// itself, its target is reached (if at all) through its real path below
// the home. Entries on another file system, device files and hard links to
// files of another owner are skipped and reported.
//
// The manager lock is held only while the site is looked up, so a long walk
// does not block other site operations.
func (m *Manager) ResetPermissions(ctx context.Context, user string) (agentapi.SitePermissionsResetResponse, error) {
	if err := (agentapi.SitePermissionsResetRequest{Site: user}).Validate(); err != nil {
		return agentapi.SitePermissionsResetResponse{}, err
	}

	hfd, site, homeGID, err := m.openHomeForReset(user)
	if err != nil {
		return agentapi.SitePermissionsResetResponse{}, err
	}

	defer unix.Close(hfd)

	w := &resetWalk{ctx: ctx, uid: site.UID, gid: site.GID}

	var st unix.Stat_t
	if err := unix.Fstat(hfd, &st); err != nil {
		return w.res, fmt.Errorf("stat home: %w", err)
	}

	w.dev = st.Dev
	w.res.Checked++

	if err := w.set(hfd, &st, site.UID, homeGID, HomeMode, true); err != nil {
		return w.res, fmt.Errorf("home: %w", err)
	}

	if err := w.dir(hfd, "", 1, false); err != nil {
		return w.res, err
	}

	m.opt.Log.InfoContext(ctx, "site permissions reset", "user", user, "checked", w.res.Checked, "changed", w.res.Changed, "skipped", w.res.Skipped)

	return w.res, nil
}

// openHomeForReset checks the site, its pool and its account under the
// manager lock and returns an open fd of the home.
func (m *Manager) openHomeForReset(user string) (int, Site, uint32, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	site, ok := m.reg.Site(user)
	if !ok {
		return -1, Site{}, 0, ErrNotFound
	}

	p, ok := m.reg.Pool(site.Pool)
	if !ok {
		return -1, Site{}, 0, fmt.Errorf("%w: pool %q is not registered", ErrPoolUnavailable, site.Pool)
	}

	acct, present, err := m.acc.Lookup(user)
	if err != nil {
		return -1, Site{}, 0, fmt.Errorf("look up %q: %w", user, err)
	}

	// Never hand files to an id that is not the site's account any more.
	if !present || acct.UID != site.UID || acct.GID != site.GID {
		return -1, Site{}, 0, fmt.Errorf("%w: %s is %d:%d (present %v), registry says %d:%d", ErrAccountMismatch, user, acct.UID, acct.GID, present, site.UID, site.GID)
	}

	pfd, err := openPool(p, m.opt.OwnerUID)
	if err != nil {
		return -1, Site{}, 0, err
	}

	defer unix.Close(pfd)

	hfd, err := unix.Openat2(pfd, user, &unix.OpenHow{Flags: dirFlags, Resolve: noEscape})
	if err != nil {
		return -1, Site{}, 0, fmt.Errorf("%w: open home: %w", ErrHomeUnsafe, err)
	}

	return hfd, site, m.homeGID(site.GID), nil
}

type resetWalk struct {
	ctx      context.Context
	uid, gid uint32
	dev      uint64
	res      agentapi.SitePermissionsResetResponse
}

func (w *resetWalk) skip(rel, reason string) {
	w.res.Skipped++

	if len(w.res.SkippedPaths) < agentapi.MaxPermissionsSkipped {
		w.res.SkippedPaths = append(w.res.SkippedPaths, agentapi.PermissionsSkip{Path: rel, Reason: reason})
	}
}

// dir resets the entries of the open directory dfd (path rel below the
// home, "" for the home). It does not close dfd.
func (w *resetWalk) dir(dfd int, rel string, depth int, private bool) error {
	// A second fd for reading, so the caller's fd stays usable.
	rfd, err := unix.Openat2(dfd, ".", &unix.OpenHow{Flags: dirFlags, Resolve: noEscape})
	if err != nil {
		return fmt.Errorf("%w: reopen %q: %w", ErrHomeUnsafe, rel, err)
	}

	f := os.NewFile(uintptr(rfd), "dir")
	defer f.Close()

	for {
		names, err := f.Readdirnames(256)

		for _, n := range names {
			p := n
			if rel != "" {
				p = rel + "/" + n
			}

			if err := w.entry(dfd, n, p, depth, private || rel == "" && privateDirs[n]); err != nil {
				return err
			}
		}

		if errors.Is(err, io.EOF) {
			return nil
		}

		if err != nil {
			return fmt.Errorf("list %q: %w", rel, err)
		}
	}
}

func (w *resetWalk) entry(parent int, name, rel string, depth int, private bool) error {
	if w.res.Checked%resetCheckRate == 0 {
		if err := w.ctx.Err(); err != nil {
			return err
		}
	}

	// O_PATH|O_NOFOLLOW opens a symbolic link itself instead of failing;
	// noEscape refuses any link on the way and other mounts.
	fd, err := unix.Openat2(parent, name, &unix.OpenHow{Flags: unix.O_PATH | unix.O_NOFOLLOW | unix.O_CLOEXEC, Resolve: noEscape})
	if err != nil {
		switch {
		case errors.Is(err, unix.ENOENT):
			return nil // removed meanwhile
		case errors.Is(err, unix.EXDEV):
			w.res.Checked++
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

	w.res.Checked++

	if st.Dev != w.dev {
		w.skip(rel, skipOtherFS)

		return nil
	}

	switch st.Mode & unix.S_IFMT {
	case unix.S_IFDIR:
		mode := uint32(ResetDirMode)
		if private {
			mode = ResetPrivateDir
		}

		if err := w.set(fd, &st, w.uid, w.gid, mode, true); err != nil {
			return fmt.Errorf("%q: %w", rel, err)
		}

		if depth >= maxRemoveDepth {
			w.skip(rel, skipTooDeep)

			return nil
		}

		return w.dir(fd, rel, depth+1, private)
	case unix.S_IFREG:
		// protected_hardlinks stops a site user from linking a foreign
		// file into the home; this check also holds where it is off.
		if st.Nlink > 1 && st.Uid != w.uid {
			w.skip(rel, skipHardLink)

			return nil
		}

		if err := w.set(fd, &st, w.uid, w.gid, fileMode(st.Mode, private), true); err != nil {
			return fmt.Errorf("%q: %w", rel, err)
		}
	case unix.S_IFLNK, unix.S_IFIFO, unix.S_IFSOCK:
		// Owner only: a link has no mode of its own, and a socket or FIFO
		// keeps the mode its program chose.
		if err := w.set(fd, &st, w.uid, w.gid, 0, false); err != nil {
			return fmt.Errorf("%q: %w", rel, err)
		}
	default:
		w.skip(rel, skipDevice)
	}

	return nil
}

// fileMode keeps a file executable if any execute bit was set and drops
// setuid, setgid and sticky.
func fileMode(mode uint32, private bool) uint32 {
	exec := mode&0o111 != 0

	switch {
	case private && exec:
		return ResetPrivateExec
	case private:
		return ResetPrivateFile
	case exec:
		return ResetExecMode
	default:
		return ResetFileMode
	}
}

// set changes owner and (if withMode) mode of the object behind the O_PATH
// or directory fd, only where they differ. chown clears setuid and setgid,
// so the mode is written after it.
func (w *resetWalk) set(fd int, st *unix.Stat_t, uid, gid, mode uint32, withMode bool) error {
	changed := false

	if st.Uid != uid || st.Gid != gid {
		if err := unix.Fchownat(fd, "", int(uid), int(gid), unix.AT_EMPTY_PATH|unix.AT_SYMLINK_NOFOLLOW); err != nil {
			return fmt.Errorf("chown: %w", err)
		}

		changed = true
	}

	if withMode && (changed || st.Mode&0o7777 != mode) {
		if err := fchmodFD(fd, mode); err != nil {
			return fmt.Errorf("chmod: %w", err)
		}

		changed = changed || st.Mode&0o7777 != mode
	}

	if changed {
		w.res.Changed++
	}

	return nil
}

// fchmodFD changes the mode of the object behind fd, which may be an O_PATH
// fd. fchmodat2 (Linux 6.6) does that directly; older kernels go through
// the fd's magic link in /proc, which names the same inode.
func fchmodFD(fd int, mode uint32) error {
	err := unix.Fchmodat(fd, "", mode, unix.AT_EMPTY_PATH)
	if !errors.Is(err, unix.EOPNOTSUPP) && !errors.Is(err, unix.ENOSYS) {
		return err
	}

	return unix.Fchmodat(unix.AT_FDCWD, "/proc/self/fd/"+strconv.Itoa(fd), mode, 0)
}
