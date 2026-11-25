// SPDX-License-Identifier: AGPL-3.0-or-later

package serve

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/maki-it/hostashi/internal/agentapi"
	"github.com/maki-it/hostashi/internal/agentrpc"
)

type stubServerAgent struct {
	mu        sync.Mutex
	acted     []agentapi.ServiceActionRequest
	siteCalls int
	err       error
}

func (a *stubServerAgent) HostStats(context.Context) (agentapi.HostStats, error) {
	return agentapi.HostStats{CPUs: 1}, nil
}

func (a *stubServerAgent) Services(context.Context, agentapi.ServicesStatusRequest) ([]agentapi.ServiceState, error) {
	return []agentapi.ServiceState{
		{ID: "nginx", Unit: "nginx.service", Installed: true, Active: "active", Sub: "running"},
		{ID: "mariadb", Unit: "mariadb.service", Installed: true, Active: "active", Sub: "running"},
		{ID: "php", Site: "blog", Unit: "hostashi-php-8.4@blog.service", Installed: true, Active: "active"},
	}, nil
}

func (a *stubServerAgent) Action(_ context.Context, r agentapi.ServiceActionRequest) (agentapi.ServiceState, error) {
	a.mu.Lock()
	defer a.mu.Unlock()

	if a.err != nil {
		return agentapi.ServiceState{}, a.err
	}

	a.acted = append(a.acted, r)

	return agentapi.ServiceState{ID: r.ID, Site: r.Site, Installed: true, Active: "active"}, nil
}

func (a *stubServerAgent) count() int {
	a.mu.Lock()
	defer a.mu.Unlock()

	return len(a.acted)
}

func TestServicesAPI(t *testing.T) {
	e := newAPI(t)
	c := e.adminCookie(t)

	if rec := e.do(t, reqOpts{method: "GET", path: "/api/v1/services"}); rec.Code != http.StatusUnauthorized {
		t.Fatalf("anonymous: %d", rec.Code)
	}

	rec := e.do(t, reqOpts{method: "GET", path: "/api/v1/services", cookie: c})

	var list struct {
		Services []struct {
			ID      string   `json:"id"`
			Site    string   `json:"site"`
			Actions []string `json:"actions"`
			Confirm []string `json:"confirm"`
		} `json:"services"`
	}
	if rec.Code != http.StatusOK || json.Unmarshal(rec.Body.Bytes(), &list) != nil || len(list.Services) != 3 {
		t.Fatalf("list: %d %s", rec.Code, rec.Body)
	}

	if s := list.Services[1]; s.ID != "mariadb" || len(s.Confirm) != 1 || s.Confirm[0] != "stop" {
		t.Fatalf("mariadb row %+v", s)
	}

	rec = e.do(t, reqOpts{method: "GET", path: "/api/v1/sites/blog/services", cookie: c})
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"site":"blog"`) || strings.Contains(rec.Body.String(), `"nginx"`) {
		t.Fatalf("site list: %d %s", rec.Code, rec.Body)
	}

	// Refused before the agent sees it.
	for _, tc := range []struct {
		path, body string
		code       int
	}{
		{"/api/v1/services/nginx/stop", "", http.StatusBadRequest},
		{"/api/v1/services/ssh/restart", "", http.StatusBadRequest},
		{"/api/v1/services/bogus/start", "", http.StatusBadRequest},
		{"/api/v1/services/php/reload", "", http.StatusBadRequest},
		{"/api/v1/services/mariadb/stop", "", http.StatusBadRequest},
		{"/api/v1/services/mariadb/stop", `{"confirm":"stop nginx"}`, http.StatusBadRequest},
		{"/api/v1/sites/Bad/services/php/reload", "", http.StatusNotFound},
		{"/api/v1/sites/blog/services/nginx/reload", "", http.StatusBadRequest},
	} {
		if rec := e.do(t, reqOpts{method: "POST", path: tc.path, body: tc.body, cookie: c}); rec.Code != tc.code {
			t.Errorf("%s %s: %d %s", tc.path, tc.body, rec.Code, rec.Body)
		}
	}

	if n := e.srvAgent.count(); n != 0 {
		t.Fatalf("agent called %d times", n)
	}

	for _, tc := range []struct{ path, body string }{
		{"/api/v1/services/nginx/reload", ""},
		{"/api/v1/services/mariadb/stop", `{"confirm":"stop mariadb"}`},
		{"/api/v1/sites/blog/services/php/restart", ""},
	} {
		if rec := e.do(t, reqOpts{method: "POST", path: tc.path, body: tc.body, cookie: c}); rec.Code != http.StatusOK {
			t.Fatalf("%s: %d %s", tc.path, rec.Code, rec.Body)
		}
	}

	if n := e.srvAgent.count(); n != 3 {
		t.Fatalf("agent called %d times", n)
	}

	// The actions are events with the admin's name, and in the audit log.
	rec = e.do(t, reqOpts{method: "GET", path: "/api/v1/services/events", cookie: c})

	var ev struct {
		Events []serviceEventJSON `json:"events"`
	}
	if rec.Code != http.StatusOK || json.Unmarshal(rec.Body.Bytes(), &ev) != nil || len(ev.Events) != 3 {
		t.Fatalf("events: %d %s", rec.Code, rec.Body)
	}

	if g := ev.Events[0]; g.Service != "php" || g.Site != "blog" || g.Kind != "action" || g.Detail != "restart" || g.User != "kim" {
		t.Fatalf("event %+v", g)
	}

	rec = e.do(t, reqOpts{method: "GET", path: "/api/v1/services/events?service=mariadb&limit=5", cookie: c})
	if rec.Code != http.StatusOK || json.Unmarshal(rec.Body.Bytes(), &ev) != nil || len(ev.Events) != 1 {
		t.Fatalf("filtered: %d %s", rec.Code, rec.Body)
	}

	for _, q := range []string{"?site=blog", "?service=php", "?service=bogus", "?limit=0", "?limit=x"} {
		if rec := e.do(t, reqOpts{method: "GET", path: "/api/v1/services/events" + q, cookie: c}); rec.Code != http.StatusBadRequest {
			t.Errorf("events%s: %d", q, rec.Code)
		}
	}

	var n int
	if err := e.st.RawCount(t.Context(), `SELECT COUNT(*) FROM audit_log WHERE action = ?`, "service.action", &n); err != nil || n != 3 {
		t.Fatalf("audit rows %d %v", n, err)
	}

	// Agent errors map to HTTP codes; a failure is audited too.
	for _, tc := range []struct {
		err  error
		code int
	}{
		{&agentrpc.Error{Code: agentrpc.CodePrecondition, Message: "nginx -t rejected"}, http.StatusConflict},
		{&agentrpc.Error{Code: agentrpc.CodeNotFound, Message: "the site has no PHP"}, http.StatusNotFound},
		{&agentrpc.Error{Code: agentrpc.CodeInternal, Message: "internal error"}, http.StatusServiceUnavailable},
		{&agentrpc.Error{Code: agentrpc.CodeInternal, Message: "systemd did not complete the action, see the agent's log"}, http.StatusBadGateway},
	} {
		e.srvAgent.err = tc.err
		if rec := e.do(t, reqOpts{method: "POST", path: "/api/v1/services/nginx/reload", cookie: c}); rec.Code != tc.code {
			t.Errorf("%v: %d %s", tc.err, rec.Code, rec.Body)
		}
	}

	e.srvAgent.err = nil

	if err := e.st.RawCount(t.Context(), `SELECT COUNT(*) FROM audit_log WHERE action = ?`, "service.action", &n); err != nil || n != 7 {
		t.Fatalf("audit rows after errors %d %v", n, err)
	}
}

func TestDashboardAPI(t *testing.T) {
	e := newAPI(t)
	c := e.adminCookie(t)

	now := time.Now()
	e.monitor.Now = func() time.Time { return now }
	e.monitor.Sample(t.Context())

	now = now.Add(15 * time.Second)

	e.monitor.Sample(t.Context())

	rec := e.do(t, reqOpts{method: "GET", path: "/api/v1/dashboard", cookie: c})

	var out struct {
		Range  string            `json:"range"`
		Step   int               `json:"step"`
		Points []json.RawMessage `json:"points"`
	}
	if rec.Code != http.StatusOK || json.Unmarshal(rec.Body.Bytes(), &out) != nil || out.Range != "1h" || out.Step != 15 || len(out.Points) != 1 {
		t.Fatalf("1h: %d %s", rec.Code, rec.Body)
	}

	for _, r := range []string{"24h", "7d", "30d"} {
		rec := e.do(t, reqOpts{method: "GET", path: "/api/v1/dashboard?range=" + r, cookie: c})
		if rec.Code != http.StatusOK || json.Unmarshal(rec.Body.Bytes(), &out) != nil || out.Points == nil {
			t.Fatalf("%s: %d %s", r, rec.Code, rec.Body)
		}
	}

	if rec := e.do(t, reqOpts{method: "GET", path: "/api/v1/dashboard?range=1y", cookie: c}); rec.Code != http.StatusBadRequest {
		t.Fatalf("bad range: %d", rec.Code)
	}

	// A token with the scope reads it; one without does not.
	tok := e.createToken(t, c, `{"name":"mon","scopes":["services:read"]}`)
	if rec := e.do(t, reqOpts{method: "GET", path: "/api/v1/dashboard", header: bearerHeader(tok.Token)}); rec.Code != http.StatusOK {
		t.Fatalf("token: %d", rec.Code)
	}

	if rec := e.do(t, reqOpts{method: "POST", path: "/api/v1/services/nginx/reload", header: bearerHeader(tok.Token)}); rec.Code != http.StatusForbidden {
		t.Fatalf("read token acted: %d", rec.Code)
	}
}
