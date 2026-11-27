// SPDX-License-Identifier: AGPL-3.0-or-later

package sites

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"sync"
	"testing"
)

// testIDs returns ids that the current user may chown to: its own, or fixed
// unprivileged ids when the tests run as root.
func testIDs() (uid, gid uint32) {
	uid, gid = uint32(os.Getuid()), uint32(os.Getgid())

	if uid == 0 {
		uid = 54321
	}

	if gid == 0 {
		gid = 54321
	}

	return uid, gid
}

// fakeAccounts records calls instead of running useradd.
type fakeAccounts struct {
	mu        sync.Mutex
	users     map[string]Account
	groups    map[string]bool
	inUse     bool
	createErr error
	// distinct gives every new account its own ids (copy tests).
	distinct bool
	creates  int
	deletes  int
}

func newFake() *fakeAccounts {
	return &fakeAccounts{users: map[string]Account{}, groups: map[string]bool{}}
}

func (f *fakeAccounts) Exists(name string) (bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()

	_, u := f.users[name]

	return u || f.groups[name], nil
}

func (f *fakeAccounts) Lookup(name string) (Account, bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()

	a, ok := f.users[name]

	return a, ok, nil
}

func (f *fakeAccounts) Create(_ context.Context, name, _ string) (Account, error) {
	f.mu.Lock()
	defer f.mu.Unlock()

	f.creates++

	if f.createErr != nil {
		return Account{}, f.createErr
	}

	uid, gid := testIDs()
	if f.distinct {
		uid, gid = 60000+uint32(f.creates), 60000+uint32(f.creates)
	}

	a := Account{UID: uid, GID: gid}
	f.users[name] = a

	return a, nil
}

func (f *fakeAccounts) Delete(_ context.Context, name string) error {
	f.mu.Lock()
	defer f.mu.Unlock()

	f.deletes++

	if f.inUse {
		return ErrInUse
	}

	delete(f.users, name)

	return nil
}

type env struct {
	t     *testing.T
	state string
	pool  string
	reg   *Registry
	acc   *fakeAccounts
	mgr   *Manager
}

// newEnv builds a registry with one pool, both inside private directories.
func newEnv(t *testing.T) *env {
	t.Helper()

	base, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}

	e := &env{t: t, state: filepath.Join(base, "state"), pool: filepath.Join(base, "pool"), acc: newFake()}

	for _, d := range []string{e.state, e.pool} {
		if err := os.Mkdir(d, 0o700); err != nil {
			t.Fatal(err)
		}
	}

	e.open()

	return e
}

// open (re)loads the registry from disk, as after an agent restart.
func (e *env) open() {
	e.t.Helper()

	owner := uint32(os.Getuid())

	reg, err := OpenRegistry(e.state, owner)
	if err != nil {
		e.t.Fatal(err)
	}

	reg.pools = map[string]Pool{"default": {Name: "default", Path: e.pool}}

	e.reg = reg
	e.mgr = NewManager(reg, e.acc, Options{OwnerUID: owner})
}

func must(t *testing.T, err error) {
	t.Helper()

	if err != nil {
		t.Fatal(err)
	}
}

func wantErr(t *testing.T, err, want error) {
	t.Helper()

	if !errors.Is(err, want) {
		t.Fatalf("got %v, want %v", err, want)
	}
}
