// SPDX-License-Identifier: AGPL-3.0-or-later

package serve

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/maki-it/hostashi/internal/agentapi"
	"github.com/maki-it/hostashi/internal/agentrpc"
)

type stubWP struct {
	reqs []agentapi.WPInstallRequest
	err  error
}

func (s *stubWP) WPInstallStart(_ context.Context, req agentapi.WPInstallRequest) (agentapi.WPInstallStatus, error) {
	if s.err != nil {
		return agentapi.WPInstallStatus{}, s.err
	}

	s.reqs = append(s.reqs, req)

	return agentapi.WPInstallStatus{Site: req.Site, State: "running"}, nil
}

func (s *stubWP) WPInstallStatus(_ context.Context, req agentapi.WPStatusRequest) (agentapi.WPInstallStatus, error) {
	return agentapi.WPInstallStatus{Site: req.Site, State: "failed", Error: "core install failed", Log: "Error: no database"}, nil
}

func TestWordPressOverHTTP(t *testing.T) {
	e := newAPIWithAgent(t, &stubAgent{})
	c := e.adminCookie(t)

	const body = `{"locale":"en_US","title":"Blog","adminUser":"kim","adminEmail":"kim@example.com"}`

	for _, b := range []string{
		`{"name":"blog","php":"8.4","domains":["blog.example.com"]}`,
		`{"name":"docs","domains":["docs.example.com"]}`,
	} {
		if rec := e.do(t, reqOpts{method: "POST", path: "/api/v1/sites", cookie: c, body: b}); rec.Code != http.StatusCreated {
			t.Fatalf("create: %d %s", rec.Code, rec.Body)
		}
	}

	// Without the agent part, the routes say so.
	if rec := e.do(t, reqOpts{method: "POST", path: "/api/v1/sites/blog/wordpress", cookie: c, body: body}); rec.Code != http.StatusConflict ||
		!strings.Contains(rec.Body.String(), "wordpress_unavailable") {
		t.Fatalf("unavailable: %d %s", rec.Code, rec.Body)
	}

	wp := &stubWP{}
	e.sites.EnableWordPress(wp)

	rec := e.do(t, reqOpts{method: "POST", path: "/api/v1/sites/blog/wordpress", cookie: c, body: body})
	if rec.Code != http.StatusAccepted || rec.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("start: %d %s", rec.Code, rec.Body)
	}

	var got wordPressStartJSON
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil || got.State != "running" || got.Database != "blog_wp" ||
		got.AdminPassword == "" || got.AdminPassword != wp.reqs[0].AdminPassword {
		t.Fatalf("start: %+v %v", got, err)
	}

	// The panel keeps no copy: the status never shows the password.
	rec = e.do(t, reqOpts{method: "GET", path: "/api/v1/sites/blog/wordpress", cookie: c})
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"state":"failed"`) || strings.Contains(rec.Body.String(), got.AdminPassword) {
		t.Fatalf("status: %d %s", rec.Code, rec.Body)
	}

	for name, tc := range map[string]struct {
		site, body string
		code       int
	}{
		"static site":  {"docs", body, http.StatusConflict},
		"unknown site": {"none", body, http.StatusNotFound},
		"admin user":   {"blog", strings.Replace(body, `"kim"`, `"admin"`, 1), http.StatusUnprocessableEntity},
		"language":     {"blog", strings.Replace(body, "en_US", "fr_FR", 1), http.StatusUnprocessableEntity},
		"unknown key":  {"blog", strings.Replace(body, "{", `{"dbHost":"db.example.com",`, 1), http.StatusBadRequest},
	} {
		if rec := e.do(t, reqOpts{method: "POST", path: "/api/v1/sites/" + tc.site + "/wordpress", cookie: c, body: tc.body}); rec.Code != tc.code {
			t.Errorf("%s: %d %s", name, rec.Code, rec.Body)
		}
	}

	// The agent refuses a document root with files: 409 with its reason.
	wp.err = &agentrpc.Error{Code: agentrpc.CodeConflict, Message: "the document root is not empty"}

	if rec := e.do(t, reqOpts{method: "POST", path: "/api/v1/sites/blog/wordpress", cookie: c, body: body}); rec.Code != http.StatusConflict ||
		!strings.Contains(rec.Body.String(), "not empty") {
		t.Fatalf("not empty: %d %s", rec.Code, rec.Body)
	}

	// A token needs databases:write as well.
	tok := e.createToken(t, c, `{"name":"wp","scopes":["sites:read","sites:write"]}`)
	if rec := e.do(t, reqOpts{method: "POST", path: "/api/v1/sites/blog/wordpress", header: bearerHeader(tok.Token), body: body}); rec.Code != http.StatusForbidden ||
		!strings.Contains(rec.Body.String(), "databases:write") {
		t.Fatalf("scope: %d %s", rec.Code, rec.Body)
	}
}
