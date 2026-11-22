// SPDX-License-Identifier: AGPL-3.0-or-later

package serve

import (
	"encoding/json"
	"net/http"
	"reflect"
	"testing"

	"github.com/maki-it/hostashi/internal/agentapi"
)

func TestRedirectSiteOverHTTP(t *testing.T) {
	ag := &stubAgent{}
	e := newAPIWithAgent(t, ag)
	c := e.adminCookie(t)

	for body, want := range map[string]int{
		`{"name":"old","domains":["old.example.com"],"type":"redirect"}`:                                                             http.StatusUnprocessableEntity,
		`{"name":"old","domains":["old.example.com"],"redirect":{"target":"https://new.example.com"}}`:                               http.StatusUnprocessableEntity,
		`{"name":"old","domains":["old.example.com"],"type":"redirect","redirect":{"target":"https://old.example.com/x"}}`:           http.StatusUnprocessableEntity,
		`{"name":"old","domains":["old.example.com"],"type":"redirect","redirect":{"target":"https://new.example.com/;x"}}`:          http.StatusUnprocessableEntity,
		`{"name":"old","domains":["old.example.com"],"type":"redirect","php":"8.4","redirect":{"target":"https://new.example.com"}}`: http.StatusUnprocessableEntity,
		`{"name":"old","domains":["old.example.com"],"type":"node"}`:                                                                 http.StatusUnprocessableEntity,
		`{"name":"old","domains":["old.example.com"],"type":"static","php":"8.4"}`:                                                   http.StatusUnprocessableEntity,
	} {
		if rec := e.do(t, reqOpts{method: "POST", path: "/api/v1/sites", cookie: c, body: body}); rec.Code != want {
			t.Errorf("%s: %d %s", body, rec.Code, rec.Body)
		}
	}

	rec := e.do(t, reqOpts{
		method: "POST", path: "/api/v1/sites", cookie: c,
		body: `{"name":"old","domains":["old.example.com"],"type":"redirect","redirect":{"target":"https://new.example.com/","keepPath":true}}`,
	})
	if rec.Code != http.StatusCreated {
		t.Fatalf("create: %d %s", rec.Code, rec.Body)
	}

	var site siteJSON
	if err := json.Unmarshal(rec.Body.Bytes(), &site); err != nil || site.Type != "redirect" || site.Account ||
		!reflect.DeepEqual(site.Redirect, &redirectJSON{Target: "https://new.example.com/", Status: 301, KeepPath: true}) {
		t.Fatalf("site %+v %v", site, err)
	}

	if len(ag.created) != 0 {
		t.Fatalf("account created: %v", ag.created)
	}

	if ws := ag.web.Sites[0]; ws.User != "old" || !reflect.DeepEqual(ws.Redirect, &agentapi.WebRedirect{Target: "https://new.example.com/", Status: 301, KeepPath: true}) || ws.PHP != "" {
		t.Fatalf("web state %+v", ws)
	}

	rec = e.do(t, reqOpts{method: "PUT", path: "/api/v1/sites/old/redirect", cookie: c, body: `{"target":"https://other.example.com/landing","status":302}`})
	if rec.Code != http.StatusOK {
		t.Fatalf("set: %d %s", rec.Code, rec.Body)
	}

	if r := ag.web.Sites[0].Redirect; r == nil || r.Target != "https://other.example.com/landing" || r.Status != 302 || r.KeepPath {
		t.Fatalf("after set %+v", r)
	}

	for path, body := range map[string]string{
		"/api/v1/sites/old/redirect": `{"target":"https://other.example.com","status":303}`,
	} {
		if rec := e.do(t, reqOpts{method: "PUT", path: path, cookie: c, body: body}); rec.Code != http.StatusUnprocessableEntity {
			t.Errorf("%s %s: %d %s", path, body, rec.Code, rec.Body)
		}
	}

	// A domain of the site cannot become the target, and the target
	// cannot become a domain.
	if rec := e.do(t, reqOpts{method: "PUT", path: "/api/v1/sites/old/domains", cookie: c, body: `{"domains":["old.example.com","other.example.com"]}`}); rec.Code != http.StatusUnprocessableEntity {
		t.Errorf("domain loop: %d %s", rec.Code, rec.Body)
	}

	// Things that need files or PHP refuse a redirect site.
	for _, req := range []reqOpts{
		{method: "PUT", path: "/api/v1/sites/old/php", body: `{"php":"8.4"}`},
		{method: "PUT", path: "/api/v1/sites/old/web", body: `{"dir":"","spa":false,"assetCache":false}`},
		{method: "PUT", path: "/api/v1/sites/old/php-programs", body: `{"enabled":true}`},
		{method: "PUT", path: "/api/v1/sites/old/snippets", body: `{"server":"","root":""}`},
		{method: "GET", path: "/api/v1/sites/old/files?path=/"},
	} {
		req.cookie = c
		if rec := e.do(t, req); rec.Code != http.StatusConflict || errCode(t, rec) != "wrong_site_type" {
			t.Errorf("%s %s: %d %s", req.method, req.path, rec.Code, rec.Body)
		}
	}

	// Not on a site of another type.
	if rec := e.do(t, reqOpts{method: "POST", path: "/api/v1/sites", cookie: c, body: `{"name":"blog","domains":["blog.example.com"]}`}); rec.Code != http.StatusCreated {
		t.Fatalf("blog: %d %s", rec.Code, rec.Body)
	}

	if rec := e.do(t, reqOpts{method: "PUT", path: "/api/v1/sites/blog/redirect", cookie: c, body: `{"target":"https://x.example.com","status":301}`}); rec.Code != http.StatusConflict {
		t.Errorf("redirect on blog: %d %s", rec.Code, rec.Body)
	}

	if rec := e.do(t, reqOpts{method: "DELETE", path: "/api/v1/sites/old", cookie: c}); rec.Code != http.StatusNoContent {
		t.Fatalf("delete: %d %s", rec.Code, rec.Body)
	}
}
