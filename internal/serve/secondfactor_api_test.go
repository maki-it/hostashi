// SPDX-License-Identifier: AGPL-3.0-or-later

package serve

import (
	"net/http"
	"strings"
	"testing"
)

// TestSecondFactorOverHTTP checks ADR-0035, question 2: admins and managers
// need a second factor, a user only when an admin demands one, and an API
// token is refused while its owner has none.
func TestSecondFactorOverHTTP(t *testing.T) {
	e, _ := newFullAPI(t)
	c := e.adminCookie(t)

	tok := e.createToken(t, c, `{"name":"ci","scopes":["sites:read"],"expiresInDays":30}`)
	bearer := map[string]string{"Authorization": "Bearer " + tok.Token}

	if rec := e.do(t, reqOpts{method: "GET", path: "/api/v1/sites", header: bearer}); rec.Code != http.StatusOK {
		t.Fatalf("token with an owner with a second factor: %d %s", rec.Code, rec.Body)
	}

	// Without the second factor the owner's token stops working, and so
	// does the session, except for /auth/me and setting a factor up.
	if err := e.st.RawExec(t.Context(), `DELETE FROM passkeys`); err != nil {
		t.Fatal(err)
	}

	for _, o := range []reqOpts{
		{method: "GET", path: "/api/v1/sites", header: bearer},
		{method: "GET", path: "/api/v1/sites", cookie: c},
		{method: "GET", path: "/api/v1/users", cookie: c},
		{method: "POST", path: "/api/v1/tokens", cookie: c, body: `{"name":"x","scopes":["sites:read"],"expiresInDays":1}`},
	} {
		if rec := e.do(t, o); rec.Code != http.StatusForbidden || errCode(t, rec) != "second_factor_required" {
			t.Errorf("%s %s without a second factor: %d %s", o.method, o.path, rec.Code, rec.Body)
		}
	}

	rec := e.do(t, reqOpts{method: "GET", path: "/api/v1/auth/me", cookie: c})
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"secondFactor":false,"secondFactorRequired":true`) {
		t.Fatalf("me: %d %s", rec.Code, rec.Body)
	}

	if rec := e.do(t, reqOpts{method: "POST", path: "/api/v1/auth/totp/enroll", cookie: c}); rec.Code != http.StatusOK {
		t.Fatalf("enroll stays open: %d %s", rec.Code, rec.Body)
	}

	e.giveSecondFactor(t, "kim")

	// A user needs none, until an admin demands one.
	uma := e.createUser(t, c, "uma", "user")

	rec = e.do(t, reqOpts{
		method: "POST", path: "/api/v1/invitations/accept",
		body: `{"token":"` + uma.Invitation.Token + `","password":"` + apiPassword + `"}`,
	})
	if rec.Code != http.StatusOK {
		t.Fatalf("accept: %d %s", rec.Code, rec.Body)
	}

	uc := sessionCookieOf(t, rec)

	if rec := e.do(t, reqOpts{method: "GET", path: "/api/v1/sites", cookie: uc}); rec.Code != http.StatusOK {
		t.Fatalf("user without a second factor: %d %s", rec.Code, rec.Body)
	}

	rec = e.do(t, reqOpts{method: "PUT", path: userPath(uma.User.ID, ""), cookie: c, body: `{"requireSecondFactor":true}`})
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"requireSecondFactor":true`) {
		t.Fatalf("require: %d %s", rec.Code, rec.Body)
	}

	if rec := e.do(t, reqOpts{method: "GET", path: "/api/v1/sites", cookie: uc}); rec.Code != http.StatusForbidden || errCode(t, rec) != "second_factor_required" {
		t.Fatalf("user after the demand: %d %s", rec.Code, rec.Body)
	}

	e.giveSecondFactor(t, "uma")

	if rec := e.do(t, reqOpts{method: "GET", path: "/api/v1/sites", cookie: uc}); rec.Code != http.StatusOK {
		t.Fatalf("user with a second factor: %d %s", rec.Code, rec.Body)
	}

	// With only a passkey, the password alone does not log in.
	rec = e.do(t, reqOpts{method: "POST", path: "/api/v1/auth/login", body: `{"username":"uma","password":"` + apiPassword + `"}`})
	if rec.Code != http.StatusUnauthorized || errCode(t, rec) != "passkey_required" || len(rec.Result().Cookies()) != 0 {
		t.Fatalf("password login with only a passkey: %d %s", rec.Code, rec.Body)
	}
}
