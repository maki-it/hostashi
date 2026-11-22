// SPDX-License-Identifier: AGPL-3.0-or-later

package hosting

import (
	"errors"
	"testing"

	"github.com/maki-it/hostashi/internal/agentapi"
	"github.com/maki-it/hostashi/internal/store"
)

func TestSetTypeBetweenAccountTypes(t *testing.T) {
	s, ag, st := newService(t)
	ctx := t.Context()
	apps := &fakeAppAgent{installed: []string{"24"}, specs: map[string]agentapi.AppSpec{}}
	s.EnableApps(apps)

	if _, err := s.Create(ctx, 1, CreateRequest{Name: "shop", PHP: "8.4", Domains: []string{"shop.example.com"}}); err != nil {
		t.Fatal(err)
	}

	for name, req := range map[string]TypeRequest{
		"same":          {Type: store.SiteTypePHP, PHP: "8.3"},
		"unknown":       {Type: "python"},
		"php without":   {Type: store.SiteTypePHP},
		"static w/ php": {Type: store.SiteTypeStatic, PHP: "8.4"},
		"node no app":   {Type: store.SiteTypeNode},
		"node missing":  {Type: store.SiteTypeNode, App: &store.SiteApp{Node: "22"}},
		"app with node": {Type: store.SiteTypeApp, App: &store.SiteApp{Node: "24"}},
		"redirect loop": {Type: store.SiteTypeRedirect, Redirect: &store.SiteRedirect{Target: "https://shop.example.com/x", Status: 301}},
		"proxy w/ app":  {Type: store.SiteTypeProxy, App: &store.SiteApp{}},
	} {
		if _, err := s.SetType(ctx, 1, "shop", req); err == nil {
			t.Errorf("%s accepted", name)
		}
	}

	// php -> node: the pool stops with the web state, the app gets a port.
	site, err := s.SetType(ctx, 1, "shop", TypeRequest{Type: store.SiteTypeNode, App: &store.SiteApp{Node: "24", StartKind: agentapi.AppStartScript, Start: "server.js"}})
	if err != nil || site.Type != store.SiteTypeNode || site.PHP != "" || site.App == nil || site.App.Port != agentapi.MinAppPort {
		t.Fatalf("to node: %+v %v", site, err)
	}

	ws, _ := webOf(ag, "shop")
	if ws.PHP != "" || ws.App == nil || ws.App.Port != agentapi.MinAppPort {
		t.Fatalf("web %+v", ws)
	}

	if a, ok := apps.specs["shop"]; !ok || a.Node != "24" || a.Stopped {
		t.Fatalf("app %+v %v", a, ok)
	}

	// The agent refuses the app of the next type: the Node.js app stays.
	apps.applyErr = errDown
	if _, err := s.SetType(ctx, 1, "shop", TypeRequest{Type: store.SiteTypeApp, App: &store.SiteApp{StartKind: agentapi.AppStartBinary, Start: "bin/server"}}); !errors.Is(err, errDown) {
		t.Fatalf("refused app: %v", err)
	}

	apps.applyErr = nil

	if got, _ := s.Get(ctx, "shop"); got.Type != store.SiteTypeNode || got.App == nil || got.App.Start != "server.js" {
		t.Fatalf("after a refused change: %+v", got)
	}

	// node -> app keeps the port.
	site, err = s.SetType(ctx, 1, "shop", TypeRequest{Type: store.SiteTypeApp, App: &store.SiteApp{StartKind: agentapi.AppStartBinary, Start: "bin/server", Args: []string{"-p"}}})
	if err != nil || site.Type != store.SiteTypeApp || site.App.Port != agentapi.MinAppPort || apps.specs["shop"].Node != "" {
		t.Fatalf("to app: %+v %v", site, err)
	}

	// A failing web state keeps the app site and its app.
	ag.webErr = errDown
	if _, err := s.SetType(ctx, 1, "shop", TypeRequest{Type: store.SiteTypeStatic}); !errors.Is(err, errDown) {
		t.Fatalf("web refused: %v", err)
	}

	ag.webErr = nil

	if got, _ := s.Get(ctx, "shop"); got.Type != store.SiteTypeApp || got.App == nil {
		t.Fatalf("after a refused web state: %+v", got)
	}

	if _, ok := apps.specs["shop"]; !ok {
		t.Fatal("app removed by a failed change")
	}

	// app -> static: the app goes after the web state.
	site, err = s.SetType(ctx, 1, "shop", TypeRequest{Type: store.SiteTypeStatic})
	if err != nil || site.Type != store.SiteTypeStatic || site.App != nil {
		t.Fatalf("to static: %+v %v", site, err)
	}

	if _, ok := apps.specs["shop"]; ok {
		t.Fatal("app left after a change to static")
	}

	if ws, _ := webOf(ag, "shop"); ws.App != nil || ws.PHP != "" || ws.WebDir != "" && ws.WebDir != site.WebDir {
		t.Fatalf("web %+v", ws)
	}

	// static -> php.
	if site, err = s.SetType(ctx, 1, "shop", TypeRequest{Type: store.SiteTypePHP, PHP: "8.3"}); err != nil || site.PHP != "8.3" {
		t.Fatalf("to php: %+v %v", site, err)
	}

	if ws, _ := webOf(ag, "shop"); ws.PHP != "8.3" {
		t.Fatalf("web %+v", ws)
	}

	if !ag.accounts["shop"] {
		t.Fatal("account gone")
	}

	entries, _, err := st.AuditEntries(ctx, store.AuditFilter{Action: "site.type"}, 0, 50)
	if err != nil || len(entries) != 4 {
		t.Fatalf("audit %d %v", len(entries), err)
	}
}

func TestSetTypeWithoutAccount(t *testing.T) {
	s, ag, st := newService(t)
	ctx := t.Context()
	lim := newFakeLimits()
	s.EnableLimits(lim)

	redirect := &store.SiteRedirect{Target: "https://example.com", Status: 302}
	if _, err := s.Create(ctx, 1, CreateRequest{Name: "old", Domains: []string{"old.example.com"}, Redirect: redirect}); err != nil {
		t.Fatal(err)
	}

	// The account cannot be created: the redirect stays.
	ag.createErr = errDown
	if _, err := s.SetType(ctx, 1, "old", TypeRequest{Type: store.SiteTypePHP, PHP: "8.4"}); !errors.Is(err, errDown) {
		t.Fatalf("account refused: %v", err)
	}

	ag.createErr = nil

	// The web state fails: the new account goes again.
	ag.webErr = errDown
	if _, err := s.SetType(ctx, 1, "old", TypeRequest{Type: store.SiteTypePHP, PHP: "8.4"}); !errors.Is(err, errDown) {
		t.Fatalf("web refused: %v", err)
	}

	ag.webErr = nil

	got, _ := s.Get(ctx, "old")
	if got.Type != store.SiteTypeRedirect || got.Account || ag.accounts["old"] {
		t.Fatalf("after failures: %+v account %v", got, ag.accounts["old"])
	}

	if _, ok, _ := st.SiteLimits(ctx, got.ID); ok {
		t.Fatal("limits left after a failed change")
	}

	// redirect -> php creates the account with the preset's limits.
	site, err := s.SetType(ctx, 1, "old", TypeRequest{Type: store.SiteTypePHP, PHP: "8.4"})
	if err != nil || !site.Account || site.PHP != "8.4" || !ag.accounts["old"] {
		t.Fatalf("to php: %+v %v", site, err)
	}

	if _, ok := lim.slices["old"]; !ok {
		t.Fatal("no limits for the new account")
	}

	// php -> redirect keeps the account and its files.
	site, err = s.SetType(ctx, 1, "old", TypeRequest{Type: store.SiteTypeRedirect, Redirect: &store.SiteRedirect{Target: "https://example.org", Status: 301, KeepPath: true}})
	if err != nil || site.Type != store.SiteTypeRedirect || !site.Account || site.PHP != "" || !ag.accounts["old"] {
		t.Fatalf("to redirect: %+v %v", site, err)
	}

	ws, _ := webOf(ag, "old")
	if ws.Redirect == nil || ws.Redirect.Target != "https://example.org" || !ws.Redirect.KeepPath || ws.PHP != "" {
		t.Fatalf("web %+v", ws)
	}

	// Back to static: the account is there, nothing is created again.
	if _, err := s.SetType(ctx, 1, "old", TypeRequest{Type: store.SiteTypeStatic}); err != nil {
		t.Fatal(err)
	}
}

func TestRemoveAccount(t *testing.T) {
	s, ag, st := newService(t)
	ctx := t.Context()

	if _, err := s.Create(ctx, 1, CreateRequest{Name: "old", PHP: "8.4", Domains: []string{"old.example.com"}}); err != nil {
		t.Fatal(err)
	}

	if _, err := s.RemoveAccount(ctx, 1, "old"); !errors.Is(err, ErrSiteType) {
		t.Fatalf("php site: %v", err)
	}

	if _, _, err := s.CreateDatabase(ctx, 1, NewDatabase{Site: "old", Name: "old_wp"}); err != nil {
		t.Fatal(err)
	}

	if _, err := s.SetType(ctx, 1, "old", TypeRequest{Type: store.SiteTypeRedirect, Redirect: &store.SiteRedirect{Target: "https://example.org", Status: 301}}); err != nil {
		t.Fatal(err)
	}

	got, err := s.RemoveAccount(ctx, 1, "old")
	if err != nil || got.Account || got.Type != store.SiteTypeRedirect {
		t.Fatalf("remove: %+v %v", got, err)
	}

	if ag.accounts["old"] || len(ag.dbs) != 0 {
		t.Fatalf("account %v databases %v", ag.accounts["old"], ag.dbs)
	}

	if dbs, _ := st.Databases(ctx, "old"); len(dbs) != 0 {
		t.Fatalf("database rows left: %v", dbs)
	}

	if ws, ok := webOf(ag, "old"); !ok || ws.Redirect == nil {
		t.Fatalf("redirect gone: %+v", ws)
	}

	if _, err := s.RemoveAccount(ctx, 1, "old"); !errors.Is(err, ErrInvalid) {
		t.Fatalf("second removal: %v", err)
	}
}
