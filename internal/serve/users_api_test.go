// SPDX-License-Identifier: AGPL-3.0-or-later

package serve

import (
	"encoding/json"
	"net/http"
	"strconv"
	"strings"
	"testing"
)

type createdUser struct {
	User       accountJSON    `json:"user"`
	Invitation invitationJSON `json:"invitation"`
}

func (e *apiEnv) createUser(t *testing.T, c *http.Cookie, name, role string) createdUser {
	t.Helper()

	rec := e.do(t, reqOpts{
		method: "POST", path: "/api/v1/users", cookie: c,
		body: `{"username":"` + name + `","role":"` + role + `"}`,
	})
	if rec.Code != http.StatusCreated {
		t.Fatalf("create user: %d %s", rec.Code, rec.Body)
	}

	var out createdUser
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}

	return out
}

func userPath(id int64, rest string) string {
	return "/api/v1/users/" + strconv.FormatInt(id, 10) + rest
}

func TestUsersOverHTTP(t *testing.T) {
	e := newAPI(t)
	c := e.adminCookie(t)

	anna := e.createUser(t, c, "anna", "manager")
	if anna.User.Role != "manager" || !anna.User.Invited || anna.Invitation.Token == "" {
		t.Fatalf("created %+v", anna)
	}

	// The list never carries a secret.
	rec := e.do(t, reqOpts{method: "GET", path: "/api/v1/users", cookie: c})
	if rec.Code != http.StatusOK || strings.Contains(rec.Body.String(), anna.Invitation.Token) ||
		!strings.Contains(rec.Body.String(), `"username":"anna"`) || !strings.Contains(rec.Body.String(), `"username":"kim"`) {
		t.Fatalf("list: %d %s", rec.Code, rec.Body)
	}

	for _, c2 := range []struct {
		body, code string
		status     int
	}{
		{`{"username":"anna","role":"user"}`, "user_exists", http.StatusConflict},
		{`{"username":"bob","role":"root"}`, "invalid_argument", http.StatusUnprocessableEntity},
		{`{"username":"Bad Name","role":"user"}`, "invalid_argument", http.StatusUnprocessableEntity},
	} {
		rec := e.do(t, reqOpts{method: "POST", path: "/api/v1/users", cookie: c, body: c2.body})
		if rec.Code != c2.status || errCode(t, rec) != c2.code {
			t.Errorf("%s: %d %s", c2.body, rec.Code, rec.Body)
		}
	}

	// Check and accept the invitation; that logs anna in.
	rec = e.do(t, reqOpts{method: "POST", path: "/api/v1/invitations/check", body: `{"token":"` + anna.Invitation.Token + `"}`})
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"anna"`) {
		t.Fatalf("check: %d %s", rec.Code, rec.Body)
	}

	rec = e.do(t, reqOpts{
		method: "POST", path: "/api/v1/invitations/accept",
		body: `{"token":"` + anna.Invitation.Token + `","password":"` + apiPassword + `"}`,
	})
	if rec.Code != http.StatusOK {
		t.Fatalf("accept: %d %s", rec.Code, rec.Body)
	}

	annaCookie := sessionCookieOf(t, rec)

	rec = e.do(t, reqOpts{
		method: "POST", path: "/api/v1/invitations/accept",
		body: `{"token":"` + anna.Invitation.Token + `","password":"` + apiPassword + `"}`,
	})
	if rec.Code != http.StatusNotFound || errCode(t, rec) != "invitation_invalid" {
		t.Fatalf("second accept: %d %s", rec.Code, rec.Body)
	}

	// A manager cannot manage users.
	rec = e.do(t, reqOpts{method: "GET", path: "/api/v1/users", cookie: annaCookie})
	if rec.Code != http.StatusForbidden || errCode(t, rec) != "forbidden" {
		t.Fatalf("manager lists users: %d %s", rec.Code, rec.Body)
	}

	// Kim cannot change their own account.
	kimID := int64(1)
	for _, o := range []reqOpts{
		{method: "PUT", path: userPath(kimID, ""), body: `{"role":"user"}`},
		{method: "DELETE", path: userPath(kimID, "")},
		{method: "POST", path: userPath(kimID, "/reset")},
	} {
		o.cookie = c
		if rec := e.do(t, o); rec.Code != http.StatusConflict || errCode(t, rec) != "own_account" {
			t.Errorf("%s %s: %d %s", o.method, o.path, rec.Code, rec.Body)
		}
	}

	// Disabling anna ends her session.
	rec = e.do(t, reqOpts{method: "PUT", path: userPath(anna.User.ID, ""), cookie: c, body: `{"disabled":true}`})
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"disabled":true`) {
		t.Fatalf("disable: %d %s", rec.Code, rec.Body)
	}

	if rec := e.do(t, reqOpts{method: "GET", path: "/api/v1/auth/me", cookie: annaCookie}); rec.Code != http.StatusUnauthorized {
		t.Fatalf("disabled session: %d %s", rec.Code, rec.Body)
	}

	for _, o := range []reqOpts{
		{method: "PUT", path: userPath(anna.User.ID, ""), body: `{}`},
		{method: "PUT", path: userPath(99, ""), body: `{"role":"user"}`},
		{method: "GET", path: "/api/v1/users/abc"},
		{method: "GET", path: "/api/v1/users/0"},
	} {
		o.cookie = c
		if rec := e.do(t, o); rec.Code != http.StatusUnprocessableEntity && rec.Code != http.StatusNotFound {
			t.Errorf("%s %s: %d %s", o.method, o.path, rec.Code, rec.Body)
		}
	}

	// Reset returns a new link; delete removes the account.
	rec = e.do(t, reqOpts{method: "POST", path: userPath(anna.User.ID, "/reset"), cookie: c})
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"token"`) {
		t.Fatalf("reset: %d %s", rec.Code, rec.Body)
	}

	if rec := e.do(t, reqOpts{method: "DELETE", path: userPath(anna.User.ID, ""), cookie: c}); rec.Code != http.StatusNoContent {
		t.Fatalf("delete: %d %s", rec.Code, rec.Body)
	}

	if rec := e.do(t, reqOpts{method: "GET", path: userPath(anna.User.ID, ""), cookie: c}); rec.Code != http.StatusNotFound {
		t.Fatalf("deleted user: %d %s", rec.Code, rec.Body)
	}
}

func TestLastAdminOverHTTP(t *testing.T) {
	e := newAPI(t)
	c := e.adminCookie(t)

	bob := e.createUser(t, c, "bob", "admin")

	rec := e.do(t, reqOpts{
		method: "POST", path: "/api/v1/invitations/accept",
		body: `{"token":"` + bob.Invitation.Token + `","password":"` + apiPassword + `"}`,
	})
	if rec.Code != http.StatusOK {
		t.Fatalf("accept: %d %s", rec.Code, rec.Body)
	}

	bobCookie := sessionCookieOf(t, rec)

	// Bob demotes Kim; then Kim, now a manager, cannot touch users, and Bob
	// cannot be removed because he is the last admin.
	if rec := e.do(t, reqOpts{method: "PUT", path: userPath(1, ""), cookie: bobCookie, body: `{"role":"manager"}`}); rec.Code != http.StatusOK {
		t.Fatalf("demote: %d %s", rec.Code, rec.Body)
	}

	// The role change ended Kim's session.
	if rec := e.do(t, reqOpts{method: "GET", path: "/api/v1/users", cookie: c}); rec.Code != http.StatusUnauthorized {
		t.Fatalf("old session: %d %s", rec.Code, rec.Body)
	}

	// Kim, now a manager, cannot change Bob back.
	rec = e.do(t, reqOpts{method: "POST", path: "/api/v1/auth/login", body: `{"username":"kim","password":"` + apiPassword + `"}`})
	kimCookie := sessionCookieOf(t, rec)

	if rec := e.do(t, reqOpts{method: "PUT", path: userPath(bob.User.ID, ""), cookie: kimCookie, body: `{"role":"user"}`}); rec.Code != http.StatusForbidden {
		t.Fatalf("manager demotes admin: %d %s", rec.Code, rec.Body)
	}

	// Through the API an admin always acts on someone else, so at least
	// the acting admin stays; the store's last-admin guard is the second
	// line (tested in internal/store). Self changes are refused here.
	for _, o := range []reqOpts{
		{method: "PUT", path: userPath(bob.User.ID, ""), body: `{"disabled":true}`},
		{method: "DELETE", path: userPath(bob.User.ID, "")},
	} {
		o.cookie = bobCookie
		if rec := e.do(t, o); errCode(t, rec) != "own_account" {
			t.Errorf("%s: %d %s", o.method, rec.Code, rec.Body)
		}
	}
}

func TestGrantsOverHTTP(t *testing.T) {
	e := newAPIWithAgent(t, &stubAgent{})
	c := e.adminCookie(t)

	if rec := e.do(t, reqOpts{
		method: "POST", path: "/api/v1/sites", cookie: c,
		body: `{"name":"alpha","domains":["alpha.example"]}`,
	}); rec.Code != http.StatusCreated && rec.Code != http.StatusAccepted {
		t.Fatalf("create site: %d %s", rec.Code, rec.Body)
	}

	u := e.createUser(t, c, "anna", "user")

	rec := e.do(t, reqOpts{method: "PUT", path: userPath(u.User.ID, "/grants"), cookie: c, body: `{"sites":["alpha"]}`})
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `["alpha"]`) {
		t.Fatalf("grant: %d %s", rec.Code, rec.Body)
	}

	rec = e.do(t, reqOpts{method: "GET", path: userPath(u.User.ID, "/grants"), cookie: c})
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `["alpha"]`) {
		t.Fatalf("read grants: %d %s", rec.Code, rec.Body)
	}

	for _, c2 := range []struct {
		path, body, code string
	}{
		{userPath(u.User.ID, "/grants"), `{"sites":["missing"]}`, "invalid_argument"},
		{userPath(u.User.ID, "/grants"), `{}`, "invalid_argument"},
		{userPath(1, "/grants"), `{"sites":["alpha"]}`, "not_grantee"},
		{userPath(99, "/grants"), `{"sites":[]}`, "not_found"},
	} {
		rec := e.do(t, reqOpts{method: "PUT", path: c2.path, cookie: c, body: c2.body})
		if errCode(t, rec) != c2.code {
			t.Errorf("%s %s: %d %s", c2.path, c2.body, rec.Code, rec.Body)
		}
	}

	rec = e.do(t, reqOpts{method: "PUT", path: userPath(u.User.ID, "/grants"), cookie: c, body: `{"sites":[]}`})
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"sites":[]`) {
		t.Fatalf("clear: %d %s", rec.Code, rec.Body)
	}
}
