// SPDX-License-Identifier: AGPL-3.0-or-later

package serve

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/maki-it/hostashi/internal/agentapi"
	"github.com/maki-it/hostashi/internal/agentrpc"
	"github.com/maki-it/hostashi/internal/cloudpanel/plan"
	"github.com/maki-it/hostashi/internal/cloudpanel/scan"
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
		}
	}

	b, err := json.Marshal(scan.Report{Format: scan.Format, Sites: []scan.Site{
		site("shop", "shop.example.com"), site("blog", "blog.example.org"),
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

func (s *stubImports) Get(_ context.Context, id string) (agentapi.CloudPanelImport, error) {
	if id != "0123456789abcdef" {
		return agentapi.CloudPanelImport{}, &agentrpc.Error{Code: agentrpc.CodeNotFound, Message: "no such import"}
	}

	return agentapi.CloudPanelImport{ID: id, State: agentapi.ImportDone}, nil
}

func TestCloudPanelSitesOverHTTP(t *testing.T) {
	e := newAPIWithAgent(t, &stubAgent{})
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

	if strings.Contains(rec.Body.String(), "s3cr3t") {
		t.Fatalf("snippet in the refusal: %s", rec.Body)
	}

	rec = e.do(t, reqOpts{method: "POST", path: sites + "shop", cookie: c, body: `{"reviewed":true}`})
	if rec.Code != http.StatusCreated {
		t.Fatalf("create: %d %s", rec.Code, rec.Body)
	}

	s, err := e.sites.Get(t.Context(), "shop")
	if err != nil || s.PHP != "8.4" || len(s.Domains) != 1 || s.Domains[0] != "shop.example.com" {
		t.Fatalf("site %+v %v", s, err)
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

	var n int
	if err := e.st.RawCount(t.Context(), `SELECT count(*) FROM audit_log WHERE action LIKE ? AND action <> 'cloudpanel.report.upload'`, "cloudpanel.%", &n); err != nil || n != 2 {
		t.Fatalf("audit entries: %d %v", n, err)
	}
}
