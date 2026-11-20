// SPDX-License-Identifier: AGPL-3.0-or-later

// Package sshaccess keeps the SSH and SFTP access of the sites (ADR-0032):
// one root-owned key file per site that sshd reads for the members of the
// group hostashi-sites, and the site user's login shell, which is a real
// shell only while the site has a shell key. A disabled site keeps its keys
// in the agent's state but gets an empty key file. The agent calls it.
package sshaccess

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"os"
	"path/filepath"
	"sync"
	"syscall"

	"github.com/maki-it/hostashi/internal/agentapi"
	"github.com/maki-it/hostashi/internal/sshkeys"
	"github.com/maki-it/hostashi/internal/sysuser"
)

// DefaultKeyDir is where sshd looks for the sites' keys
// (AuthorizedKeysFile in packaging/ssh/10-hostashi.conf). The installer
// creates it, root 0755.
const DefaultKeyDir = "/etc/hostashi-ssh/keys"

// Shells sets a site user's login shell (sysuser.Accounts on a server).
type Shells interface {
	SetShell(ctx context.Context, name, shell string) error
}

// Manager applies the key lists. Set at least StateFile and Shells.
type Manager struct {
	StateFile string // root 0600, the last applied lists
	KeyDir    string // default DefaultKeyDir
	Shells    Shells
	Log       *slog.Logger

	mu sync.Mutex
	// dirOwner is the uid the key directory must belong to: root, except
	// in tests that run as a normal user.
	dirOwner *uint32
}

type siteState struct {
	Keys     []agentapi.SSHKey `json:"keys,omitempty"`
	Disabled bool              `json:"disabled,omitempty"`
}

type state struct {
	Version int                  `json:"version"`
	Sites   map[string]siteState `json:"sites"`
}

func (m *Manager) keyDir() string {
	if m.KeyDir != "" {
		return m.KeyDir
	}

	return DefaultKeyDir
}

func (m *Manager) log() *slog.Logger {
	if m.Log != nil {
		return m.Log
	}

	return slog.New(slog.DiscardHandler)
}

// Apply replaces the keys of a site and reports whether the site user now
// has a login shell.
func (m *Manager) Apply(ctx context.Context, req agentapi.SSHApplyRequest) (bool, error) {
	if err := req.Validate(); err != nil {
		return false, err
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	st, err := m.load()
	if err != nil {
		return false, err
	}

	ss := st.Sites[req.Site]
	ss.Keys = req.Keys

	if len(ss.Keys) == 0 && !ss.Disabled {
		delete(st.Sites, req.Site)
	} else {
		st.Sites[req.Site] = ss
	}

	if err := m.save(st); err != nil {
		return false, err
	}

	return m.render(ctx, req.Site, ss)
}

// SetDisabled empties the key file of a disabled site and takes its shell
// away, or puts both back for an enabled one. Running sessions are not
// ended.
func (m *Manager) SetDisabled(ctx context.Context, site string, disabled bool) error {
	if err := agentapi.ValidateSiteUser(site); err != nil {
		return err
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	st, err := m.load()
	if err != nil {
		return err
	}

	ss, had := st.Sites[site]
	if ss.Disabled == disabled || (!had && !disabled) {
		return nil
	}

	ss.Disabled = disabled

	if len(ss.Keys) == 0 && !disabled {
		delete(st.Sites, site)
	} else {
		st.Sites[site] = ss
	}

	if err := m.save(st); err != nil {
		return err
	}

	_, err = m.render(ctx, site, ss)

	return err
}

// Remove deletes the key file and the state of a deleted site. The user
// itself is removed by the caller.
func (m *Manager) Remove(_ context.Context, site string) error {
	if err := agentapi.ValidateSiteUser(site); err != nil {
		return err
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	if err := os.Remove(filepath.Join(m.keyDir(), site)); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return fmt.Errorf("sshaccess: %w", err)
	}

	st, err := m.load()
	if err != nil {
		return err
	}

	if _, ok := st.Sites[site]; !ok {
		return nil
	}

	delete(st.Sites, site)

	return m.save(st)
}

// render writes the key file first and sets the shell second, so a key
// file without shell keys never meets a leftover login shell for long:
// sshd forces internal-sftp for those keys anyway.
func (m *Manager) render(ctx context.Context, site string, ss siteState) (bool, error) {
	keys := ss.Keys
	if ss.Disabled {
		keys = nil
	}

	list := agentapi.SSHApplyRequest{Site: site, Keys: keys}.KeyList()

	data, err := sshkeys.Render(list)
	if err != nil {
		return false, err
	}

	owner := uint32(0)
	if m.dirOwner != nil {
		owner = *m.dirOwner
	}

	if err := checkKeyDir(m.keyDir(), owner); err != nil {
		return false, err
	}

	if err := writeFile(filepath.Join(m.keyDir(), site), data); err != nil {
		return false, err
	}

	shell := sshkeys.NeedsShell(list)

	login := sysuser.NoLogin
	if shell {
		login = sysuser.Bash
	}

	if err := m.Shells.SetShell(ctx, site, login); err != nil {
		return false, fmt.Errorf("sshaccess: login shell of %s: %w", site, err)
	}

	m.log().InfoContext(ctx, "ssh keys applied", "site", site, "keys", len(list), "shell", shell)

	return shell, nil
}

// checkKeyDir refuses a key directory that is a symlink or that anyone but
// root can write: sshd would read keys from it.
func checkKeyDir(dir string, owner uint32) error {
	fi, err := os.Lstat(dir)
	if err != nil {
		return fmt.Errorf("sshaccess: key directory: %w", err)
	}

	sys, ok := fi.Sys().(*syscall.Stat_t)
	if !fi.IsDir() || !ok || sys.Uid != owner || fi.Mode().Perm()&0o022 != 0 {
		return fmt.Errorf("sshaccess: %s is not a root-owned directory closed to writes", dir)
	}

	return nil
}

func (m *Manager) load() (state, error) {
	st := state{Version: 1, Sites: map[string]siteState{}}

	b, err := os.ReadFile(m.StateFile)
	if errors.Is(err, fs.ErrNotExist) {
		return st, nil
	}

	if err != nil {
		return st, fmt.Errorf("sshaccess: %w", err)
	}

	if err := json.Unmarshal(b, &st); err != nil {
		return st, fmt.Errorf("sshaccess: state: %w", err)
	}

	if st.Sites == nil {
		st.Sites = map[string]siteState{}
	}

	return st, nil
}

func (m *Manager) save(st state) error {
	b, err := json.Marshal(st)
	if err != nil {
		return fmt.Errorf("sshaccess: %w", err)
	}

	return writeFileMode(m.StateFile, b, 0o600)
}

// writeFile replaces a key file atomically: root-owned, 0644, as sshd's
// StrictModes wants it.
func writeFile(name string, data []byte) error { return writeFileMode(name, data, 0o644) }

func writeFileMode(name string, data []byte, mode os.FileMode) error {
	tmp := name + ".tmp"
	_ = os.Remove(tmp)

	f, err := os.OpenFile(tmp, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return fmt.Errorf("sshaccess: %w", err)
	}

	err = errors.Join(
		f.Chmod(mode),
		func() error { _, err := f.Write(data); return err }(),
		f.Sync(),
	)

	if cerr := f.Close(); err == nil {
		err = cerr
	}

	if err == nil {
		err = os.Rename(tmp, name)
	}

	if err != nil {
		_ = os.Remove(tmp)

		return fmt.Errorf("sshaccess: %w", err)
	}

	return nil
}
