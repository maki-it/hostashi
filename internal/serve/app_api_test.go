// SPDX-License-Identifier: AGPL-3.0-or-later

package serve

import (
	"context"
	"encoding/json"
	"net/http"
	"sync"
	"testing"

	"github.com/maki-it/hostashi/internal/agentapi"
)

type stubApps struct {
	mu    sync.Mutex
	specs map[string]agentapi.AppSpec
}

func (s *stubApps) NodeStatus(context.Context) (agentapi.NodeStatus, error) {
	return agentapi.NodeStatus{Installed: []agentapi.NodeInstalled{{Major: "24"}}}, nil
}

func (s *stubApps) AppApply(_ context.Context, a agentapi.AppSpec) (agentapi.AppStatus, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.specs[a.Site] = a

	if a.Stopped {
		return agentapi.AppStatus{Site: a.Site, State: "inactive"}, nil
	}

	return agentapi.AppStatus{Site: a.Site, State: "active", SubState: "running"}, nil
}

func (s *stubApps) AppRemove(context.Context, string) error { return nil }

func (s *stubApps) AppAction(_ context.Context, req agentapi.AppActionRequest) (agentapi.AppStatus, error) {
	return agentapi.AppStatus{Site: req.Site, State: "active", Restarts: 1}, nil
}

func (s *stubApps) AppStatus(_ context.Context, site string) (agentapi.AppStatus, error) {
	return agentapi.AppStatus{Site: site, State: "active", UsedBytes: 4096}, nil
}

func TestNodeSiteOverHTTP(t *testing.T) {
	ag := &stubAgent{}
	e := newAPIWithAgent(t, ag)
	c := e.adminCookie(t)

	apps := &stubApps{specs: map[string]agentapi.AppSpec{}}
	e.sites.EnableApps(apps)

	for body, want := range map[string]int{
		`{"name":"shop","domains":["shop.example.com"],"type":"node"}`:                                             http.StatusUnprocessableEntity,
		`{"name":"shop","domains":["shop.example.com"],"node":{"version":"24"}}`:                                   http.StatusUnprocessableEntity,
		`{"name":"shop","domains":["shop.example.com"],"type":"node","php":"8.4","node":{"version":"24"}}`:         http.StatusUnprocessableEntity,
		`{"name":"shop","domains":["shop.example.com"],"type":"node","node":{"version":"30"}}`:                     http.StatusUnprocessableEntity,
		`{"name":"shop","domains":["shop.example.com"],"type":"node","node":{"version":"22"}}`:                     http.StatusConflict,
		`{"name":"shop","domains":["shop.example.com"],"type":"node","node":{"version":"24","workdir":"/etc"}}`:    http.StatusUnprocessableEntity,
		`{"name":"shop","domains":["shop.example.com"],"type":"node","node":{"version":"24","startKind":"shell"}}`: http.StatusUnprocessableEntity,
	} {
		if rec := e.do(t, reqOpts{method: "POST", path: "/api/v1/sites", cookie: c, body: body}); rec.Code != want {
			t.Errorf("%s: %d %s", body, rec.Code, rec.Body)
		}
	}

	rec := e.do(t, reqOpts{method: "POST", path: "/api/v1/sites", cookie: c, body: `{"name":"shop","domains":["shop.example.com"],"type":"node","node":{"version":"24"}}`})
	if rec.Code != http.StatusCreated {
		t.Fatalf("create: %d %s", rec.Code, rec.Body)
	}

	var site siteJSON
	if err := json.Unmarshal(rec.Body.Bytes(), &site); err != nil || site.Type != "node" || !site.Account || site.Node == nil ||
		*site.Node != (nodeAppJSON{Version: "24", Workdir: "htdocs", Port: agentapi.MinAppPort}) {
		t.Fatalf("site %+v %+v %v", site, site.Node, err)
	}

	if rec := e.do(t, reqOpts{method: "POST", path: "/api/v1/sites/shop/app/start", cookie: c}); rec.Code != http.StatusUnprocessableEntity {
		t.Errorf("start without a command: %d %s", rec.Code, rec.Body)
	}

	rec = e.do(t, reqOpts{method: "PUT", path: "/api/v1/sites/shop/node", cookie: c, body: `{"version":"24","startKind":"npm","start":"start"}`})
	if rec.Code != http.StatusOK {
		t.Fatalf("set: %d %s", rec.Code, rec.Body)
	}

	var out siteAppJSON
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil || out.Status.State != "active" || out.Site.Node.Start != "start" || out.Site.Node.Port != agentapi.MinAppPort {
		t.Fatalf("set: %+v %v", out, err)
	}

	if rec := e.do(t, reqOpts{method: "PUT", path: "/api/v1/sites/shop/node", cookie: c, body: `{"version":"24","startKind":"npm","start":"start; id"}`}); rec.Code != http.StatusUnprocessableEntity {
		t.Errorf("shell text: %d %s", rec.Code, rec.Body)
	}

	if rec := e.do(t, reqOpts{method: "POST", path: "/api/v1/sites/shop/app/stop", cookie: c}); rec.Code != http.StatusOK || !apps.specs["shop"].Stopped {
		t.Errorf("stop: %d %s", rec.Code, rec.Body)
	}

	// Restart starts a stopped app as well.
	if rec := e.do(t, reqOpts{method: "POST", path: "/api/v1/sites/shop/app/restart", cookie: c}); rec.Code != http.StatusOK || apps.specs["shop"].Stopped {
		t.Errorf("restart a stopped app: %d %s", rec.Code, rec.Body)
	}

	if rec := e.do(t, reqOpts{method: "POST", path: "/api/v1/sites/shop/app/start", cookie: c}); rec.Code != http.StatusOK || apps.specs["shop"].Stopped {
		t.Errorf("start: %d %s", rec.Code, rec.Body)
	}

	rec = e.do(t, reqOpts{method: "GET", path: "/api/v1/sites/shop/app", cookie: c})

	var st appStatusJSON
	if err := json.Unmarshal(rec.Body.Bytes(), &st); rec.Code != http.StatusOK || err != nil || st.State != "active" || st.UsedBytes != 4096 {
		t.Errorf("status: %d %s", rec.Code, rec.Body)
	}

	// Not for other types.
	if rec := e.do(t, reqOpts{method: "POST", path: "/api/v1/sites", cookie: c, body: `{"name":"docs","domains":["docs.example.com"]}`}); rec.Code != http.StatusCreated {
		t.Fatalf("static: %d %s", rec.Code, rec.Body)
	}

	if rec := e.do(t, reqOpts{method: "GET", path: "/api/v1/sites/docs/app", cookie: c}); rec.Code != http.StatusConflict {
		t.Errorf("status of a static site: %d %s", rec.Code, rec.Body)
	}

	if rec := e.do(t, reqOpts{method: "PUT", path: "/api/v1/sites/shop/web", cookie: c, body: `{"dir":"public","spa":false,"assetCache":false}`}); rec.Code != http.StatusConflict {
		t.Errorf("web options of a Node.js site: %d %s", rec.Code, rec.Body)
	}
}
