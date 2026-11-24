// SPDX-License-Identifier: AGPL-3.0-or-later

package serve

import (
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"testing"

	"golang.org/x/crypto/bcrypt"

	"github.com/maki-it/hostashi/internal/agentapi"
)

// webAccessFor returns the access setting the agent got for a site.
func webAccessFor(ag *stubAgent, user string) *agentapi.WebAccess {
	for _, s := range ag.web.Sites {
		if s.User == user {
			return s.Access
		}
	}

	return nil
}

func TestAccessOverHTTP(t *testing.T) {
	ag := &stubAgent{}
	e := newAPIWithAgent(t, ag)
	c := e.adminCookie(t)

	if rec := e.do(t, reqOpts{method: "POST", path: "/api/v1/sites", cookie: c, body: `{"name":"blog","domains":["blog.example.com"]}`}); rec.Code != http.StatusCreated {
		t.Fatalf("site: %d %s", rec.Code, rec.Body)
	}

	rec := e.do(t, reqOpts{method: "GET", path: "/api/v1/sites/blog/access", cookie: c})

	var got accessJSON
	if rec.Code != http.StatusOK || json.Unmarshal(rec.Body.Bytes(), &got) != nil || got.IPMode != "" || got.Password || len(got.Users) != 0 || got.Limits.Users != agentapi.MaxAccessUsers || got.ClientAddress == "" {
		t.Fatalf("get: %d %s", rec.Code, rec.Body)
	}

	// Password protection needs a user first.
	if rec := e.do(t, reqOpts{method: "PUT", path: "/api/v1/sites/blog/access", cookie: c, body: `{"password":true}`}); rec.Code != http.StatusUnprocessableEntity || !strings.Contains(rec.Body.String(), "access_rejected") {
		t.Fatalf("password without users: %d %s", rec.Code, rec.Body)
	}

	// A generated password comes back once; an own one does not.
	rec = e.do(t, reqOpts{method: "POST", path: "/api/v1/sites/blog/access/users", cookie: c, body: `{"name":"kim"}`})

	var pw accessPasswordJSON
	if rec.Code != http.StatusCreated || json.Unmarshal(rec.Body.Bytes(), &pw) != nil || len(pw.Password) != 24 {
		t.Fatalf("add generated: %d %s", rec.Code, rec.Body)
	}

	if rec := e.do(t, reqOpts{method: "POST", path: "/api/v1/sites/blog/access/users", cookie: c, body: `{"name":"office","password":"correct horse battery"}`}); rec.Code != http.StatusCreated || strings.Contains(rec.Body.String(), "correct horse") {
		t.Fatalf("add own: %d %s", rec.Code, rec.Body)
	}

	for body, code := range map[string]int{
		`{"name":"kim"}`:                              http.StatusConflict,
		`{"name":"a:b"}`:                              http.StatusUnprocessableEntity,
		`{"name":"x","password":"short"}`:             http.StatusUnprocessableEntity,
		`{"name":"x","password":"has a\nnewline ok"}`: http.StatusUnprocessableEntity,
	} {
		if rec := e.do(t, reqOpts{method: "POST", path: "/api/v1/sites/blog/access/users", cookie: c, body: body}); rec.Code != code {
			t.Errorf("add %s: %d, want %d: %s", body, rec.Code, code, rec.Body)
		}
	}

	// No user change reaches nginx while password protection is off.
	if a := webAccessFor(ag, "blog"); a != nil {
		t.Fatalf("web state before switching on: %+v", a)
	}

	rec = e.do(t, reqOpts{method: "PUT", path: "/api/v1/sites/blog/access", cookie: c, body: `{"ipMode":"deny","ips":["203.0.113.7"],"password":true,"paths":["/wp-admin"],"bypass":["198.51.100.0/24"]}`})
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"office"`) || strings.Contains(rec.Body.String(), "$2a$") {
		t.Fatalf("set: %d %s", rec.Code, rec.Body)
	}

	a := webAccessFor(ag, "blog")
	if a == nil || a.IPMode != "deny" || a.Auth == nil || len(a.Auth.Users) != 2 || a.Auth.Paths[0] != "/wp-admin" {
		t.Fatalf("web state: %+v", a)
	}

	var kimHash string

	for _, u := range a.Auth.Users {
		if u.Name == "kim" {
			kimHash = u.Hash
		}

		if u.Name == "office" && bcrypt.CompareHashAndPassword([]byte(u.Hash), []byte("correct horse battery")) != nil {
			t.Errorf("office hash does not match")
		}
	}

	if bcrypt.CompareHashAndPassword([]byte(kimHash), []byte(pw.Password)) != nil {
		t.Errorf("kim hash does not match the generated password")
	}

	if cost, _ := bcrypt.Cost([]byte(kimHash)); cost != agentapi.AccessBcryptCost {
		t.Errorf("cost %d", cost)
	}

	for body, code := range map[string]int{
		`{"ipMode":"allow"}`:                              http.StatusUnprocessableEntity,
		`{"ipMode":"deny","ips":["10.0.0.1; allow all"]}`: http.StatusUnprocessableEntity,
		`{"password":true,"paths":["/a/../b"]}`:           http.StatusUnprocessableEntity,
		`{"password":false,"paths":["no-slash"]}`:         http.StatusUnprocessableEntity,
		`{"password":true,"bypass":["127.0.0.1"]}`:        http.StatusUnprocessableEntity,
		`{"password":true,"realm":"x"}`:                   http.StatusBadRequest,
	} {
		if rec := e.do(t, reqOpts{method: "PUT", path: "/api/v1/sites/blog/access", cookie: c, body: body}); rec.Code != code {
			t.Errorf("set %s: %d, want %d: %s", body, rec.Code, code, rec.Body)
		}
	}

	// A new password replaces the hash and reaches nginx.
	rec = e.do(t, reqOpts{method: "PUT", path: "/api/v1/sites/blog/access/users/kim", cookie: c, body: `{}`})
	if rec.Code != http.StatusOK || json.Unmarshal(rec.Body.Bytes(), &pw) != nil || len(pw.Password) != 24 {
		t.Fatalf("new password: %d %s", rec.Code, rec.Body)
	}

	for _, u := range webAccessFor(ag, "blog").Auth.Users {
		if u.Name == "kim" && u.Hash == kimHash {
			t.Error("hash not replaced")
		}
	}

	if rec := e.do(t, reqOpts{method: "PUT", path: "/api/v1/sites/blog/access/users/nobody", cookie: c, body: `{}`}); rec.Code != http.StatusNotFound {
		t.Errorf("unknown user: %d", rec.Code)
	}

	// A snippet with auth_basic is refused while the password is on.
	if rec := e.do(t, reqOpts{method: "PUT", path: "/api/v1/sites/blog/snippets", cookie: c, body: `{"server":"location /x {\n    auth_basic off;\n}\n","root":""}`}); rec.Code != http.StatusUnprocessableEntity {
		t.Errorf("snippet: %d %s", rec.Code, rec.Body)
	}

	// The last user stays while the password is on.
	if rec := e.do(t, reqOpts{method: "DELETE", path: "/api/v1/sites/blog/access/users/office", cookie: c}); rec.Code != http.StatusNoContent {
		t.Fatalf("delete: %d %s", rec.Code, rec.Body)
	}

	if rec := e.do(t, reqOpts{method: "DELETE", path: "/api/v1/sites/blog/access/users/kim", cookie: c}); rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("delete last: %d %s", rec.Code, rec.Body)
	}

	// A failed web.apply restores the old setting.
	ag.webErr = errors.New("nginx refused")

	if rec := e.do(t, reqOpts{method: "PUT", path: "/api/v1/sites/blog/access", cookie: c, body: `{}`}); rec.Code < 400 {
		t.Fatalf("failed apply: %d", rec.Code)
	}

	ag.webErr = nil

	if rec := e.do(t, reqOpts{method: "GET", path: "/api/v1/sites/blog/access", cookie: c}); !strings.Contains(rec.Body.String(), `"203.0.113.7"`) {
		t.Fatalf("not restored: %s", rec.Body)
	}

	// A user with a grant for the site may change it (ADR-0066).
	gina, gid := matrixLogin(t, e, c, "gina", "user")
	if rec := e.do(t, reqOpts{method: "PUT", path: userPath(gid, "/grants"), cookie: c, body: `{"sites":["blog"]}`}); rec.Code != http.StatusOK {
		t.Fatalf("grant: %d %s", rec.Code, rec.Body)
	}

	if rec := e.do(t, reqOpts{method: "PUT", path: "/api/v1/sites/blog/access", cookie: gina, body: `{}`}); rec.Code != http.StatusOK {
		t.Errorf("user: %d %s", rec.Code, rec.Body)
	}

	if a := webAccessFor(ag, "blog"); a != nil {
		t.Errorf("after switching off: %+v", a)
	}

	// With the password off, the last user may go.
	if rec := e.do(t, reqOpts{method: "DELETE", path: "/api/v1/sites/blog/access/users/kim", cookie: gina}); rec.Code != http.StatusNoContent {
		t.Errorf("delete last while off: %d %s", rec.Code, rec.Body)
	}

	if rec := e.do(t, reqOpts{method: "PUT", path: "/api/v1/sites/nope/access", cookie: c, body: `{}`}); rec.Code != http.StatusNotFound {
		t.Errorf("unknown site: %d", rec.Code)
	}
}
