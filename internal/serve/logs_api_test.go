// SPDX-License-Identifier: AGPL-3.0-or-later

package serve

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/maki-it/hostashi/internal/agentapi"
	"github.com/maki-it/hostashi/internal/agentrpc"
)

type stubLogs struct {
	mu   sync.Mutex
	reqs []agentapi.SiteLogsReadRequest
	days int
	fail error
}

func (s *stubLogs) Read(_ context.Context, req agentapi.SiteLogsReadRequest) (agentapi.SiteLogsReadResponse, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.reqs = append(s.reqs, req)

	if req.Site == "gone" {
		return agentapi.SiteLogsReadResponse{}, &agentrpc.Error{Code: agentrpc.CodeNotFound, Message: "unknown site gone"}
	}

	return agentapi.SiteLogsReadResponse{Lines: []string{"GET / 200", "<script>"}, Cursor: agentapi.LogCursor{Inode: 42, Offset: 99}, Rotated: req.After != nil}, nil
}

func (s *stubLogs) SetRetention(_ context.Context, days int) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.fail != nil {
		return s.fail
	}

	s.days = days

	return nil
}

func (s *stubLogs) last() agentapi.SiteLogsReadRequest {
	s.mu.Lock()
	defer s.mu.Unlock()

	return s.reqs[len(s.reqs)-1]
}

func TestSiteLogsOverHTTP(t *testing.T) {
	e, _ := newFullAPI(t)
	c := e.adminCookie(t)

	for _, s := range []string{"alpha", "beta"} {
		if rec := e.do(t, reqOpts{method: "POST", path: "/api/v1/sites", cookie: c, body: `{"name":"` + s + `","php":"8.4","domains":["` + s + `.example"]}`}); rec.Code >= 300 {
			t.Fatalf("create %s: %d %s", s, rec.Code, rec.Body)
		}
	}

	rec := e.do(t, reqOpts{method: "GET", path: "/api/v1/sites/alpha/logs/access", cookie: c})
	if rec.Code != http.StatusOK || rec.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("read: %d %s", rec.Code, rec.Body)
	}

	var out siteLogsJSON
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil || out.Cursor != "42.99" || len(out.Lines) != 2 || out.Lines[1] != "<script>" {
		t.Fatalf("body %s: %v", rec.Body, err)
	}

	if got := e.logs.last(); got.Site != "alpha" || got.Kind != agentapi.LogAccess || got.Lines != 200 || got.After != nil {
		t.Fatalf("default request %+v", got)
	}

	if rec := e.do(t, reqOpts{method: "GET", path: "/api/v1/sites/alpha/logs/php?lines=50", cookie: c}); rec.Code != http.StatusOK || e.logs.last().Lines != 50 || e.logs.last().Kind != agentapi.LogPHP {
		t.Fatalf("lines: %d %+v", rec.Code, e.logs.last())
	}

	// The WordPress install log (ADR-0069) is read like the others.
	if rec := e.do(t, reqOpts{method: "GET", path: "/api/v1/sites/alpha/logs/wordpress", cookie: c}); rec.Code != http.StatusOK || e.logs.last().Kind != agentapi.LogWordPress {
		t.Fatalf("wordpress: %d %+v", rec.Code, e.logs.last())
	}

	rec = e.do(t, reqOpts{method: "GET", path: "/api/v1/sites/alpha/logs/error?cursor=42.99", cookie: c})
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"rotated":true`) {
		t.Fatalf("cursor: %d %s", rec.Code, rec.Body)
	}

	if got := e.logs.last(); got.After == nil || *got.After != (agentapi.LogCursor{Inode: 42, Offset: 99}) || got.Lines != 0 {
		t.Fatalf("cursor request %+v", got)
	}

	for path, code := range map[string]int{
		"/api/v1/sites/alpha/logs/shadow":                    http.StatusNotFound,
		"/api/v1/sites/alpha/logs/access?lines=0":            http.StatusUnprocessableEntity,
		"/api/v1/sites/alpha/logs/access?lines=1001":         http.StatusUnprocessableEntity,
		"/api/v1/sites/alpha/logs/access?lines=x":            http.StatusUnprocessableEntity,
		"/api/v1/sites/alpha/logs/access?cursor=1":           http.StatusUnprocessableEntity,
		"/api/v1/sites/alpha/logs/access?cursor=1.-5":        http.StatusUnprocessableEntity,
		"/api/v1/sites/alpha/logs/access?cursor=1.5&lines=3": http.StatusUnprocessableEntity,
		"/api/v1/sites/gone/logs/access":                     http.StatusNotFound,
	} {
		if rec := e.do(t, reqOpts{method: "GET", path: path, cookie: c}); rec.Code != code {
			t.Errorf("%s: %d %s, want %d", path, rec.Code, rec.Body, code)
		}
	}

	// A user reads the logs of granted sites only.
	gina, gid := matrixLogin(t, e, c, "gina", "user")
	if rec := e.do(t, reqOpts{method: "PUT", path: userPath(gid, "/grants"), cookie: c, body: `{"sites":["alpha"]}`}); rec.Code != http.StatusOK {
		t.Fatalf("grant: %d %s", rec.Code, rec.Body)
	}

	if rec := e.do(t, reqOpts{method: "GET", path: "/api/v1/sites/alpha/logs/access", cookie: gina}); rec.Code != http.StatusOK {
		t.Fatalf("own site: %d %s", rec.Code, rec.Body)
	}

	n := len(e.logs.reqs)
	if rec := e.do(t, reqOpts{method: "GET", path: "/api/v1/sites/beta/logs/access", cookie: gina}); rec.Code != http.StatusNotFound {
		t.Fatalf("other site: %d %s", rec.Code, rec.Body)
	}

	if len(e.logs.reqs) != n {
		t.Fatal("the agent was asked for a site without grant")
	}

	// Tokens need logs:read.
	tok := e.createToken(t, c, `{"name":"logs","scopes":["logs:read"],"expiresInDays":1}`)
	other := e.createToken(t, c, `{"name":"sites","scopes":["sites:read"],"expiresInDays":1}`)

	if rec := e.do(t, reqOpts{method: "GET", path: "/api/v1/sites/beta/logs/access", header: bearerHeader(tok.Token)}); rec.Code != http.StatusOK {
		t.Fatalf("token: %d %s", rec.Code, rec.Body)
	}

	if rec := e.do(t, reqOpts{method: "GET", path: "/api/v1/sites/beta/logs/access", header: bearerHeader(other.Token)}); rec.Code != http.StatusForbidden {
		t.Fatalf("token without logs:read: %d %s", rec.Code, rec.Body)
	}
}

func TestLogRetentionSetting(t *testing.T) {
	e, _ := newFullAPI(t)
	c := e.adminCookie(t)

	rec := e.do(t, reqOpts{method: "GET", path: "/api/v1/settings/logs", cookie: c})
	if rec.Code != http.StatusOK || rec.Body.String() != `{"days":7,"minDays":1,"maxDays":365,"defaultDays":7}`+"\n" {
		t.Fatalf("default: %d %s", rec.Code, rec.Body)
	}

	for _, bad := range []string{`{}`, `{"days":0}`, `{"days":366}`, `{"days":"7"}`} {
		if rec := e.do(t, reqOpts{method: "PUT", path: "/api/v1/settings/logs", cookie: c, body: bad}); rec.Code != http.StatusUnprocessableEntity && rec.Code != http.StatusBadRequest {
			t.Errorf("%s: %d %s", bad, rec.Code, rec.Body)
		}
	}

	// A failing agent stores nothing.
	e.logs.fail = errors.New("agent down")
	if rec := e.do(t, reqOpts{method: "PUT", path: "/api/v1/settings/logs", cookie: c, body: `{"days":30}`}); rec.Code < 500 {
		t.Fatalf("agent down: %d %s", rec.Code, rec.Body)
	}

	if rec := e.do(t, reqOpts{method: "GET", path: "/api/v1/settings/logs", cookie: c}); !strings.Contains(rec.Body.String(), `"days":7,`) {
		t.Fatalf("stored despite the agent: %s", rec.Body)
	}

	e.logs.fail = nil
	if rec := e.do(t, reqOpts{method: "PUT", path: "/api/v1/settings/logs", cookie: c, body: `{"days":30}`}); rec.Code != http.StatusOK || e.logs.days != 30 {
		t.Fatalf("set: %d %s %d", rec.Code, rec.Body, e.logs.days)
	}

	if rec := e.do(t, reqOpts{method: "GET", path: "/api/v1/settings/logs", cookie: c}); !strings.Contains(rec.Body.String(), `"days":30,`) {
		t.Fatalf("after set: %s", rec.Body)
	}

	// Managers and tokens cannot change it.
	mara, _ := matrixLogin(t, e, c, "mara", "manager")
	if rec := e.do(t, reqOpts{method: "PUT", path: "/api/v1/settings/logs", cookie: mara, body: `{"days":1}`}); rec.Code != http.StatusForbidden {
		t.Fatalf("manager: %d %s", rec.Code, rec.Body)
	}

	// The panel sends the stored value again after a start.
	e.logs.days = -1
	SyncLogRetention(t.Context(), e.logs, e.settings, 3, time.Millisecond, slog.New(slog.DiscardHandler))

	if e.logs.days != 30 {
		t.Fatalf("sync sent %d", e.logs.days)
	}
}
