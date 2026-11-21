// SPDX-License-Identifier: AGPL-3.0-or-later

package cpmigrate_test

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"encoding/json"
	"errors"
	"slices"
	"strings"
	"testing"

	"golang.org/x/crypto/ssh"

	"github.com/maki-it/hostashi/internal/agentapi"
	"github.com/maki-it/hostashi/internal/agentrpc"
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

func (f *fakeImporter) Activate(_ context.Context, site string) (agentapi.CloudPanelActivated, error) {
	if site == "empty" {
		return agentapi.CloudPanelActivated{}, &agentrpc.Error{Code: agentrpc.CodePrecondition, Message: "import the site's files first"}
	}

	return agentapi.CloudPanelActivated{Site: site, Previous: "htdocs-before-import-20261119-120000"}, nil
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
		site("alpha", "php", scan.StatusClean, "8.4", func(s *scan.Site) {
			s.RedirectFrom = []string{"www.alpha.example.com"}
			s.Usage = &scan.Usage{Bytes: 12 << 30, Inodes: 700_000}
			s.Cron = []scan.CronJob{{Schedule: "*/5 * * * *", Command: "php wp-cron.php", Line: 1}, {Schedule: "@reboot", Command: "x", Line: 2}}
			s.SSHKeys = []scan.SSHKey{{Public: edKey(t), Comment: "kim@laptop"}, {Public: "ssh-dss AAAA", Comment: "old"}}
		}),
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
	svc.Connect(func(_ context.Context, _ int64, s cpmigrate.NewSite) (cpmigrate.Created, error) {
		created = append(created, s)
		existing = append(existing, s.Name)

		return cpmigrate.Created{Cron: len(s.Cron), SSHKeys: len(s.SSHKeys)}, nil
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

	// The new site's limits start from the old home's use (ADR-0015).
	if u := created[0].Usage; u == nil || u.Bytes != 12<<30 || u.Inodes != 700_000 || ps.HomeBytes != 12<<30 || ps.HomeInodes != 700_000 {
		t.Fatalf("usage %+v, plan %d bytes %d inodes", u, ps.HomeBytes, ps.HomeInodes)
	}

	if len(created[0].SSHKeys) != 1 || created[0].SSHKeys[0].Label != "kim@laptop" || ps.SSHImport != 1 || ps.SSHKeys != 1 {
		t.Fatalf("ssh keys %+v, plan %d/%d", created[0].SSHKeys, ps.SSHKeys, ps.SSHImport)
	}

	if len(created[0].Cron) != 1 || created[0].Cron[0].Line != 1 || ps.CronImport != 1 {
		t.Fatalf("cron %+v %d", created[0].Cron, ps.CronImport)
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

	if r, err := svc.Activate(t.Context(), 1, "alpha"); err != nil || r.Previous == "" {
		t.Fatalf("activate: %+v %v", r, err)
	}

	if _, err := svc.Activate(t.Context(), 1, "../x"); !errors.Is(err, cpmigrate.ErrBadRequest) {
		t.Fatalf("activate bad site: %v", err)
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
	svc.Connect(func(context.Context, int64, cpmigrate.NewSite) (cpmigrate.Created, error) {
		return cpmigrate.Created{}, nil
	}, &fakeImporter{})

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

func edKey(t *testing.T) string {
	t.Helper()

	pub, _, err := ed25519.GenerateKey(nil)
	if err != nil {
		t.Fatal(err)
	}

	pk, err := ssh.NewPublicKey(pub)
	if err != nil {
		t.Fatal(err)
	}

	return strings.TrimSuffix(string(ssh.MarshalAuthorizedKey(pk)), "\n")
}

func TestCreateSiteWithPageCache(t *testing.T) {
	var existing []string

	svc, _ := newService(t, func(context.Context) (plan.Target, error) {
		return plan.Target{Sites: existing, PHP: []string{"8.4"}}, nil
	})

	site := func(user string) scan.Site {
		return scan.Site{
			Name: user + ".example.com", User: user, Type: "wordpress", Status: scan.StatusReview,
			Domains: []string{user + ".example.com"}, DocRoot: "/home/" + user + "/htdocs/" + user + ".example.com",
			Pool:     &scan.Pool{User: user, Version: "8.4"},
			Sources:  []string{"/etc/nginx/sites-enabled/" + user + ".conf"},
			Findings: []scan.Finding{{Severity: scan.SevReview, Code: "vhost_cache_tier", Msg: "cache"}},
		}
	}

	b, err := json.Marshal(scan.Report{Format: scan.Format, Sites: []scan.Site{site("alpha"), site("beta")}})
	if err != nil {
		t.Fatal(err)
	}

	rep, err := svc.Upload(t.Context(), 1, bytes.NewReader(b))
	if err != nil {
		t.Fatal(err)
	}

	var got []string

	svc.Connect(func(_ context.Context, _ int64, s cpmigrate.NewSite) (cpmigrate.Created, error) {
		got = append(got, s.PageCache)
		existing = append(existing, s.Name)

		return cpmigrate.Created{PageCache: s.PageCache != ""}, nil
	}, &fakeImporter{})

	ps, err := svc.CreateSite(t.Context(), 1, rep.ID, "alpha", cpmigrate.CreateOptions{Reviewed: true})
	if err != nil || ps.PageCache != agentapi.PageCacheWordPress {
		t.Fatalf("proposed cache: %+v %v", ps, err)
	}

	off := false

	ps, err = svc.CreateSite(t.Context(), 1, rep.ID, "beta", cpmigrate.CreateOptions{Reviewed: true, PageCache: &off})
	if err != nil || ps.PageCache != "" {
		t.Fatalf("cache left off: %+v %v", ps, err)
	}

	if !slices.Equal(got, []string{agentapi.PageCacheWordPress, ""}) {
		t.Fatalf("created with %q", got)
	}
}
