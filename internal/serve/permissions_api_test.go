// SPDX-License-Identifier: AGPL-3.0-or-later

package serve

import (
	"encoding/json"
	"net/http"
	"testing"
)

func TestPermissionsResetOverHTTP(t *testing.T) {
	e, _ := newFullAPI(t)
	c := e.adminCookie(t)

	for _, body := range []string{
		`{"name":"alpha","php":"8.4","domains":["alpha.example"]}`,
		`{"name":"beta","php":"8.4","domains":["beta.example"]}`,
		`{"name":"old","domains":["old.example.com"],"type":"redirect","redirect":{"target":"https://new.example.com"}}`,
	} {
		if rec := e.do(t, reqOpts{method: "POST", path: "/api/v1/sites", cookie: c, body: body}); rec.Code >= 300 {
			t.Fatalf("create %s: %d %s", body, rec.Code, rec.Body)
		}
	}

	path := "/api/v1/sites/alpha/permissions/reset"

	rec := e.do(t, reqOpts{method: "POST", path: path, cookie: c})

	var got permissionsResetJSON
	if rec.Code != http.StatusOK || json.Unmarshal(rec.Body.Bytes(), &got) != nil || got.Checked != 12 || got.Changed != 3 ||
		got.Skipped != 1 || len(got.SkippedPaths) != 1 || got.SkippedPaths[0].Path != "htdocs/mnt" {
		t.Fatalf("reset: %d %s", rec.Code, rec.Body)
	}

	p := e.auditPage(t, c, "limit=1&action=site.permissions.reset")
	if len(p.Entries) != 1 || p.Entries[0].Target != "alpha" {
		t.Fatalf("audit: %+v", p.Entries)
	}

	if rec := e.do(t, reqOpts{method: "POST", path: "/api/v1/sites/nosuch/permissions/reset", cookie: c}); rec.Code != http.StatusNotFound {
		t.Fatalf("unknown site: %d %s", rec.Code, rec.Body)
	}

	// A redirect site has no files.
	if rec := e.do(t, reqOpts{method: "POST", path: "/api/v1/sites/old/permissions/reset", cookie: c}); rec.Code != http.StatusConflict || errCode(t, rec) != "wrong_site_type" {
		t.Fatalf("redirect site: %d %s", rec.Code, rec.Body)
	}

	// A token with sites:write for alpha only.
	tok := e.createToken(t, c, `{"name":"ops","scopes":["sites:write"],"expiresInDays":1,"sites":["alpha"]}`)
	h := bearerHeader(tok.Token)

	if rec := e.do(t, reqOpts{method: "POST", path: path, header: h}); rec.Code != http.StatusOK {
		t.Fatalf("token: %d %s", rec.Code, rec.Body)
	}

	if rec := e.do(t, reqOpts{method: "POST", path: "/api/v1/sites/beta/permissions/reset", header: h}); rec.Code != http.StatusNotFound {
		t.Fatalf("token on another site: %d %s", rec.Code, rec.Body)
	}

	ro := e.createToken(t, c, `{"name":"ro","scopes":["sites:read"],"expiresInDays":1}`)
	if rec := e.do(t, reqOpts{method: "POST", path: path, header: bearerHeader(ro.Token)}); rec.Code != http.StatusForbidden {
		t.Fatalf("read-only token: %d %s", rec.Code, rec.Body)
	}

	// A user with a grant may reset their own site, not another.
	gina, gid := matrixLogin(t, e, c, "gina", "user")
	if rec := e.do(t, reqOpts{method: "PUT", path: userPath(gid, "/grants"), cookie: c, body: `{"sites":["alpha"]}`}); rec.Code != http.StatusOK {
		t.Fatalf("grant: %d %s", rec.Code, rec.Body)
	}

	if rec := e.do(t, reqOpts{method: "POST", path: path, cookie: gina}); rec.Code != http.StatusOK {
		t.Fatalf("user: %d %s", rec.Code, rec.Body)
	}

	if rec := e.do(t, reqOpts{method: "POST", path: "/api/v1/sites/beta/permissions/reset", cookie: gina}); rec.Code != http.StatusNotFound {
		t.Fatalf("user on another site: %d %s", rec.Code, rec.Body)
	}
}
