// SPDX-License-Identifier: AGPL-3.0-or-later

package sites

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sync"

	"golang.org/x/sys/unix"

	"github.com/maki-it/hostashi/internal/agentapi"
)

// Errors a caller can act on.
var (
	ErrExists   = errors.New("sites: site or system account already exists")
	ErrNotFound = errors.New("sites: unknown site")
	ErrInUse    = errors.New("sites: site user still has running processes")
	// ErrAccountMismatch means the system account of that name no longer has
	// the uid and gid in the registry. Nothing is deleted then.
	ErrAccountMismatch = errors.New("sites: system account does not match the registry")
)

// Account is the identity of a freshly created Linux user.
type Account struct {
	UID, GID uint32
}

// Accounts creates and removes Linux users. The real implementation is
// package sysuser; tests use a fake.
type Accounts interface {
	// Exists reports whether a user or group of that name is already known.
	Exists(name string) (bool, error)
	// Lookup returns the ids of user name, or false if there is no such user.
	Lookup(name string) (Account, bool, error)
	// Create adds user and group name with the given home directory. It does
	// not create the directory. An unwanted existing name is an error.
	Create(ctx context.Context, name, home string) (Account, error)
	// Delete removes user and group. It returns ErrInUse while the user has
	// processes and nil if the user is already gone.
	Delete(ctx context.Context, name string) error
}

// Options configure a Manager.
type Options struct {
	// OwnerUID is the uid that must own pools and the state directory: 0 on a
	// server, the test user's uid in unprivileged tests.
	OwnerUID uint32
	// WebGID is the group of every site home: the web server's group
	// (www-data), so nginx can enter the home and read the web root. Zero
	// keeps the site's own group (tests).
	WebGID uint32
	Log    *slog.Logger
}

// Manager creates and deletes sites. Calls are serialized, because they
// change the account database, the pool and the registry together.
type Manager struct {
	mu  sync.Mutex
	reg *Registry
	acc Accounts
	opt Options
}

// NewManager builds a Manager on a loaded registry.
func NewManager(reg *Registry, acc Accounts, opt Options) *Manager {
	if opt.Log == nil {
		opt.Log = slog.New(slog.DiscardHandler)
	}

	return &Manager{reg: reg, acc: acc, opt: opt}
}

// Create adds the Linux user and group, the home directory below the pool and
// the registry entry. If a later step fails, the earlier ones are undone.
func (m *Manager) Create(ctx context.Context, user, pool string) (Site, error) {
	if err := (agentapi.SiteCreateRequest{User: user, Pool: pool}).Validate(); err != nil {
		return Site{}, err
	}

	if pool == "" {
		pool = agentapi.DefaultPool
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	p, ok := m.reg.Pool(pool)
	if !ok {
		return Site{}, fmt.Errorf("%w: pool %q is not registered", ErrPoolUnavailable, pool)
	}

	if _, dup := m.reg.Site(user); dup {
		return Site{}, ErrExists
	}

	taken, err := m.acc.Exists(user)
	if err != nil {
		return Site{}, fmt.Errorf("look up %q: %w", user, err)
	}

	if taken {
		return Site{}, ErrExists
	}

	pfd, err := openPool(p, m.opt.OwnerUID)
	if err != nil {
		return Site{}, err
	}

	defer unix.Close(pfd)

	acct, err := m.acc.Create(ctx, user, p.Path+"/"+user)
	if err != nil {
		return Site{}, fmt.Errorf("create account %q: %w", user, err)
	}

	// Rollbacks must also run when the request context is already canceled.
	undo := context.WithoutCancel(ctx)

	if err := createHome(pfd, user, acct.UID, acct.GID, m.homeGID(acct.GID)); err != nil {
		m.rollbackAccount(undo, user)

		return Site{}, err
	}

	site := Site{User: user, UID: acct.UID, GID: acct.GID, Pool: pool}

	if err := m.reg.add(site); err != nil {
		if rerr := removeHome(pfd, user, acct.UID, m.homeGID(acct.GID)); rerr != nil {
			m.opt.Log.ErrorContext(ctx, "rollback: remove home failed", "user", user, "err", rerr)
		}

		m.rollbackAccount(undo, user)

		return Site{}, err
	}

	m.opt.Log.InfoContext(ctx, "site created", "user", user, "pool", pool, "uid", acct.UID)

	return site, nil
}

func (m *Manager) rollbackAccount(ctx context.Context, user string) {
	if err := m.acc.Delete(ctx, user); err != nil {
		m.opt.Log.ErrorContext(ctx, "rollback: remove account failed", "user", user, "err", err)
	}
}

// Delete removes the account first, so a site with running processes is left
// untouched (ErrInUse), then the home directory, then the registry entry. If
// a step after the account fails, the registry entry stays and a retry
// finishes the job.
func (m *Manager) Delete(ctx context.Context, user string) error {
	if err := (agentapi.SiteDeleteRequest{User: user}).Validate(); err != nil {
		return err
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	site, ok := m.reg.Site(user)
	if !ok {
		return ErrNotFound
	}

	p, ok := m.reg.Pool(site.Pool)
	if !ok {
		return fmt.Errorf("%w: pool %q is not registered", ErrPoolUnavailable, site.Pool)
	}

	// Check the pool before touching the account: with the volume missing the
	// home cannot be removed, and the site must not end up half deleted.
	pfd, err := openPool(p, m.opt.OwnerUID)
	if err != nil {
		return err
	}

	defer unix.Close(pfd)

	acct, present, err := m.acc.Lookup(user)
	if err != nil {
		return fmt.Errorf("look up %q: %w", user, err)
	}

	if present && (acct.UID != site.UID || acct.GID != site.GID) {
		return fmt.Errorf("%w: %s is %d:%d, registry says %d:%d", ErrAccountMismatch, user, acct.UID, acct.GID, site.UID, site.GID)
	}

	if err := m.acc.Delete(ctx, user); err != nil {
		if errors.Is(err, ErrInUse) {
			return ErrInUse
		}

		return fmt.Errorf("delete account %q: %w", user, err)
	}

	if err := removeHome(pfd, user, site.UID, m.homeGID(site.GID)); err != nil {
		return err
	}

	if err := m.reg.remove(user); err != nil {
		return err
	}

	m.opt.Log.InfoContext(ctx, "site deleted", "user", user, "pool", site.Pool)

	return nil
}

func (m *Manager) homeGID(siteGID uint32) uint32 {
	if m.opt.WebGID != 0 {
		return m.opt.WebGID
	}

	return siteGID
}

// Home returns the home directory of a registered site.
func (m *Manager) Home(user string) (string, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()

	site, ok := m.reg.Site(user)
	if !ok {
		return "", false
	}

	p, ok := m.reg.Pool(site.Pool)
	if !ok {
		return "", false
	}

	return p.Path + "/" + user, true
}

// WebRoot returns the document root of a registered site.
func (m *Manager) WebRoot(user string) (string, bool) {
	home, ok := m.Home(user)
	if !ok {
		return "", false
	}

	return home + "/" + WebRootName, true
}

// Pools returns all registered pools sorted by name.
func (m *Manager) Pools() []Pool {
	m.mu.Lock()
	defer m.mu.Unlock()

	return m.reg.Pools()
}

// List returns all registered sites sorted by user.
func (m *Manager) List() []Site {
	m.mu.Lock()
	defer m.mu.Unlock()

	return m.reg.List()
}
