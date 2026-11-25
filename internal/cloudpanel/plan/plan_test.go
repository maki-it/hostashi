// SPDX-License-Identifier: AGPL-3.0-or-later

package plan_test

import (
	"bytes"
	"crypto/ed25519"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"golang.org/x/crypto/ssh"

	"github.com/maki-it/hostashi/internal/agentapi"
	"github.com/maki-it/hostashi/internal/cloudpanel/fixture"
	"github.com/maki-it/hostashi/internal/cloudpanel/plan"
	"github.com/maki-it/hostashi/internal/cloudpanel/scan"
)

var now = time.Date(2026, 11, 19, 12, 0, 0, 0, time.UTC)

// fixtureJSON scans the synthetic CloudPanel server and returns the full
// report as the scan command writes it.
func fixtureJSON(t *testing.T) []byte {
	t.Helper()

	dir := filepath.Join(t.TempDir(), "srv")
	if _, err := fixture.Build(dir, now); err != nil {
		t.Fatal(err)
	}

	rep, err := scan.Run(scan.Options{Root: dir, RequireRootOwned: os.Geteuid() == 0, Now: now})
	if err != nil {
		t.Fatal(err)
	}

	b, err := json.Marshal(rep)
	if err != nil {
		t.Fatal(err)
	}

	return b
}

func parse(t *testing.T, b []byte) *scan.Report {
	t.Helper()

	rep, err := plan.Parse(bytes.NewReader(b))
	if err != nil {
		t.Fatal(err)
	}

	return rep
}

func TestFixtureRoundTrip(t *testing.T) {
	rep := parse(t, fixtureJSON(t))

	p := plan.Build(rep, plan.Target{PHP: []string{"8.3", "8.4"}})
	if p.Summary.Sites != len(rep.Sites) || p.Summary.Import+p.Summary.Review+p.Summary.Skip != p.Summary.Sites {
		t.Fatalf("summary %+v for %d sites", p.Summary, len(rep.Sites))
	}

	byName := map[string]plan.Site{}
	for _, s := range p.Sites {
		byName[s.Source] = s

		if s.Action == plan.ActionImport && len(s.Reasons) != 0 {
			t.Errorf("%s: import with reasons %v", s.Source, s.Reasons)
		}

		if s.Action != plan.ActionImport && len(s.Reasons) == 0 {
			t.Errorf("%s: %s without a reason", s.Source, s.Action)
		}
	}

	// A site the scan blocked is always skipped.
	for _, s := range rep.Sites {
		got := byName[s.Sources[0]]
		if s.Status == scan.StatusBlocked && got.Action != plan.ActionSkip {
			t.Errorf("%s: blocked by the scan, plan %s", s.Sources[0], got.Action)
		}
	}

	if p.Summary.Import == 0 || p.Summary.Review == 0 || p.Summary.Skip == 0 {
		t.Fatalf("fixture should give every action: %+v", p.Summary)
	}
}

func hasCode(s plan.Site, code string) bool {
	for _, r := range s.Reasons {
		if r.Code == code {
			return true
		}
	}

	return false
}

func base() scan.Site {
	return scan.Site{
		Name: "shop.example.com", User: "shop", Type: "wordpress", Status: scan.StatusClean,
		Domains: []string{"shop.example.com"}, DocRoot: "/home/shop/htdocs/shop.example.com",
		Pool:    &scan.Pool{Name: "shop", Version: "8.4", User: "shop", Group: "shop"},
		TLS:     scan.TLS{Kind: "letsencrypt"},
		Sources: []string{"/etc/nginx/sites-enabled/shop.example.com.conf"},
	}
}

func one(s scan.Site, t plan.Target) plan.Site {
	return plan.Build(&scan.Report{Format: scan.Format, Sites: []scan.Site{s}}, t).Sites[0]
}

func TestBuildSite(t *testing.T) {
	target := plan.Target{PHP: []string{"8.4"}}

	got := one(base(), target)
	if got.Action != plan.ActionImport || got.TLS != plan.TLSReissue || got.Name != "shop" || got.PHP != "8.4" {
		t.Fatalf("clean site: %+v", got)
	}

	cases := []struct {
		name   string
		change func(*scan.Site, *plan.Target)
		action plan.Action
		code   string
	}{
		{"site exists", func(_ *scan.Site, t *plan.Target) { t.Sites = []string{"shop"} }, plan.ActionSkip, "site_exists"},
		{"domain taken", func(_ *scan.Site, t *plan.Target) { t.Domains = []string{"shop.example.com"} }, plan.ActionSkip, "domain_taken"},
		{"redirect host taken", func(s *scan.Site, t *plan.Target) {
			s.RedirectFrom = []string{"www.shop.example.com"}
			t.Domains = []string{"www.shop.example.com"}
		}, plan.ActionSkip, "domain_taken"},
		{"panel's name", func(_ *scan.Site, t *plan.Target) { t.Panel = "shop.example.com" }, plan.ActionSkip, "domain_reserved"},
		{"panel's name as redirect host", func(s *scan.Site, t *plan.Target) {
			s.RedirectFrom = []string{"www.shop.example.com"}
			t.Panel = "www.shop.example.com"
		}, plan.ActionSkip, "domain_reserved"},
		{"unknown type", func(s *scan.Site, _ *plan.Target) { s.Type, s.Pool = "ruby", nil }, plan.ActionSkip, "type_unsupported"},
		{"php 5.6", func(s *scan.Site, _ *plan.Target) { s.Pool.Version = "5.6" }, plan.ActionSkip, "php_unsupported"},
		{"php not installed", func(s *scan.Site, _ *plan.Target) { s.Pool.Version = "8.3" }, plan.ActionReview, "php_missing"},
		{"php legacy", func(s *scan.Site, t *plan.Target) { s.Pool.Version, t.PHP = "7.4", []string{"7.4"} }, plan.ActionReview, "php_legacy"},
		{"custom cert", func(s *scan.Site, _ *plan.Target) { s.TLS.Kind = "custom" }, plan.ActionReview, "tls_copy"},
		{"snippets", func(s *scan.Site, _ *plan.Target) { s.Custom = []scan.Custom{{Raw: "add_header X-A b;"}} }, plan.ActionReview, "custom_snippets"},
		{"cron", func(s *scan.Site, _ *plan.Target) { s.Cron = []scan.CronJob{{Schedule: "* * * * *", Command: "true"}} }, plan.ActionReview, "cron"},
		{"cron not accepted", func(s *scan.Site, _ *plan.Target) {
			s.Cron = []scan.CronJob{{Schedule: "@reboot", Command: "true"}}
		}, plan.ActionReview, "cron_not_carried"},
		{"scan review finding", func(s *scan.Site, _ *plan.Target) {
			s.Status = scan.StatusReview
			s.Findings = []scan.Finding{{Severity: scan.SevReview, Code: "pool_env", Msg: "env"}}
		}, plan.ActionReview, "pool_env"},
		{"scan blocked", func(s *scan.Site, _ *plan.Target) {
			s.Status = scan.StatusBlocked
			s.Findings = []scan.Finding{{Severity: scan.SevBlocker, Code: "docroot_outside_home", Msg: "x"}}
		}, plan.ActionSkip, "docroot_outside_home"},
		{"skip wins over review", func(s *scan.Site, t *plan.Target) {
			s.Cron = []scan.CronJob{{Schedule: "@daily", Command: "true"}}
			t.Sites = []string{"shop"}
		}, plan.ActionSkip, "cron"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			s, tg := base(), plan.Target{PHP: []string{"8.4"}}
			s.Pool = &scan.Pool{Name: "shop", Version: "8.4", User: "shop"}
			tc.change(&s, &tg)

			got := one(s, tg)
			if got.Action != tc.action || !hasCode(got, tc.code) {
				t.Fatalf("got %s %v, want %s with %s", got.Action, got.Reasons, tc.action, tc.code)
			}
		})
	}
}

func TestBuildProxySite(t *testing.T) {
	proxy := func(upstream string) scan.Site {
		s := base()
		s.Type, s.Pool, s.Upstream = "proxy", nil, upstream
		s.Cron = []scan.CronJob{{Schedule: "@daily", Command: "true"}}
		s.SSHKeys = []scan.SSHKey{{Public: testKey(t)}}
		s.Usage = &scan.Usage{Bytes: 1 << 20, Inodes: 10}

		return s
	}

	got := one(proxy("http://10.0.0.5:8080"), plan.Target{})
	if got.Action != plan.ActionReview || got.Proxy != "http://10.0.0.5:8080" || got.PHP != "" || got.Node != "" ||
		got.CronImport != 0 || got.SSHKeys != 0 || got.HomeBytes != 0 {
		t.Fatalf("proxy: %+v", got)
	}

	for _, code := range []string{"cron_no_account", "ssh_keys_no_account"} {
		if !hasCode(got, code) {
			t.Errorf("proxy without %s: %v", code, got.Reasons)
		}
	}

	if hasCode(got, "cron_not_carried") || hasCode(got, "proxy_tls_verify") {
		t.Errorf("proxy reasons %v", got.Reasons)
	}

	if got := one(proxy("https://backend.example.net"), plan.Target{}); !hasCode(got, "proxy_tls_verify") {
		t.Errorf("https target: %v", got.Reasons)
	}

	// Targets the panel refuses: this server, link-local, the site itself.
	for _, u := range []string{"http://127.0.0.1:3000", "http://169.254.169.254", "https://shop.example.com", "unix:/run/app.sock"} {
		if got := one(proxy(u), plan.Target{}); got.Action != plan.ActionSkip || !hasCode(got, "proxy_target") {
			t.Errorf("%s: %s %v", u, got.Action, got.Reasons)
		}
	}

	s := proxy("http://10.0.0.5")
	s.Custom = []scan.Custom{{Raw: "add_header X-A b;"}}

	if got := one(s, plan.Target{}); !hasCode(got, "custom_snippets_proxy") || hasCode(got, "custom_snippets") {
		t.Errorf("snippets: %v", got.Reasons)
	}
}

func testKey(t *testing.T) string {
	t.Helper()

	pub, _, err := ed25519.GenerateKey(nil)
	if err != nil {
		t.Fatal(err)
	}

	pk, err := ssh.NewPublicKey(pub)
	if err != nil {
		t.Fatal(err)
	}

	return strings.TrimSpace(string(ssh.MarshalAuthorizedKey(pk)))
}

func TestBuildNodeSite(t *testing.T) {
	s := base()
	s.Type, s.Pool, s.Upstream = "nodejs", nil, "http://127.0.0.1:3000"
	s.Status = scan.StatusReview
	s.Findings = []scan.Finding{{Severity: scan.SevReview, Code: "app_process", Msg: "the app process is started outside nginx"}}

	got := one(s, plan.Target{Node: []string{"22", "24"}})
	if got.Action != plan.ActionReview || got.Node != "24" || got.PHP != "" || got.Proxy != "" {
		t.Fatalf("node: %+v", got)
	}

	if !hasCode(got, "node_version") || !hasCode(got, "node_start") || hasCode(got, "app_process") || hasCode(got, "node_missing") {
		t.Fatalf("reasons %v", got.Reasons)
	}

	if r := got.Reasons[len(got.Reasons)-1]; !strings.Contains(r.Msg, "instead of 3000") {
		t.Errorf("port: %v", got.Reasons)
	}

	// Nothing installed: the newest pinned line, to be installed first.
	got = one(s, plan.Target{})
	if !hasCode(got, "node_missing") || got.Node != agentapi.NodeReleases[len(agentapi.NodeReleases)-1].Major {
		t.Fatalf("none installed: %+v", got)
	}
}

func TestBuildPythonSite(t *testing.T) {
	s := base()
	s.Type, s.Pool, s.Upstream, s.Project = "python", nil, "http://127.0.0.1:8000", "requirements.txt"
	s.Status = scan.StatusReview
	s.Findings = []scan.Finding{{Severity: scan.SevReview, Code: "app_process", Msg: "the app process is started outside nginx"}}

	got := one(s, plan.Target{Python: []string{"3.12", "3.13"}})
	if got.Action != plan.ActionReview || got.Python != "3.13" || got.PHP != "" || got.Proxy != "" || got.Node != "" {
		t.Fatalf("python: %+v", got)
	}

	if !hasCode(got, "python_version") || !hasCode(got, "python_start") || hasCode(got, "app_process") || hasCode(got, "python_missing") {
		t.Fatalf("reasons %v", got.Reasons)
	}

	if r := got.Reasons[len(got.Reasons)-1]; !strings.Contains(r.Msg, "instead of 8000") || !strings.Contains(r.Msg, "requirements.txt into ~/.venv") {
		t.Errorf("port and project: %v", got.Reasons)
	}

	// Nothing installed: the newest pinned line, to be installed first.
	got = one(s, plan.Target{})
	if !hasCode(got, "python_missing") || got.Python != agentapi.PythonReleases[len(agentapi.PythonReleases)-1].Line {
		t.Fatalf("none installed: %+v", got)
	}
}

func TestBuildDuplicatesInReport(t *testing.T) {
	a, b := base(), base()
	b.Name, b.Domains, b.DocRoot = "other.example.com", []string{"other.example.com"}, "/home/shop/htdocs/other.example.com"
	c := base()
	c.User, c.Pool, c.DocRoot = "other", nil, "/home/other/htdocs/shop.example.com"

	p := plan.Build(&scan.Report{Sites: []scan.Site{a, b, c}}, plan.Target{PHP: []string{"8.4"}})
	if !hasCode(p.Sites[0], "user_twice") || !hasCode(p.Sites[1], "user_twice") || !hasCode(p.Sites[2], "domain_twice") || !hasCode(p.Sites[0], "domain_twice") {
		t.Fatalf("duplicates not reported: %+v", p.Sites)
	}

	if p.Summary.Skip != 3 {
		t.Fatalf("summary %+v", p.Summary)
	}
}

func report(t *testing.T, change func(*scan.Report)) []byte {
	t.Helper()

	rep := scan.Report{Format: scan.Format, Sites: []scan.Site{base()}}
	change(&rep)

	b, err := json.Marshal(rep)
	if err != nil {
		t.Fatal(err)
	}

	return b
}

func TestParseRefuses(t *testing.T) {
	site := func(f func(*scan.Site)) func(*scan.Report) { return func(r *scan.Report) { f(&r.Sites[0]) } }

	cases := map[string][]byte{
		"empty":           nil,
		"not json":        []byte("site: x"),
		"unknown field":   []byte(`{"format":"` + scan.Format + `","sites":[],"extra":1}`),
		"trailing data":   append(report(t, func(*scan.Report) {}), []byte(` {}`)...),
		"other format":    report(t, func(r *scan.Report) { r.Format = "hostashi-cloudpanel-scan/0" }),
		"no format":       report(t, func(r *scan.Report) { r.Format = "" }),
		"no sites":        report(t, func(r *scan.Report) { r.Sites = nil }),
		"too large":       bytes.Repeat([]byte(" "), plan.MaxReportBytes+1),
		"bad status":      report(t, site(func(s *scan.Site) { s.Status = "fine" })),
		"root user":       report(t, site(func(s *scan.Site) { s.User = "root" })),
		"user with slash": report(t, site(func(s *scan.Site) { s.User = "../etc" })),
		"bad domain":      report(t, site(func(s *scan.Site) { s.Domains = []string{"shop.example.com;"} })),
		"bad redirect":    report(t, site(func(s *scan.Site) { s.RedirectFrom = []string{"*.example.com"} })),
		"no domain":       report(t, site(func(s *scan.Site) { s.Domains = nil })),
		"docroot of other user": report(t, site(func(s *scan.Site) {
			s.DocRoot = "/home/other/htdocs/shop.example.com"
		})),
		"docroot dotdot":     report(t, site(func(s *scan.Site) { s.DocRoot = "/home/shop/../root" })),
		"docroot is home":    report(t, site(func(s *scan.Site) { s.DocRoot = "/home/shop/" })),
		"docroot variable":   report(t, site(func(s *scan.Site) { s.DocRoot = "/home/shop/htdocs/$host" })),
		"docroot newline":    report(t, site(func(s *scan.Site) { s.DocRoot = "/home/shop/htdocs/a\nb" })),
		"pool other user":    report(t, site(func(s *scan.Site) { s.Pool.User = "www-data" })),
		"php version string": report(t, site(func(s *scan.Site) { s.Pool.Version = "8.4; rm" })),
		"too many snippets": report(t, site(func(s *scan.Site) {
			s.Custom = make([]scan.Custom, 501)
		})),
	}
	for name, b := range cases {
		t.Run(name, func(t *testing.T) {
			if _, err := plan.Parse(bytes.NewReader(b)); !errors.Is(err, plan.ErrInvalid) {
				t.Fatalf("got %v, want ErrInvalid", err)
			}
		})
	}
}

func TestParseAcceptsBlockedSiteWithoutDetails(t *testing.T) {
	b := report(t, func(r *scan.Report) {
		r.Sites[0] = scan.Site{Name: "broken.conf", Status: scan.StatusBlocked, Sources: []string{"/etc/nginx/sites-enabled/broken.conf"}}
	})

	got := plan.Build(parse(t, b), plan.Target{})
	if got.Sites[0].Action != plan.ActionSkip || !hasCode(got.Sites[0], "scan_blocked") {
		t.Fatalf("%+v", got.Sites[0])
	}
}

func TestPlanJSONHasNoSnippets(t *testing.T) {
	s := base()
	s.Custom = []scan.Custom{{Raw: "proxy_set_header Authorization secret;"}}
	s.Cron = []scan.CronJob{{Command: "curl -H 'X-Token: secret'"}}

	b, err := json.Marshal(plan.Build(&scan.Report{Sites: []scan.Site{s}}, plan.Target{}))
	if err != nil {
		t.Fatal(err)
	}

	if strings.Contains(string(b), "secret") {
		t.Fatalf("plan leaks snippet text: %s", b)
	}
}

func TestHomeUsage(t *testing.T) {
	s := base()
	p := plan.Build(&scan.Report{Sites: []scan.Site{s}}, plan.Target{PHP: []string{"8.4"}})

	if ps := p.Sites[0]; ps.HomeBytes != 0 || ps.HomeInodes != 0 {
		t.Fatalf("unmeasured: %+v", ps)
	}

	s.Usage = &scan.Usage{Bytes: 3 << 30, Inodes: 42}
	p = plan.Build(&scan.Report{Sites: []scan.Site{s}}, plan.Target{PHP: []string{"8.4"}})

	if ps := p.Sites[0]; ps.HomeBytes != 3<<30 || ps.HomeInodes != 42 {
		t.Fatalf("measured: %+v", ps)
	}
}

func TestCronJobs(t *testing.T) {
	s := base()
	s.Cron = []scan.CronJob{
		{Schedule: "*/5 * * * *", Command: "php wp-cron.php", Line: 1},
		{Schedule: "@reboot", Command: "start.sh", Line: 2},
		{Schedule: "0 3 * * *", Command: "", Line: 3},
		{Schedule: "@daily", Command: "backup.sh", Line: 4},
	}

	got := plan.CronJobs(s)
	if len(got) != 2 || got[0].Line != 1 || got[1].Line != 4 {
		t.Fatalf("%+v", got)
	}

	p := plan.Build(&scan.Report{Sites: []scan.Site{s}}, plan.Target{PHP: []string{"8.4"}})
	if ps := p.Sites[0]; ps.Cron != 4 || ps.CronImport != 2 || !hasCode(ps, "cron") || !hasCode(ps, "cron_not_carried") {
		t.Fatalf("%+v", ps)
	}

	many := base()
	for i := range 60 {
		many.Cron = append(many.Cron, scan.CronJob{Schedule: "@hourly", Command: "true", Line: i + 1})
	}

	if n := len(plan.CronJobs(many)); n != 50 {
		t.Fatalf("limit: %d", n)
	}
}

func TestBuildProposesPageCache(t *testing.T) {
	tier := scan.Finding{Severity: scan.SevReview, Code: "vhost_cache_tier", Msg: "cache"}
	target := plan.Target{PHP: []string{"8.4"}}

	for _, tc := range []struct {
		typ, want string
		php       bool
	}{
		{"wordpress", "wordpress", true},
		{"php", "php", true},
		{"static", "", false},
	} {
		s := base()
		s.Type, s.Status, s.Findings = tc.typ, scan.StatusReview, []scan.Finding{tier}

		if !tc.php {
			s.Pool = nil
		}

		got := one(s, target)
		if got.PageCache != tc.want || got.Action != plan.ActionReview {
			t.Errorf("%s: page cache %q, action %s, want %q", tc.typ, got.PageCache, got.Action, tc.want)
		}

		// A site without PHP keeps the scanner's note instead.
		if code := map[bool]string{true: "page_cache", false: "vhost_cache_tier"}[tc.php]; !hasCode(got, code) {
			t.Errorf("%s: reasons %v, want %s", tc.typ, got.Reasons, code)
		}
	}

	if got := one(base(), target); got.PageCache != "" {
		t.Errorf("site without a cache tier: %q", got.PageCache)
	}
}
