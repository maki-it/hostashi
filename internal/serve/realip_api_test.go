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

// webRealIPOf returns the real IP setting the agent got for a site.
func webRealIPOf(ag *stubAgent, user string) *agentapi.WebRealIP {
	for _, s := range ag.web.Sites {
		if s.User == user {
			return s.RealIP
		}
	}

	return nil
}

func TestRealIPOverHTTP(t *testing.T) {
	ag := &stubAgent{}
	e := newAPIWithAgent(t, ag)
	c := e.adminCookie(t)

	if rec := e.do(t, reqOpts{method: "POST", path: "/api/v1/sites", cookie: c, body: `{"name":"blog","domains":["blog.example.com"]}`}); rec.Code != http.StatusCreated {
		t.Fatalf("site: %d %s", rec.Code, rec.Body)
	}

	if r := webRealIPOf(ag, "blog"); r != nil {
		t.Fatalf("a new site trusts a proxy: %+v", r)
	}

	rec := e.do(t, reqOpts{method: "GET", path: "/api/v1/sites/blog/real-ip", cookie: c})

	var got realIPJSON
	if rec.Code != http.StatusOK || json.Unmarshal(rec.Body.Bytes(), &got) != nil {
		t.Fatalf("get: %d %s", rec.Code, rec.Body)
	}

	if !got.RealIP.IsZero() || len(got.Headers) != 4 || got.Headers[0] != agentapi.RealIPXForwardedFor ||
		len(got.Cloudflare) != len(agentapi.CloudflareRanges) || got.CloudflareDate == "" || got.UpdatedAt != nil {
		t.Fatalf("get: %s", rec.Body)
	}

	got = realIPJSON{}

	rec = e.do(t, reqOpts{method: "PUT", path: "/api/v1/sites/blog/real-ip", cookie: c, body: `{"cloudflare":true,"ranges":["198.51.100.7"]}`})
	if rec.Code != http.StatusOK || json.Unmarshal(rec.Body.Bytes(), &got) != nil || got.UpdatedAt == nil || !got.RealIP.Cloudflare || len(got.RealIP.Ranges) != 1 {
		t.Fatalf("set: %d %s", rec.Code, rec.Body)
	}

	if r := webRealIPOf(ag, "blog"); r == nil || !r.Cloudflare || r.Ranges[0] != "198.51.100.7" {
		t.Fatalf("web state: %+v", r)
	}

	for body, code := range map[string]int{
		`{"ranges":["0.0.0.0/0"]}`:                      http.StatusUnprocessableEntity,
		`{"ranges":["127.0.0.1"]}`:                      http.StatusUnprocessableEntity,
		`{"ranges":["169.254.169.254"]}`:                http.StatusUnprocessableEntity,
		`{"cloudflare":true,"header":"X-Client-IP"}`:    http.StatusUnprocessableEntity,
		`{"header":"X-Forwarded-For"}`:                  http.StatusUnprocessableEntity,
		`{"ranges":["10.0.0.1; set_real_ip_from 0/0"]}`: http.StatusUnprocessableEntity,
		`{"cloudflare":true,"trustAll":true}`:           http.StatusBadRequest, // unknown field
	} {
		rec := e.do(t, reqOpts{method: "PUT", path: "/api/v1/sites/blog/real-ip", cookie: c, body: body})
		if rec.Code != code || (code == http.StatusUnprocessableEntity && !strings.Contains(rec.Body.String(), "real_ip_rejected")) {
			t.Errorf("%s: %d, want %d: %s", body, rec.Code, code, rec.Body)
		}
	}

	// A failed web.apply restores the old setting.
	ag.webErr = errors.New("nginx refused")

	if rec := e.do(t, reqOpts{method: "PUT", path: "/api/v1/sites/blog/real-ip", cookie: c, body: `{}`}); rec.Code < 400 {
		t.Fatalf("failed apply: %d", rec.Code)
	}

	ag.webErr = nil

	if rec := e.do(t, reqOpts{method: "GET", path: "/api/v1/sites/blog/real-ip", cookie: c}); !strings.Contains(rec.Body.String(), `"198.51.100.7"`) {
		t.Fatalf("not restored: %s", rec.Body)
	}

	// A user with a grant may switch Cloudflare and the header and remove
	// own ranges, but not add one (ADR-0059).
	gina, gid := matrixLogin(t, e, c, "gina", "user")
	if rec := e.do(t, reqOpts{method: "PUT", path: userPath(gid, "/grants"), cookie: c, body: `{"sites":["blog"]}`}); rec.Code != http.StatusOK {
		t.Fatalf("grant: %d %s", rec.Code, rec.Body)
	}

	for _, c := range []struct {
		body string
		code int
	}{
		{`{"cloudflare":true,"ranges":["198.51.100.7"],"header":"CF-Connecting-IP"}`, http.StatusOK},
		{`{"cloudflare":true,"ranges":["198.51.100.7/32"]}`, http.StatusOK}, // the same range
		{`{"cloudflare":true,"ranges":["198.51.100.7","198.51.100.8"]}`, http.StatusForbidden},
		{`{"ranges":["203.0.113.0/24"]}`, http.StatusForbidden},
		{`{"cloudflare":true}`, http.StatusOK},
		{`{"cloudflare":true,"ranges":["198.51.100.7"]}`, http.StatusForbidden}, // gone now
	} {
		if rec := e.do(t, reqOpts{method: "PUT", path: "/api/v1/sites/blog/real-ip", cookie: gina, body: c.body}); rec.Code != c.code {
			t.Errorf("user %s: %d, want %d: %s", c.body, rec.Code, c.code, rec.Body)
		}
	}

	if r := webRealIPOf(ag, "blog"); r == nil || !r.Cloudflare || len(r.Ranges) != 0 {
		t.Fatalf("after the user: %+v", r)
	}

	if rec := e.do(t, reqOpts{method: "PUT", path: "/api/v1/sites/nope/real-ip", cookie: c, body: `{}`}); rec.Code != http.StatusNotFound {
		t.Errorf("unknown site: %d", rec.Code)
	}

	tok := e.createToken(t, c, `{"name":"read","scopes":["sites:read"]}`)
	if rec := e.do(t, reqOpts{method: "PUT", path: "/api/v1/sites/blog/real-ip", header: bearerHeader(tok.Token), body: `{}`}); rec.Code != http.StatusForbidden {
		t.Errorf("write with sites:read: %d", rec.Code)
	}

	if rec := e.do(t, reqOpts{method: "GET", path: "/api/v1/sites/blog/real-ip", header: bearerHeader(tok.Token)}); rec.Code != http.StatusOK {
		t.Errorf("read with sites:read: %d", rec.Code)
	}
}
