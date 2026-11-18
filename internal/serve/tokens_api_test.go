// SPDX-License-Identifier: AGPL-3.0-or-later

package serve

import (
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"

	"github.com/maki-it/hostashi/internal/auth"
)

// login returns the session cookie of the admin.
func (e *apiEnv) adminCookie(t *testing.T) *http.Cookie {
	t.Helper()

	e.setupAdmin(t)

	rec := e.do(t, reqOpts{
		method: "POST", path: "/api/v1/auth/login",
		body: `{"username":"kim","password":"` + apiPassword + `"}`,
	})
	if rec.Code != http.StatusOK {
		t.Fatalf("login: %d %s", rec.Code, rec.Body)
	}

	return sessionCookieOf(t, rec)
}

type createdToken struct {
	ID       int64    `json:"id"`
	Token    string   `json:"token"`
	Prefix   string   `json:"prefix"`
	Warnings []string `json:"warnings"`
	Never    bool     `json:"neverExpires"`
}

func (e *apiEnv) createToken(t *testing.T, c *http.Cookie, body string) createdToken {
	t.Helper()

	rec := e.do(t, reqOpts{method: "POST", path: "/api/v1/tokens", body: body, cookie: c})
	if rec.Code != http.StatusCreated {
		t.Fatalf("create token: %d %s", rec.Code, rec.Body)
	}

	var out createdToken
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}

	return out
}

func bearerHeader(tok string) map[string]string {
	return map[string]string{"Authorization": "Bearer " + tok}
}

func TestTokenLifecycleOverHTTP(t *testing.T) {
	e := newAPI(t)
	c := e.adminCookie(t)

	tok := e.createToken(t, c, `{"name":"deploy","scopes":["sites:read","sites:write"]}`)
	if !strings.HasPrefix(tok.Token, "hst_") || len(tok.Warnings) != 0 || tok.Never || !strings.HasPrefix(tok.Token, tok.Prefix) {
		t.Fatalf("%+v", tok)
	}

	// The token works on its own, without a cookie, and says what it is.
	rec := e.do(t, reqOpts{method: "GET", path: "/api/v1/auth/me", header: bearerHeader(tok.Token)})
	if rec.Code != 200 || !strings.Contains(rec.Body.String(), `"auth":"token"`) || !strings.Contains(rec.Body.String(), `"sites:read"`) {
		t.Fatalf("%d %s", rec.Code, rec.Body)
	}

	// The list shows the token without its secret.
	rec = e.do(t, reqOpts{method: "GET", path: "/api/v1/tokens", cookie: c})
	if rec.Code != 200 || strings.Contains(rec.Body.String(), tok.Token) || !strings.Contains(rec.Body.String(), `"deploy"`) {
		t.Fatalf("%d %s", rec.Code, rec.Body)
	}

	// Revoking ends it at once.
	rec = e.do(t, reqOpts{method: "DELETE", path: "/api/v1/tokens/" + itoa(tok.ID), cookie: c})
	if rec.Code != http.StatusNoContent {
		t.Fatalf("%d %s", rec.Code, rec.Body)
	}

	rec = e.do(t, reqOpts{method: "GET", path: "/api/v1/auth/me", header: bearerHeader(tok.Token)})
	if rec.Code != http.StatusUnauthorized || errCode(t, rec) != "unauthenticated" || rec.Header().Get("WWW-Authenticate") != "Bearer" {
		t.Fatalf("%d %s", rec.Code, rec.Body)
	}

	rec = e.do(t, reqOpts{method: "DELETE", path: "/api/v1/tokens/" + itoa(tok.ID), cookie: c})
	if rec.Code != http.StatusNotFound {
		t.Fatalf("second revoke: %d", rec.Code)
	}
}

func itoa(n int64) string { return strconv.FormatInt(n, 10) }

func TestUnlimitedAndHighRiskTokensCarryWarnings(t *testing.T) {
	e := newAPI(t)
	c := e.adminCookie(t)

	tok := e.createToken(t, c, `{"name":"forever","scopes":["sites:delete"],"unlimited":true}`)
	if !tok.Never || len(tok.Warnings) != 2 ||
		!strings.HasPrefix(tok.Warnings[0], "never_expires") || !strings.HasPrefix(tok.Warnings[1], "high_risk_scope") {
		t.Fatalf("%+v", tok)
	}
}

func TestTokenCreationNeedsASessionAndValidInput(t *testing.T) {
	e := newAPI(t)
	c := e.adminCookie(t)
	tok := e.createToken(t, c, `{"name":"a","scopes":["sites:read","sites:write"]}`)

	// No login, no token.
	rec := e.do(t, reqOpts{method: "POST", path: "/api/v1/tokens", body: `{"name":"x","scopes":["sites:read"]}`})
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("%d", rec.Code)
	}

	// A token cannot mint or list tokens, change the allowlist or enroll TOTP.
	for _, r := range []reqOpts{
		{method: "POST", path: "/api/v1/tokens", body: `{"name":"x","scopes":["sites:read"]}`},
		{method: "GET", path: "/api/v1/tokens"},
		{method: "DELETE", path: "/api/v1/tokens/" + itoa(tok.ID)},
		{method: "GET", path: "/api/v1/settings/api-ip-allowlist"},
		{method: "PUT", path: "/api/v1/settings/api-ip-allowlist", body: `{"entries":[]}`},
		{method: "POST", path: "/api/v1/auth/totp/enroll"},
	} {
		r.header = bearerHeader(tok.Token)

		rec := e.do(t, r)
		if rec.Code != http.StatusForbidden || errCode(t, rec) != "session_required" {
			t.Errorf("%s %s with token: %d %s", r.method, r.path, rec.Code, rec.Body)
		}
	}

	// Invalid input is a 422 and creates nothing.
	for _, body := range []string{
		`{"name":"","scopes":["sites:read"]}`, `{"name":"x","scopes":[]}`, `{"name":"x","scopes":["*"]}`,
		`{"name":"x","scopes":["sites:*"]}`, `{"name":"x","scopes":["sites:read"],"expiresInDays":366}`,
		`{"name":"x","scopes":["sites:read"],"expiresInDays":5,"unlimited":true}`,
		`{"name":"x","scopes":["sites:read"],"userId":1}`, `not json`,
	} {
		rec := e.do(t, reqOpts{method: "POST", path: "/api/v1/tokens", body: body, cookie: c})
		if rec.Code != http.StatusUnprocessableEntity && rec.Code != http.StatusBadRequest {
			t.Errorf("%s: %d %s", body, rec.Code, rec.Body)
		}
	}

	rec = e.do(t, reqOpts{method: "GET", path: "/api/v1/tokens", cookie: c})
	if strings.Count(rec.Body.String(), `"id"`) != 1 {
		t.Fatalf("tokens were created from bad input: %s", rec.Body)
	}

	// Cross-site requests are refused like every other unsafe request.
	rec = e.do(t, reqOpts{
		method: "POST", path: "/api/v1/tokens", body: `{"name":"x","scopes":["sites:read"]}`, cookie: c,
		header: map[string]string{"Sec-Fetch-Site": "cross-site"},
	})
	if rec.Code != http.StatusForbidden {
		t.Fatalf("cross-site: %d", rec.Code)
	}
}

func TestBadBearerNeverFallsBackToTheCookie(t *testing.T) {
	e := newAPI(t)
	c := e.adminCookie(t)

	for _, h := range []string{"Bearer", "Bearer ", "Bearer nope", "Basic abc", "hst_abc", "Bearer hst_" + strings.Repeat("A", 43)} {
		rec := e.do(t, reqOpts{method: "GET", path: "/api/v1/auth/me", cookie: c, header: map[string]string{"Authorization": h}})
		if rec.Code != http.StatusUnauthorized {
			t.Errorf("%q with a valid cookie: %d", h, rec.Code)
		}
	}
}

func TestScopedHandler(t *testing.T) {
	e := newAPI(t)
	c := e.adminCookie(t)
	read := e.createToken(t, c, `{"name":"r","scopes":["sites:read"]}`)

	a := authn{svc: e.svc, fail: newFail(slog.New(slog.DiscardHandler))}
	h := a.scoped("sites:write", func(w http.ResponseWriter, _ *http.Request, p auth.Principal) {
		writeJSON(w, http.StatusOK, map[string]string{"user": p.User.Username})
	})

	call := func(hdr map[string]string, cookie *http.Cookie) *httptest.ResponseRecorder {
		r := httptest.NewRequest("GET", "/x", nil)
		r.RemoteAddr = "192.0.2.10:5555"

		for k, v := range hdr {
			r.Header.Set(k, v)
		}

		if cookie != nil {
			r.AddCookie(cookie)
		}

		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, r)

		return rec
	}

	if rec := call(bearerHeader(read.Token), nil); rec.Code != http.StatusForbidden || errCode(t, rec) != "insufficient_scope" {
		t.Fatalf("read token on a write route: %d %s", rec.Code, rec.Body)
	}

	if rec := call(nil, nil); rec.Code != http.StatusUnauthorized {
		t.Fatalf("anonymous: %d", rec.Code)
	}

	// A browser session passes the scope check (the role check is the handler's job).
	if rec := call(nil, c); rec.Code != http.StatusOK {
		t.Fatalf("session: %d %s", rec.Code, rec.Body)
	}

	write := e.createToken(t, c, `{"name":"w","scopes":["sites:write"]}`)
	if rec := call(bearerHeader(write.Token), nil); rec.Code != http.StatusOK {
		t.Fatalf("write token: %d %s", rec.Code, rec.Body)
	}
}

func TestAllowlistOverHTTP(t *testing.T) {
	e := newAPI(t)
	c := e.adminCookie(t)
	tok := e.createToken(t, c, `{"name":"a","scopes":["sites:read"]}`)

	rec := e.do(t, reqOpts{method: "PUT", path: "/api/v1/settings/api-ip-allowlist", body: `{"entries":["198.51.100.0/24"]}`, cookie: c})
	if rec.Code != 200 || !strings.Contains(rec.Body.String(), "198.51.100.0/24") {
		t.Fatalf("%d %s", rec.Code, rec.Body)
	}

	// The test client is 192.0.2.10, which is not on the list: the token is
	// refused, the browser session is not affected.
	rec = e.do(t, reqOpts{method: "GET", path: "/api/v1/auth/me", header: bearerHeader(tok.Token)})
	if rec.Code != http.StatusForbidden || errCode(t, rec) != "ip_not_allowed" {
		t.Fatalf("%d %s", rec.Code, rec.Body)
	}

	if rec = e.do(t, reqOpts{method: "GET", path: "/api/v1/auth/me", cookie: c}); rec.Code != 200 {
		t.Fatalf("session blocked by the allowlist: %d", rec.Code)
	}

	// A bad entry is refused and the list stays.
	rec = e.do(t, reqOpts{method: "PUT", path: "/api/v1/settings/api-ip-allowlist", body: `{"entries":["nope"]}`, cookie: c})
	if rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("%d %s", rec.Code, rec.Body)
	}

	rec = e.do(t, reqOpts{method: "GET", path: "/api/v1/settings/api-ip-allowlist", cookie: c})
	if !strings.Contains(rec.Body.String(), "198.51.100.0/24") {
		t.Fatalf("%s", rec.Body)
	}

	// Clearing it lets the token in again.
	e.do(t, reqOpts{method: "PUT", path: "/api/v1/settings/api-ip-allowlist", body: `{"entries":[]}`, cookie: c})

	if rec = e.do(t, reqOpts{method: "GET", path: "/api/v1/auth/me", header: bearerHeader(tok.Token)}); rec.Code != 200 {
		t.Fatalf("%d", rec.Code)
	}
}

func TestScopeCatalogEndpoint(t *testing.T) {
	e := newAPI(t)
	c := e.adminCookie(t)

	if rec := e.do(t, reqOpts{method: "GET", path: "/api/v1/scopes"}); rec.Code != http.StatusUnauthorized {
		t.Fatalf("anonymous: %d", rec.Code)
	}

	rec := e.do(t, reqOpts{method: "GET", path: "/api/v1/scopes", cookie: c})
	if rec.Code != 200 || !strings.Contains(rec.Body.String(), `"name":"sites:delete","description":"Delete sites and their files","highRisk":true`) {
		t.Fatalf("%d %s", rec.Code, rec.Body)
	}
}
