// SPDX-License-Identifier: AGPL-3.0-or-later

package cpimport

import (
	"errors"
	"fmt"
	"path/filepath"
	"time"

	"golang.org/x/sys/unix"

	"github.com/maki-it/hostashi/internal/agentapi"
	"github.com/maki-it/hostashi/internal/sites"
)

// ErrNothingImported: the site has no imported files to activate.
var ErrNothingImported = errors.New("cpimport: the site has no imported files")

// previousPrefix names the kept web root: htdocs-before-import-<UTC time>.
const previousPrefix = sites.WebRootName + "-before-import-"

// Activate makes <home>/cloudpanel-import the site's web root. The two
// directories are exchanged in one rename, so the site never has no web
// root; the previous one is kept as htdocs-before-import-<time>. It is
// refused while an import runs. A later import copies into a new
// cloudpanel-import from scratch.
func (a *Agent) Activate(site string) (agentapi.CloudPanelActivated, error) {
	if err := (agentapi.CloudPanelActivateRequest{Site: site}).Validate(); err != nil {
		return agentapi.CloudPanelActivated{}, fmt.Errorf("%w: %w", ErrInvalid, err)
	}

	a.mu.Lock()
	defer a.mu.Unlock()

	home, err := siteHome(a.StateDir, site, owner())
	if err != nil {
		return agentapi.CloudPanelActivated{}, err
	}

	if err := ensureDir(a.Dir); err != nil {
		return agentapi.CloudPanelActivated{}, err
	}

	// The job holds the lock while it writes; taking it keeps a new one
	// from starting until the swap is done.
	unlock, err := lock(filepath.Join(a.Dir, lockFile))
	if err != nil {
		return agentapi.CloudPanelActivated{}, err
	}
	defer unlock()

	all, err := a.imports()
	if err != nil {
		return agentapi.CloudPanelActivated{}, err
	}

	for _, r := range all {
		if r.State == agentapi.ImportQueued && time.Since(r.Created) < time.Minute {
			return agentapi.CloudPanelActivated{}, ErrBusy
		}
	}

	prev := previousPrefix + time.Now().UTC().Format("20060102-150405")

	if err := swapWebRoot(home, prev); err != nil {
		return agentapi.CloudPanelActivated{}, err
	}

	return agentapi.CloudPanelActivated{Site: site, Previous: prev}, nil
}

// swapWebRoot exchanges htdocs and cloudpanel-import below the home, gives
// the new web root the web root's mode and renames the old one to prev.
// Every name is opened without symlinks and must be the site user's.
func swapWebRoot(home sites.Home, prev string) error {
	hfd, err := unix.Openat2(unix.AT_FDCWD, home.Path, &unix.OpenHow{
		Flags:   unix.O_RDONLY | unix.O_DIRECTORY | unix.O_NOFOLLOW | unix.O_CLOEXEC,
		Resolve: unix.RESOLVE_NO_SYMLINKS | unix.RESOLVE_NO_MAGICLINKS,
	})
	if err != nil {
		return fmt.Errorf("open home %s: %w", home.Path, err)
	}
	defer unix.Close(hfd)

	if !ownedDir(hfd, ".", home.UID) {
		return fmt.Errorf("home %s is not the site user's", home.Path)
	}

	if !ownedDir(hfd, agentapi.CloudPanelImportDir, home.UID) {
		return ErrNothingImported
	}

	hasRoot := ownedDir(hfd, sites.WebRootName, home.UID)

	if hasRoot {
		err = unix.Renameat2(hfd, agentapi.CloudPanelImportDir, hfd, sites.WebRootName, unix.RENAME_EXCHANGE)
	} else {
		// No web root (or not a directory of the site user): nothing to
		// keep, but never replace what is there.
		err = unix.Renameat2(hfd, agentapi.CloudPanelImportDir, hfd, sites.WebRootName, unix.RENAME_NOREPLACE)
	}

	if err != nil {
		return fmt.Errorf("cpimport: swap web root: %w", err)
	}

	// The name could have been replaced between the check and the
	// rename; the new web root must still be the site user's directory.
	fd, err := unix.Openat2(hfd, sites.WebRootName, &unix.OpenHow{
		Flags:   unix.O_RDONLY | unix.O_DIRECTORY | unix.O_NOFOLLOW | unix.O_CLOEXEC,
		Resolve: unix.RESOLVE_BENEATH | unix.RESOLVE_NO_SYMLINKS | unix.RESOLVE_NO_MAGICLINKS,
	})
	if err != nil || !ownedFD(fd, home.UID) {
		if err == nil {
			_ = unix.Close(fd)
		}

		undo(hfd, hasRoot)

		return errors.New("cpimport: the imported directory changed during the swap")
	}

	defer unix.Close(fd)

	if err := unix.Fchmod(fd, sites.WebRootMode); err != nil {
		undo(hfd, hasRoot)

		return fmt.Errorf("cpimport: web root mode: %w", err)
	}

	if hasRoot {
		if err := unix.Renameat2(hfd, agentapi.CloudPanelImportDir, hfd, prev, unix.RENAME_NOREPLACE); err != nil {
			return fmt.Errorf("cpimport: keep the previous web root: %w", err)
		}
	}

	return nil
}

func undo(hfd int, hasRoot bool) {
	if hasRoot {
		_ = unix.Renameat2(hfd, agentapi.CloudPanelImportDir, hfd, sites.WebRootName, unix.RENAME_EXCHANGE)
	} else {
		_ = unix.Renameat2(hfd, sites.WebRootName, hfd, agentapi.CloudPanelImportDir, unix.RENAME_NOREPLACE)
	}
}

func ownedDir(dirfd int, name string, uid uint32) bool {
	var st unix.Stat_t
	if err := unix.Fstatat(dirfd, name, &st, unix.AT_SYMLINK_NOFOLLOW); err != nil {
		return false
	}

	return st.Mode&unix.S_IFMT == unix.S_IFDIR && st.Uid == uid
}

func ownedFD(fd int, uid uint32) bool {
	var st unix.Stat_t

	return unix.Fstat(fd, &st) == nil && st.Mode&unix.S_IFMT == unix.S_IFDIR && st.Uid == uid
}
