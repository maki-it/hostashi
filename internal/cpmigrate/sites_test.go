// SPDX-License-Identifier: AGPL-3.0-or-later

package cpmigrate_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"slices"
	"testing"

	"github.com/maki-it/hostashi/internal/agentapi"
	"github.com/maki-it/hostashi/internal/cloudpanel/plan"
	"github.com/maki-it/hostashi/internal/cloudpanel/scan"
	"github.com/maki-it/hostashi/internal/cpmigrate"
)

type fakeImporter struct {
	got []agentapi.CloudPanelImportRequest
}

func (f *fakeImporter) Start(_ context.Context, r agentapi.CloudPanelImportRequest) (agentapi.CloudPanelImport, error) {
	f.got = append(f.got, r)

	return agentapi.CloudPanelImport{ID: "0123456789abcdef", Site: r.Site, State: agentapi.ImportQueued}, nil
}

func (f *fakeImporter) List(_ context.Context, site string) ([]agentapi.CloudPanelImport, error) {
	return []agentapi.CloudPanelImport{{ID: "0123456789abcdef", Site: site}}, nil
}

func (f *fakeImporter) Get(_ context.Context, id string) (agentapi.CloudPanelImport, error) {
	return agentapi.CloudPanelImport{ID: id}, nil
}

func sitesReport(t *testing.T) []byte {
	t.Helper()

	site := func(user, typ string, st scan.Status, php string, extra func(*scan.Site)) scan.Site {
		s := scan.Site{
			Name: user + ".example.com", User: user, Type: typ, Status: st,
			Domains: []string{user + ".example.com"}, DocRoot: "/home/" + user + "/htdocs/" + user + ".example.com",
			Sources: []string{"/etc/nginx/sites-enabled/" + user + ".conf"},
		}
		if php != "" {
			s.Pool = &scan.Pool{User: user, Version: php}
		}

		if extra != nil {
			extra(&s)
		}

		return s
	}

	b, err := json.Marshal(scan.Report{Format: scan.Format, Sites: []scan.Site{
		site("alpha", "php", scan.StatusClean, "8.4", func(s *scan.Site) { s.RedirectFrom = []string{"www.alpha.example.com"} }),
		site("beta", "php", scan.StatusClean, "8.1", nil),
		site("gamma", "nodejs", scan.StatusClean, "", nil),
		{Name: "x", Status: scan.StatusBlocked, Findings: []scan.Finding{{Severity: scan.SevBlocker, Code: "path_escape", Msg: "no"}}},
	}})
	if err != nil {
		t.Fatal(err)
	}

	return b
}

func TestCreateSiteAndImport(t *testing.T) {
	var existing []string

	target := func(context.Context) (plan.Target, error) {
		return plan.Target{Sites: existing, PHP: []string{"8.4"}}, nil
	}

	svc, _ := newService(t, target)

	rep, err := svc.Upload(t.Context(), 1, bytes.NewReader(sitesReport(t)))
	if err != nil {
		t.Fatal(err)
	}

	if _, err := svc.CreateSite(t.Context(), 1, rep.ID, "alpha", cpmigrate.CreateOptions{}); !errors.Is(err, cpmigrate.ErrNoImporter) {
		t.Fatalf("not connected: %v", err)
	}

	var created []cpmigrate.NewSite

	imp := &fakeImporter{}
	svc.Connect(func(_ context.Context, _ int64, s cpmigrate.NewSite) error {
		created = append(created, s)
		existing = append(existing, s.Name)

		return nil
	}, imp)

	// The redirect host makes alpha a review site.
	var pe *cpmigrate.PlanError
	if _, err := svc.CreateSite(t.Context(), 1, rep.ID, "alpha", cpmigrate.CreateOptions{}); !errors.Is(err, cpmigrate.ErrNeedsReview) || !errors.As(err, &pe) || len(pe.Reasons) == 0 {
		t.Fatalf("unreviewed: %v", err)
	}

	ps, err := svc.CreateSite(t.Context(), 1, rep.ID, "alpha", cpmigrate.CreateOptions{Reviewed: true})
	if err != nil {
		t.Fatal(err)
	}

	if len(created) != 1 || created[0].Name != "alpha" || created[0].PHP != "8.4" ||
		!slices.Equal(created[0].Domains, []string{"alpha.example.com", "www.alpha.example.com"}) || ps.Name != "alpha" {
		t.Fatalf("created %+v", created)
	}

	// PHP 8.1 is not installed: review, with another version.
	if _, err := svc.CreateSite(t.Context(), 1, rep.ID, "beta", cpmigrate.CreateOptions{Reviewed: true, PHP: "7.0"}); !errors.Is(err, cpmigrate.ErrBadRequest) {
		t.Fatalf("bad php: %v", err)
	}

	if _, err := svc.CreateSite(t.Context(), 1, rep.ID, "beta", cpmigrate.CreateOptions{Reviewed: true, PHP: "8.4"}); err != nil || created[1].PHP != "8.4" {
		t.Fatalf("beta: %v %+v", err, created)
	}

	for name, want := range map[string]error{"gamma": cpmigrate.ErrSkipped, "x": cpmigrate.ErrNoSite, "nobody": cpmigrate.ErrNoSite, "alpha": cpmigrate.ErrSkipped} {
		if _, err := svc.CreateSite(t.Context(), 1, rep.ID, name, cpmigrate.CreateOptions{Reviewed: true}); !errors.Is(err, want) {
			t.Errorf("%s: %v, want %v", name, err, want)
		}
	}

	if _, err := svc.CreateSite(t.Context(), 1, "00000000000000000000000000000000", "alpha", cpmigrate.CreateOptions{}); !errors.Is(err, cpmigrate.ErrNotFound) {
		t.Fatalf("unknown report: %v", err)
	}

	// The import takes the remote user and document root from the report.
	dbs := []agentapi.CloudPanelDBMap{{From: "alpha_wp", To: "alpha_db"}}

	got, err := svc.StartImport(t.Context(), 1, rep.ID, "alpha", dbs)
	if err != nil || got.ID != "0123456789abcdef" {
		t.Fatalf("import: %+v %v", got, err)
	}

	want := agentapi.CloudPanelImportRequest{Site: "alpha", RemoteUser: "alpha", DocRoot: "/home/alpha/htdocs/alpha.example.com", Databases: dbs}
	if len(imp.got) != 1 || imp.got[0].DocRoot != want.DocRoot || imp.got[0].RemoteUser != want.RemoteUser || imp.got[0].Databases[0] != dbs[0] {
		t.Fatalf("request %+v", imp.got)
	}

	if _, err := svc.StartImport(t.Context(), 1, rep.ID, "alpha", []agentapi.CloudPanelDBMap{{From: "a", To: "mysql"}}); !errors.Is(err, cpmigrate.ErrBadRequest) {
		t.Fatalf("bad database: %v", err)
	}

	if _, err := svc.StartImport(t.Context(), 1, rep.ID, "x", nil); !errors.Is(err, cpmigrate.ErrNoSite) {
		t.Fatalf("blocked site: %v", err)
	}

	if l, err := svc.Imports(t.Context(), "alpha"); err != nil || len(l) != 1 {
		t.Fatalf("list: %v %v", l, err)
	}

	if _, err := svc.Imports(t.Context(), "Bad"); !errors.Is(err, cpmigrate.ErrBadRequest) {
		t.Fatalf("list bad site: %v", err)
	}

	if _, err := svc.Import(t.Context(), "nope"); !errors.Is(err, cpmigrate.ErrNotFound) {
		t.Fatalf("bad id: %v", err)
	}

	if g, err := svc.Import(t.Context(), "0123456789abcdef"); err != nil || g.ID != "0123456789abcdef" {
		t.Fatalf("get: %v", err)
	}
}

func TestImportRefusesTwinUsers(t *testing.T) {
	svc, _ := newService(t, nil)
	svc.Connect(func(context.Context, int64, cpmigrate.NewSite) error { return nil }, &fakeImporter{})

	s := scan.Site{Name: "a", User: "alpha", Type: "static", Status: scan.StatusClean, Domains: []string{"a.example.com"}, DocRoot: "/home/alpha/htdocs/a"}
	s2 := s
	s2.Domains, s2.DocRoot = []string{"b.example.com"}, "/home/alpha/htdocs/b"

	b, _ := json.Marshal(scan.Report{Format: scan.Format, Sites: []scan.Site{s, s2}})

	rep, err := svc.Upload(t.Context(), 1, bytes.NewReader(b))
	if err != nil {
		t.Fatal(err)
	}

	if _, err := svc.StartImport(t.Context(), 1, rep.ID, "alpha", nil); !errors.Is(err, cpmigrate.ErrSkipped) {
		t.Fatalf("twin users: %v", err)
	}
}
