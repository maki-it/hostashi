// SPDX-License-Identifier: AGPL-3.0-or-later

package auth

import (
	"context"
	"errors"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/maki-it/hostashi/internal/store"
)

// tokenEnv has the admin "kim" (user id 1).
func tokenEnv(t *testing.T) *env {
	t.Helper()

	e := newEnv(t)
	e.setup(t)

	return e
}

func (e *env) newToken(t *testing.T, req TokenRequest) NewToken {
	t.Helper()

	if req.Name == "" {
		req.Name = "ci"
	}

	if req.Scopes == nil {
		req.Scopes = []string{"sites:read"}
	}

	nt, err := e.svc.CreateToken(context.Background(), 1, req, "192.0.2.1")
	if err != nil {
		t.Fatal(err)
	}

	return nt
}

func TestCreateTokenDefaults(t *testing.T) {
	e := tokenEnv(t)

	nt := e.newToken(t, TokenRequest{Scopes: []string{"sites:write", "sites:read", "sites:read"}})

	if !strings.HasPrefix(nt.Secret, TokenPrefix) || len(nt.Secret) != len(TokenPrefix)+43 {
		t.Fatalf("secret %q", nt.Secret)
	}

	if !slices.Equal(nt.Token.Scopes, []string{"sites:read", "sites:write"}) {
		t.Fatalf("scopes %v", nt.Token.Scopes)
	}

	want := e.clk.now().Add(365 * 24 * time.Hour).UTC()
	if nt.Token.ExpiresAt == nil || !nt.Token.ExpiresAt.Truncate(time.Second).Equal(want.Truncate(time.Second)) {
		t.Fatalf("expires %v, want %v", nt.Token.ExpiresAt, want)
	}

	if !strings.HasPrefix(nt.Secret, TokenPrefix+nt.Token.Prefix) {
		t.Fatalf("prefix %q does not belong to %q", nt.Token.Prefix, nt.Secret)
	}

	// The listing never carries the secret, and neither does the audit log.
	list, err := e.svc.ListTokens(context.Background(), 1)
	if err != nil || len(list) != 1 {
		t.Fatalf("%v %v", list, err)
	}

	var n int
	if err := e.st.RawCount(context.Background(), `SELECT count(*) FROM audit_log WHERE detail LIKE ?`, "%"+nt.Secret[len(TokenPrefix):]+"%", &n); err != nil || n != 0 {
		t.Fatalf("secret in audit log: %d %v", n, err)
	}

	if err := e.st.RawCount(context.Background(), `SELECT count(*) FROM audit_log WHERE action = ?`, "token.create", &n); err != nil || n != 1 {
		t.Fatalf("audit entries: %d %v", n, err)
	}
}

func TestCreateTokenLifetime(t *testing.T) {
	e := tokenEnv(t)

	if nt := e.newToken(t, TokenRequest{Days: 30}); nt.Token.ExpiresAt == nil ||
		nt.Token.ExpiresAt.Sub(e.clk.now()) > 30*24*time.Hour+time.Second {
		t.Fatalf("30 days: %v", nt.Token.ExpiresAt)
	}

	// "Unlimited" is a deliberate choice and stored as no expiry.
	if nt := e.newToken(t, TokenRequest{Unlimited: true}); nt.Token.ExpiresAt != nil {
		t.Fatalf("unlimited has expiry %v", nt.Token.ExpiresAt)
	}

	for _, req := range []TokenRequest{{Days: 366}, {Days: -1}, {Days: 1000000}, {Days: 30, Unlimited: true}} {
		req.Name, req.Scopes = "x", []string{"sites:read"}

		if _, err := e.svc.CreateToken(context.Background(), 1, req, ""); !errors.Is(err, ErrInvalidToken) {
			t.Errorf("%+v: got %v", req, err)
		}
	}
}

func TestCreateTokenRejectsBadInput(t *testing.T) {
	e := tokenEnv(t)

	bad := []TokenRequest{
		{Name: "", Scopes: []string{"sites:read"}},
		{Name: strings.Repeat("a", 65), Scopes: []string{"sites:read"}},
		{Name: " padded", Scopes: []string{"sites:read"}},
		{Name: "tab\there", Scopes: []string{"sites:read"}},
		{Name: "new\nline", Scopes: []string{"sites:read"}},
		{Name: "ok", Scopes: nil},
		{Name: "ok", Scopes: []string{}},
		{Name: "ok", Scopes: []string{"*"}},
		{Name: "ok", Scopes: []string{"sites:*"}},
		{Name: "ok", Scopes: []string{"*:*"}},
		{Name: "ok", Scopes: []string{"admin"}},
		{Name: "ok", Scopes: []string{"sites:read", "nope:read"}},
		{Name: "ok", Scopes: []string{strings.Repeat("x", 5000)}},
		{Name: "ok", Scopes: make([]string, 33)},
	}

	for _, req := range bad {
		if _, err := e.svc.CreateToken(context.Background(), 1, req, ""); !errors.Is(err, ErrInvalidToken) {
			t.Errorf("%+v: got %v", req.Name, err)
		}
	}

	if list, _ := e.svc.ListTokens(context.Background(), 1); len(list) != 0 {
		t.Fatalf("%d tokens created from bad input", len(list))
	}
}

func TestAuthenticateToken(t *testing.T) {
	e := tokenEnv(t)
	ctx := context.Background()

	nt := e.newToken(t, TokenRequest{Scopes: []string{"sites:read", "backups:read"}})

	p, err := e.svc.AuthenticateToken(ctx, nt.Secret, "192.0.2.1")
	if err != nil {
		t.Fatal(err)
	}

	if !p.IsToken() || p.User.Username != "kim" || p.User.PasswordHash != "" || p.TokenID != nt.Token.ID {
		t.Fatalf("principal %+v", p)
	}

	if !p.Allows("sites:read") || p.Allows("sites:write") || p.Allows("backups:restore") || p.Allows("") {
		t.Fatalf("scopes %v", p.Scopes)
	}

	// A session principal is not limited by token scopes.
	if !(Principal{}).Allows("sites:delete") {
		t.Fatal("session principal restricted")
	}

	for _, raw := range []string{
		"", "hst_", nt.Secret + "x", nt.Secret[:len(nt.Secret)-1], strings.TrimPrefix(nt.Secret, TokenPrefix),
		"hst_" + strings.Repeat("A", 43), strings.Repeat("a", 500), " " + nt.Secret,
	} {
		if _, err := e.svc.AuthenticateToken(ctx, raw, "192.0.2.9"); !errors.Is(err, ErrUnauthenticated) {
			t.Errorf("%q: got %v", truncate(raw, 20), err)
		}
	}
}

func TestTokenExpiresRevokedAndDisabledOwner(t *testing.T) {
	e := tokenEnv(t)
	ctx := context.Background()

	short := e.newToken(t, TokenRequest{Days: 1})
	forever := e.newToken(t, TokenRequest{Unlimited: true})
	gone := e.newToken(t, TokenRequest{})

	if err := e.svc.RevokeToken(ctx, 1, gone.Token.ID, "192.0.2.1"); err != nil {
		t.Fatal(err)
	}

	if _, err := e.svc.AuthenticateToken(ctx, gone.Secret, "192.0.2.1"); !errors.Is(err, ErrUnauthenticated) {
		t.Fatalf("revoked token: %v", err)
	}

	if err := e.svc.RevokeToken(ctx, 1, gone.Token.ID, ""); !errors.Is(err, ErrTokenNotFound) {
		t.Fatalf("second revoke: %v", err)
	}

	e.clk.t = e.clk.t.Add(25 * time.Hour)

	if _, err := e.svc.AuthenticateToken(ctx, short.Secret, "192.0.2.1"); !errors.Is(err, ErrUnauthenticated) {
		t.Fatalf("expired token: %v", err)
	}

	if _, err := e.svc.AuthenticateToken(ctx, forever.Secret, "192.0.2.1"); err != nil {
		t.Fatalf("unlimited token after a day: %v", err)
	}

	e.clk.t = e.clk.t.Add(20 * 365 * 24 * time.Hour)

	if _, err := e.svc.AuthenticateToken(ctx, forever.Secret, "192.0.2.1"); err != nil {
		t.Fatalf("unlimited token after 20 years: %v", err)
	}

	if err := e.st.RawExec(ctx, `UPDATE users SET disabled = 1 WHERE id = 1`); err != nil {
		t.Fatal(err)
	}

	if _, err := e.svc.AuthenticateToken(ctx, forever.Secret, "192.0.2.1"); !errors.Is(err, ErrUnauthenticated) {
		t.Fatalf("disabled owner: %v", err)
	}
}

func TestRevokeOnlyOwnTokens(t *testing.T) {
	e := tokenEnv(t)
	ctx := context.Background()

	if err := e.st.RawExec(ctx, `INSERT INTO users (id, username) VALUES (2, 'other')`); err != nil {
		t.Fatal(err)
	}

	nt := e.newToken(t, TokenRequest{})

	if err := e.svc.RevokeToken(ctx, 2, nt.Token.ID, ""); !errors.Is(err, ErrTokenNotFound) {
		t.Fatalf("other user revoked it: %v", err)
	}

	if list, _ := e.svc.ListTokens(ctx, 2); len(list) != 0 {
		t.Fatalf("other user sees %d tokens", len(list))
	}

	if _, err := e.svc.AuthenticateToken(ctx, nt.Secret, "192.0.2.1"); err != nil {
		t.Fatalf("token broken by foreign revoke: %v", err)
	}
}

func TestGuessingTokensIsRateLimited(t *testing.T) {
	e := tokenEnv(t)
	ctx := context.Background()

	nt := e.newToken(t, TokenRequest{})

	for range 30 {
		_, _ = e.svc.AuthenticateToken(ctx, "hst_"+strings.Repeat("B", 43), "203.0.113.5")
	}

	// Even the right token is refused from the guessing address ...
	if _, err := e.svc.AuthenticateToken(ctx, nt.Secret, "203.0.113.5"); !errors.Is(err, ErrRateLimited) {
		t.Fatalf("got %v", err)
	}

	// ... but not from another one, and the block ends with the window.
	if _, err := e.svc.AuthenticateToken(ctx, nt.Secret, "203.0.113.6"); err != nil {
		t.Fatalf("other address: %v", err)
	}

	e.clk.t = e.clk.t.Add(16 * time.Minute)

	if _, err := e.svc.AuthenticateToken(ctx, nt.Secret, "203.0.113.5"); err != nil {
		t.Fatalf("after the window: %v", err)
	}
}

func TestTokenLimitPerUser(t *testing.T) {
	e := tokenEnv(t)

	for range store.MaxActiveTokens {
		e.newToken(t, TokenRequest{})
	}

	_, err := e.svc.CreateToken(context.Background(), 1, TokenRequest{Name: "one too many", Scopes: []string{"sites:read"}}, "")
	if !errors.Is(err, ErrTooManyTokens) {
		t.Fatalf("got %v", err)
	}

	// Revoking one makes room again.
	list, _ := e.svc.ListTokens(context.Background(), 1)
	if err := e.svc.RevokeToken(context.Background(), 1, list[0].ID, ""); err != nil {
		t.Fatal(err)
	}

	e.newToken(t, TokenRequest{})
}

func TestLastUsedIsRecordedAndThrottled(t *testing.T) {
	e := tokenEnv(t)
	ctx := context.Background()

	nt := e.newToken(t, TokenRequest{})
	first := e.clk.now()

	if _, err := e.svc.AuthenticateToken(ctx, nt.Secret, "192.0.2.1"); err != nil {
		t.Fatal(err)
	}

	e.clk.t = first.Add(10 * time.Second)

	if _, err := e.svc.AuthenticateToken(ctx, nt.Secret, "192.0.2.2"); err != nil {
		t.Fatal(err)
	}

	list, _ := e.svc.ListTokens(ctx, 1)
	if list[0].LastUsedAt == nil || list[0].LastUsedAt.Unix() != first.Unix() || list[0].LastUsedIP != "192.0.2.1" {
		t.Fatalf("throttled write failed: %+v", list[0])
	}

	e.clk.t = first.Add(2 * time.Minute)

	if _, err := e.svc.AuthenticateToken(ctx, nt.Secret, "192.0.2.3"); err != nil {
		t.Fatal(err)
	}

	list, _ = e.svc.ListTokens(ctx, 1)
	if list[0].LastUsedIP != "192.0.2.3" {
		t.Fatalf("last use not updated: %+v", list[0])
	}
}

func TestIPAllowlist(t *testing.T) {
	e := tokenEnv(t)
	ctx := context.Background()

	nt := e.newToken(t, TokenRequest{})

	// Empty: every address.
	if got, err := e.svc.IPAllowlist(ctx); err != nil || len(got) != 0 {
		t.Fatalf("%v %v", got, err)
	}

	got, err := e.svc.SetIPAllowlist(ctx, 1, []string{"198.51.100.77/24", " 2001:db8::1 ", "198.51.100.0/24"}, "192.0.2.1")
	if err != nil || !slices.Equal(got, []string{"198.51.100.0/24", "2001:db8::1/128"}) {
		t.Fatalf("%v %v", got, err)
	}

	for ip, want := range map[string]bool{
		"198.51.100.7": true, "198.51.100.255": true, "::ffff:198.51.100.7": true, "2001:db8::1": true,
		// The server itself always: the hostashi command on it.
		"127.0.0.1": true, "::1": true, "::ffff:127.0.0.1": true,
		"127.0.0.2": false, "198.51.101.1": false, "203.0.113.1": false, "2001:db8::2": false, "not-an-ip": false, "": false,
	} {
		_, err := e.svc.AuthenticateToken(ctx, nt.Secret, ip)

		switch {
		case want && err != nil:
			t.Errorf("%s: %v", ip, err)
		case !want && !errors.Is(err, ErrTokenNotAllowedFromIP):
			t.Errorf("%s: got %v, want not allowed", ip, err)
		}
	}

	// A denied address is not a guessing attempt: it did not use a wrong token.
	if _, err := e.svc.AuthenticateToken(ctx, nt.Secret, "198.51.100.8"); err != nil {
		t.Fatalf("legit address blocked: %v", err)
	}

	var n int
	if err := e.st.RawCount(context.Background(), `SELECT count(*) FROM audit_log WHERE action = ?`, "token.denied", &n); err != nil || n == 0 {
		t.Fatalf("denials not audited: %d %v", n, err)
	}

	// Empty list removes the restriction again.
	if _, err := e.svc.SetIPAllowlist(ctx, 1, nil, ""); err != nil {
		t.Fatal(err)
	}

	if _, err := e.svc.AuthenticateToken(ctx, nt.Secret, "203.0.113.1"); err != nil {
		t.Fatalf("after clearing: %v", err)
	}
}

func TestIPAllowlistRejectsBadEntries(t *testing.T) {
	e := tokenEnv(t)

	for _, entries := range [][]string{
		{"abc"},
		{""},
		{"1.2.3.4/33"},
		{"300.0.0.1"},
		{"1.2.3.4/"},
		{strings.Repeat("1", 200)},
		make([]string, 65),
	} {
		if _, err := e.svc.SetIPAllowlist(context.Background(), 1, entries, ""); !errors.Is(err, ErrInvalidAllowlist) {
			t.Errorf("%q: got %v", truncate(strings.Join(entries, ","), 30), err)
		}
	}

	// Nothing was stored by the failed calls.
	if got, _ := e.svc.IPAllowlist(context.Background()); len(got) != 0 {
		t.Fatalf("allowlist changed: %v", got)
	}
}
