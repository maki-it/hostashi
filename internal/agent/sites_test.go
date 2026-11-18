// SPDX-License-Identifier: AGPL-3.0-or-later

package agent

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/maki-it/hostashi/internal/agentapi"
	"github.com/maki-it/hostashi/internal/agentrpc"
	"github.com/maki-it/hostashi/internal/php"
	"github.com/maki-it/hostashi/internal/sites"
	"github.com/maki-it/hostashi/internal/web"
)

// memAccounts is an in-memory account database.
type memAccounts struct {
	mu    sync.Mutex
	users map[string]sites.Account
}

func (m *memAccounts) ids() sites.Account {
	uid, gid := uint32(os.Getuid()), uint32(os.Getgid())
	if uid == 0 {
		uid, gid = 54321, 54321
	}

	return sites.Account{UID: uid, GID: gid}
}

func (m *memAccounts) Exists(name string) (bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	_, ok := m.users[name]

	return ok, nil
}

func (m *memAccounts) Lookup(name string) (sites.Account, bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	a, ok := m.users[name]

	return a, ok, nil
}

func (m *memAccounts) Create(_ context.Context, name, _ string) (sites.Account, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	a := m.ids()
	m.users[name] = a

	return a, nil
}

func (m *memAccounts) Delete(_ context.Context, name string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	delete(m.users, name)

	return nil
}

func startSiteAgent(t *testing.T) (*agentrpc.Client, string) {
	t.Helper()

	return startSiteAgentWith(t, nil)
}

func startSiteAgentWith(t *testing.T, w *web.Reconciler) (*agentrpc.Client, string) {
	t.Helper()

	return startSiteAgentPHP(t, w, nil)
}

func startSiteAgentPHP(t *testing.T, w *web.Reconciler, p *php.Manager) (*agentrpc.Client, string) {
	t.Helper()

	base, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}

	state, pool, run := filepath.Join(base, "state"), filepath.Join(base, "pool"), filepath.Join(base, "run")
	for _, d := range []string{state, pool, run} {
		if err := os.Mkdir(d, 0o700); err != nil {
			t.Fatal(err)
		}
	}

	conf := `{"version":1,"pools":[{"name":"default","path":"` + pool + `"}],"sites":[]}`
	if err := os.WriteFile(filepath.Join(state, sites.StateFile), []byte(conf), 0o600); err != nil {
		t.Fatal(err)
	}

	owner := uint32(os.Getuid())

	reg, err := sites.OpenRegistry(state, owner)
	if err != nil {
		t.Fatal(err)
	}

	mgr := sites.NewManager(reg, &memAccounts{users: map[string]sites.Account{}}, sites.Options{OwnerUID: owner})

	sock := filepath.Join(run, "agent.sock")
	s := NewServer(Config{Version: "t", Sites: mgr, Web: w, PHP: p}, owner)

	if err := s.Listen(context.Background(), sock, -1); err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})

	go func() { _ = s.Serve(ctx); close(done) }()

	t.Cleanup(func() { cancel(); <-done })

	return agentrpc.NewClient(sock), pool
}

func wantCode(t *testing.T, err error, code string) {
	t.Helper()

	var e *agentrpc.Error
	if !errors.As(err, &e) || e.Code != code {
		t.Fatalf("got %v, want code %s", err, code)
	}
}

func TestSiteOperationsThroughServer(t *testing.T) {
	c, pool := startSiteAgent(t)
	ctx := context.Background()

	created, err := agentapi.SiteCreate.Call(ctx, c, agentapi.SiteCreateRequest{User: "blog"})
	if err != nil {
		t.Fatal(err)
	}

	if created.User != "blog" || created.Pool != "default" || created.UID == 0 {
		t.Fatalf("created = %+v", created)
	}

	if _, err := os.Stat(filepath.Join(pool, "blog")); err != nil {
		t.Fatal(err)
	}

	list, err := agentapi.SiteList.Call(ctx, c, agentapi.SiteListRequest{})
	if err != nil || len(list.Sites) != 1 || list.Sites[0] != created {
		t.Fatalf("list = %+v, %v", list, err)
	}

	_, err = agentapi.SiteCreate.Call(ctx, c, agentapi.SiteCreateRequest{User: "blog"})
	wantCode(t, err, agentrpc.CodeConflict)

	_, err = agentapi.SiteCreate.Call(ctx, c, agentapi.SiteCreateRequest{User: "shop", Pool: "missing"})
	wantCode(t, err, agentrpc.CodePrecondition)

	_, err = agentapi.SiteDelete.Call(ctx, c, agentapi.SiteDeleteRequest{User: "nope"})
	wantCode(t, err, agentrpc.CodeNotFound)

	if _, err := agentapi.SiteDelete.Call(ctx, c, agentapi.SiteDeleteRequest{User: "blog"}); err != nil {
		t.Fatal(err)
	}

	if _, err := os.Lstat(filepath.Join(pool, "blog")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("home still there: %v", err)
	}
}

func TestSiteOperationsRejectBadInput(t *testing.T) {
	c, _ := startSiteAgent(t)

	// The client refuses before sending ...
	_, err := agentapi.SiteCreate.Call(context.Background(), c, agentapi.SiteCreateRequest{User: "../etc"})
	wantCode(t, err, agentrpc.CodeInvalidArgument)

	// ... and the server refuses on its own, for a client that skips validation.
	// uid, gid and paths are not request fields, so they are unknown fields.
	raw := agentrpc.Op[rawRequest, rawResponse]{Name: "site.create"}

	for _, body := range []string{
		`{"user":"blog","uid":0}`, `{"user":"blog","home":"/etc"}`, `{"user":"blog","pool":"/etc"}`,
		`{"user":"Blog"}`, `{"user":"root"}`, `{}`,
	} {
		_, err := raw.Call(context.Background(), c, rawRequest(body))
		if err == nil {
			t.Errorf("%s accepted", body)
		}
	}
}

// rawRequest sends its content as the params of a call, without client validation.
type rawRequest string

func (rawRequest) Validate() error { return nil }

func (r rawRequest) MarshalJSON() ([]byte, error) { return []byte(r), nil }

type rawResponse map[string]any

func TestInternalErrorsDoNotLeakDetails(t *testing.T) {
	err := mapSiteError(errors.New("open /var/lib/secret: permission denied"))
	if err == nil || !strings.Contains(err.Error(), "/var/lib") {
		t.Fatal("unknown errors must pass through for logging; the server hides them from callers")
	}

	for _, e := range []error{sites.ErrNotFound, sites.ErrExists, sites.ErrInUse, sites.ErrPoolUnavailable, sites.ErrHomeUnsafe, sites.ErrAccountMismatch} {
		wrapped := mapSiteError(errors.Join(e, errors.New("/srv/secret/path")))

		var ae *agentrpc.Error
		if !errors.As(wrapped, &ae) || strings.Contains(ae.Message, "/srv") {
			t.Errorf("%v mapped to %v", e, wrapped)
		}
	}
}
