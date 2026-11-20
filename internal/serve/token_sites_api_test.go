// SPDX-License-Identifier: AGPL-3.0-or-later

package serve

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"
)

// TestTokenSitesOverHTTP checks the site list of a token (ADR-0018,
// ADR-0035): the token reaches only its sites, sees only them in lists and
// cannot do what spans all sites, whatever its owner's role.
func TestTokenSitesOverHTTP(t *testing.T) {
	e, _ := newFullAPI(t)
	c := e.adminCookie(t)

	for _, s := range []string{"alpha", "beta"} {
		if rec := e.do(t, reqOpts{method: "POST", path: "/api/v1/sites", cookie: c, body: `{"name":"` + s + `","domains":["` + s + `.example"]}`}); rec.Code >= 300 {
			t.Fatalf("create %s: %d %s", s, rec.Code, rec.Body)
		}
	}

	scopes := `"scopes":["sites:read","firewall:read"],"expiresInDays":1`

	for _, bad := range []string{`"sites":[]`, `"sites":["nope"]`} {
		rec := e.do(t, reqOpts{method: "POST", path: "/api/v1/tokens", cookie: c, body: `{"name":"x",` + scopes + `,` + bad + `}`})
		if rec.Code != http.StatusUnprocessableEntity || errCode(t, rec) != "invalid_argument" {
			t.Errorf("%s: %d %s", bad, rec.Code, rec.Body)
		}
	}

	tok := e.createToken(t, c, `{"name":"alpha only",`+scopes+`,"sites":["alpha"]}`)
	h := bearerHeader(tok.Token)

	rec := e.do(t, reqOpts{method: "GET", path: "/api/v1/sites", header: h})
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"alpha"`) || strings.Contains(rec.Body.String(), `"beta"`) {
		t.Fatalf("list: %d %s", rec.Code, rec.Body)
	}

	if rec := e.do(t, reqOpts{method: "GET", path: "/api/v1/sites/alpha", header: h}); rec.Code != http.StatusOK {
		t.Fatalf("own site: %d %s", rec.Code, rec.Body)
	}

	if rec := e.do(t, reqOpts{method: "GET", path: "/api/v1/sites/beta", header: h}); rec.Code != http.StatusNotFound || errCode(t, rec) != "not_found" {
		t.Fatalf("other site: %d %s", rec.Code, rec.Body)
	}

	if rec := e.do(t, reqOpts{method: "GET", path: "/api/v1/firewall", header: h}); rec.Code != http.StatusForbidden || errCode(t, rec) != "forbidden" {
		t.Fatalf("server action: %d %s", rec.Code, rec.Body)
	}

	// The token list shows the site list; null for a token without one.
	e.createToken(t, c, `{"name":"all",`+scopes+`}`)

	rec = e.do(t, reqOpts{method: "GET", path: "/api/v1/tokens", cookie: c})

	var list struct {
		Tokens []struct {
			Name  string   `json:"name"`
			Sites []string `json:"sites"`
		} `json:"tokens"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &list); err != nil || len(list.Tokens) != 2 {
		t.Fatalf("tokens: %v %s", err, rec.Body)
	}

	for _, x := range list.Tokens {
		if (x.Name == "all") != (x.Sites == nil) || x.Name == "alpha only" && (len(x.Sites) != 1 || x.Sites[0] != "alpha") {
			t.Errorf("token %q: sites %v", x.Name, x.Sites)
		}
	}

	// Deleting the site leaves the token with no site, not with all.
	if rec := e.do(t, reqOpts{method: "DELETE", path: "/api/v1/sites/alpha", cookie: c}); rec.Code >= 300 {
		t.Fatalf("delete alpha: %d %s", rec.Code, rec.Body)
	}

	rec = e.do(t, reqOpts{method: "GET", path: "/api/v1/sites", header: h})
	if rec.Code != http.StatusOK || strings.Contains(rec.Body.String(), `"beta"`) {
		t.Fatalf("list after delete: %d %s", rec.Code, rec.Body)
	}
}

// A user's token may only name granted sites and the scopes of the role.
func TestUserTokenLimits(t *testing.T) {
	e, _ := newFullAPI(t)
	c := e.adminCookie(t)

	if rec := e.do(t, reqOpts{method: "POST", path: "/api/v1/sites", cookie: c, body: `{"name":"alpha","domains":["alpha.example"]}`}); rec.Code >= 300 {
		t.Fatalf("create site: %d %s", rec.Code, rec.Body)
	}

	if rec := e.do(t, reqOpts{method: "POST", path: "/api/v1/sites", cookie: c, body: `{"name":"beta","domains":["beta.example"]}`}); rec.Code >= 300 {
		t.Fatalf("create site: %d %s", rec.Code, rec.Body)
	}

	gina, gid := matrixLogin(t, e, c, "gina", "user")
	if rec := e.do(t, reqOpts{method: "PUT", path: userPath(gid, "/grants"), cookie: c, body: `{"sites":["alpha"]}`}); rec.Code != http.StatusOK {
		t.Fatalf("grant: %d %s", rec.Code, rec.Body)
	}

	rec := e.do(t, reqOpts{method: "GET", path: "/api/v1/scopes", cookie: gina})
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"sites:read"`) || strings.Contains(rec.Body.String(), `"firewall:read"`) {
		t.Fatalf("scopes of a user: %d %s", rec.Code, rec.Body)
	}

	for _, bad := range []string{
		`{"name":"x","scopes":["firewall:read"],"expiresInDays":1}`,
		`{"name":"x","scopes":["sites:read"],"expiresInDays":1,"sites":["beta"]}`,
	} {
		if rec := e.do(t, reqOpts{method: "POST", path: "/api/v1/tokens", cookie: gina, body: bad}); rec.Code != http.StatusUnprocessableEntity {
			t.Errorf("%s: %d %s", bad, rec.Code, rec.Body)
		}
	}

	tok := e.createToken(t, gina, `{"name":"x","scopes":["sites:read"],"expiresInDays":1,"sites":["alpha"]}`)
	if rec := e.do(t, reqOpts{method: "GET", path: "/api/v1/sites/alpha", header: bearerHeader(tok.Token)}); rec.Code != http.StatusOK {
		t.Fatalf("user token on a granted site: %d %s", rec.Code, rec.Body)
	}

	// Taking the grant away also takes it from the token.
	if rec := e.do(t, reqOpts{method: "PUT", path: userPath(gid, "/grants"), cookie: c, body: `{"sites":[]}`}); rec.Code != http.StatusOK {
		t.Fatalf("revoke grant: %d %s", rec.Code, rec.Body)
	}

	if rec := e.do(t, reqOpts{method: "GET", path: "/api/v1/sites/alpha", header: bearerHeader(tok.Token)}); rec.Code != http.StatusNotFound {
		t.Fatalf("user token after the grant is gone: %d %s", rec.Code, rec.Body)
	}
}
