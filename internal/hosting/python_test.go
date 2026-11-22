// SPDX-License-Identifier: AGPL-3.0-or-later

package hosting

import (
	"errors"
	"slices"
	"testing"

	"github.com/maki-it/hostashi/internal/agentapi"
	"github.com/maki-it/hostashi/internal/store"
)

func TestPythonSite(t *testing.T) {
	s, ag, _ := newService(t)
	ctx := t.Context()
	req := CreateRequest{Name: "shop", Domains: []string{"shop.example.com"}, App: &store.SiteApp{Python: "3.13"}}

	if _, err := s.Create(ctx, 1, req); !errors.Is(err, ErrNoApps) {
		t.Fatalf("without apps: %v", err)
	}

	apps := &fakeAppAgent{installed: []string{"24"}, python: []string{"3.13", "3.14"}, specs: map[string]agentapi.AppSpec{}}
	s.EnableApps(apps)

	for name, a := range map[string]*store.SiteApp{
		"not there": {Python: "3.12"},
		"bad line":  {Python: "2.7"},
		"and node":  {Python: "3.13", Node: "24"},
		"npm":       {Python: "3.13", StartKind: agentapi.AppStartNPM, Start: "start"},
		"js":        {Python: "3.13", StartKind: agentapi.AppStartScript, Start: "server.js"},
		"bind":      {Python: "3.13", StartKind: agentapi.AppStartWSGI, Start: "a:b", Args: []string{"--bind", "0.0.0.0:80"}},
		"args only": {Python: "3.13", Args: []string{"x"}},
	} {
		if _, err := s.Create(ctx, 1, CreateRequest{Name: "shop", Domains: req.Domains, App: a}); err == nil {
			t.Errorf("%s accepted", name)
		} else if name == "not there" && !errors.Is(err, ErrPythonMissing) {
			t.Errorf("not there: %v", err)
		}
	}

	site, err := s.Create(ctx, 1, req)
	if err != nil {
		t.Fatal(err)
	}

	if site.Type != store.SiteTypePython || site.App == nil || site.App.Python != "3.13" || site.App.Port != agentapi.MinAppPort {
		t.Fatalf("created %+v %+v", site, site.App)
	}

	if a := apps.specs["shop"]; a.Python != "3.13" || !a.Stopped || a.Node != "" {
		t.Errorf("spec %+v", a)
	}

	if w, ok := webOf(ag, "shop"); !ok || w.App == nil || w.App.Port != agentapi.MinAppPort || w.PHP != "" {
		t.Errorf("web %+v", w)
	}

	// The start command, same line: no new environment.
	set := store.SiteApp{Python: "3.13", StartKind: agentapi.AppStartWSGI, Start: "shop.wsgi:application", Args: []string{"--workers", "3"}}

	site, _, err = s.SetApp(ctx, 1, "shop", set)
	if err != nil || site.App.Start != set.Start || len(apps.venvs) != 0 {
		t.Fatalf("set: %+v %v %v", site.App, err, apps.venvs)
	}

	if a := apps.specs["shop"]; a.Stopped || !slices.Equal(a.Args, set.Args) {
		t.Errorf("spec after set %+v", a)
	}

	// A new line clears the environment before the app is applied.
	set.Python = "3.14"

	if _, _, err := s.SetApp(ctx, 1, "shop", set); err != nil {
		t.Fatal(err)
	}

	if !slices.Equal(apps.venvs, []agentapi.PythonVenvRequest{{Site: "shop", Python: "3.14", Clear: true}}) || apps.specs["shop"].Python != "3.14" {
		t.Errorf("line change: %v %+v", apps.venvs, apps.specs["shop"])
	}

	// A refused environment keeps the stored app.
	apps.venvErr = errDown
	set.Python = "3.13"

	if _, _, err := s.SetApp(ctx, 1, "shop", set); !errors.Is(err, errDown) {
		t.Errorf("refused venv: %v", err)
	}

	if site, _ := s.Get(ctx, "shop"); site.App.Python != "3.14" {
		t.Errorf("after refused venv %+v", site.App)
	}

	if _, err := s.RecreateVenv(ctx, 1, "shop"); !errors.Is(err, errDown) {
		t.Errorf("recreate refused: %v", err)
	}

	apps.venvErr = nil
	apps.venvs = nil

	if _, err := s.RecreateVenv(ctx, 1, "shop"); err != nil || !slices.Equal(apps.venvs, []agentapi.PythonVenvRequest{{Site: "shop", Python: "3.14", Clear: true}}) {
		t.Errorf("recreate: %v %v", err, apps.venvs)
	}

	// A Node.js site has no environment; it becomes a Python site.
	if _, err := s.Create(ctx, 1, CreateRequest{Name: "blog", Domains: []string{"blog.example.com"}, App: &store.SiteApp{Node: "24"}}); err != nil {
		t.Fatal(err)
	}

	if _, err := s.RecreateVenv(ctx, 1, "blog"); !errors.Is(err, ErrSiteType) {
		t.Errorf("recreate on node: %v", err)
	}

	if _, _, err := s.SetApp(ctx, 1, "blog", store.SiteApp{Node: "24", Python: "3.13"}); !errors.Is(err, ErrInvalid) {
		t.Errorf("python line on node: %v", err)
	}

	if _, err := s.SetType(ctx, 1, "blog", TypeRequest{Type: store.SiteTypePython, App: &store.SiteApp{Python: "3.13"}}); err != nil {
		t.Fatal(err)
	}

	if a := apps.specs["blog"]; a.Python != "3.13" || a.Node != "" {
		t.Errorf("blog after type change %+v", a)
	}
}
