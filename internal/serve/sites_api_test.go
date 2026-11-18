// SPDX-License-Identifier: AGPL-3.0-or-later

package serve

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/maki-it/hostashi/internal/agentapi"
	"github.com/maki-it/hostashi/internal/agentrpc"
)

// stubAgent accepts every change and remembers the last web state.
type stubAgent struct {
	mu      sync.Mutex
	web     agentapi.WebApplyRequest
	webErr  error
	deleted []string
}

func (a *stubAgent) SiteCreate(_ context.Context, req agentapi.SiteCreateRequest) (agentapi.SiteInfo, error) {
	return agentapi.SiteInfo{User: req.User, UID: 1001, GID: 1001, Pool: req.Pool}, nil
}

func (a *stubAgent) SiteDelete(_ context.Context, req agentapi.SiteDeleteRequest) error {
	a.mu.Lock()
	defer a.mu.Unlock()

	a.deleted = append(a.deleted, req.User)

	return nil
}

func (a *stubAgent) WebApply(_ context.Context, req agentapi.WebApplyRequest) (agentapi.WebApplyResponse, error) {
	a.mu.Lock()
	defer a.mu.Unlock()

	if a.webErr != nil {
		return agentapi.WebApplyResponse{}, a.webErr
	}

	a.web = req

	return agentapi.WebApplyResponse{Generation: 2, Changed: true}, nil
}

func (a *stubAgent) PHPVersions(context.Context) ([]string, error) { return []string{"8.4"}, nil }

func (a *stubAgent) TLSCSR(_ context.Context, user string, _ []string) (string, error) {
	return "csr " + user, nil
}

func (a *stubAgent) TLSInstall(_ context.Context, _, _ string) (agentapi.TLSCertInfo, error) {
	return agentapi.TLSCertInfo{Domains: []string{"blog.example.org"}, NotAfter: time.Now().Add(90 * 24 * time.Hour)}, nil
}

func (a *stubAgent) DBCreate(context.Context, string, string) error { return nil }

func (a *stubAgent) DBDelete(context.Context, string) error { return nil }

func (a *stubAgent) DBUserCreate(context.Context, string, string, string) error { return nil }

func (a *stubAgent) DBUserDelete(context.Context, string) error { return nil }

func (a *stubAgent) DBUserPassword(context.Context, string, string) error { return nil }

type stubIssuer struct{}

func (stubIssuer) Obtain(context.Context, []byte) ([]byte, error) { return []byte("chain"), nil }

func TestSiteLifecycleOverHTTP(t *testing.T) {
	ag := &stubAgent{}
	e := newAPIWithAgent(t, ag)
	c := e.adminCookie(t)

	rec := e.do(t, reqOpts{method: "POST", path: "/api/v1/sites", cookie: c, body: `{"name":"blog","domains":["blog.example.com","www.blog.example.com"]}`})
	if rec.Code != http.StatusCreated {
		t.Fatalf("create: %d %s", rec.Code, rec.Body)
	}

	var site siteJSON
	if err := json.Unmarshal(rec.Body.Bytes(), &site); err != nil || site.State != "active" || site.Pool != "default" || len(site.Domains) != 2 {
		t.Fatalf("site %+v %v", site, err)
	}

	if len(ag.web.Sites) != 1 || ag.web.Sites[0].User != "blog" {
		t.Fatalf("web state %+v", ag.web)
	}

	rec = e.do(t, reqOpts{method: "POST", path: "/api/v1/sites", cookie: c, body: `{"name":"blog","domains":["x.example.com"]}`})
	if rec.Code != http.StatusConflict || errCode(t, rec) != "site_exists" {
		t.Fatalf("duplicate: %d %s", rec.Code, rec.Body)
	}

	rec = e.do(t, reqOpts{method: "POST", path: "/api/v1/sites", cookie: c, body: `{"name":"shop","domains":["www.blog.example.com"]}`})
	if rec.Code != http.StatusConflict || errCode(t, rec) != "domain_taken" {
		t.Fatalf("taken: %d %s", rec.Code, rec.Body)
	}

	rec = e.do(t, reqOpts{method: "PUT", path: "/api/v1/sites/blog/domains", cookie: c, body: `{"domains":["blog.example.org"]}`})
	if rec.Code != http.StatusOK || ag.web.Sites[0].Domains[0] != "blog.example.org" {
		t.Fatalf("set domains: %d %s", rec.Code, rec.Body)
	}

	rec = e.do(t, reqOpts{method: "PUT", path: "/api/v1/sites/blog/php", cookie: c, body: `{"php":"8.4"}`})
	if rec.Code != http.StatusOK || ag.web.Sites[0].PHP != "8.4" {
		t.Fatalf("set php: %d %s", rec.Code, rec.Body)
	}

	if err := json.Unmarshal(rec.Body.Bytes(), &site); err != nil || site.PHP != "8.4" {
		t.Fatalf("site %+v %v", site, err)
	}

	for body, code := range map[string]int{`{"php":"8.0"}`: http.StatusUnprocessableEntity, `{}`: http.StatusUnprocessableEntity, `{"php":8.4}`: http.StatusBadRequest} {
		if rec := e.do(t, reqOpts{method: "PUT", path: "/api/v1/sites/blog/php", cookie: c, body: body}); rec.Code != code {
			t.Errorf("set php %s: %d %s", body, rec.Code, rec.Body)
		}
	}

	rec = e.do(t, reqOpts{method: "GET", path: "/api/v1/php/versions", cookie: c})
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"installed":["8.4"]`) || !strings.Contains(rec.Body.String(), `"legacy":["7.4","8.1","8.2"]`) {
		t.Fatalf("versions: %d %s", rec.Code, rec.Body)
	}

	rec = e.do(t, reqOpts{method: "GET", path: "/api/v1/sites", cookie: c})

	var list struct {
		Sites []siteJSON `json:"sites"`
	}
	if rec.Code != http.StatusOK || json.Unmarshal(rec.Body.Bytes(), &list) != nil || len(list.Sites) != 1 {
		t.Fatalf("list: %d %s", rec.Code, rec.Body)
	}

	rec = e.do(t, reqOpts{method: "DELETE", path: "/api/v1/sites/blog", cookie: c})
	if rec.Code != http.StatusNoContent || len(ag.deleted) != 1 || len(ag.web.Sites) != 0 {
		t.Fatalf("delete: %d %s, deleted %v", rec.Code, rec.Body, ag.deleted)
	}

	rec = e.do(t, reqOpts{method: "GET", path: "/api/v1/sites/blog", cookie: c})
	if rec.Code != http.StatusNotFound {
		t.Fatalf("get deleted: %d", rec.Code)
	}
}

func TestSiteInputAndAgentErrorsOverHTTP(t *testing.T) {
	ag := &stubAgent{}
	e := newAPIWithAgent(t, ag)
	c := e.adminCookie(t)

	for _, body := range []string{
		`{"name":"../etc","domains":["a.example.com"]}`,
		`{"name":"blog","domains":[]}`,
		`{"name":"blog","domains":["a.example.com;"]}`,
		`{"name":"blog","php":"5.6","domains":["a.example.com"]}`,
	} {
		rec := e.do(t, reqOpts{method: "POST", path: "/api/v1/sites", cookie: c, body: body})
		if rec.Code != http.StatusUnprocessableEntity {
			t.Errorf("%s: %d %s", body, rec.Code, rec.Body)
		}
	}

	// A root path or uid is not a request field.
	rec := e.do(t, reqOpts{method: "POST", path: "/api/v1/sites", cookie: c, body: `{"name":"blog","domains":["a.example.com"],"root":"/etc"}`})
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("unknown field: %d", rec.Code)
	}

	ag.webErr = &agentrpc.Error{Code: agentrpc.CodePrecondition, Message: "nginx rejected the configuration, nothing was changed"}

	rec = e.do(t, reqOpts{method: "POST", path: "/api/v1/sites", cookie: c, body: `{"name":"blog","domains":["a.example.com"]}`})
	if rec.Code != http.StatusConflict || errCode(t, rec) != "agent_failed_precondition" {
		t.Fatalf("agent refused: %d %s", rec.Code, rec.Body)
	}

	ag.webErr = errors.New("dial unix /run/hostashi/agent.sock: connect: no such file or directory")

	rec = e.do(t, reqOpts{method: "POST", path: "/api/v1/web/sync", cookie: c})
	if rec.Code != http.StatusServiceUnavailable || errCode(t, rec) != "agent_unavailable" {
		t.Fatalf("agent down: %d %s", rec.Code, rec.Body)
	}

	if body := rec.Body.String(); strings.Contains(body, "/run/hostashi") || strings.Contains(body, "dial") {
		t.Fatalf("internal detail leaked: %s", body)
	}
}

func TestSiteRoutesNeedScopes(t *testing.T) {
	e := newAPIWithAgent(t, &stubAgent{})
	c := e.adminCookie(t)

	read := e.createToken(t, c, `{"name":"ro","scopes":["sites:read"]}`)
	write := e.createToken(t, c, `{"name":"rw","scopes":["sites:read","sites:write"]}`)

	rec := e.do(t, reqOpts{method: "GET", path: "/api/v1/sites"})
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("anonymous: %d", rec.Code)
	}

	rec = e.do(t, reqOpts{method: "GET", path: "/api/v1/sites", header: bearerHeader(read.Token)})
	if rec.Code != http.StatusOK {
		t.Fatalf("read token list: %d %s", rec.Code, rec.Body)
	}

	rec = e.do(t, reqOpts{method: "POST", path: "/api/v1/sites", header: bearerHeader(read.Token), body: `{"name":"blog","domains":["a.example.com"]}`})
	if rec.Code != http.StatusForbidden || errCode(t, rec) != "insufficient_scope" {
		t.Fatalf("read token create: %d %s", rec.Code, rec.Body)
	}

	rec = e.do(t, reqOpts{method: "POST", path: "/api/v1/sites", header: bearerHeader(write.Token), body: `{"name":"blog","domains":["a.example.com"]}`})
	if rec.Code != http.StatusCreated {
		t.Fatalf("write token create: %d %s", rec.Code, rec.Body)
	}

	rec = e.do(t, reqOpts{method: "PUT", path: "/api/v1/sites/blog/php", header: bearerHeader(read.Token), body: `{"php":"8.4"}`})
	if rec.Code != http.StatusForbidden || errCode(t, rec) != "insufficient_scope" {
		t.Fatalf("read token set php: %d %s", rec.Code, rec.Body)
	}

	rec = e.do(t, reqOpts{method: "GET", path: "/api/v1/php/versions", header: bearerHeader(read.Token)})
	if rec.Code != http.StatusOK {
		t.Fatalf("read token versions: %d %s", rec.Code, rec.Body)
	}

	// Deleting needs the high-risk scope by name.
	rec = e.do(t, reqOpts{method: "DELETE", path: "/api/v1/sites/blog", header: bearerHeader(write.Token)})
	if rec.Code != http.StatusForbidden || errCode(t, rec) != "insufficient_scope" {
		t.Fatalf("write token delete: %d %s", rec.Code, rec.Body)
	}
}

func TestSiteTLSOverHTTP(t *testing.T) {
	ag := &stubAgent{}
	e := newAPIWithAgent(t, ag)
	c := e.adminCookie(t)

	if rec := e.do(t, reqOpts{method: "POST", path: "/api/v1/sites", cookie: c, body: `{"name":"blog","domains":["blog.example.org"]}`}); rec.Code != http.StatusCreated {
		t.Fatalf("create: %d %s", rec.Code, rec.Body)
	}

	rec := e.do(t, reqOpts{method: "PUT", path: "/api/v1/sites/blog/tls", cookie: c, body: `{"enabled":true}`})
	if rec.Code != http.StatusConflict || errCode(t, rec) != "tls_unavailable" {
		t.Fatalf("without issuer: %d %s", rec.Code, rec.Body)
	}

	rec = e.do(t, reqOpts{method: "POST", path: "/api/v1/sites", cookie: c, body: `{"name":"shop","domains":["shop.example.org"],"tls":true}`})
	if rec.Code != http.StatusConflict || errCode(t, rec) != "tls_unavailable" {
		t.Fatalf("create with tls, no issuer: %d %s", rec.Code, rec.Body)
	}

	e.sites.EnableTLS(stubIssuer{})

	rec = e.do(t, reqOpts{method: "POST", path: "/api/v1/sites", cookie: c, body: `{"name":"shop","domains":["shop.example.org"],"tls":true,"hsts":true}`})
	if rec.Code != http.StatusCreated || !strings.Contains(rec.Body.String(), `"tls":{"enabled":true,"active":false,"hsts":true`) {
		t.Fatalf("create with tls: %d %s", rec.Code, rec.Body)
	}

	if rec := e.do(t, reqOpts{method: "DELETE", path: "/api/v1/sites/shop", cookie: c}); rec.Code != http.StatusNoContent {
		t.Fatalf("delete shop: %d %s", rec.Code, rec.Body)
	}

	rec = e.do(t, reqOpts{method: "PUT", path: "/api/v1/sites/blog/tls", cookie: c, body: `{"enabled":true}`})
	if rec.Code != http.StatusAccepted || !strings.Contains(rec.Body.String(), `"tls":{"enabled":true,"active":false,"hsts":false,"domains":[],"notAfter":null}`) {
		t.Fatalf("tls on: %d %s", rec.Code, rec.Body)
	}

	e.sites.RenewDue(t.Context())

	rec = e.do(t, reqOpts{method: "GET", path: "/api/v1/sites/blog", cookie: c})
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"enabled":true,"active":true,"hsts":false,"domains":["blog.example.org"]`) || !ag.web.Sites[0].TLS {
		t.Fatalf("after issue: %d %s", rec.Code, rec.Body)
	}

	for _, body := range []string{`{}`, `{"enabled":"yes"}`} {
		if rec := e.do(t, reqOpts{method: "PUT", path: "/api/v1/sites/blog/tls", cookie: c, body: body}); rec.Code < 400 {
			t.Errorf("%s: %d", body, rec.Code)
		}
	}

	rec = e.do(t, reqOpts{method: "PUT", path: "/api/v1/sites/blog/hsts", cookie: c, body: `{"enabled":true}`})
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"hsts":true`) || !ag.web.Sites[0].HSTS {
		t.Fatalf("hsts on: %d %s", rec.Code, rec.Body)
	}

	if rec := e.do(t, reqOpts{method: "PUT", path: "/api/v1/sites/blog/hsts", cookie: c, body: `{}`}); rec.Code != http.StatusUnprocessableEntity {
		t.Errorf("hsts without enabled: %d", rec.Code)
	}

	if rec := e.do(t, reqOpts{method: "PUT", path: "/api/v1/sites/nope/hsts", cookie: c, body: `{"enabled":true}`}); rec.Code != http.StatusNotFound {
		t.Errorf("hsts of a missing site: %d", rec.Code)
	}

	rec = e.do(t, reqOpts{method: "PUT", path: "/api/v1/sites/blog/tls", cookie: c, body: `{"enabled":false}`})
	if rec.Code != http.StatusOK || ag.web.Sites[0].TLS {
		t.Fatalf("tls off: %d %s", rec.Code, rec.Body)
	}
}
