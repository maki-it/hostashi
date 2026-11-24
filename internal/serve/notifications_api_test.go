// SPDX-License-Identifier: AGPL-3.0-or-later

package serve

import (
	"net/http"
	"strings"
	"testing"
)

func TestAccountNotifications(t *testing.T) {
	e := newAPI(t)
	c := e.adminCookie(t)

	get := func(c *http.Cookie) string {
		rec := e.do(t, reqOpts{method: "GET", path: "/api/v1/account/notifications", cookie: c})
		if rec.Code != http.StatusOK {
			t.Fatalf("get: %d %s", rec.Code, rec.Body)
		}

		return strings.TrimSpace(rec.Body.String())
	}

	// Admins see every category, switched on by default.
	want := `{"email":"","mail_enabled":false,"categories":[{"id":"backups","enabled":true},{"id":"certificates","enabled":true},{"id":"doctor","enabled":true},{"id":"services","enabled":true},{"id":"malware","enabled":true},{"id":"deploys","enabled":false},{"id":"php","enabled":true},{"id":"disk","enabled":true}]}`
	if got := get(c); got != want {
		t.Fatalf("admin:\n%s\nwant\n%s", got, want)
	}

	rec := e.do(t, reqOpts{method: "PUT", path: "/api/v1/account/notifications", body: `{"backups":false,"php":false,"services":false}`, cookie: c})
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `{"id":"backups","enabled":false}`) || !strings.Contains(rec.Body.String(), `{"id":"services","enabled":false}`) || !strings.Contains(rec.Body.String(), `{"id":"php","enabled":false}`) {
		t.Fatalf("put: %d %s", rec.Code, rec.Body)
	}

	// A manager sees only the site categories, off by default, and cannot
	// switch on server ones.
	manager, _ := matrixLogin(t, e, c, "mona", "manager")

	want = `{"email":"","mail_enabled":false,"categories":[{"id":"certificates","enabled":false},{"id":"services","enabled":false},{"id":"malware","enabled":false},{"id":"deploys","enabled":false},{"id":"php","enabled":false},{"id":"disk","enabled":false}]}`
	if got := get(manager); got != want {
		t.Fatalf("manager:\n%s\nwant\n%s", got, want)
	}

	for _, body := range []string{`{"backups":true}`, `{"mail":true}`, `[]`, `{"php":"yes"}`} {
		if rec := e.do(t, reqOpts{method: "PUT", path: "/api/v1/account/notifications", body: body, cookie: manager}); rec.Code != http.StatusUnprocessableEntity && rec.Code != http.StatusBadRequest {
			t.Errorf("%s: %d %s", body, rec.Code, rec.Body)
		}
	}

	// API tokens cannot read or change them.
	tok := e.createToken(t, c, `{"name":"all","scopes":["sites:read"]}`)
	if rec := e.do(t, reqOpts{method: "GET", path: "/api/v1/account/notifications", header: bearerHeader(tok.Token)}); rec.Code != http.StatusForbidden {
		t.Fatalf("token: %d", rec.Code)
	}
}
