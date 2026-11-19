// SPDX-License-Identifier: AGPL-3.0-or-later

package serve

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"
)

func TestDatabaseLifecycleOverHTTP(t *testing.T) {
	e := newAPIWithAgent(t, &stubAgent{})
	c := e.adminCookie(t)

	if rec := e.do(t, reqOpts{method: "POST", path: "/api/v1/sites", cookie: c, body: `{"name":"shop","domains":["shop.example.com"]}`}); rec.Code != http.StatusCreated {
		t.Fatalf("create site: %d %s", rec.Code, rec.Body)
	}

	rec := e.do(t, reqOpts{method: "POST", path: "/api/v1/sites/shop/databases", cookie: c, body: `{"name":"shop_wp","user":"shop_wp"}`})
	if rec.Code != http.StatusCreated {
		t.Fatalf("create database: %d %s", rec.Code, rec.Body)
	}

	var created struct {
		databaseJSON
		Password string `json:"password"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &created); err != nil || created.Name != "shop_wp" || created.Site != "shop" || len(created.Password) != 32 {
		t.Fatalf("%+v %v", created, err)
	}

	// The password is shown once: no read returns it.
	for _, path := range []string{"/api/v1/databases", "/api/v1/sites/shop/databases", "/api/v1/databases/shop_wp"} {
		rec := e.do(t, reqOpts{method: "GET", path: path, cookie: c})
		if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"shop_wp"`) || strings.Contains(rec.Body.String(), created.Password) {
			t.Fatalf("%s: %d %s", path, rec.Code, rec.Body)
		}
	}

	// A given password is not echoed.
	rec = e.do(t, reqOpts{method: "POST", path: "/api/v1/databases/shop_wp/users", cookie: c, body: `{"user":"reader","password":"Given-pass_123456"}`})
	if rec.Code != http.StatusCreated || strings.Contains(rec.Body.String(), "Given-pass") {
		t.Fatalf("create user: %d %s", rec.Code, rec.Body)
	}

	rec = e.do(t, reqOpts{method: "PUT", path: "/api/v1/databases/shop_wp/users/reader/password", cookie: c, body: `{}`})
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"password":"`) {
		t.Fatalf("reset password: %d %s", rec.Code, rec.Body)
	}

	cases := []struct {
		method, path, body string
		code               int
	}{
		{"POST", "/api/v1/sites/shop/databases", `{"name":"shop_wp"}`, http.StatusConflict},
		{"POST", "/api/v1/sites/shop/databases", `{"name":"mysql"}`, http.StatusUnprocessableEntity},
		{"POST", "/api/v1/sites/shop/databases", `{"name":"x1","user":"u1","password":"it's bad 123"}`, http.StatusUnprocessableEntity},
		{"POST", "/api/v1/sites/shop/databases", `{"name":"x1","extra":1}`, http.StatusBadRequest},
		{"POST", "/api/v1/sites/nope/databases", `{"name":"x1"}`, http.StatusNotFound},
		{"GET", "/api/v1/sites/nope/databases", ``, http.StatusNotFound},
		{"GET", "/api/v1/databases/nope", ``, http.StatusNotFound},
		{"POST", "/api/v1/databases/nope/users", `{"user":"u1"}`, http.StatusNotFound},
		{"PUT", "/api/v1/databases/other/users/reader/password", `{}`, http.StatusNotFound},
		{"DELETE", "/api/v1/databases/shop_wp/users/nobody", ``, http.StatusNotFound},
	}
	for _, tc := range cases {
		rec := e.do(t, reqOpts{method: tc.method, path: tc.path, cookie: c, body: tc.body})
		if rec.Code != tc.code {
			t.Errorf("%s %s %s: %d, want %d: %s", tc.method, tc.path, tc.body, rec.Code, tc.code, rec.Body)
		}
	}

	if rec := e.do(t, reqOpts{method: "DELETE", path: "/api/v1/databases/shop_wp/users/reader", cookie: c}); rec.Code != http.StatusNoContent {
		t.Fatalf("delete user: %d %s", rec.Code, rec.Body)
	}

	if rec := e.do(t, reqOpts{method: "DELETE", path: "/api/v1/databases/shop_wp", cookie: c}); rec.Code != http.StatusNoContent {
		t.Fatalf("delete database: %d %s", rec.Code, rec.Body)
	}
}

func TestDatabaseScopes(t *testing.T) {
	e := newAPIWithAgent(t, &stubAgent{})
	c := e.adminCookie(t)

	if rec := e.do(t, reqOpts{method: "POST", path: "/api/v1/sites", cookie: c, body: `{"name":"shop","domains":["shop.example.com"]}`}); rec.Code != http.StatusCreated {
		t.Fatalf("create site: %d %s", rec.Code, rec.Body)
	}

	read := bearerHeader(e.createToken(t, c, `{"name":"r","scopes":["databases:read"]}`).Token)
	write := bearerHeader(e.createToken(t, c, `{"name":"w","scopes":["databases:write"]}`).Token)
	sites := bearerHeader(e.createToken(t, c, `{"name":"s","scopes":["sites:read","sites:write","sites:delete"]}`).Token)
	del := bearerHeader(e.createToken(t, c, `{"name":"d","scopes":["databases:delete"]}`).Token)

	if rec := e.do(t, reqOpts{method: "POST", path: "/api/v1/sites/shop/databases", header: read, body: `{"name":"a1"}`}); rec.Code != http.StatusForbidden {
		t.Fatalf("read token wrote: %d", rec.Code)
	}

	if rec := e.do(t, reqOpts{method: "GET", path: "/api/v1/databases", header: sites}); rec.Code != http.StatusForbidden {
		t.Fatalf("sites token read databases: %d", rec.Code)
	}

	if rec := e.do(t, reqOpts{method: "POST", path: "/api/v1/sites/shop/databases", header: write, body: `{"name":"a1"}`}); rec.Code != http.StatusCreated {
		t.Fatalf("write token: %d %s", rec.Code, rec.Body)
	}

	if rec := e.do(t, reqOpts{method: "GET", path: "/api/v1/databases", header: read}); rec.Code != http.StatusOK {
		t.Fatalf("read token: %d", rec.Code)
	}

	// Deleting data needs its own high-risk scope.
	if rec := e.do(t, reqOpts{method: "DELETE", path: "/api/v1/databases/a1", header: write}); rec.Code != http.StatusForbidden {
		t.Fatalf("write token deleted: %d", rec.Code)
	}

	if rec := e.do(t, reqOpts{method: "DELETE", path: "/api/v1/databases/a1", header: del}); rec.Code != http.StatusNoContent {
		t.Fatalf("delete token: %d %s", rec.Code, rec.Body)
	}
}

func TestDatabaseDumpsOverHTTP(t *testing.T) {
	e := newAPIWithAgent(t, &stubAgent{})
	c := e.adminCookie(t)

	if rec := e.do(t, reqOpts{method: "POST", path: "/api/v1/sites", cookie: c, body: `{"name":"shop","domains":["shop.example.com"]}`}); rec.Code != http.StatusCreated {
		t.Fatalf("create site: %d %s", rec.Code, rec.Body)
	}

	for _, name := range []string{"shop_wp", "shop_two"} {
		if rec := e.do(t, reqOpts{method: "POST", path: "/api/v1/sites/shop/databases", cookie: c, body: `{"name":"` + name + `"}`}); rec.Code != http.StatusCreated {
			t.Fatalf("create database: %d %s", rec.Code, rec.Body)
		}
	}

	rec := e.do(t, reqOpts{method: "POST", path: "/api/v1/databases/shop_wp/import", cookie: c, body: `{"path":"dump.sql"}`})
	if rec.Code != http.StatusAccepted || !strings.Contains(rec.Body.String(), `"id":"`+stubJob+`"`) || !strings.Contains(rec.Body.String(), `"state":"running"`) {
		t.Fatalf("import: %d %s", rec.Code, rec.Body)
	}

	read := bearerHeader(e.createToken(t, c, `{"name":"r","scopes":["databases:read"]}`).Token)

	cases := []struct {
		method, path, body string
		header             map[string]string
		code               int
	}{
		{"GET", "/api/v1/databases/shop_wp/jobs/" + stubJob, ``, read, http.StatusOK},
		{"GET", "/api/v1/databases/shop_two/jobs/" + stubJob, ``, read, http.StatusNotFound},
		{"GET", "/api/v1/databases/shop_wp/jobs/00000000000000bb", ``, read, http.StatusNotFound},
		{"GET", "/api/v1/databases/shop_wp/jobs/nothex", ``, read, http.StatusNotFound},
		{"POST", "/api/v1/databases/shop_wp/import", `{"path":"dump.sql"}`, read, http.StatusForbidden},
		{"POST", "/api/v1/databases/shop_wp/export", `{"path":"backup.sql.gz"}`, nil, http.StatusAccepted},
		{"POST", "/api/v1/databases/shop_wp/export", `{"path":"exists.sql.gz"}`, nil, http.StatusConflict},
		{"POST", "/api/v1/databases/shop_wp/import", `{"path":"../other/dump.sql"}`, nil, http.StatusUnprocessableEntity},
		{"POST", "/api/v1/databases/shop_wp/import", `{"path":"/etc/shadow.sql"}`, nil, http.StatusUnprocessableEntity},
		{"POST", "/api/v1/databases/shop_wp/import", `{"path":"dump.zip"}`, nil, http.StatusUnprocessableEntity},
		{"POST", "/api/v1/databases/shop_wp/import", `{"path":"dump.sql","user":"root"}`, nil, http.StatusBadRequest},
		{"POST", "/api/v1/databases/nope/import", `{"path":"dump.sql"}`, nil, http.StatusNotFound},
	}
	for _, tc := range cases {
		o := reqOpts{method: tc.method, path: tc.path, header: tc.header, body: tc.body}
		if tc.header == nil {
			o.cookie = c
		}

		if rec := e.do(t, o); rec.Code != tc.code {
			t.Errorf("%s %s %s: %d, want %d: %s", tc.method, tc.path, tc.body, rec.Code, tc.code, rec.Body)
		}
	}
}
