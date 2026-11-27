// SPDX-License-Identifier: AGPL-3.0-or-later

package serve

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/maki-it/hostashi/internal/agentapi"
)

// stubClones starts copy jobs that stay running.
type stubClones struct {
	copies []agentapi.SiteHomeCopyRequest
}

func (s *stubClones) SiteHomeCopy(_ context.Context, req agentapi.SiteHomeCopyRequest) (agentapi.SiteHomeCopyJob, error) {
	s.copies = append(s.copies, req)

	return agentapi.SiteHomeCopyJob{ID: "0123456789abcdef", Source: req.Source, Target: req.Target, State: agentapi.JobRunning}, nil
}

func (s *stubClones) SiteHomeCopyStatus(_ context.Context, id string) (agentapi.SiteHomeCopyJob, error) {
	return agentapi.SiteHomeCopyJob{ID: id, State: agentapi.JobRunning, Files: 12, Bytes: 3456}, nil
}

func (s *stubClones) DBCopy(_ context.Context, source, target string) (agentapi.DBJob, error) {
	return agentapi.DBJob{ID: "fedcba9876543210", Kind: "copy", Source: source, Database: target, State: agentapi.JobRunning}, nil
}

func (s *stubClones) WPCloneStart(_ context.Context, req agentapi.WPCloneRequest) (agentapi.WPInstallStatus, error) {
	return agentapi.WPInstallStatus{Site: req.Site, State: agentapi.DeployRunning}, nil
}

func (s *stubClones) DotEnvCloneStart(_ context.Context, req agentapi.DotEnvCloneRequest) (agentapi.WPInstallStatus, error) {
	return agentapi.WPInstallStatus{Site: req.Site, State: agentapi.DeployRunning}, nil
}

func (s *stubClones) WPCloneStatus(_ context.Context, site string) (agentapi.WPInstallStatus, error) {
	return agentapi.WPInstallStatus{Site: site, State: agentapi.DeployRunning}, nil
}

func (s *stubClones) PoolList(context.Context) ([]agentapi.PoolInfo, error) { return nil, nil }

func (s *stubClones) HostStats(context.Context) (agentapi.HostStats, error) {
	return agentapi.HostStats{}, nil
}

func TestCloneOverHTTP(t *testing.T) {
	e := newAPIWithAgent(t, &stubAgent{})
	c := e.adminCookie(t)

	if rec := e.do(t, reqOpts{method: "POST", path: "/api/v1/sites", cookie: c, body: `{"name":"shop","php":"8.4","domains":["shop.example.com"]}`}); rec.Code != http.StatusCreated {
		t.Fatalf("create: %d %s", rec.Code, rec.Body)
	}

	const body = `{"name":"shop-stage","domain":"stage.shop.example.com","staging":true}`

	if rec := e.do(t, reqOpts{method: "POST", path: "/api/v1/sites/shop/clone", cookie: c, body: body}); rec.Code != http.StatusConflict ||
		!strings.Contains(rec.Body.String(), "clone_unavailable") {
		t.Fatalf("unavailable: %d %s", rec.Code, rec.Body)
	}

	cl := &stubClones{}
	e.sites.EnableClones(cl)

	rec := e.do(t, reqOpts{method: "POST", path: "/api/v1/sites/shop/clone", cookie: c, body: body})

	var started cloneStartJSON
	if rec.Code != http.StatusAccepted || json.Unmarshal(rec.Body.Bytes(), &started) != nil || started.Site.Name != "shop-stage" {
		t.Fatalf("clone: %d %s", rec.Code, rec.Body)
	}

	// A staging clone is behind a generated user, shown in this answer.
	if started.Access == nil || started.Access.User != "staging" || len(started.Access.Password) != 24 {
		t.Fatalf("access: %s", rec.Body)
	}

	rec = e.do(t, reqOpts{method: "GET", path: "/api/v1/sites/shop-stage/clone", cookie: c})

	var got cloneJSON
	if rec.Code != http.StatusOK || json.Unmarshal(rec.Body.Bytes(), &got) != nil || got.Source != "shop" || !got.Staging || got.State != "copying" {
		t.Fatalf("status: %d %s", rec.Code, rec.Body)
	}

	for name, tc := range map[string]struct {
		method, path, body string
		code               int
	}{
		"source is no clone":  {"GET", "/api/v1/sites/shop/clone", "", http.StatusNotFound},
		"refresh while busy":  {"POST", "/api/v1/sites/shop-stage/clone/refresh", "", http.StatusConflict},
		"clone while busy":    {"POST", "/api/v1/sites/shop/clone", `{"name":"shop-two","domain":"two.shop.example.com"}`, http.StatusConflict},
		"unknown source":      {"POST", "/api/v1/sites/none/clone", body, http.StatusNotFound},
		"unknown field":       {"POST", "/api/v1/sites/shop/clone", `{"name":"x","domain":"x.example.com","files":true}`, http.StatusBadRequest},
		"refresh of no clone": {"POST", "/api/v1/sites/shop/clone/refresh", "", http.StatusNotFound},
		"refresh body":        {"POST", "/api/v1/sites/shop/clone/refresh", `{"maintenance":true}`, http.StatusNotFound},
		"refresh bad body":    {"POST", "/api/v1/sites/shop-stage/clone/refresh", `{"files":true}`, http.StatusBadRequest},
	} {
		if rec := e.do(t, reqOpts{method: tc.method, path: tc.path, cookie: c, body: tc.body}); rec.Code != tc.code {
			t.Errorf("%s: %d %s", name, rec.Code, rec.Body)
		}
	}

	// A token needs databases:write as well.
	tok := e.createToken(t, c, `{"name":"clone","scopes":["sites:read","sites:write"]}`)
	if rec := e.do(t, reqOpts{method: "POST", path: "/api/v1/sites/shop/clone", header: bearerHeader(tok.Token), body: body}); rec.Code != http.StatusForbidden ||
		!strings.Contains(rec.Body.String(), "databases:write") {
		t.Fatalf("scope: %d %s", rec.Code, rec.Body)
	}
}
