// SPDX-License-Identifier: AGPL-3.0-or-later

package serve

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"sync"
	"testing"

	"github.com/maki-it/hostashi/internal/agentapi"
)

type stubApps struct {
	mu    sync.Mutex
	specs map[string]agentapi.AppSpec
	venvs []agentapi.PythonVenvRequest
}

func (s *stubApps) PythonStatus(context.Context) (agentapi.PythonStatus, error) {
	return agentapi.PythonStatus{Installed: []agentapi.PythonInstalled{{Line: "3.13"}, {Line: "3.14"}}}, nil
}

func (s *stubApps) PythonVenv(_ context.Context, req agentapi.PythonVenvRequest) (agentapi.AppStatus, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.venvs = append(s.venvs, req)

	return agentapi.AppStatus{Site: req.Site, State: "active"}, nil
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

func TestAppSiteOverHTTP(t *testing.T) {
	ag := &stubAgent{}
	e := newAPIWithAgent(t, ag)
	c := e.adminCookie(t)

	apps := &stubApps{specs: map[string]agentapi.AppSpec{}}
	e.sites.EnableApps(apps)

	for body, want := range map[string]int{
		`{"name":"svc","domains":["svc.example.com"],"type":"app"}`:                                            http.StatusUnprocessableEntity,
		`{"name":"svc","domains":["svc.example.com"],"app":{}}`:                                                http.StatusUnprocessableEntity,
		`{"name":"svc","domains":["svc.example.com"],"type":"app","app":{},"node":{"version":"24"}}`:           http.StatusUnprocessableEntity,
		`{"name":"svc","domains":["svc.example.com"],"type":"node","node":{}}`:                                 http.StatusUnprocessableEntity,
		`{"name":"svc","domains":["svc.example.com"],"type":"app","app":{"program":"/bin/sh"}}`:                http.StatusUnprocessableEntity,
		`{"name":"svc","domains":["svc.example.com"],"type":"app","app":{"program":"../x"}}`:                   http.StatusUnprocessableEntity,
		`{"name":"svc","domains":["svc.example.com"],"type":"app","app":{"program":"bin/x","args":["a\nb"]}}`:  http.StatusUnprocessableEntity,
		`{"name":"svc","domains":["svc.example.com"],"type":"app","app":{"args":["--port"]}}`:                  http.StatusUnprocessableEntity,
		`{"name":"svc","domains":["svc.example.com"],"type":"app","php":"8.4","app":{"program":"bin/x"}}`:      http.StatusUnprocessableEntity,
		`{"name":"svc","domains":["svc.example.com"],"type":"app","app":{"program":"bin/x","workdir":"/etc"}}`: http.StatusUnprocessableEntity,
	} {
		if rec := e.do(t, reqOpts{method: "POST", path: "/api/v1/sites", cookie: c, body: body}); rec.Code != want {
			t.Errorf("%s: %d %s", body, rec.Code, rec.Body)
		}
	}

	rec := e.do(t, reqOpts{method: "POST", path: "/api/v1/sites", cookie: c, body: `{"name":"svc","domains":["svc.example.com"],"type":"app","app":{"program":"bin/server","args":["--log","$HOME 100%"]}}`})
	if rec.Code != http.StatusCreated {
		t.Fatalf("create: %d %s", rec.Code, rec.Body)
	}

	var site siteJSON
	if err := json.Unmarshal(rec.Body.Bytes(), &site); err != nil || site.Type != "app" || !site.Account || site.App == nil || site.Node != nil ||
		site.App.Program != "bin/server" || len(site.App.Args) != 2 || site.App.Workdir != "htdocs" || site.App.Port != agentapi.MinAppPort {
		t.Fatalf("site %+v %+v %v", site, site.App, err)
	}

	spec := apps.specs["svc"]
	if !spec.Binary() || spec.StartKind != agentapi.AppStartBinary || spec.Start != "bin/server" || len(spec.Args) != 2 || spec.Args[1] != "$HOME 100%" || spec.Stopped {
		t.Fatalf("agent got %+v", spec)
	}

	rec = e.do(t, reqOpts{method: "PUT", path: "/api/v1/sites/svc/app", cookie: c, body: `{"workdir":"srv","program":"srv/app","args":[]}`})

	var out siteAppJSON
	if err := json.Unmarshal(rec.Body.Bytes(), &out); rec.Code != http.StatusOK || err != nil || out.Site.App.Program != "srv/app" || len(out.Site.App.Args) != 0 {
		t.Fatalf("set: %d %s", rec.Code, rec.Body)
	}

	// No program: the app stays stopped.
	if rec := e.do(t, reqOpts{method: "PUT", path: "/api/v1/sites/svc/app", cookie: c, body: `{"program":""}`}); rec.Code != http.StatusOK || !apps.specs["svc"].Stopped {
		t.Errorf("unset: %d %s %+v", rec.Code, rec.Body, apps.specs["svc"])
	}

	// The routes of the other type refuse.
	if rec := e.do(t, reqOpts{method: "PUT", path: "/api/v1/sites/svc/node", cookie: c, body: `{"version":"24"}`}); rec.Code != http.StatusConflict {
		t.Errorf("node route on an app site: %d %s", rec.Code, rec.Body)
	}

	if rec := e.do(t, reqOpts{method: "POST", path: "/api/v1/sites", cookie: c, body: `{"name":"shop","domains":["shop.example.com"],"type":"node","node":{"version":"24"}}`}); rec.Code != http.StatusCreated {
		t.Fatalf("node site: %d %s", rec.Code, rec.Body)
	}

	if rec := e.do(t, reqOpts{method: "PUT", path: "/api/v1/sites/shop/app", cookie: c, body: `{"program":"bin/x"}`}); rec.Code != http.StatusConflict {
		t.Errorf("app route on a Node.js site: %d %s", rec.Code, rec.Body)
	}

	if rec := e.do(t, reqOpts{method: "GET", path: "/api/v1/sites/svc/app", cookie: c}); rec.Code != http.StatusOK {
		t.Errorf("status: %d %s", rec.Code, rec.Body)
	}
}

func TestAppEnvOverHTTP(t *testing.T) {
	e := newAPIWithAgent(t, &stubAgent{})
	c := e.adminCookie(t)

	apps := &stubApps{specs: map[string]agentapi.AppSpec{}}
	e.sites.EnableApps(apps)

	if rec := e.do(t, reqOpts{method: "POST", path: "/api/v1/sites", cookie: c, body: `{"name":"svc","domains":["svc.example.com"],"type":"app","app":{"program":"bin/server"}}`}); rec.Code != http.StatusCreated {
		t.Fatalf("create: %d %s", rec.Code, rec.Body)
	}

	for body, want := range map[string]int{
		`{"variables":[{"name":"PORT","value":"80"}]}`:                                                        http.StatusUnprocessableEntity,
		`{"variables":[{"name":"a-b","value":"x"}]}`:                                                          http.StatusUnprocessableEntity,
		`{"variables":[{"name":"A","value":"x\ny"}]}`:                                                         http.StatusUnprocessableEntity,
		`{"variables":[{"name":"A","value":"1"},{"name":"A","value":"2"}]}`:                                   http.StatusUnprocessableEntity,
		`{"variables":[{"name":"DB_URL","value":"mysql://u:p@x/db"},{"name":"API_KEY","value":"s3cr3t $X"}]}`: http.StatusOK,
	} {
		if rec := e.do(t, reqOpts{method: "PUT", path: "/api/v1/sites/svc/app/env", cookie: c, body: body}); rec.Code != want {
			t.Errorf("%s: %d %s", body, rec.Code, rec.Body)
		}
	}

	if env := apps.specs["svc"].Env; len(env) != 2 || env[0].Name != "API_KEY" || env[0].Value != "s3cr3t $X" {
		t.Fatalf("agent got %v", env)
	}

	rec := e.do(t, reqOpts{method: "GET", path: "/api/v1/sites/svc/app/env", cookie: c})
	if rec.Code != http.StatusOK || rec.Body.String() != `{"names":["API_KEY","DB_URL"]}`+"\n" {
		t.Fatalf("names: %d %q", rec.Code, rec.Body)
	}

	rec = e.do(t, reqOpts{method: "GET", path: "/api/v1/sites/svc/app/env/values", cookie: c})
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `{"name":"API_KEY","value":"s3cr3t $X"}`) || rec.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("values: %d %s", rec.Code, rec.Body)
	}

	// Reading the site shows the names, not the values.
	read := bearerHeader(e.createToken(t, c, `{"name":"r","scopes":["sites:read"]}`).Token)
	if rec := e.do(t, reqOpts{method: "GET", path: "/api/v1/sites/svc/app/env", header: read}); rec.Code != http.StatusOK {
		t.Errorf("names with sites:read: %d %s", rec.Code, rec.Body)
	}

	if rec := e.do(t, reqOpts{method: "GET", path: "/api/v1/sites/svc/app/env/values", header: read}); rec.Code != http.StatusForbidden || strings.Contains(rec.Body.String(), "s3cr3t") {
		t.Errorf("values with sites:read: %d %s", rec.Code, rec.Body)
	}

	// The audit log names the variables only.
	if rec := e.do(t, reqOpts{method: "GET", path: "/api/v1/audit?action=site.app.env", cookie: c}); rec.Code != http.StatusOK ||
		!strings.Contains(rec.Body.String(), "API_KEY") || strings.Contains(rec.Body.String(), "s3cr3t") || strings.Contains(rec.Body.String(), "mysql://") {
		t.Errorf("audit: %d %s", rec.Code, rec.Body)
	}

	if rec := e.do(t, reqOpts{method: "PUT", path: "/api/v1/sites/svc/app/env", cookie: c, body: `{"variables":[]}`}); rec.Code != http.StatusOK || len(apps.specs["svc"].Env) != 0 {
		t.Fatalf("clear: %d %s", rec.Code, rec.Body)
	}
}
