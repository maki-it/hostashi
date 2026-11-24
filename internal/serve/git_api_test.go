// SPDX-License-Identifier: AGPL-3.0-or-later

package serve

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"
)

func TestGitOverHTTP(t *testing.T) {
	e := newAPIWithAgent(t, &stubAgent{})
	c := e.adminCookie(t)

	if rec := e.do(t, reqOpts{method: "POST", path: "/api/v1/sites", cookie: c, body: `{"name":"blog","domains":["blog.example.com"]}`}); rec.Code != http.StatusCreated {
		t.Fatalf("create: %d %s", rec.Code, rec.Body)
	}

	rec := e.do(t, reqOpts{method: "GET", path: "/api/v1/sites/blog/git", cookie: c})

	var v gitJSON
	if err := json.Unmarshal(rec.Body.Bytes(), &v); rec.Code != http.StatusOK || err != nil || v.Configured || v.Branch != "main" || v.BuildTimeoutMinutes != 15 {
		t.Fatalf("defaults: %d %s", rec.Code, rec.Body)
	}

	if rec := e.do(t, reqOpts{method: "GET", path: "/api/v1/sites/nope/git", cookie: c}); rec.Code != http.StatusNotFound {
		t.Errorf("missing site: %d", rec.Code)
	}

	base := `"branch":"main","deployDir":"htdocs","keep":5,"buildTimeoutMinutes":15`

	for body, want := range map[string]int{
		`{"url":"file:///srv/x","auth":"none",` + base + `}`:                                                                          http.StatusUnprocessableEntity,
		`{"url":"https://github.com/a/b","auth":"token",` + base + `}`:                                                                http.StatusUnprocessableEntity,
		`{"url":"https://github.com/a/b","auth":"none","branch":"main","deployDir":"../x","keep":5,"buildTimeoutMinutes":15}`:         http.StatusUnprocessableEntity,
		`{"url":"https://github.com/a/b","auth":"none",` + base + `,"buildTimeoutMinutes":0}`:                                         http.StatusUnprocessableEntity,
		`{"url":"git@github.com:acme/blog.git","auth":"ssh",` + base + `,"shared":[".env"],"commands":["composer install --no-dev"]}`: http.StatusOK,
	} {
		if rec := e.do(t, reqOpts{method: "PUT", path: "/api/v1/sites/blog/git", cookie: c, body: body}); rec.Code != want {
			t.Errorf("%s: %d %s", body, rec.Code, rec.Body)
		}
	}

	rec = e.do(t, reqOpts{method: "GET", path: "/api/v1/sites/blog/git", cookie: c})
	if err := json.Unmarshal(rec.Body.Bytes(), &v); err != nil || !v.Configured || !strings.HasPrefix(v.PublicKey, "ssh-ed25519 ") || v.Shared[0] != ".env" || v.UpdatedAt == "" {
		t.Fatalf("set: %s", rec.Body)
	}

	if strings.Contains(rec.Body.String(), "PRIVATE") {
		t.Fatal("private key in the answer")
	}

	rec = e.do(t, reqOpts{method: "POST", path: "/api/v1/sites/blog/git/key", cookie: c})

	var k gitJSON
	if err := json.Unmarshal(rec.Body.Bytes(), &k); rec.Code != http.StatusOK || err != nil || k.PublicKey == v.PublicKey {
		t.Fatalf("new key: %d %s", rec.Code, rec.Body)
	}

	// A token is accepted and never shown.
	body := `{"url":"https://github.com/acme/blog.git","auth":"token","tokenUser":"x-access-token","token":"ghp_s3cr3t",` + base + `}`
	if rec := e.do(t, reqOpts{method: "PUT", path: "/api/v1/sites/blog/git", cookie: c, body: body}); rec.Code != http.StatusOK ||
		strings.Contains(rec.Body.String(), "s3cr3t") || !strings.Contains(rec.Body.String(), `"hasToken":true`) {
		t.Fatalf("token: %d %s", rec.Code, rec.Body)
	}

	if rec := e.do(t, reqOpts{method: "GET", path: "/api/v1/audit?action=site.git", cookie: c}); rec.Code != http.StatusOK || strings.Contains(rec.Body.String(), "s3cr3t") {
		t.Errorf("audit: %d %s", rec.Code, rec.Body)
	}

	// Reading the site does not allow changing the setting.
	read := bearerHeader(e.createToken(t, c, `{"name":"r","scopes":["sites:read"]}`).Token)
	if rec := e.do(t, reqOpts{method: "GET", path: "/api/v1/sites/blog/git", header: read}); rec.Code != http.StatusOK {
		t.Errorf("read with sites:read: %d", rec.Code)
	}

	if rec := e.do(t, reqOpts{method: "DELETE", path: "/api/v1/sites/blog/git", header: read}); rec.Code != http.StatusForbidden {
		t.Errorf("delete with sites:read: %d", rec.Code)
	}

	write := bearerHeader(e.createToken(t, c, `{"name":"w","scopes":["git:write"]}`).Token)
	if rec := e.do(t, reqOpts{method: "DELETE", path: "/api/v1/sites/blog/git", header: write}); rec.Code != http.StatusNoContent {
		t.Fatalf("delete: %d %s", rec.Code, rec.Body)
	}

	if rec := e.do(t, reqOpts{method: "DELETE", path: "/api/v1/sites/blog/git", cookie: c}); rec.Code != http.StatusNotFound || !strings.Contains(rec.Body.String(), "git_not_configured") {
		t.Errorf("delete twice: %d %s", rec.Code, rec.Body)
	}
}
