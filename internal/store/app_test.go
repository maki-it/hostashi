// SPDX-License-Identifier: AGPL-3.0-or-later

package store

import (
	"errors"
	"testing"
	"time"

	"github.com/maki-it/hostashi/internal/agentapi"
)

func TestNodeSites(t *testing.T) {
	st := open(t, dbPath(t))
	ctx := t.Context()
	now := time.Unix(1_800_000_000, 0)

	a := SiteApp{Node: "24", Workdir: "htdocs", StartKind: agentapi.AppStartScript, Start: "server.js", Port: 1}

	s1, err := st.CreateNodeSite(ctx, "shop", "default", a, []string{"shop.example.com"}, now)
	if err != nil {
		t.Fatal(err)
	}

	if s1.Type != SiteTypeNode || !s1.Account || s1.App == nil || s1.App.Port != agentapi.MinAppPort {
		t.Fatalf("created %+v %+v", s1, s1.App)
	}

	s2, err := st.CreateNodeSite(ctx, "blog", "default", SiteApp{Node: "22", Workdir: "app"}, []string{"blog.example.com"}, now)
	if err != nil || s2.App.Port != agentapi.MinAppPort+1 {
		t.Fatalf("second: %v %+v", err, s2.App)
	}

	got, err := st.SiteByName(ctx, "blog")
	if err != nil || got.App == nil || *got.App != (SiteApp{Node: "22", Workdir: "app", Port: agentapi.MinAppPort + 1}) {
		t.Fatalf("read %+v %v", got.App, err)
	}

	// A freed port is used again.
	if err := st.DeleteSite(ctx, "shop"); err != nil {
		t.Fatal(err)
	}

	s3, err := st.CreateNodeSite(ctx, "next", "default", a, []string{"next.example.com"}, now)
	if err != nil || s3.App.Port != agentapi.MinAppPort {
		t.Fatalf("reuse: %v %+v", err, s3.App)
	}

	old, err := st.SetSiteApp(ctx, "blog", SiteApp{Node: "24", Workdir: "app", StartKind: agentapi.AppStartNPM, Start: "start", Stopped: true}, now)
	if err != nil || old.Node != "22" || old.StartKind != "" {
		t.Fatalf("set: %+v %v", old, err)
	}

	got, _ = st.SiteByName(ctx, "blog")
	if *got.App != (SiteApp{Node: "24", Workdir: "app", StartKind: agentapi.AppStartNPM, Start: "start", Port: agentapi.MinAppPort + 1, Stopped: true}) {
		t.Errorf("after set %+v", got.App)
	}

	// The CHECKs: a start without its kind, an unknown kind.
	for _, bad := range []SiteApp{
		{Node: "24", Workdir: "app", Start: "x.js"},
		{Node: "24", Workdir: "app", StartKind: "sh", Start: "x"},
		{Node: "x", Workdir: "app"},
		{Node: "24", Workdir: ""},
	} {
		if _, err := st.SetSiteApp(ctx, "blog", bad, now); err == nil {
			t.Errorf("%+v accepted", bad)
		}
	}

	if _, err := st.CreateSite(ctx, "plain", "default", "", []string{"plain.example.com"}, now); err != nil {
		t.Fatal(err)
	}

	if _, err := st.SetSiteApp(ctx, "plain", a, now); !errors.Is(err, ErrSiteType) {
		t.Errorf("static site: %v", err)
	}

	if plain, _ := st.SiteByName(ctx, "plain"); plain.App != nil {
		t.Errorf("static site has an app: %+v", plain.App)
	}

	if _, err := st.SetSiteApp(ctx, "missing", a, now); !errors.Is(err, ErrNotFound) {
		t.Errorf("missing: %v", err)
	}
}
