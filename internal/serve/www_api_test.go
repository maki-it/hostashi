// SPDX-License-Identifier: AGPL-3.0-or-later

package serve

import (
	"net/http"
	"strings"
	"testing"

	"github.com/maki-it/hostashi/internal/cpmigrate"
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

	put := func(body string) int {
		t.Helper()

		return e.do(t, reqOpts{method: "PUT", path: "/api/v1/sites/blog/www", cookie: c, body: body}).Code
	}

	if code := put(`{"mode":"to-www"}`); code != http.StatusUnprocessableEntity {
		t.Fatalf("no pair: %d", code)
	}

	for _, body := range []string{`{}`, `{"mode":""}`, `{"mode":"both"}`, `{"mode":"TO-WWW"}`} {
		if code := put(body); code != http.StatusUnprocessableEntity {
			t.Errorf("%s: %d", body, code)
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

// The CloudPanel import sets the www redirect of a created site; a site
// without a pair stays as it is.
func TestSiteCreatorSetsWWW(t *testing.T) {
	e := newAPIWithAgent(t, &stubAgent{})
	ctx := t.Context()
	create := siteCreator(e.sites, nil)

	if _, err := create(ctx, 1, cpmigrate.NewSite{Name: "shop", Domains: []string{"shop.example.com", "www.shop.example.com"}, WWWRedirect: "to-non-www"}); err != nil {
		t.Fatal(err)
	}

	if s, err := e.sites.Get(ctx, "shop"); err != nil || s.WWWRedirect != "to-non-www" {
		t.Fatalf("shop: %q %v", s.WWWRedirect, err)
	}

	if _, err := create(ctx, 1, cpmigrate.NewSite{Name: "intra", Domains: []string{"intra.example.com", "www.intra.example.com"}, Proxy: "http://10.0.0.5:8080", WWWRedirect: "to-www"}); err != nil {
		t.Fatal(err)
	}

	if s, err := e.sites.Get(ctx, "intra"); err != nil || s.WWWRedirect != "to-www" {
		t.Fatalf("proxy: %q %v", s.WWWRedirect, err)
	}

	// No pair: the site stands, the mode is not set.
	if _, err := create(ctx, 1, cpmigrate.NewSite{Name: "lone", Domains: []string{"lone.example.com"}, WWWRedirect: "to-www"}); err != nil {
		t.Fatal(err)
	}

	if s, err := e.sites.Get(ctx, "lone"); err != nil || s.WWWRedirect != "" {
		t.Fatalf("lone: %q %v", s.WWWRedirect, err)
	}
}
