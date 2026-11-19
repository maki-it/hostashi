// SPDX-License-Identifier: AGPL-3.0-or-later

package serve

import (
	"net/http"
	"strings"
	"testing"
)

func (e *apiEnv) setMetrics(t *testing.T, c *http.Cookie, body string) {
	t.Helper()

	rec := e.do(t, reqOpts{method: "PUT", path: "/api/v1/settings/metrics", body: body, cookie: c})
	if rec.Code != http.StatusOK || rec.Body.String() != body+"\n" {
		t.Fatalf("set metrics %s: %d %s", body, rec.Code, rec.Body)
	}
}

func TestMetricsOffByDefault(t *testing.T) {
	e := newAPI(t)
	c := e.adminCookie(t)
	tok := e.createToken(t, c, `{"name":"prometheus","scopes":["metrics:read"]}`)

	rec := e.do(t, reqOpts{method: "GET", path: "/api/v1/settings/metrics", cookie: c})
	if rec.Code != 200 || rec.Body.String() != `{"enabled":false}`+"\n" {
		t.Fatalf("%d %s", rec.Code, rec.Body)
	}

	// Off means 404, even with the right token, and it is not the frontend.
	rec = e.do(t, reqOpts{method: "GET", path: "/metrics", header: bearerHeader(tok.Token)})
	if rec.Code != http.StatusNotFound || errCode(t, rec) != "not_found" {
		t.Fatalf("%d %s", rec.Code, rec.Body)
	}
}

func TestMetricsAccess(t *testing.T) {
	e := newAPI(t)
	c := e.adminCookie(t)
	e.setMetrics(t, c, `{"enabled":true}`)

	good := e.createToken(t, c, `{"name":"prometheus","scopes":["metrics:read"]}`)
	other := e.createToken(t, c, `{"name":"deploy","scopes":["sites:read"]}`)

	// A browser session does not open the endpoint.
	rec := e.do(t, reqOpts{method: "GET", path: "/metrics", cookie: c})
	if rec.Code != http.StatusUnauthorized || rec.Header().Get("WWW-Authenticate") != "Bearer" {
		t.Fatalf("session: %d %s", rec.Code, rec.Body)
	}

	rec = e.do(t, reqOpts{method: "GET", path: "/metrics"})
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("anonymous: %d %s", rec.Code, rec.Body)
	}

	rec = e.do(t, reqOpts{method: "GET", path: "/metrics", header: bearerHeader("hst_wrong")})
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("bad token: %d %s", rec.Code, rec.Body)
	}

	rec = e.do(t, reqOpts{method: "GET", path: "/metrics", header: bearerHeader(other.Token)})
	if rec.Code != http.StatusForbidden || errCode(t, rec) != "insufficient_scope" {
		t.Fatalf("other scope: %d %s", rec.Code, rec.Body)
	}

	// Only GET.
	rec = e.do(t, reqOpts{method: "POST", path: "/metrics", header: bearerHeader(good.Token)})
	if rec.Code != http.StatusMethodNotAllowed {
		t.Fatalf("post: %d %s", rec.Code, rec.Body)
	}

	rec = e.do(t, reqOpts{method: "GET", path: "/metrics", header: bearerHeader(good.Token)})
	if rec.Code != http.StatusOK || !strings.HasPrefix(rec.Header().Get("Content-Type"), "text/plain; version=0.0.4") {
		t.Fatalf("scrape: %d %q %s", rec.Code, rec.Header().Get("Content-Type"), rec.Body)
	}

	body := rec.Body.String()
	for _, want := range []string{"go_goroutines", `hostashi_http_requests_total{code="2xx",route="POST /api/v1/tokens"}`, `route="PUT /api/v1/settings/metrics"`} {
		if !strings.Contains(body, want) {
			t.Errorf("missing %q", want)
		}
	}

	// Switching off closes it again.
	e.setMetrics(t, c, `{"enabled":false}`)

	rec = e.do(t, reqOpts{method: "GET", path: "/metrics", header: bearerHeader(good.Token)})
	if rec.Code != http.StatusNotFound {
		t.Fatalf("after off: %d %s", rec.Code, rec.Body)
	}
}

func TestMetricsSettingNeedsSession(t *testing.T) {
	e := newAPI(t)
	c := e.adminCookie(t)
	tok := e.createToken(t, c, `{"name":"prometheus","scopes":["metrics:read"]}`)

	rec := e.do(t, reqOpts{method: "PUT", path: "/api/v1/settings/metrics", body: `{"enabled":true}`, header: bearerHeader(tok.Token)})
	if rec.Code != http.StatusForbidden || errCode(t, rec) != "session_required" {
		t.Fatalf("token: %d %s", rec.Code, rec.Body)
	}

	rec = e.do(t, reqOpts{method: "PUT", path: "/api/v1/settings/metrics", body: `{"enabled":true}`})
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("anonymous: %d %s", rec.Code, rec.Body)
	}

	for _, body := range []string{`{}`, `{"enabled":"yes"}`, `{"enabled":true,"x":1}`} {
		rec = e.do(t, reqOpts{method: "PUT", path: "/api/v1/settings/metrics", body: body, cookie: c})
		if rec.Code != http.StatusBadRequest && rec.Code != http.StatusUnprocessableEntity {
			t.Errorf("%s: %d %s", body, rec.Code, rec.Body)
		}
	}
}
