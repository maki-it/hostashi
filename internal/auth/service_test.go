// SPDX-License-Identifier: AGPL-3.0-or-later

package auth

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/maki-it/hostashi/internal/store"
)

const testPassword = "a long enough password"

type env struct {
	svc       *Service
	st        *store.Store
	tokenPath string
	clk       *fakeClock
}

func newEnv(t *testing.T) *env {
	t.Helper()

	dir := filepath.Join(t.TempDir(), "lib")
	if err := os.Mkdir(dir, 0o750); err != nil {
		t.Fatal(err)
	}

	st, err := store.Open(context.Background(), filepath.Join(dir, "h.db"))
	if err != nil {
		t.Fatal(err)
	}

	t.Cleanup(func() { _ = st.Close() })

	clk := &fakeClock{t: time.Now()}
	tokenPath := filepath.Join(dir, "setup-token")

	return &env{
		svc:       NewService(Config{Store: st, SetupTokenPath: tokenPath, Now: clk.now}),
		st:        st,
		tokenPath: tokenPath,
		clk:       clk,
	}
}

// setup creates the first admin through the real setup flow.
func (e *env) setup(t *testing.T) string {
	t.Helper()

	ctx := context.Background()
	if err := EnsureSetupToken(ctx, e.st, e.tokenPath, e.clk.now()); err != nil {
		t.Fatal(err)
	}

	tok, err := ReadSetupToken(e.tokenPath, e.clk.now())
	if err != nil {
		t.Fatal(err)
	}

	if _, err := e.svc.Setup(ctx, tok, "kim", testPassword, "10.0.0.1"); err != nil {
		t.Fatal(err)
	}

	return tok
}

func TestSetupFlow(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()

	if req, err := e.svc.SetupRequired(ctx); err != nil || !req {
		t.Fatalf("SetupRequired %v %v", req, err)
	}

	if err := EnsureSetupToken(ctx, e.st, e.tokenPath, e.clk.now()); err != nil {
		t.Fatal(err)
	}

	fi, err := os.Stat(e.tokenPath)
	if err != nil || fi.Mode().Perm() != 0o600 {
		t.Fatalf("token file: %v %v", fi, err)
	}

	tok, err := ReadSetupToken(e.tokenPath, e.clk.now())
	if err != nil || len(tok) != 48 {
		t.Fatalf("token %q %v", tok, err)
	}

	// A second Ensure keeps the same token.
	if err := EnsureSetupToken(ctx, e.st, e.tokenPath, e.clk.now()); err != nil {
		t.Fatal(err)
	}

	if tok2, _ := ReadSetupToken(e.tokenPath, e.clk.now()); tok2 != tok {
		t.Fatal("token changed")
	}

	// Wrong token, bad username, bad password are all refused.
	if _, err := e.svc.Setup(ctx, "wrong", "kim", testPassword, "1.1.1.1"); !errors.Is(err, ErrSetupNotAllowed) {
		t.Fatalf("wrong token: %v", err)
	}

	if _, err := e.svc.Setup(ctx, tok, "Kim!", testPassword, "1.1.1.1"); !errors.Is(err, ErrInvalidUsername) {
		t.Fatalf("bad username: %v", err)
	}

	if _, err := e.svc.Setup(ctx, tok, "kim", "short", "1.1.1.1"); !errors.Is(err, ErrWeakPassword) {
		t.Fatalf("weak password: %v", err)
	}

	if _, err := e.svc.Setup(ctx, tok, "kim", testPassword, "1.1.1.1"); err != nil {
		t.Fatal(err)
	}

	if _, err := os.Stat(e.tokenPath); !os.IsNotExist(err) {
		t.Fatal("token file still there after setup")
	}

	// Setup works only once, even with a valid token.
	if err := os.WriteFile(e.tokenPath, []byte(tok+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	if _, err := e.svc.Setup(ctx, tok, "other", testPassword, "1.1.1.1"); !errors.Is(err, ErrSetupNotAllowed) {
		t.Fatalf("second setup: %v", err)
	}

	if err := EnsureSetupToken(ctx, e.st, filepath.Join(filepath.Dir(e.tokenPath), "unused"), e.clk.now()); err != nil {
		t.Fatal(err)
	}

	if _, err := os.Stat(filepath.Join(filepath.Dir(e.tokenPath), "unused")); !os.IsNotExist(err) {
		t.Fatal("token created although a user exists")
	}
}

func TestSetupTokenExpiryAndPermissions(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()

	if err := EnsureSetupToken(ctx, e.st, e.tokenPath, e.clk.now()); err != nil {
		t.Fatal(err)
	}

	first, _ := ReadSetupToken(e.tokenPath, e.clk.now())

	late := e.clk.now().Add(SetupTokenTTL + time.Minute)
	if _, err := ReadSetupToken(e.tokenPath, late); !errors.Is(err, ErrNoSetupToken) {
		t.Fatalf("expired token accepted: %v", err)
	}

	// Ensure replaces an expired token.
	if err := EnsureSetupToken(ctx, e.st, e.tokenPath, late); err != nil {
		t.Fatal(err)
	}

	if err := os.Chtimes(e.tokenPath, late, late); err != nil {
		t.Fatal(err)
	}

	if second, err := ReadSetupToken(e.tokenPath, late); err != nil || second == first {
		t.Fatalf("token not renewed: %v", err)
	}

	// A token file that others can read is not trusted.
	if err := os.Chmod(e.tokenPath, 0o644); err != nil {
		t.Fatal(err)
	}

	if _, err := ReadSetupToken(e.tokenPath, late); !errors.Is(err, ErrNoSetupToken) {
		t.Fatalf("world-readable token accepted: %v", err)
	}

	// A symlink is not followed.
	link := filepath.Join(filepath.Dir(e.tokenPath), "link")
	if err := os.Symlink(e.tokenPath, link); err != nil {
		t.Fatal(err)
	}

	if _, err := ReadSetupToken(link, late); !errors.Is(err, ErrNoSetupToken) {
		t.Fatalf("symlink followed: %v", err)
	}

	// Garbage content.
	if err := os.WriteFile(e.tokenPath, []byte("short\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	if _, err := ReadSetupToken(e.tokenPath, e.clk.now()); !errors.Is(err, ErrNoSetupToken) {
		t.Fatalf("garbage token accepted: %v", err)
	}
}

func TestSetupRateLimit(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()

	if err := EnsureSetupToken(ctx, e.st, e.tokenPath, e.clk.now()); err != nil {
		t.Fatal(err)
	}

	for range 10 {
		_, _ = e.svc.Setup(ctx, "wrong", "kim", testPassword, "9.9.9.9")
	}

	tok, _ := ReadSetupToken(e.tokenPath, e.clk.now())
	if _, err := e.svc.Setup(ctx, tok, "kim", testPassword, "9.9.9.9"); !errors.Is(err, ErrRateLimited) {
		t.Fatalf("want ErrRateLimited, got %v", err)
	}

	// Another address is not affected.
	if _, err := e.svc.Setup(ctx, tok, "kim", testPassword, "9.9.9.8"); err != nil {
		t.Fatal(err)
	}
}

func TestLoginSessionLogout(t *testing.T) {
	e := newEnv(t)
	e.setup(t)

	ctx := context.Background()

	tok, u, err := e.svc.Login(ctx, "kim", testPassword, "10.0.0.1")
	if err != nil || u.Username != "kim" || u.PasswordHash != "" {
		t.Fatalf("login: %+v %v", u, err)
	}

	if len(tok) != 43 {
		t.Fatalf("token length %d", len(tok))
	}

	got, err := e.svc.Authenticate(ctx, tok)
	if err != nil || got.Username != "kim" || got.Role != "admin" {
		t.Fatalf("authenticate: %+v %v", got, err)
	}

	// The database holds only the hash of the token.
	var n int
	if err := queryCount(t, e.st, tok, &n); err != nil || n != 0 {
		t.Fatalf("raw token stored: %d %v", n, err)
	}

	// Session expires.
	e.clk.t = e.clk.t.Add(SessionTTL + time.Second)

	if _, err := e.svc.Authenticate(ctx, tok); !errors.Is(err, ErrUnauthenticated) {
		t.Fatalf("expired session: %v", err)
	}

	e.clk.t = e.clk.t.Add(-SessionTTL - time.Second)

	// Logout kills the session; a repeated logout is fine.
	if err := e.svc.Logout(ctx, tok); err != nil {
		t.Fatal(err)
	}

	if _, err := e.svc.Authenticate(ctx, tok); !errors.Is(err, ErrUnauthenticated) {
		t.Fatalf("session alive after logout: %v", err)
	}

	if err := e.svc.Logout(ctx, tok); err != nil {
		t.Fatal(err)
	}

	for _, bad := range []string{"", "nope", strings.Repeat("a", 129)} {
		if _, err := e.svc.Authenticate(ctx, bad); !errors.Is(err, ErrUnauthenticated) {
			t.Fatalf("token %q: %v", bad, err)
		}
	}
}

func queryCount(t *testing.T, st *store.Store, token string, n *int) error {
	t.Helper()

	return st.RawCount(context.Background(), `SELECT count(*) FROM sessions WHERE token_hash = ?`, []byte(token), n)
}

func TestLoginFailuresAreUniform(t *testing.T) {
	e := newEnv(t)
	e.setup(t)

	ctx := context.Background()

	for name, tc := range map[string][2]string{
		"wrong password": {"kim", "not the password!"},
		"unknown user":   {"nobody", testPassword},
		"empty":          {"", ""},
		"huge password":  {"kim", strings.Repeat("x", 10000)},
	} {
		t.Run(name, func(t *testing.T) {
			if _, _, err := e.svc.Login(ctx, tc[0], tc[1], "10.0.0.2"); !errors.Is(err, ErrInvalidCredentials) {
				t.Fatalf("got %v", err)
			}
		})
	}
}

func TestLoginRateLimitAndRecovery(t *testing.T) {
	e := newEnv(t)
	e.setup(t)

	ctx := context.Background()

	for range 10 {
		_, _, _ = e.svc.Login(ctx, "kim", "wrong password!!", "6.6.6.6")
	}

	// Blocked even with the right password.
	if _, _, err := e.svc.Login(ctx, "kim", testPassword, "6.6.6.6"); !errors.Is(err, ErrRateLimited) {
		t.Fatalf("want ErrRateLimited, got %v", err)
	}

	e.clk.t = e.clk.t.Add(16 * time.Minute)

	if _, _, err := e.svc.Login(ctx, "kim", testPassword, "6.6.6.6"); err != nil {
		t.Fatalf("not unblocked after the window: %v", err)
	}
}

func TestDisabledUserAndEmptyHashCannotLogin(t *testing.T) {
	e := newEnv(t)
	e.setup(t)

	ctx := context.Background()

	tok, _, err := e.svc.Login(ctx, "kim", testPassword, "10.0.0.1")
	if err != nil {
		t.Fatal(err)
	}

	if err := e.st.RawExec(ctx, `UPDATE users SET disabled = 1`); err != nil {
		t.Fatal(err)
	}

	if _, _, err := e.svc.Login(ctx, "kim", testPassword, "10.0.0.1"); !errors.Is(err, ErrInvalidCredentials) {
		t.Fatalf("disabled user logged in: %v", err)
	}

	// An existing session of a disabled user stops working too.
	if _, err := e.svc.Authenticate(ctx, tok); !errors.Is(err, ErrUnauthenticated) {
		t.Fatalf("session of disabled user alive: %v", err)
	}

	if err := e.st.RawExec(ctx, `UPDATE users SET disabled = 0, password_hash = ''`); err != nil {
		t.Fatal(err)
	}

	if _, _, err := e.svc.Login(ctx, "kim", "", "10.0.0.1"); !errors.Is(err, ErrInvalidCredentials) {
		t.Fatalf("empty hash accepted an empty password: %v", err)
	}
}

func TestCleanupRemovesExpiredSessions(t *testing.T) {
	e := newEnv(t)
	e.setup(t)

	ctx := context.Background()

	if _, _, err := e.svc.Login(ctx, "kim", testPassword, "10.0.0.1"); err != nil {
		t.Fatal(err)
	}

	e.clk.t = e.clk.t.Add(SessionTTL + time.Minute)

	if err := e.svc.Cleanup(ctx); err != nil {
		t.Fatal(err)
	}

	var n int
	if err := e.st.RawCount(ctx, `SELECT count(*) FROM sessions WHERE 1 = ?`, 1, &n); err != nil || n != 0 {
		t.Fatalf("sessions left: %d %v", n, err)
	}
}
