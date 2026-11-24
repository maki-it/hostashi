// SPDX-License-Identifier: AGPL-3.0-or-later

package serve

import (
	"net/http"
	"strings"
	"testing"
)

func TestWWWRedirectOverHTTP(t *testing.T) {
	ag := &stubAgent{}
	e := newAPIWithAgent(t, ag)
	c := e.adminCookie(t)

	if rec := e.do(t, reqOpts{method: "POST", path: "/api/v1/sites", cookie: c, body: `{"name":"blog","domains":["blog.example.com"]}`}); rec.Code != http.StatusCreated {
		t.Fatalf("site: %d %s", rec.Code, rec.Body)
	}

	// The site object tells the UI which name is missing.
	rec := e.do(t, reqOpts{method: "GET", path: "/api/v1/sites/blog", cookie: c})
	if !strings.Contains(rec.Body.String(), `"www":{"mode":"off","pairs":[],"missing":["www.blog.example.com"]}`) {
		t.Fatalf("site: %s", rec.Body)
	}

	put := func(body string) *http.Response {
		t.Helper()

		return e.do(t, reqOpts{method: "PUT", path: "/api/v1/sites/blog/www", cookie: c, body: body}).Result()
	}

	if r := put(`{"mode":"to-www"}`); r.StatusCode != http.StatusUnprocessableEntity {
		t.Fatalf("no pair: %d", r.StatusCode)
	}

	for _, body := range []string{`{}`, `{"mode":""}`, `{"mode":"both"}`, `{"mode":"TO-WWW"}`} {
		if r := put(body); r.StatusCode != http.StatusUnprocessableEntity {
			t.Errorf("%s: %d", body, r.StatusCode)
		}
	}

	if rec := e.do(t, reqOpts{method: "PUT", path: "/api/v1/sites/blog/domains", cookie: c, body: `{"domains":["blog.example.com","www.blog.example.com"]}`}); rec.Code != http.StatusOK {
		t.Fatalf("domains: %d %s", rec.Code, rec.Body)
	}

	rec = e.do(t, reqOpts{method: "PUT", path: "/api/v1/sites/blog/www", cookie: c, body: `{"mode":"to-non-www"}`})
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"www":{"mode":"to-non-www","pairs":[{"bare":"blog.example.com","www":"www.blog.example.com"}],"missing":[]}`) {
		t.Fatalf("set: %d %s", rec.Code, rec.Body)
	}

	for _, s := range ag.web.Sites {
		if s.User == "blog" && s.WWWRedirect != "to-non-www" {
			t.Fatalf("agent got %q", s.WWWRedirect)
		}
	}

	rec = e.do(t, reqOpts{method: "PUT", path: "/api/v1/sites/blog/www", cookie: c, body: `{"mode":"off"}`})
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"mode":"off"`) {
		t.Fatalf("off: %d %s", rec.Code, rec.Body)
	}

	if r := e.do(t, reqOpts{method: "PUT", path: "/api/v1/sites/nope/www", cookie: c, body: `{"mode":"off"}`}); r.Code != http.StatusNotFound && r.Code != http.StatusForbidden {
		t.Errorf("unknown site: %d", r.Code)
	}
}
