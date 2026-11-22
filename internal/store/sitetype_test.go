// SPDX-License-Identifier: AGPL-3.0-or-later

package store

import (
	"errors"
	"reflect"
	"testing"
	"time"

	"github.com/maki-it/hostashi/internal/agentapi"
)

func TestSetSiteType(t *testing.T) {
	st := open(t, dbPath(t))
	ctx := t.Context()
	now := time.Unix(1_800_000_000, 0)

	if _, err := st.CreateSite(ctx, "shop", "default", "8.4", []string{"shop.example.com"}, now); err != nil {
		t.Fatal(err)
	}

	// php -> node: the version goes, the app gets the lowest free port.
	node := SiteApp{Node: "24", Workdir: "htdocs", StartKind: agentapi.AppStartScript, Start: "server.js"}

	old, err := st.SetSiteType(ctx, "shop", SiteTypeChange{Type: SiteTypeNode, Account: true, App: &node}, now)
	if err != nil || !reflect.DeepEqual(old, SiteTypeChange{Type: SiteTypePHP, PHP: "8.4", Account: true}) {
		t.Fatalf("to node: %+v %v", old, err)
	}

	got, err := st.SiteByName(ctx, "shop")
	if err != nil || got.Type != SiteTypeNode || got.PHP != "" || got.App == nil || got.App.Port != agentapi.MinAppPort {
		t.Fatalf("node: %+v %+v %v", got, got.App, err)
	}

	// node -> app keeps the port.
	prog := SiteApp{Workdir: "htdocs", StartKind: agentapi.AppStartBinary, Start: "bin/server", Args: []string{"-v"}}

	old, err = st.SetSiteType(ctx, "shop", SiteTypeChange{Type: SiteTypeApp, Account: true, App: &prog}, now)
	if err != nil || old.Type != SiteTypeNode || old.App == nil || old.App.Port != agentapi.MinAppPort || old.App.Start != "server.js" {
		t.Fatalf("to app: %+v %v", old, err)
	}

	got, _ = st.SiteByName(ctx, "shop")
	if got.Type != SiteTypeApp || got.App.Port != agentapi.MinAppPort || !reflect.DeepEqual(got.App.Args, []string{"-v"}) {
		t.Fatalf("app: %+v %+v", got, got.App)
	}

	// app -> proxy frees the port, the account stays.
	old, err = st.SetSiteType(ctx, "shop", SiteTypeChange{Type: SiteTypeProxy, Account: true, Proxy: &SiteProxy{Target: "http://10.0.0.5:8080", Insecure: true}}, now)
	if err != nil || old.App == nil {
		t.Fatalf("to proxy: %+v %v", old, err)
	}

	got, _ = st.SiteByName(ctx, "shop")
	if got.Type != SiteTypeProxy || !got.Account || got.App != nil || got.Proxy == nil || !got.Proxy.Insecure {
		t.Fatalf("proxy: %+v", got)
	}

	// The undo restores the app with its port.
	if _, err := st.SetSiteType(ctx, "shop", old, now); err != nil {
		t.Fatal(err)
	}

	got, _ = st.SiteByName(ctx, "shop")
	if got.Type != SiteTypeApp || got.App == nil || got.App.Port != agentapi.MinAppPort || got.App.Start != "bin/server" {
		t.Fatalf("restored: %+v %+v", got, got.App)
	}

	// A redirect without an account, then php with a new account.
	if _, err := st.CreateRedirectSite(ctx, "old", "default", SiteRedirect{Target: "https://example.com", Status: 302}, []string{"old.example.com"}, now); err != nil {
		t.Fatal(err)
	}

	old, err = st.SetSiteType(ctx, "old", SiteTypeChange{Type: SiteTypePHP, PHP: "8.3", Account: true}, now)
	if err != nil || old.Account || old.Redirect == nil || old.Redirect.Status != 302 {
		t.Fatalf("redirect to php: %+v %v", old, err)
	}

	got, _ = st.SiteByName(ctx, "old")
	if got.Type != SiteTypePHP || got.PHP != "8.3" || !got.Account || got.Redirect == nil {
		t.Fatalf("php: %+v", got)
	}

	// The table's rules still apply: arguments need a binary.
	bad := SiteApp{Node: "24", Workdir: "htdocs", StartKind: agentapi.AppStartScript, Start: "x.js", Args: []string{"x"}}
	if _, err := st.SetSiteType(ctx, "old", SiteTypeChange{Type: SiteTypeNode, Account: true, App: &bad}, now); err == nil {
		t.Error("node app with arguments stored")
	}

	if got, _ := st.SiteByName(ctx, "old"); got.Type != SiteTypePHP {
		t.Errorf("failed change left type %q", got.Type)
	}

	if _, err := st.SetSiteType(ctx, "nope", SiteTypeChange{Type: SiteTypeStatic}, now); !errors.Is(err, ErrNotFound) {
		t.Errorf("unknown site: %v", err)
	}
}
