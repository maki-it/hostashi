// SPDX-License-Identifier: AGPL-3.0-or-later

package serve

import (
	"net/http"
	"net/http/httptest"
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

func TestCheckMetricsListen(t *testing.T) {
	ok := []string{"127.0.0.1:9471", "[::1]:9471", "127.0.0.5:1"}
	bad := []string{"localhost:9471", "0.0.0.0:9471", ":9471", "[::]:9471", "192.0.2.1:9471", "127.0.0.1", "127.0.0.1:", "127.0.0.1:0", ""}

	for _, a := range ok {
		if err := CheckMetricsListen(a); err != nil {
			t.Errorf("%q rejected: %v", a, err)
		}
	}

	for _, a := range bad {
		if err := CheckMetricsListen(a); err == nil {
			t.Errorf("%q accepted", a)
		}
	}
}

func TestLocalMetricsHandler(t *testing.T) {
	e := newAPI(t)
	c := e.adminCookie(t)
	h := LocalMetricsHandler(e.metrics, e.settings, nil)

	get := func(method, path string) *httptest.ResponseRecorder {
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, httptest.NewRequest(method, path, nil))

		return rec
	}

	if rec := get("GET", "/metrics"); rec.Code != http.StatusNotFound {
		t.Fatalf("off: %d %s", rec.Code, rec.Body)
	}

	e.setMetrics(t, c, `{"enabled":true}`)

	rec := get("GET", "/metrics")
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "go_goroutines") || rec.Header().Get("X-Content-Type-Options") != "nosniff" {
		t.Fatalf("on: %d %s", rec.Code, rec.Body)
	}

	// Nothing else is served on this listener, not the API and not the UI.
	for _, p := range []string{"/", "/api/v1/health", "/api/v1/sites", "/metrics/x"} {
		if rec := get("GET", p); rec.Code != http.StatusNotFound {
			t.Errorf("%s: %d", p, rec.Code)
		}
	}

	if rec := get("POST", "/metrics"); rec.Code != http.StatusMethodNotAllowed {
		t.Errorf("post: %d", rec.Code)
	}
}

func TestRequestID(t *testing.T) {
	e := newAPI(t)

	forged := strings.Repeat("a", 32)
	rec := e.do(t, reqOpts{method: "GET", path: "/api/v1/auth/me", header: map[string]string{RequestIDHeader: forged}})

	id := rec.Header().Get(RequestIDHeader)
	if len(id) != 32 || id == forged {
		t.Fatalf("id %q", id)
	}

	// The error body carries the same ID, so a user can quote it.
	if rec.Code != http.StatusUnauthorized || !strings.Contains(rec.Body.String(), `"requestId":"`+id+`"`) {
		t.Fatalf("%d %s", rec.Code, rec.Body)
	}

	if again := e.do(t, reqOpts{method: "GET", path: "/api/v1/health"}).Header().Get(RequestIDHeader); again == id || len(again) != 32 {
		t.Fatalf("not fresh: %q", again)
	}
}
