// SPDX-License-Identifier: AGPL-3.0-or-later

package serve

import (
	"encoding/json"
	"net/http"
	"testing"
)

func TestPageCacheOverHTTP(t *testing.T) {
	e, _ := newFullAPI(t)
	c := e.adminCookie(t)

	for _, s := range []string{"alpha", "beta"} {
		if rec := e.do(t, reqOpts{method: "POST", path: "/api/v1/sites", cookie: c, body: `{"name":"` + s + `","php":"8.4","domains":["` + s + `.example"]}`}); rec.Code >= 300 {
			t.Fatalf("create %s: %d %s", s, rec.Code, rec.Body)
		}
	}

	path := "/api/v1/sites/alpha/page-cache"

	rec := e.do(t, reqOpts{method: "GET", path: path, cookie: c})

	var got pageCacheJSON
	if rec.Code != http.StatusOK || json.Unmarshal(rec.Body.Bytes(), &got) != nil || got.Enabled || got.SizeMB != 256 ||
		len(got.Choices.Presets) != 3 || got.Exclude == nil {
		t.Fatalf("get: %d %s", rec.Code, rec.Body)
	}

	// Purging a cache that is off is refused.
	if rec := e.do(t, reqOpts{method: "POST", path: path + "/purge", cookie: c}); rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("purge while off: %d %s", rec.Code, rec.Body)
	}

	for _, bad := range []string{
		`{}`,
		`{"enabled":true,"preset":"varnish","minutes":10}`,
		`{"enabled":true,"preset":"php","minutes":3}`,
		`{"enabled":true,"preset":"php","minutes":10,"exclude":["/a\"; deny all; \""]}`,
	} {
		if rec := e.do(t, reqOpts{method: "PUT", path: path, cookie: c, body: bad}); rec.Code != http.StatusUnprocessableEntity {
			t.Errorf("%s: %d %s", bad, rec.Code, rec.Body)
		}
	}

	// The size has its own route.
	if rec := e.do(t, reqOpts{method: "PUT", path: path, cookie: c, body: `{"enabled":true,"preset":"php","minutes":10,"sizeMB":512}`}); rec.Code != http.StatusBadRequest {
		t.Errorf("size in the settings: %d %s", rec.Code, rec.Body)
	}

	rec = e.do(t, reqOpts{method: "PUT", path: path, cookie: c, body: `{"enabled":true,"preset":"wordpress","minutes":10,"exclude":["/shop*"]}`})
	if rec.Code != http.StatusOK {
		t.Fatalf("on: %d %s", rec.Code, rec.Body)
	}

	if rec := e.do(t, reqOpts{method: "PUT", path: path + "/size", cookie: c, body: `{"sizeMB":2048}`}); rec.Code != http.StatusOK {
		t.Fatalf("size: %d %s", rec.Code, rec.Body)
	}

	if rec := e.do(t, reqOpts{method: "PUT", path: path + "/size", cookie: c, body: `{"sizeMB":3000}`}); rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("bad size: %d %s", rec.Code, rec.Body)
	}

	if rec := e.do(t, reqOpts{method: "PUT", path: "/api/v1/sites/beta/page-cache", cookie: c, body: `{"enabled":true,"preset":"php","minutes":5}`}); rec.Code != http.StatusOK {
		t.Fatalf("beta on: %d %s", rec.Code, rec.Body)
	}

	// A token for a CMS: only purge, only its site.
	tok := e.createToken(t, c, `{"name":"wordpress","scopes":["pagecache:purge"],"expiresInDays":30,"sites":["alpha"]}`)
	h := bearerHeader(tok.Token)

	rec = e.do(t, reqOpts{method: "POST", path: path + "/purge", header: h})
	if rec.Code != http.StatusNoContent {
		t.Fatalf("purge: %d %s", rec.Code, rec.Body)
	}

	rec = e.do(t, reqOpts{method: "POST", path: path + "/purge", header: h})
	if rec.Code != http.StatusTooManyRequests || errCode(t, rec) != "rate_limited" || rec.Header().Get("Retry-After") != "10" {
		t.Fatalf("second purge: %d %s %q", rec.Code, rec.Body, rec.Header().Get("Retry-After"))
	}

	if rec := e.do(t, reqOpts{method: "POST", path: "/api/v1/sites/beta/page-cache/purge", header: h}); rec.Code != http.StatusNotFound {
		t.Fatalf("other site: %d %s", rec.Code, rec.Body)
	}

	for _, r := range []reqOpts{
		{method: "GET", path: path},
		{method: "PUT", path: path, body: `{"enabled":false,"preset":"php","minutes":10}`},
		{method: "GET", path: "/api/v1/sites/alpha"},
	} {
		r.header = h
		if rec := e.do(t, r); rec.Code != http.StatusForbidden || errCode(t, rec) != "insufficient_scope" {
			t.Errorf("%s %s with the purge token: %d %s", r.method, r.path, rec.Code, rec.Body)
		}
	}

	// A user with a grant switches the cache but cannot set its size.
	gina, gid := matrixLogin(t, e, c, "gina", "user")
	if rec := e.do(t, reqOpts{method: "PUT", path: userPath(gid, "/grants"), cookie: c, body: `{"sites":["alpha"]}`}); rec.Code != http.StatusOK {
		t.Fatalf("grant: %d %s", rec.Code, rec.Body)
	}

	if rec := e.do(t, reqOpts{method: "PUT", path: path, cookie: gina, body: `{"enabled":true,"preset":"woocommerce","minutes":30}`}); rec.Code != http.StatusOK {
		t.Fatalf("user change: %d %s", rec.Code, rec.Body)
	}

	if rec := e.do(t, reqOpts{method: "PUT", path: path + "/size", cookie: gina, body: `{"sizeMB":64}`}); rec.Code != http.StatusForbidden {
		t.Fatalf("user size: %d %s", rec.Code, rec.Body)
	}

	if rec := e.do(t, reqOpts{method: "PUT", path: "/api/v1/sites/beta/page-cache", cookie: gina, body: `{"enabled":false,"preset":"php","minutes":5}`}); rec.Code != http.StatusNotFound {
		t.Fatalf("user on another site: %d %s", rec.Code, rec.Body)
	}

	rec = e.do(t, reqOpts{method: "GET", path: path, cookie: gina})
	if json.Unmarshal(rec.Body.Bytes(), &got) != nil || got.Preset != "woocommerce" || got.Minutes != 30 || got.SizeMB != 2048 || len(got.Exclude) != 0 {
		t.Fatalf("after user change: %s", rec.Body)
	}
}
