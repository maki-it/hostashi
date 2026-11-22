// SPDX-License-Identifier: AGPL-3.0-or-later

package serve

import (
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"testing"

	"github.com/maki-it/hostashi/internal/agentapi"
)

// webHeadersOf returns the headers the agent got for a site.
func webHeadersOf(ag *stubAgent, user string) *agentapi.WebHeaders {
	for _, s := range ag.web.Sites {
		if s.User == user {
			return s.Headers
		}
	}

	return nil
}

func TestHeadersOverHTTP(t *testing.T) {
	ag := &stubAgent{}
	e := newAPIWithAgent(t, ag)
	c := e.adminCookie(t)

	// A new site gets the defaults unless the request says otherwise.
	for _, body := range []string{
		`{"name":"blog","php":"8.4","domains":["blog.example.com"]}`,
		`{"name":"bare","domains":["bare.example.com"],"headers":{}}`,
		`{"name":"own","domains":["own.example.com"],"headers":{"frameOptions":"DENY"}}`,
	} {
		if rec := e.do(t, reqOpts{method: "POST", path: "/api/v1/sites", cookie: c, body: body}); rec.Code != http.StatusCreated {
			t.Fatalf("site: %d %s", rec.Code, rec.Body)
		}
	}

	if h := webHeadersOf(ag, "blog"); h == nil || *h != agentapi.DefaultWebHeaders {
		t.Fatalf("defaults not sent: %+v", h)
	}

	if h := webHeadersOf(ag, "bare"); h != nil {
		t.Fatalf("empty headers sent: %+v", h)
	}

	if h := webHeadersOf(ag, "own"); h == nil || *h != (agentapi.WebHeaders{FrameOptions: agentapi.FrameDeny}) {
		t.Fatalf("own headers: %+v", h)
	}

	if rec := e.do(t, reqOpts{method: "POST", path: "/api/v1/sites", cookie: c, body: `{"name":"evil","domains":["evil.example.com"],"headers":{"csp":"default-src $host"}}`}); rec.Code != http.StatusUnprocessableEntity || !strings.Contains(rec.Body.String(), "header_rejected") {
		t.Fatalf("bad headers on create: %d %s", rec.Code, rec.Body)
	}

	rec := e.do(t, reqOpts{method: "GET", path: "/api/v1/sites/blog/headers", cookie: c})

	var got headersJSON
	if rec.Code != http.StatusOK || json.Unmarshal(rec.Body.Bytes(), &got) != nil {
		t.Fatalf("get: %d %s", rec.Code, rec.Body)
	}

	if got.Headers != agentapi.DefaultWebHeaders || got.Defaults != agentapi.DefaultWebHeaders || len(got.ReferrerPolicies) != 8 || got.PermissionsPolicyStrict == "" {
		t.Fatalf("get: %s", rec.Body)
	}

	body := `{"noSniff":true,"csp":"default-src 'self'","cspReportOnly":true}`

	got = headersJSON{}

	rec = e.do(t, reqOpts{method: "PUT", path: "/api/v1/sites/blog/headers", cookie: c, body: body})
	if rec.Code != http.StatusOK || json.Unmarshal(rec.Body.Bytes(), &got) != nil || got.UpdatedAt == nil || got.Headers.CSP != "default-src 'self'" || got.Headers.FrameOptions != "" {
		t.Fatalf("set: %d %s", rec.Code, rec.Body)
	}

	if h := webHeadersOf(ag, "blog"); h == nil || !h.CSPReportOnly {
		t.Fatalf("web state: %+v", h)
	}

	for body, code := range map[string]int{
		`{"frameOptions":"ALLOWALL"}`:                       http.StatusUnprocessableEntity,
		`{"csp":"default-src 'self'\" always; return 200"}`: http.StatusUnprocessableEntity,
		`{"permissionsPolicy":"camera=('self')"}`:           http.StatusUnprocessableEntity,
		`{"xssProtection":true}`:                            http.StatusBadRequest, // unknown field
	} {
		if rec := e.do(t, reqOpts{method: "PUT", path: "/api/v1/sites/blog/headers", cookie: c, body: body}); rec.Code != code {
			t.Errorf("%s: %d, want %d: %s", body, rec.Code, code, rec.Body)
		}
	}

	// A failed web.apply restores the old headers.
	ag.webErr = errors.New("nginx refused")

	if rec := e.do(t, reqOpts{method: "PUT", path: "/api/v1/sites/blog/headers", cookie: c, body: `{}`}); rec.Code < 400 {
		t.Fatalf("failed apply: %d", rec.Code)
	}

	ag.webErr = nil

	if rec := e.do(t, reqOpts{method: "GET", path: "/api/v1/sites/blog/headers", cookie: c}); !strings.Contains(rec.Body.String(), `"cspReportOnly":true`) {
		t.Fatalf("not restored: %s", rec.Body)
	}

	// A snippet must not set a header that is switched on, either way.
	rec = e.do(t, reqOpts{method: "PUT", path: "/api/v1/sites/blog/snippets", cookie: c, body: `{"server":"add_header X-Content-Type-Options nosniff;","root":""}`})
	if rec.Code != http.StatusUnprocessableEntity || !strings.Contains(rec.Body.String(), "header_conflict") {
		t.Fatalf("snippet conflict: %d %s", rec.Code, rec.Body)
	}

	if rec := e.do(t, reqOpts{method: "PUT", path: "/api/v1/sites/blog/snippets", cookie: c, body: `{"server":"add_header X-Frame-Options DENY;","root":""}`}); rec.Code != http.StatusOK {
		t.Fatalf("snippet without conflict: %d %s", rec.Code, rec.Body)
	}

	rec = e.do(t, reqOpts{method: "PUT", path: "/api/v1/sites/blog/headers", cookie: c, body: `{"frameOptions":"SAMEORIGIN"}`})
	if rec.Code != http.StatusUnprocessableEntity || !strings.Contains(rec.Body.String(), "header_conflict") {
		t.Fatalf("header conflict: %d %s", rec.Code, rec.Body)
	}

	if rec := e.do(t, reqOpts{method: "PUT", path: "/api/v1/sites/nope/headers", cookie: c, body: `{}`}); rec.Code != http.StatusNotFound {
		t.Errorf("unknown site: %d", rec.Code)
	}

	tok := e.createToken(t, c, `{"name":"read","scopes":["sites:read"]}`)
	if rec := e.do(t, reqOpts{method: "PUT", path: "/api/v1/sites/blog/headers", header: bearerHeader(tok.Token), body: `{}`}); rec.Code != http.StatusForbidden {
		t.Errorf("write with sites:read: %d", rec.Code)
	}

	if rec := e.do(t, reqOpts{method: "GET", path: "/api/v1/sites/blog/headers", header: bearerHeader(tok.Token)}); rec.Code != http.StatusOK {
		t.Errorf("read with sites:read: %d", rec.Code)
	}
}
