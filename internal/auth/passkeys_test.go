// SPDX-License-Identifier: AGPL-3.0-or-later

package auth

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/maki-it/hostashi/internal/auth/passkeytest"
	"github.com/maki-it/hostashi/internal/store"
)

const panelURL = "https://panel.example.com"

func passkeyEnv(t *testing.T) *env {
	t.Helper()

	e := tokenEnv(t)
	if err := e.svc.EnablePasskeys(PasskeyConfig{PublicURL: panelURL}); err != nil {
		t.Fatal(err)
	}

	return e
}

func mustJSON(t *testing.T, v any) []byte {
	t.Helper()

	b, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}

	return b
}

func newAuthenticator(t *testing.T, origin string) *passkeytest.Authenticator {
	t.Helper()

	a, err := passkeytest.New(origin)
	if err != nil {
		t.Fatal(err)
	}

	return a
}

// register adds a passkey for user 1 and returns the error of the finish step.
func (e *env) register(t *testing.T, a *passkeytest.Authenticator, flags byte) error {
	t.Helper()

	id, opts, err := e.svc.PasskeyRegisterBegin(context.Background(), 1)
	if err != nil {
		t.Fatal(err)
	}

	resp, err := a.Register(mustJSON(t, opts), flags)
	if err != nil {
		t.Fatal(err)
	}

	_, err = e.svc.PasskeyRegisterFinish(context.Background(), 1, id, "laptop", resp, "192.0.2.1")

	return err
}

func (e *env) passkeyLogin(t *testing.T, a *passkeytest.Authenticator) (string, store.User, error) {
	t.Helper()

	id, opts, err := e.svc.PasskeyLoginBegin("192.0.2.1")
	if err != nil {
		t.Fatal(err)
	}

	resp, err := a.Login(mustJSON(t, opts))
	if err != nil {
		t.Fatal(err)
	}

	return e.svc.PasskeyLoginFinish(context.Background(), id, resp, "192.0.2.1")
}

func TestPasskeyRegisterAndLogin(t *testing.T) {
	e := passkeyEnv(t)
	a := newAuthenticator(t, panelURL)

	if err := e.register(t, a, 0); err != nil {
		t.Fatal(err)
	}

	// TOTP is on, yet the passkey alone is enough: it is both factors.
	if err := e.st.RawExec(context.Background(), `UPDATE users SET totp_enabled = 1 WHERE id = 1`); err != nil {
		t.Fatal(err)
	}

	token, u, err := e.passkeyLogin(t, a)
	if err != nil {
		t.Fatal(err)
	}

	if u.Username != "kim" || u.PasswordHash != "" {
		t.Fatalf("user %+v", u)
	}

	if got, err := e.svc.Authenticate(context.Background(), token); err != nil || got.ID != 1 {
		t.Fatalf("session: %+v %v", got, err)
	}

	list, err := e.svc.ListPasskeys(context.Background(), 1)
	if err != nil || len(list) != 1 || list[0].LastUsedAt == nil || list[0].Name != "laptop" {
		t.Fatalf("%+v %v", list, err)
	}

	// A second login works too (the counter moves on).
	if _, _, err := e.passkeyLogin(t, a); err != nil {
		t.Fatal(err)
	}
}

func TestPasskeyRegistrationNeedsUserVerificationAndRightOrigin(t *testing.T) {
	e := passkeyEnv(t)

	if err := e.register(t, newAuthenticator(t, panelURL), passkeytest.FlagUP|passkeytest.FlagAT); !errors.Is(err, ErrPasskeyFailed) {
		t.Fatalf("without UV: %v", err)
	}

	if err := e.register(t, newAuthenticator(t, "https://evil.example.net"), 0); !errors.Is(err, ErrPasskeyFailed) {
		t.Fatalf("phishing origin: %v", err)
	}

	if list, _ := e.svc.ListPasskeys(context.Background(), 1); len(list) != 0 {
		t.Fatalf("%d passkeys stored", len(list))
	}
}

func TestPasskeyCeremoniesAreSingleUseAndShortLived(t *testing.T) {
	e := passkeyEnv(t)
	ctx := context.Background()
	a := newAuthenticator(t, panelURL)

	id, opts, err := e.svc.PasskeyRegisterBegin(ctx, 1)
	if err != nil {
		t.Fatal(err)
	}

	resp, _ := a.Register(mustJSON(t, opts), 0)

	// Another user cannot finish this registration.
	if err := e.st.RawExec(ctx, `INSERT INTO users (id, username) VALUES (2, 'other')`); err != nil {
		t.Fatal(err)
	}

	if _, err := e.svc.PasskeyRegisterFinish(ctx, 2, id, "x", resp, ""); !errors.Is(err, ErrPasskeyFailed) {
		t.Fatalf("foreign finish: %v", err)
	}

	// The attempt above used the id up.
	if _, err := e.svc.PasskeyRegisterFinish(ctx, 1, id, "x", resp, ""); !errors.Is(err, ErrPasskeyFailed) {
		t.Fatalf("reuse: %v", err)
	}

	// Expired.
	id, opts, _ = e.svc.PasskeyRegisterBegin(ctx, 1)
	resp, _ = a.Register(mustJSON(t, opts), 0)
	e.clk.t = e.clk.t.Add(6 * time.Minute)

	if _, err := e.svc.PasskeyRegisterFinish(ctx, 1, id, "x", resp, ""); !errors.Is(err, ErrPasskeyFailed) {
		t.Fatalf("expired: %v", err)
	}

	// A login id is no registration id.
	lid, _, _ := e.svc.PasskeyLoginBegin("192.0.2.1")
	if _, err := e.svc.PasskeyRegisterFinish(ctx, 1, lid, "x", resp, ""); !errors.Is(err, ErrPasskeyFailed) {
		t.Fatalf("wrong kind: %v", err)
	}
}

func TestPasskeyLoginFailures(t *testing.T) {
	e := passkeyEnv(t)
	ctx := context.Background()
	a := newAuthenticator(t, panelURL)

	if err := e.register(t, a, 0); err != nil {
		t.Fatal(err)
	}

	// Without user verification.
	a.Flags = passkeytest.FlagUP
	if _, _, err := e.passkeyLogin(t, a); !errors.Is(err, ErrPasskeyFailed) {
		t.Fatalf("no UV: %v", err)
	}

	a.Flags = passkeytest.FlagUP | passkeytest.FlagUV

	if _, _, err := e.passkeyLogin(t, a); err != nil {
		t.Fatal(err)
	}

	// The counter goes backwards: a copy of the key is in use somewhere.
	a.SignCount = 1
	if _, _, err := e.passkeyLogin(t, a); !errors.Is(err, ErrPasskeyFailed) {
		t.Fatalf("clone: %v", err)
	}

	a.SignCount = 100

	// A key that was never registered, presenting the user's handle.
	stranger := newAuthenticator(t, panelURL)
	if _, err := stranger.Register(mustJSON(t, map[string]any{"publicKey": map[string]any{
		"challenge": "x", "rp": map[string]string{"id": "panel.example.com"}, "user": map[string]string{"id": "aGFuZGxl"},
	}}), 0); err != nil {
		t.Fatal(err)
	}

	if _, _, err := e.passkeyLogin(t, stranger); !errors.Is(err, ErrPasskeyFailed) {
		t.Fatalf("unregistered key: %v", err)
	}

	// Garbage and unknown ceremony ids.
	id, _, _ := e.svc.PasskeyLoginBegin("192.0.2.1")
	if _, _, err := e.svc.PasskeyLoginFinish(ctx, id, []byte(`{"id":"x"}`), "192.0.2.1"); !errors.Is(err, ErrPasskeyFailed) {
		t.Fatalf("garbage: %v", err)
	}

	if _, _, err := e.svc.PasskeyLoginFinish(ctx, "nope", []byte(`{}`), "192.0.2.1"); !errors.Is(err, ErrPasskeyFailed) {
		t.Fatalf("unknown id: %v", err)
	}

	// A disabled account cannot log in, even with a valid passkey.
	if err := e.st.RawExec(ctx, `UPDATE users SET disabled = 1 WHERE id = 1`); err != nil {
		t.Fatal(err)
	}

	if _, _, err := e.passkeyLogin(t, a); !errors.Is(err, ErrPasskeyFailed) {
		t.Fatalf("disabled: %v", err)
	}
}

func TestPasskeyLoginIsRateLimited(t *testing.T) {
	e := passkeyEnv(t)
	ctx := context.Background()

	for range 10 {
		_, _, _ = e.svc.PasskeyLoginFinish(ctx, "nope", []byte(`{}`), "203.0.113.7")
	}

	if _, _, err := e.svc.PasskeyLoginBegin("203.0.113.7"); !errors.Is(err, ErrRateLimited) {
		t.Fatalf("begin: %v", err)
	}

	if _, _, err := e.svc.PasskeyLoginFinish(ctx, "nope", []byte(`{}`), "203.0.113.7"); !errors.Is(err, ErrRateLimited) {
		t.Fatalf("finish: %v", err)
	}
}

func TestDeletePasskey(t *testing.T) {
	e := passkeyEnv(t)
	ctx := context.Background()
	a := newAuthenticator(t, panelURL)

	if err := e.register(t, a, 0); err != nil {
		t.Fatal(err)
	}

	list, _ := e.svc.ListPasskeys(ctx, 1)

	if err := e.st.RawExec(ctx, `INSERT INTO users (id, username) VALUES (2, 'other')`); err != nil {
		t.Fatal(err)
	}

	if err := e.svc.DeletePasskey(ctx, 2, list[0].ID, ""); !errors.Is(err, ErrPasskeyNotFound) {
		t.Fatalf("foreign delete: %v", err)
	}

	// An admin keeps the last second factor (ADR-0035).
	if err := e.svc.DeletePasskey(ctx, 1, list[0].ID, ""); !errors.Is(err, ErrSecondFactorRequired) {
		t.Fatalf("admin deletes the last factor: %v", err)
	}

	if err := e.st.RawExec(ctx, `UPDATE users SET role = 'user' WHERE id = 1`); err != nil {
		t.Fatal(err)
	}

	if err := e.svc.DeletePasskey(ctx, 1, list[0].ID, ""); err != nil {
		t.Fatal(err)
	}

	if _, _, err := e.passkeyLogin(t, a); !errors.Is(err, ErrPasskeyFailed) {
		t.Fatalf("deleted passkey still works: %v", err)
	}
}

func TestPasskeyRegistrationLimits(t *testing.T) {
	e := passkeyEnv(t)
	a := newAuthenticator(t, panelURL)

	if err := e.register(t, a, 0); err != nil {
		t.Fatal(err)
	}

	// The same authenticator twice is refused.
	if err := e.register(t, a, 0); err == nil {
		t.Fatal("duplicate credential accepted")
	}

	for range store.MaxPasskeys - 1 {
		if err := e.register(t, newAuthenticator(t, panelURL), 0); err != nil {
			t.Fatal(err)
		}
	}

	if _, _, err := e.svc.PasskeyRegisterBegin(context.Background(), 1); !errors.Is(err, ErrTooManyPasskeys) {
		t.Fatalf("limit: %v", err)
	}
}

func TestPasskeyConfig(t *testing.T) {
	e := tokenEnv(t)

	if _, _, err := e.svc.PasskeyLoginBegin("192.0.2.1"); !errors.Is(err, ErrPasskeyUnavailable) {
		t.Fatalf("not configured: %v", err)
	}

	for _, u := range []string{"http://panel.example.com", "ftp://panel.example.com", "https://", "https://panel.example.com/sub", "not a url"} {
		if err := e.svc.EnablePasskeys(PasskeyConfig{PublicURL: u}); err == nil {
			t.Errorf("%q accepted", u)
		}
	}

	for _, u := range []string{"http://localhost:8080", "https://panel.example.com:8443", "https://panel.example.com/"} {
		if err := e.svc.EnablePasskeys(PasskeyConfig{PublicURL: u}); err != nil {
			t.Errorf("%q: %v", u, err)
		}
	}
}
