// SPDX-License-Identifier: AGPL-3.0-or-later

package serve

import (
	"context"
	"encoding/json"
	"net/http"
	"net/netip"
	"strings"
	"testing"
	"time"

	"github.com/maki-it/hostashi/internal/agentapi"
	"github.com/maki-it/hostashi/internal/agentrpc"
	"github.com/maki-it/hostashi/internal/cloudpanel/plan"
	"github.com/maki-it/hostashi/internal/cloudpanel/scan"
	"github.com/maki-it/hostashi/internal/cpmigrate"
	"github.com/maki-it/hostashi/internal/hosting"
)

const reportsPath = "/api/v1/migrations/cloudpanel/reports"

func scanReportJSON(t *testing.T) string {
	t.Helper()

	site := func(user, domain string) scan.Site {
		return scan.Site{
			Name: domain, User: user, Type: "wordpress", Status: scan.StatusReview,
			Domains: []string{domain}, DocRoot: "/home/" + user + "/htdocs/" + domain,
			Pool: &scan.Pool{Name: user, Version: "8.4", User: user}, TLS: scan.TLS{Kind: "letsencrypt"},
			Custom:  []scan.Custom{{Context: "server: add_header", Raw: `add_header X-Secret "s3cr3t-in-snippet";`}},
			Sources: []string{"/etc/nginx/sites-enabled/" + domain + ".conf"},
			Cron:    []scan.CronJob{{Schedule: "*/10 * * * *", Command: "curl -s https://" + domain + "/cron?key=cr0n-t0ken", Line: 1}},
		}
	}

	shop := site("shop", "shop.example.com")
	shop.Usage = &scan.Usage{Bytes: 20 << 30, Inodes: 1_000_000}
	shop.SSHKeys = []scan.SSHKey{{Public: testSSHKey(t), Comment: "kim (laptop)", Source: "/home/shop/.ssh/authorized_keys", Line: 1}}

	b, err := json.Marshal(scan.Report{Format: scan.Format, Sites: []scan.Site{
		shop, site("blog", "blog.example.org"),
	}})
	if err != nil {
		t.Fatal(err)
	}

	return string(b)
}

func TestCloudPanelReportsOverHTTP(t *testing.T) {
	e := newAPIWithAgent(t, &stubAgent{})
	c := e.adminCookie(t)

	if rec := e.do(t, reqOpts{method: "POST", path: "/api/v1/sites", cookie: c, body: `{"name":"blog","domains":["blog.example.com"]}`}); rec.Code != http.StatusCreated {
		t.Fatalf("create site: %d %s", rec.Code, rec.Body)
	}

	body := scanReportJSON(t)

	rec := e.do(t, reqOpts{method: "POST", path: reportsPath, cookie: c, body: body})
	if rec.Code != http.StatusCreated {
		t.Fatalf("upload: %d %s", rec.Code, rec.Body)
	}

	if strings.Contains(rec.Body.String(), "s3cr3t") {
		t.Fatalf("response leaks a snippet: %s", rec.Body)
	}

	var got cloudPanelReportJSON
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil || got.Plan == nil || got.Sites != 2 || len(got.ID) != 32 {
		t.Fatalf("%+v %v", got, err)
	}

	actions := map[string]plan.Action{}
	for _, s := range got.Plan.Sites {
		actions[s.Name] = s.Action
	}

	if actions["shop"] != plan.ActionReview || actions["blog"] != plan.ActionSkip {
		t.Fatalf("plan %+v", got.Plan.Sites)
	}

	// The stored report is sealed: the snippet text is not in the database.
	var n int
	if err := e.st.RawCount(t.Context(), `SELECT count(*) FROM cloudpanel_reports WHERE instr(sealed, ?) > 0`, []byte("s3cr3t"), &n); err != nil || n != 0 {
		t.Fatalf("plain snippet in the database: %d %v", n, err)
	}

	// The plan follows the server: once the site is gone, blog can be imported.
	if rec := e.do(t, reqOpts{method: "DELETE", path: "/api/v1/sites/blog", cookie: c}); rec.Code != http.StatusNoContent {
		t.Fatalf("delete site: %d %s", rec.Code, rec.Body)
	}

	rec = e.do(t, reqOpts{method: "GET", path: reportsPath + "/" + got.ID, cookie: c})
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"review":2`) {
		t.Fatalf("get: %d %s", rec.Code, rec.Body)
	}

	rec = e.do(t, reqOpts{method: "GET", path: reportsPath, cookie: c})
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), got.ID) || strings.Contains(rec.Body.String(), `"plan"`) {
		t.Fatalf("list: %d %s", rec.Code, rec.Body)
	}

	read := bearerHeader(e.createToken(t, c, `{"name":"r","scopes":["migrations:read"]}`).Token)
	if rec := e.do(t, reqOpts{method: "GET", path: reportsPath + "/" + got.ID, header: read}); rec.Code != http.StatusOK {
		t.Fatalf("read token: %d %s", rec.Code, rec.Body)
	}

	if rec := e.do(t, reqOpts{method: "POST", path: reportsPath, header: read, body: body}); rec.Code != http.StatusForbidden || errCode(t, rec) != "insufficient_scope" {
		t.Fatalf("upload with read token: %d %s", rec.Code, rec.Body)
	}

	if rec := e.do(t, reqOpts{method: "DELETE", path: reportsPath + "/" + got.ID, cookie: c}); rec.Code != http.StatusNoContent {
		t.Fatalf("delete: %d %s", rec.Code, rec.Body)
	}

	for _, id := range []string{got.ID, "nothex", strings.ToUpper(got.ID)} {
		if rec := e.do(t, reqOpts{method: "GET", path: reportsPath + "/" + id, cookie: c}); rec.Code != http.StatusNotFound {
			t.Errorf("get %q: %d %s", id, rec.Code, rec.Body)
		}
	}
}

func TestCloudPanelReportUploadErrors(t *testing.T) {
	e := newAPIWithAgent(t, &stubAgent{})
	c := e.adminCookie(t)

	for name, tc := range map[string]struct {
		body, ctype, errCode string
		status               int
	}{
		"not a report":   {body: `{"sites":[]}`, status: http.StatusUnprocessableEntity, errCode: "invalid_report"},
		"other format":   {body: `{"format":"x","sites":[]}`, status: http.StatusUnprocessableEntity, errCode: "invalid_report"},
		"unknown fields": {body: `{"format":"` + scan.Format + `","evil":1}`, status: http.StatusUnprocessableEntity, errCode: "invalid_report"},
		"wrong type":     {body: `{}`, ctype: "text/plain", status: http.StatusUnsupportedMediaType, errCode: "unsupported_media_type"},
		"too large":      {body: strings.Repeat(" ", plan.MaxReportBytes+10), status: http.StatusRequestEntityTooLarge, errCode: "too_large"},
	} {
		t.Run(name, func(t *testing.T) {
			h := map[string]string{}
			if tc.ctype != "" {
				h["Content-Type"] = tc.ctype
			}

			rec := e.do(t, reqOpts{method: "POST", path: reportsPath, cookie: c, body: tc.body, header: h})
			if rec.Code != tc.status || errCode(t, rec) != tc.errCode {
				t.Fatalf("%d %s", rec.Code, rec.Body)
			}
		})
	}
}

// stubImports records what the panel asks the agent to import.
type stubImports struct {
	got []agentapi.CloudPanelImportRequest
}

func (s *stubImports) Start(_ context.Context, r agentapi.CloudPanelImportRequest) (agentapi.CloudPanelImport, error) {
	if r.Site == "busy" {
		return agentapi.CloudPanelImport{}, &agentrpc.Error{Code: agentrpc.CodeConflict, Message: "an import is in progress"}
	}

	s.got = append(s.got, r)

	return agentapi.CloudPanelImport{ID: "0123456789abcdef", Site: r.Site, State: agentapi.ImportQueued}, nil
}

func (s *stubImports) List(_ context.Context, site string) ([]agentapi.CloudPanelImport, error) {
	out := []agentapi.CloudPanelImport{}

	for _, r := range s.got {
		if site == "" || r.Site == site {
			out = append(out, agentapi.CloudPanelImport{ID: "0123456789abcdef", Site: r.Site, State: agentapi.ImportQueued})
		}
	}

	return out, nil
}

func (s *stubImports) Activate(_ context.Context, site string) (agentapi.CloudPanelActivated, error) {
	if site == "empty" {
		return agentapi.CloudPanelActivated{}, &agentrpc.Error{Code: agentrpc.CodePrecondition, Message: "import the site's files first"}
	}

	return agentapi.CloudPanelActivated{Site: site, Previous: "htdocs-before-import-20261119-120000"}, nil
}

func (s *stubImports) Get(_ context.Context, id string) (agentapi.CloudPanelImport, error) {
	if id != "0123456789abcdef" {
		return agentapi.CloudPanelImport{}, &agentrpc.Error{Code: agentrpc.CodeNotFound, Message: "no such import"}
	}

	imp := agentapi.CloudPanelImport{ID: id, State: agentapi.ImportDone}
	for _, r := range s.got {
		imp.Site, imp.Databases = r.Site, r.Databases
		imp.Dumps = nil

		for _, d := range r.Databases {
			imp.Dumps = append(imp.Dumps, "cloudpanel-import-db/"+d.To+"-20261119T120000Z.sql")
		}
	}

	return imp, nil
}

func TestCloudPanelSitesOverHTTP(t *testing.T) {
	e := newAPIWithAgent(t, &stubAgent{})
	cr := &stubCron{applied: map[string][]agentapi.CronJob{}}
	e.sites.EnableCron(cr)

	lim := &stubLimits{slices: map[string]agentapi.SiteLimits{}, quotas: map[string][2]uint64{}}
	e.sites.EnableLimits(lim)

	sshAg := &stubSSH{applied: map[string][]agentapi.SSHKey{}}
	e.sites.EnableSSH(sshAg)

	c := e.adminCookie(t)

	rec := e.do(t, reqOpts{method: "POST", path: reportsPath, cookie: c, body: scanReportJSON(t)})

	var rep cloudPanelReportJSON
	if rec.Code != http.StatusCreated || json.Unmarshal(rec.Body.Bytes(), &rep) != nil {
		t.Fatalf("upload: %d %s", rec.Code, rec.Body)
	}

	sites := reportsPath + "/" + rep.ID + "/sites/"

	// shop has a snippet: the review must be confirmed.
	rec = e.do(t, reqOpts{method: "POST", path: sites + "shop", cookie: c, body: `{}`})
	if rec.Code != http.StatusConflict || errCode(t, rec) != "needs_review" || !strings.Contains(rec.Body.String(), `"custom_snippets"`) {
		t.Fatalf("unreviewed: %d %s", rec.Code, rec.Body)
	}

	if strings.Contains(rec.Body.String(), "s3cr3t") || strings.Contains(rec.Body.String(), "cr0n-t0ken") {
		t.Fatalf("snippet or cron command in the refusal: %s", rec.Body)
	}

	rec = e.do(t, reqOpts{method: "POST", path: sites + "shop", cookie: c, body: `{"reviewed":true}`})
	if rec.Code != http.StatusCreated || !strings.Contains(rec.Body.String(), `"cronImport":1`) || strings.Contains(rec.Body.String(), "cr0n-t0ken") {
		t.Fatalf("create: %d %s", rec.Code, rec.Body)
	}

	// The old site user's key arrives as SFTP only (ADR-0032).
	if k := sshAg.applied["shop"]; len(k) != 1 || k[0].Access != "sftp" || k[0].Label != "kim laptop" || !strings.Contains(rec.Body.String(), `"sshImport":1`) {
		t.Fatalf("ssh keys %+v %s", k, rec.Body)
	}

	// The old site's cron job arrives paused (ADR-0028).
	if jobs := cr.applied["shop"]; len(jobs) != 1 || !jobs[0].Paused || !strings.HasSuffix(jobs[0].Command, "key=cr0n-t0ken") {
		t.Fatalf("cron jobs %+v", jobs)
	}

	s, err := e.sites.Get(t.Context(), "shop")
	if err != nil || s.PHP != "8.4" || len(s.Domains) != 1 || s.Domains[0] != "shop.example.com" {
		t.Fatalf("site %+v %v", s, err)
	}

	// The old site may have called programs from PHP: it keeps that (ADR-0033).
	if !s.PHPPrograms {
		t.Fatal("imported PHP site without programs")
	}

	// The old home used 20 GiB and a million files: the limits leave 50 %.
	sl, err := e.sites.SiteLimits(t.Context(), "shop")
	if err != nil || !sl.Set || sl.Limits.DiskBytes < 30<<30 || sl.Limits.DiskInodes < 1_500_000 {
		t.Fatalf("limits %+v %v", sl, err)
	}

	if q := lim.quotas["shop"]; q[0] < 30<<30 || q[1] < 1_500_000 {
		t.Fatalf("quota sent to the agent %v", q)
	}

	// Now it exists: the plan skips it.
	if rec := e.do(t, reqOpts{method: "POST", path: sites + "shop", cookie: c, body: `{"reviewed":true}`}); rec.Code != http.StatusConflict || errCode(t, rec) != "skipped" {
		t.Fatalf("again: %d %s", rec.Code, rec.Body)
	}

	for path, want := range map[string]int{
		sites + "nobody": http.StatusNotFound,
		reportsPath + "/" + strings.Repeat("0", 32) + "/sites/shop": http.StatusNotFound,
	} {
		if rec := e.do(t, reqOpts{method: "POST", path: path, cookie: c, body: `{"reviewed":true}`}); rec.Code != want {
			t.Errorf("%s: %d %s", path, rec.Code, rec.Body)
		}
	}

	if rec := e.do(t, reqOpts{method: "POST", path: sites + "blog", cookie: c, body: `{"reviewed":true,"php":"4.0"}`}); rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("bad php: %d %s", rec.Code, rec.Body)
	}

	// The body cannot name another path on the old server.
	if rec := e.do(t, reqOpts{method: "POST", path: sites + "shop/import", cookie: c, body: `{"docRoot":"/etc"}`}); rec.Code != http.StatusBadRequest {
		t.Fatalf("doc root in body: %d %s", rec.Code, rec.Body)
	}

	rec = e.do(t, reqOpts{method: "POST", path: sites + "shop/import", cookie: c, body: `{"databases":[{"from":"shop_wp","to":"shop_db"}]}`})
	if rec.Code != http.StatusAccepted || !strings.Contains(rec.Body.String(), `"id":"0123456789abcdef"`) {
		t.Fatalf("import: %d %s", rec.Code, rec.Body)
	}

	if g := e.imports.got; len(g) != 1 || g[0].RemoteUser != "shop" || g[0].DocRoot != "/home/shop/htdocs/shop.example.com" || g[0].Databases[0].To != "shop_db" {
		t.Fatalf("agent request %+v", g)
	}

	read := bearerHeader(e.createToken(t, c, `{"name":"r","scopes":["migrations:read"]}`).Token)
	if rec := e.do(t, reqOpts{method: "POST", path: sites + "shop/import", header: read, body: `{}`}); rec.Code != http.StatusForbidden {
		t.Fatalf("import with read token: %d %s", rec.Code, rec.Body)
	}

	if rec := e.do(t, reqOpts{method: "GET", path: "/api/v1/migrations/cloudpanel/imports/0123456789abcdef", header: read}); rec.Code != http.StatusOK ||
		!strings.Contains(rec.Body.String(), `"state":"done"`) {
		t.Fatalf("status: %d %s", rec.Code, rec.Body)
	}

	if rec := e.do(t, reqOpts{method: "GET", path: "/api/v1/migrations/cloudpanel/imports?site=shop", header: read}); rec.Code != http.StatusOK ||
		!strings.Contains(rec.Body.String(), `"site":"shop"`) {
		t.Fatalf("list: %d %s", rec.Code, rec.Body)
	}

	if rec := e.do(t, reqOpts{method: "GET", path: "/api/v1/migrations/cloudpanel/imports?site=blog", cookie: c}); rec.Code != http.StatusOK ||
		rec.Body.String() != "{\"imports\":[]}\n" {
		t.Fatalf("list other site: %d %q", rec.Code, rec.Body)
	}

	if rec := e.do(t, reqOpts{method: "GET", path: "/api/v1/migrations/cloudpanel/imports?site=..%2Fx", cookie: c}); rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("list bad site: %d %s", rec.Code, rec.Body)
	}

	for _, id := range []string{"fedcba9876543210", "nope"} {
		if rec := e.do(t, reqOpts{method: "GET", path: "/api/v1/migrations/cloudpanel/imports/" + id, cookie: c}); rec.Code != http.StatusNotFound {
			t.Errorf("%s: %d %s", id, rec.Code, rec.Body)
		}
	}

	const activate = "/api/v1/migrations/cloudpanel/sites/shop/activate"

	if rec := e.do(t, reqOpts{method: "POST", path: activate, header: read}); rec.Code != http.StatusForbidden {
		t.Fatalf("activate with read token: %d %s", rec.Code, rec.Body)
	}

	if rec := e.do(t, reqOpts{method: "POST", path: activate, cookie: c}); rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"previous":"htdocs-before-import-`) {
		t.Fatalf("activate: %d %s", rec.Code, rec.Body)
	}

	if rec := e.do(t, reqOpts{method: "POST", path: "/api/v1/migrations/cloudpanel/sites/empty/activate", cookie: c}); rec.Code != http.StatusConflict {
		t.Fatalf("activate without import: %d %s", rec.Code, rec.Body)
	}

	// The dumps of a finished import load as the databases' owner
	// (ADR-0030); a database that does not exist here is reported.
	const load = "/api/v1/migrations/cloudpanel/imports/0123456789abcdef/load"

	if rec := e.do(t, reqOpts{method: "POST", path: load, header: read}); rec.Code != http.StatusForbidden {
		t.Fatalf("load with read token: %d %s", rec.Code, rec.Body)
	}

	rec = e.do(t, reqOpts{method: "POST", path: load, cookie: c})
	if rec.Code != http.StatusAccepted || !strings.Contains(rec.Body.String(), `"database":"shop_db"`) || !strings.Contains(rec.Body.String(), `"error":`) {
		t.Fatalf("load without the database: %d %s", rec.Code, rec.Body)
	}

	if _, _, err := e.sites.CreateDatabase(t.Context(), 1, hosting.NewDatabase{Site: "shop", Name: "shop_db"}); err != nil {
		t.Fatal(err)
	}

	rec = e.do(t, reqOpts{method: "POST", path: load, cookie: c})
	if rec.Code != http.StatusAccepted || !strings.Contains(rec.Body.String(), `"asOwner":true`) ||
		!strings.Contains(rec.Body.String(), `"path":"cloudpanel-import-db/shop_db-20261119T120000Z.sql"`) || strings.Contains(rec.Body.String(), `"error":`) {
		t.Fatalf("load: %d %s", rec.Code, rec.Body)
	}

	if rec := e.do(t, reqOpts{method: "GET", path: "/api/v1/migrations/cloudpanel/imports/0123456789abcdef/loads", header: read}); rec.Code != http.StatusOK ||
		!strings.Contains(rec.Body.String(), `"id":"`+stubJob+`"`) {
		t.Fatalf("loads: %d %s", rec.Code, rec.Body)
	}

	if rec := e.do(t, reqOpts{method: "POST", path: "/api/v1/migrations/cloudpanel/imports/fedcba9876543210/load", cookie: c}); rec.Code != http.StatusNotFound {
		t.Fatalf("load unknown import: %d %s", rec.Code, rec.Body)
	}

	var n int
	if err := e.st.RawCount(t.Context(), `SELECT count(*) FROM audit_log WHERE action LIKE ? AND action <> 'cloudpanel.report.upload'`, "cloudpanel.%", &n); err != nil || n != 5 {
		t.Fatalf("audit entries: %d %v", n, err)
	}
}

type cutoverResolver map[string][]netip.Addr

func (r cutoverResolver) Lookup(_ context.Context, host string) ([]netip.Addr, time.Duration, error) {
	if a, ok := r[host]; ok {
		return a, time.Hour, nil
	}

	return nil, 0, cpmigrate.ErrDomainNotFound
}

func TestCutoverOverHTTP(t *testing.T) {
	e := newAPIWithAgent(t, &stubAgent{})
	c := e.adminCookie(t)
	path := "/api/v1/migrations/cloudpanel/sites/blog/cutover"

	if rec := e.do(t, reqOpts{method: "POST", path: "/api/v1/sites", cookie: c, body: `{"name":"blog","domains":["blog.example.org"]}`}); rec.Code != http.StatusCreated {
		t.Fatalf("create: %d %s", rec.Code, rec.Body)
	}

	if rec := e.do(t, reqOpts{method: "GET", path: path, cookie: c}); rec.Code != http.StatusNotFound {
		t.Fatalf("not enabled: %d %s", rec.Code, rec.Body)
	}

	here := netip.MustParseAddr("203.0.113.7")
	res := cutoverResolver{}
	e.cp.EnableCutover(e.sites, res, func(context.Context) ([]netip.Addr, error) { return []netip.Addr{here}, nil })
	e.sites.EnableTLS(stubIssuer{})

	rec := e.do(t, reqOpts{method: "POST", path: path, cookie: c})
	if rec.Code != http.StatusAccepted || !strings.Contains(rec.Body.String(), `"state":"waiting"`) || !strings.Contains(rec.Body.String(), `"server":["203.0.113.7"]`) {
		t.Fatalf("start: %d %s", rec.Code, rec.Body)
	}

	if rec := e.do(t, reqOpts{method: "POST", path: path, cookie: c}); rec.Code != http.StatusConflict || errCode(t, rec) != "cutover_running" {
		t.Fatalf("twice: %d %s", rec.Code, rec.Body)
	}

	for p, want := range map[string]int{
		"/api/v1/migrations/cloudpanel/sites/ghost/cutover": http.StatusConflict,
		"/api/v1/migrations/cloudpanel/sites/Bad/cutover":   http.StatusUnprocessableEntity,
	} {
		if rec := e.do(t, reqOpts{method: "POST", path: p, cookie: c}); rec.Code != want {
			t.Errorf("%s: %d %s", p, rec.Code, rec.Body)
		}
	}

	e.cp.CheckCutovers(t.Context())

	rec = e.do(t, reqOpts{method: "GET", path: path, cookie: c})
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"domain":"blog.example.org","addrs":[],"here":false,"error":"no such domain or no address"`) {
		t.Fatalf("waiting: %d %s", rec.Code, rec.Body)
	}

	res["blog.example.org"] = []netip.Addr{here}

	e.cp.CheckCutovers(t.Context())

	rec = e.do(t, reqOpts{method: "GET", path: path, cookie: c})
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"state":"switched"`) || !strings.Contains(rec.Body.String(), `"here":true,"ttl":3600`) {
		t.Fatalf("switched: %d %s", rec.Code, rec.Body)
	}

	// HTTPS is wanted now; the renewal loop issues the certificate.
	if s, err := e.sites.Get(t.Context(), "blog"); err != nil || !s.TLS {
		t.Fatalf("tls not turned on: %+v %v", s, err)
	}

	// Canceling a finished cut-over changes nothing.
	if rec := e.do(t, reqOpts{method: "DELETE", path: path, cookie: c}); rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"state":"switched"`) {
		t.Fatalf("cancel done: %d %s", rec.Code, rec.Body)
	}

	read := bearerHeader(e.createToken(t, c, `{"name":"cut","scopes":["migrations:read"]}`).Token)
	if rec := e.do(t, reqOpts{method: "POST", path: path, header: read}); rec.Code != http.StatusForbidden {
		t.Fatalf("read scope starts: %d %s", rec.Code, rec.Body)
	}
}
