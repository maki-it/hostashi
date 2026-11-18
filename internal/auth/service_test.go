// SPDX-License-Identifier: AGPL-3.0-or-later

package auth

import (
	"bytes"
	"context"
	"encoding/base32"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/maki-it/hostashi/internal/secrets"
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

	box, err := secrets.New(bytes.Repeat([]byte{9}, 32))
	if err != nil {
		t.Fatal(err)
	}

	clk := &fakeClock{t: time.Now()}
	tokenPath := filepath.Join(dir, "setup-token")

	return &env{
		svc:       NewService(Config{Store: st, SetupTokenPath: tokenPath, Box: box, Now: clk.now}),
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

	tok, u, err := e.svc.Login(ctx, "kim", testPassword, "", "10.0.0.1")
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
			if _, _, err := e.svc.Login(ctx, tc[0], tc[1], "", "10.0.0.2"); !errors.Is(err, ErrInvalidCredentials) {
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
		_, _, _ = e.svc.Login(ctx, "kim", "wrong password!!", "", "6.6.6.6")
	}

	// Blocked even with the right password.
	if _, _, err := e.svc.Login(ctx, "kim", testPassword, "", "6.6.6.6"); !errors.Is(err, ErrRateLimited) {
		t.Fatalf("want ErrRateLimited, got %v", err)
	}

	e.clk.t = e.clk.t.Add(16 * time.Minute)

	if _, _, err := e.svc.Login(ctx, "kim", testPassword, "", "6.6.6.6"); err != nil {
		t.Fatalf("not unblocked after the window: %v", err)
	}
}

func TestDisabledUserAndEmptyHashCannotLogin(t *testing.T) {
	e := newEnv(t)
	e.setup(t)

	ctx := context.Background()

	tok, _, err := e.svc.Login(ctx, "kim", testPassword, "", "10.0.0.1")
	if err != nil {
		t.Fatal(err)
	}

	if err := e.st.RawExec(ctx, `UPDATE users SET disabled = 1`); err != nil {
		t.Fatal(err)
	}

	if _, _, err := e.svc.Login(ctx, "kim", testPassword, "", "10.0.0.1"); !errors.Is(err, ErrInvalidCredentials) {
		t.Fatalf("disabled user logged in: %v", err)
	}

	// An existing session of a disabled user stops working too.
	if _, err := e.svc.Authenticate(ctx, tok); !errors.Is(err, ErrUnauthenticated) {
		t.Fatalf("session of disabled user alive: %v", err)
	}

	if err := e.st.RawExec(ctx, `UPDATE users SET disabled = 0, password_hash = ''`); err != nil {
		t.Fatal(err)
	}

	if _, _, err := e.svc.Login(ctx, "kim", "", "", "10.0.0.1"); !errors.Is(err, ErrInvalidCredentials) {
		t.Fatalf("empty hash accepted an empty password: %v", err)
	}
}

func TestCleanupRemovesExpiredSessions(t *testing.T) {
	e := newEnv(t)
	e.setup(t)

	ctx := context.Background()

	if _, _, err := e.svc.Login(ctx, "kim", testPassword, "", "10.0.0.1"); err != nil {
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

// enableTOTP runs enrollment and confirmation and returns the seed and the
// recovery codes.
func (e *env) enableTOTP(t *testing.T) ([]byte, []string) {
	t.Helper()

	ctx := context.Background()

	u, err := e.st.UserByUsername(ctx, "kim")
	if err != nil {
		t.Fatal(err)
	}

	b32, uri, err := e.svc.TOTPEnroll(ctx, u.ID)
	if err != nil || !strings.HasPrefix(uri, "otpauth://totp/Hostashi:kim?") {
		t.Fatalf("enroll: %v %s", err, uri)
	}

	seed, err := base32.StdEncoding.WithPadding(base32.NoPadding).DecodeString(b32)
	if err != nil {
		t.Fatal(err)
	}

	codes, err := e.svc.TOTPConfirm(ctx, u.ID, TOTPCode(seed, TOTPStep(e.clk.now())))
	if err != nil || len(codes) != 8 {
		t.Fatalf("confirm: %v %v", codes, err)
	}

	return seed, codes
}

func TestTOTPFlow(t *testing.T) {
	e := newEnv(t)
	e.setup(t)

	ctx := context.Background()

	seed, codes := e.enableTOTP(t)

	// The seed is stored encrypted, not in clear.
	u, _ := e.st.UserByUsername(ctx, "kim")
	if !u.TOTPEnabled || bytes.Contains(u.TOTPSecret, seed) {
		t.Fatalf("seed stored in clear or not enabled: %+v", u)
	}

	// Password alone is no longer enough, and it creates no session.
	if tok, _, err := e.svc.Login(ctx, "kim", testPassword, "", "10.0.0.1"); !errors.Is(err, ErrTOTPRequired) || tok != "" {
		t.Fatalf("want ErrTOTPRequired, got %q %v", tok, err)
	}

	// The step used for confirmation cannot be used again (replay).
	cur := TOTPCode(seed, TOTPStep(e.clk.now()))
	if _, _, err := e.svc.Login(ctx, "kim", testPassword, cur, "10.0.0.1"); !errors.Is(err, ErrInvalidCredentials) {
		t.Fatalf("replayed code accepted: %v", err)
	}

	// The next step works, and works only once.
	e.clk.t = e.clk.t.Add(30 * time.Second)
	next := TOTPCode(seed, TOTPStep(e.clk.now()))

	if _, _, err := e.svc.Login(ctx, "kim", testPassword, next, "10.0.0.1"); err != nil {
		t.Fatalf("valid code rejected: %v", err)
	}

	if _, _, err := e.svc.Login(ctx, "kim", testPassword, next, "10.0.0.1"); !errors.Is(err, ErrInvalidCredentials) {
		t.Fatalf("code accepted twice: %v", err)
	}

	// Wrong code, right password.
	if _, _, err := e.svc.Login(ctx, "kim", testPassword, "000000", "10.0.0.1"); !errors.Is(err, ErrInvalidCredentials) {
		t.Fatalf("wrong code: %v", err)
	}

	// Right code, wrong password.
	e.clk.t = e.clk.t.Add(30 * time.Second)

	if _, _, err := e.svc.Login(ctx, "kim", "wrong password!!", TOTPCode(seed, TOTPStep(e.clk.now())), "10.0.0.1"); !errors.Is(err, ErrInvalidCredentials) {
		t.Fatalf("wrong password with right code: %v", err)
	}

	// A recovery code works once, in any notation.
	rc := strings.ToUpper(strings.ReplaceAll(codes[0], "-", ""))
	if _, _, err := e.svc.Login(ctx, "kim", testPassword, rc, "10.0.0.1"); err != nil {
		t.Fatalf("recovery code rejected: %v", err)
	}

	if _, _, err := e.svc.Login(ctx, "kim", testPassword, codes[0], "10.0.0.1"); !errors.Is(err, ErrInvalidCredentials) {
		t.Fatalf("recovery code used twice: %v", err)
	}

	if _, _, err := e.svc.Login(ctx, "kim", testPassword, codes[1], "10.0.0.1"); err != nil {
		t.Fatalf("second recovery code rejected: %v", err)
	}
}

func TestTOTPEnrollmentStates(t *testing.T) {
	e := newEnv(t)
	e.setup(t)

	ctx := context.Background()
	u, _ := e.st.UserByUsername(ctx, "kim")

	if _, err := e.svc.TOTPConfirm(ctx, u.ID, "123456"); !errors.Is(err, ErrTOTPState) {
		t.Fatalf("confirm without enroll: %v", err)
	}

	if err := e.svc.TOTPDisable(ctx, u.ID, testPassword, "123456"); !errors.Is(err, ErrTOTPState) {
		t.Fatalf("disable while off: %v", err)
	}

	if _, _, err := e.svc.TOTPEnroll(ctx, u.ID); err != nil {
		t.Fatal(err)
	}

	// A wrong confirmation code does not enable anything.
	if _, err := e.svc.TOTPConfirm(ctx, u.ID, "000000"); !errors.Is(err, ErrInvalidCredentials) {
		t.Fatalf("wrong confirm code: %v", err)
	}

	if _, _, err := e.svc.Login(ctx, "kim", testPassword, "", "10.0.0.1"); err != nil {
		t.Fatalf("unconfirmed TOTP blocks login: %v", err)
	}

	// Enrolling again replaces the pending seed.
	if _, _, err := e.svc.TOTPEnroll(ctx, u.ID); err != nil {
		t.Fatal(err)
	}

	e.enableTOTP(t)

	// Once enabled, enrolling again is refused, so a stolen session cannot swap the seed.
	if _, _, err := e.svc.TOTPEnroll(ctx, u.ID); !errors.Is(err, ErrTOTPState) {
		t.Fatalf("re-enroll while enabled: %v", err)
	}

	if _, err := e.svc.TOTPConfirm(ctx, u.ID, "123456"); !errors.Is(err, ErrTOTPState) {
		t.Fatalf("confirm while enabled: %v", err)
	}
}

func TestTOTPDisable(t *testing.T) {
	e := newEnv(t)
	e.setup(t)

	ctx := context.Background()
	seed, codes := e.enableTOTP(t)
	u, _ := e.st.UserByUsername(ctx, "kim")

	e.clk.t = e.clk.t.Add(30 * time.Second)
	code := TOTPCode(seed, TOTPStep(e.clk.now()))

	// Needs the password and a second factor, each of them.
	if err := e.svc.TOTPDisable(ctx, u.ID, "wrong password!!", code); !errors.Is(err, ErrInvalidCredentials) {
		t.Fatalf("wrong password: %v", err)
	}

	if err := e.svc.TOTPDisable(ctx, u.ID, testPassword, "000000"); !errors.Is(err, ErrInvalidCredentials) {
		t.Fatalf("wrong code: %v", err)
	}

	if err := e.svc.TOTPDisable(ctx, u.ID, testPassword, codes[0]); err != nil {
		t.Fatalf("disable with recovery code: %v", err)
	}

	got, _ := e.st.UserByUsername(ctx, "kim")
	if got.TOTPEnabled || len(got.TOTPSecret) != 0 {
		t.Fatalf("TOTP still set: %+v", got)
	}

	if _, _, err := e.svc.Login(ctx, "kim", testPassword, "", "10.0.0.1"); err != nil {
		t.Fatalf("login without code after disable: %v", err)
	}

	// Remaining recovery codes are gone.
	var n int
	if err := e.st.RawCount(ctx, `SELECT count(*) FROM recovery_codes WHERE 1 = ?`, 1, &n); err != nil || n != 0 {
		t.Fatalf("recovery codes left: %d %v", n, err)
	}
}

func TestTOTPSeedBoundToUser(t *testing.T) {
	e := newEnv(t)
	e.setup(t)

	ctx := context.Background()
	seed, _ := e.enableTOTP(t)

	// Corrupt the stored seed: login with a valid code must fail, not panic.
	if err := e.st.RawExec(ctx, `UPDATE users SET totp_secret = zeroblob(40)`); err != nil {
		t.Fatal(err)
	}

	e.clk.t = e.clk.t.Add(30 * time.Second)
	code := TOTPCode(seed, TOTPStep(e.clk.now()))

	if _, _, err := e.svc.Login(ctx, "kim", testPassword, code, "10.0.0.1"); !errors.Is(err, ErrInvalidCredentials) {
		t.Fatalf("corrupt seed: %v", err)
	}
}

func TestTOTPWithoutKey(t *testing.T) {
	e := newEnv(t)
	e.setup(t)

	noBox := NewService(Config{Store: e.st, SetupTokenPath: e.tokenPath, Now: e.clk.now})
	ctx := context.Background()

	if _, _, err := noBox.TOTPEnroll(ctx, 1); !errors.Is(err, ErrTOTPUnavailable) {
		t.Fatalf("enroll: %v", err)
	}

	if _, err := noBox.TOTPConfirm(ctx, 1, "123456"); !errors.Is(err, ErrTOTPUnavailable) {
		t.Fatalf("confirm: %v", err)
	}
}
