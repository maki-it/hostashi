// SPDX-License-Identifier: AGPL-3.0-or-later

package serve

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/maki-it/hostashi/internal/auth"
	"github.com/maki-it/hostashi/internal/auth/passkeytest"
)

const testPanel = "https://panel.example.com"

func ceremonyOf(t *testing.T, body []byte) (string, []byte) {
	t.Helper()

	var c struct {
		CeremonyID string          `json:"ceremonyId"`
		Options    json.RawMessage `json:"options"`
	}
	if err := json.Unmarshal(body, &c); err != nil || c.CeremonyID == "" {
		t.Fatalf("ceremony: %s %v", body, err)
	}

	return c.CeremonyID, c.Options
}

func finishBody(t *testing.T, id, name string, cred []byte) string {
	t.Helper()

	m := map[string]any{"ceremonyId": id, "credential": json.RawMessage(cred)}
	if name != "" {
		m["name"] = name
	}

	b, err := json.Marshal(m)
	if err != nil {
		t.Fatal(err)
	}

	return string(b)
}

func TestPasskeyFlowOverHTTP(t *testing.T) {
	e := newAPI(t)
	if err := e.svc.EnablePasskeys(auth.PasskeyConfig{PublicURL: testPanel}); err != nil {
		t.Fatal(err)
	}

	c := e.adminCookie(t)

	key, err := passkeytest.New(testPanel)
	if err != nil {
		t.Fatal(err)
	}

	rec := e.do(t, reqOpts{method: "POST", path: "/api/v1/auth/passkeys/register/begin", cookie: c})
	if rec.Code != 200 {
		t.Fatalf("begin: %d %s", rec.Code, rec.Body)
	}

	id, opts := ceremonyOf(t, rec.Body.Bytes())
	if !strings.Contains(string(opts), `"residentKey":"required"`) || !strings.Contains(string(opts), `"userVerification":"required"`) {
		t.Fatalf("options do not ask for a discoverable, verified passkey: %s", opts)
	}

	cred, err := key.Register(opts, 0)
	if err != nil {
		t.Fatal(err)
	}

	rec = e.do(t, reqOpts{method: "POST", path: "/api/v1/auth/passkeys/register/finish", body: finishBody(t, id, "laptop", cred), cookie: c})
	if rec.Code != http.StatusCreated || !strings.Contains(rec.Body.String(), `"laptop"`) {
		t.Fatalf("finish: %d %s", rec.Code, rec.Body)
	}

	// Log in with the passkey only, no cookie.
	rec = e.do(t, reqOpts{method: "POST", path: "/api/v1/auth/passkey/login/begin"})
	if rec.Code != 200 {
		t.Fatalf("login begin: %d %s", rec.Code, rec.Body)
	}

	id, opts = ceremonyOf(t, rec.Body.Bytes())

	assertion, err := key.Login(opts)
	if err != nil {
		t.Fatal(err)
	}

	rec = e.do(t, reqOpts{method: "POST", path: "/api/v1/auth/passkey/login/finish", body: finishBody(t, id, "", assertion)})
	if rec.Code != 200 {
		t.Fatalf("login finish: %d %s", rec.Code, rec.Body)
	}

	pc := sessionCookieOf(t, rec)
	if rec = e.do(t, reqOpts{method: "GET", path: "/api/v1/auth/me", cookie: pc}); rec.Code != 200 {
		t.Fatalf("me: %d", rec.Code)
	}

	// Replaying the same assertion fails: the ceremony is used up.
	rec = e.do(t, reqOpts{method: "POST", path: "/api/v1/auth/passkey/login/finish", body: finishBody(t, id, "", assertion)})
	if rec.Code != http.StatusUnauthorized || errCode(t, rec) != "passkey_failed" {
		t.Fatalf("replay: %d %s", rec.Code, rec.Body)
	}

	// List and delete.
	rec = e.do(t, reqOpts{method: "GET", path: "/api/v1/auth/passkeys", cookie: c})
	if rec.Code != 200 || !strings.Contains(rec.Body.String(), `"id":1`) || strings.Contains(rec.Body.String(), "credential") {
		t.Fatalf("list: %d %s", rec.Code, rec.Body)
	}

	if rec = e.do(t, reqOpts{method: "DELETE", path: "/api/v1/auth/passkeys/1", cookie: c}); rec.Code != http.StatusNoContent {
		t.Fatalf("delete: %d %s", rec.Code, rec.Body)
	}

	if rec = e.do(t, reqOpts{method: "DELETE", path: "/api/v1/auth/passkeys/1", cookie: c}); rec.Code != http.StatusNotFound {
		t.Fatalf("second delete: %d", rec.Code)
	}
}

func TestPasskeyRoutesNeedASessionAndConfiguration(t *testing.T) {
	e := newAPI(t)
	c := e.adminCookie(t)

	// Not configured: a clear 501, also for the public login route.
	rec := e.do(t, reqOpts{method: "POST", path: "/api/v1/auth/passkey/login/begin"})
	if rec.Code != http.StatusNotImplemented || errCode(t, rec) != "passkeys_unavailable" {
		t.Fatalf("%d %s", rec.Code, rec.Body)
	}

	if err := e.svc.EnablePasskeys(auth.PasskeyConfig{PublicURL: testPanel}); err != nil {
		t.Fatal(err)
	}

	tok := e.createToken(t, c, `{"name":"a","scopes":["sites:read"]}`)

	for _, r := range []reqOpts{
		{method: "POST", path: "/api/v1/auth/passkeys/register/begin"},
		{method: "GET", path: "/api/v1/auth/passkeys"},
		{method: "DELETE", path: "/api/v1/auth/passkeys/1"},
	} {
		if rec := e.do(t, r); rec.Code != http.StatusUnauthorized {
			t.Errorf("%s %s anonymous: %d", r.method, r.path, rec.Code)
		}

		r.header = bearerHeader(tok.Token)
		if rec := e.do(t, r); rec.Code != http.StatusForbidden {
			t.Errorf("%s %s with token: %d", r.method, r.path, rec.Code)
		}
	}

	// Large but bounded credential bodies are accepted for parsing; garbage fails cleanly.
	rec = e.do(t, reqOpts{method: "POST", path: "/api/v1/auth/passkey/login/finish", body: `{"ceremonyId":"x","credential":{"id":"` + strings.Repeat("A", 10000) + `"}}`})
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("garbage: %d %s", rec.Code, rec.Body)
	}

	rec = e.do(t, reqOpts{method: "POST", path: "/api/v1/auth/passkey/login/finish", body: `{"ceremonyId":"x","credential":"` + strings.Repeat("A", 70000) + `"}`})
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("oversized: %d", rec.Code)
	}
}
