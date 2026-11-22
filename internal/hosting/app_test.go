// SPDX-License-Identifier: AGPL-3.0-or-later

package hosting

import (
	"context"
	"errors"
	"testing"

	"github.com/maki-it/hostashi/internal/agentapi"
	"github.com/maki-it/hostashi/internal/agentrpc"
	"github.com/maki-it/hostashi/internal/store"
)

type fakeAppAgent struct {
	installed []string
	python    []string
	specs     map[string]agentapi.AppSpec
	actions   []string
	venvs     []agentapi.PythonVenvRequest
	applyErr  error
	venvErr   error
}

func (f *fakeAppAgent) PythonStatus(context.Context) (agentapi.PythonStatus, error) {
	st := agentapi.PythonStatus{}
	for _, l := range f.python {
		st.Installed = append(st.Installed, agentapi.PythonInstalled{Line: l})
	}

	return st, nil
}

func (f *fakeAppAgent) PythonVenv(_ context.Context, req agentapi.PythonVenvRequest) (agentapi.AppStatus, error) {
	if f.venvErr != nil {
		return agentapi.AppStatus{}, f.venvErr
	}

	f.venvs = append(f.venvs, req)

	return agentapi.AppStatus{Site: req.Site, State: "active"}, nil
}

func (f *fakeAppAgent) NodeStatus(context.Context) (agentapi.NodeStatus, error) {
	st := agentapi.NodeStatus{}
	for _, m := range f.installed {
		st.Installed = append(st.Installed, agentapi.NodeInstalled{Major: m})
	}

	return st, nil
}

func (f *fakeAppAgent) AppApply(_ context.Context, a agentapi.AppSpec) (agentapi.AppStatus, error) {
	if err := a.Validate(); err != nil {
		return agentapi.AppStatus{}, &agentrpc.Error{Code: agentrpc.CodeInvalidArgument, Message: err.Error()}
	}

	if f.applyErr != nil {
		return agentapi.AppStatus{}, f.applyErr
	}

	f.specs[a.Site] = a

	state := "active"
	if a.Stopped {
		state = "inactive"
	}

	return agentapi.AppStatus{Site: a.Site, State: state}, nil
}

func (f *fakeAppAgent) AppRemove(_ context.Context, site string) error {
	delete(f.specs, site)

	return nil
}

func (f *fakeAppAgent) AppAction(_ context.Context, req agentapi.AppActionRequest) (agentapi.AppStatus, error) {
	f.actions = append(f.actions, req.Site+" "+req.Action)

	return agentapi.AppStatus{Site: req.Site, State: "active"}, nil
}

func (f *fakeAppAgent) AppStatus(_ context.Context, site string) (agentapi.AppStatus, error) {
	return agentapi.AppStatus{Site: site, State: "active"}, nil
}

func webOf(ag *fakeAgent, user string) (agentapi.WebSite, bool) {
	ag.mu.Lock()
	defer ag.mu.Unlock()

	for _, w := range ag.web.Sites {
		if w.User == user {
			return w, true
		}
	}

	return agentapi.WebSite{}, false
}

func TestNodeSite(t *testing.T) {
	s, ag, _ := newService(t)
	ctx := t.Context()
	node := &store.SiteApp{Node: "24"}
	req := CreateRequest{Name: "shop", Domains: []string{"shop.example.com"}, App: node}

	if _, err := s.Create(ctx, 1, req); !errors.Is(err, ErrNoApps) {
		t.Fatalf("without apps: %v", err)
	}

	apps := &fakeAppAgent{installed: []string{"24"}, specs: map[string]agentapi.AppSpec{}}
	s.EnableApps(apps)

	for name, r := range map[string]CreateRequest{
		"php":         {Name: "shop", Domains: req.Domains, App: node, PHP: "8.4"},
		"not there":   {Name: "shop", Domains: req.Domains, App: &store.SiteApp{Node: "22"}},
		"bad version": {Name: "shop", Domains: req.Domains, App: &store.SiteApp{Node: "25"}},
		"workdir":     {Name: "shop", Domains: req.Domains, App: &store.SiteApp{Node: "24", Workdir: "../x"}},
		"start":       {Name: "shop", Domains: req.Domains, App: &store.SiteApp{Node: "24", StartKind: agentapi.AppStartNPM, Start: "a b"}},
	} {
		if _, err := s.Create(ctx, 1, r); err == nil {
			t.Errorf("%s accepted", name)
		}
	}

	// The agent refuses the app: no site is left.
	apps.applyErr = errDown
	if _, err := s.Create(ctx, 1, req); !errors.Is(err, errDown) {
		t.Fatalf("app refused: %v", err)
	}

	if _, err := s.Get(ctx, "shop"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("site left after a refused app: %v", err)
	}

	apps.applyErr = nil

	site, err := s.Create(ctx, 1, req)
	if err != nil {
		t.Fatal(err)
	}

	if site.Type != store.SiteTypeNode || site.App == nil || site.App.Port != agentapi.MinAppPort || site.App.Workdir != agentapi.DefaultAppWorkdir {
		t.Fatalf("created %+v %+v", site, site.App)
	}

	// No start command yet: set up, stopped; nginx passes to its port.
	if a := apps.specs["shop"]; !a.Stopped || a.Port != agentapi.MinAppPort {
		t.Errorf("spec %+v", a)
	}

	if w, ok := webOf(ag, "shop"); !ok || w.App == nil || w.App.Port != agentapi.MinAppPort || w.PHP != "" || w.WebDir != "" {
		t.Errorf("web %+v", w)
	}

	// Web options and PHP are not for Node.js sites.
	if _, err := s.SetWebOptions(ctx, 1, "shop", store.WebOptions{Dir: "public"}); !errors.Is(err, ErrSiteType) {
		t.Errorf("web options: %v", err)
	}

	if _, err := s.SetPHP(ctx, 1, "shop", "8.4"); !errors.Is(err, ErrSiteType) {
		t.Errorf("php: %v", err)
	}

	if _, err := s.AppAction(ctx, 1, "shop", agentapi.AppActionStart); !errors.Is(err, ErrInvalid) {
		t.Errorf("start without a command: %v", err)
	}

	// The start command starts it.
	set := store.SiteApp{Node: "24", StartKind: agentapi.AppStartScript, Start: "server.js"}

	site, st, err := s.SetApp(ctx, 1, "shop", set)
	if err != nil || st.State != "active" || site.App.Start != "server.js" || site.App.Port != agentapi.MinAppPort {
		t.Fatalf("set: %+v %+v %v", site.App, st, err)
	}

	if a := apps.specs["shop"]; a.Stopped || a.Start != "server.js" {
		t.Errorf("spec after set %+v", a)
	}

	if _, _, err := s.SetApp(ctx, 1, "shop", store.SiteApp{Node: "22", StartKind: set.StartKind, Start: set.Start}); !errors.Is(err, ErrNodeMissing) {
		t.Errorf("missing line: %v", err)
	}

	// A refused change keeps the previous app.
	apps.applyErr = errDown
	if _, _, err := s.SetApp(ctx, 1, "shop", store.SiteApp{Node: "24", StartKind: agentapi.AppStartNPM, Start: "start"}); !errors.Is(err, errDown) {
		t.Fatalf("refused change: %v", err)
	}

	apps.applyErr = nil

	if site, _ := s.Get(ctx, "shop"); site.App.StartKind != agentapi.AppStartScript {
		t.Errorf("after refused change %+v", site.App)
	}

	// Stop and start are kept; restart only goes to the agent.
	if _, err := s.AppAction(ctx, 1, "shop", agentapi.AppActionStop); err != nil {
		t.Fatal(err)
	}

	if site, _ := s.Get(ctx, "shop"); !site.App.Stopped || !apps.specs["shop"].Stopped {
		t.Errorf("stop: %+v %+v", site.App, apps.specs["shop"])
	}

	if _, err := s.AppAction(ctx, 1, "shop", agentapi.AppActionStart); err != nil {
		t.Fatal(err)
	}

	if site, _ := s.Get(ctx, "shop"); site.App.Stopped || apps.specs["shop"].Stopped {
		t.Errorf("start: %+v", site.App)
	}

	if _, err := s.AppAction(ctx, 1, "shop", agentapi.AppActionRestart); err != nil || len(apps.actions) != 1 {
		t.Errorf("restart: %v %v", err, apps.actions)
	}

	if _, err := s.AppAction(ctx, 1, "shop", "reload"); !errors.Is(err, ErrInvalid) {
		t.Errorf("unknown action: %v", err)
	}

	// Disabling the site stops its app, enabling starts it again.
	if _, err := s.SetDisabled(ctx, 1, "shop", true, ""); err != nil {
		t.Fatal(err)
	}

	if !apps.specs["shop"].Stopped {
		t.Error("app runs on a disabled site")
	}

	if _, err := s.AppAction(ctx, 1, "shop", agentapi.AppActionRestart); !errors.Is(err, ErrState) {
		t.Errorf("restart on a disabled site: %v", err)
	}

	if _, err := s.SetDisabled(ctx, 1, "shop", false, ""); err != nil {
		t.Fatal(err)
	}

	if apps.specs["shop"].Stopped {
		t.Error("app stays stopped after enable")
	}

	// Sync sets up the app again.
	delete(apps.specs, "shop")

	if err := s.Sync(ctx); err != nil {
		t.Fatal(err)
	}

	if _, ok := apps.specs["shop"]; !ok {
		t.Error("sync left the app out")
	}

	// A second site gets the next port; a static site has no app.
	other, err := s.Create(ctx, 1, CreateRequest{Name: "blog", Domains: []string{"blog.example.com"}, App: node})
	if err != nil || other.App.Port != agentapi.MinAppPort+1 {
		t.Fatalf("second: %+v %v", other.App, err)
	}

	if _, err := s.Create(ctx, 1, CreateRequest{Name: "docs", Domains: []string{"docs.example.com"}}); err != nil {
		t.Fatal(err)
	}

	if _, _, err := s.SetApp(ctx, 1, "docs", set); !errors.Is(err, ErrSiteType) {
		t.Errorf("app on a static site: %v", err)
	}

	if _, err := s.AppStatus(ctx, "docs"); !errors.Is(err, ErrSiteType) {
		t.Errorf("status of a static site: %v", err)
	}

	if err := s.Delete(ctx, 1, "shop"); err != nil {
		t.Fatal(err)
	}
}

func TestAppSite(t *testing.T) {
	s, ag, _ := newService(t)
	ctx := t.Context()

	// No Node.js line is installed: an app site does not need one.
	apps := &fakeAppAgent{specs: map[string]agentapi.AppSpec{}}
	s.EnableApps(apps)

	prog := &store.SiteApp{StartKind: agentapi.AppStartBinary, Start: "bin/server", Args: []string{"--port", "x y"}}
	req := CreateRequest{Name: "svc", Domains: []string{"svc.example.com"}, App: prog}

	for name, r := range map[string]CreateRequest{
		"php":          {Name: "svc", Domains: req.Domains, App: prog, PHP: "8.4"},
		"script":       {Name: "svc", Domains: req.Domains, App: &store.SiteApp{StartKind: agentapi.AppStartScript, Start: "x.js"}},
		"absolute":     {Name: "svc", Domains: req.Domains, App: &store.SiteApp{StartKind: agentapi.AppStartBinary, Start: "/bin/sh"}},
		"args alone":   {Name: "svc", Domains: req.Domains, App: &store.SiteApp{Args: []string{"x"}}},
		"newline arg":  {Name: "svc", Domains: req.Domains, App: &store.SiteApp{StartKind: agentapi.AppStartBinary, Start: "x", Args: []string{"a\nb"}}},
		"node and arg": {Name: "svc", Domains: req.Domains, App: &store.SiteApp{Node: "24", Args: []string{"x"}}},
	} {
		if _, err := s.Create(ctx, 1, r); err == nil {
			t.Errorf("%s accepted", name)
		}
	}

	site, err := s.Create(ctx, 1, req)
	if err != nil {
		t.Fatal(err)
	}

	if site.Type != store.SiteTypeApp || site.App == nil || site.App.Node != "" || site.App.Port != agentapi.MinAppPort {
		t.Fatalf("created %+v %+v", site, site.App)
	}

	if a := apps.specs["svc"]; !a.Binary() || a.Stopped || a.Start != "bin/server" || len(a.Args) != 2 {
		t.Errorf("spec %+v", a)
	}

	if w, ok := webOf(ag, "svc"); !ok || w.App == nil || w.App.Port != agentapi.MinAppPort || w.PHP != "" {
		t.Errorf("web %+v", w)
	}

	if _, err := s.SetPHP(ctx, 1, "svc", "8.4"); !errors.Is(err, ErrSiteType) {
		t.Errorf("php: %v", err)
	}

	// A Node.js version is refused on an app site.
	if _, _, err := s.SetApp(ctx, 1, "svc", store.SiteApp{Node: "24", StartKind: agentapi.AppStartScript, Start: "x.js"}); !errors.Is(err, ErrInvalid) {
		t.Errorf("node on an app site: %v", err)
	}

	site, _, err = s.SetApp(ctx, 1, "svc", store.SiteApp{StartKind: agentapi.AppStartBinary, Start: "srv/app"})
	if err != nil || site.App.Start != "srv/app" || site.App.Args != nil {
		t.Fatalf("set: %+v %v", site.App, err)
	}

	if _, err := s.AppAction(ctx, 1, "svc", agentapi.AppActionRestart); err != nil {
		t.Errorf("restart: %v", err)
	}

	if _, err := s.SetDisabled(ctx, 1, "svc", true, ""); err != nil {
		t.Fatal(err)
	}

	if !apps.specs["svc"].Stopped {
		t.Error("disabled site's app runs")
	}
}
