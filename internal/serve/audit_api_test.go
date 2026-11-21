// SPDX-License-Identifier: AGPL-3.0-or-later

package serve

import (
	"context"
	"encoding/csv"
	"encoding/json"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"testing"
)

func (e *apiEnv) auditPage(t *testing.T, c *http.Cookie, query string) auditPageJSON {
	t.Helper()

	rec := e.do(t, reqOpts{method: "GET", path: "/api/v1/audit?" + query, cookie: c})
	if rec.Code != http.StatusOK {
		t.Fatalf("audit %q: %d %s", query, rec.Code, rec.Body)
	}

	if rec.Header().Get("Cache-Control") != "no-store" {
		t.Error("the audit log must not be cached")
	}

	var out auditPageJSON
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}

	return out
}

func TestAuditList(t *testing.T) {
	e := newAPI(t)

	if rec := e.do(t, reqOpts{method: "GET", path: "/api/v1/audit"}); rec.Code != http.StatusUnauthorized {
		t.Fatalf("anonymous: %d", rec.Code)
	}

	c := e.adminCookie(t)
	ctx := context.Background()

	for _, target := range []string{"shop", "blog", "Shop-2"} {
		if err := e.st.Audit(ctx, nil, "site.create", target, map[string]string{"php": "8.4"}); err != nil {
			t.Fatal(err)
		}
	}

	all := e.auditPage(t, c, "")
	if len(all.Entries) < 4 || all.Next != nil {
		t.Fatalf("all: %+v", all)
	}

	// The newest entry comes first; setup and login wrote theirs before.
	if top := all.Entries[0]; top.Action != "site.create" || top.Target != "Shop-2" || string(top.Detail) != `{"php":"8.4"}` || top.UserID != nil {
		t.Errorf("newest: %+v", top)
	}

	sites := e.auditPage(t, c, "action=site&target=shop")
	if len(sites.Entries) != 2 || sites.Entries[0].Target != "Shop-2" || sites.Entries[1].Target != "shop" {
		t.Errorf("filtered: %+v", sites.Entries)
	}

	// Paging walks every entry once.
	seen := map[int64]bool{}
	q := "limit=1&action=site"

	for range 5 {
		p := e.auditPage(t, c, q)
		for _, x := range p.Entries {
			if seen[x.ID] {
				t.Fatalf("entry %d twice", x.ID)
			}

			seen[x.ID] = true
		}

		if p.Next == nil {
			break
		}

		q = "limit=1&action=site&before=" + *p.Next
	}

	if len(seen) != 3 {
		t.Errorf("paged %d entries, want 3", len(seen))
	}

	kim := e.auditPage(t, c, "actor=kim")
	for _, x := range kim.Entries {
		if x.Actor != "kim" || x.UserID == nil {
			t.Errorf("actor filter: %+v", x)
		}
	}

	// Entries written while serving a request carry its address; the
	// login of adminCookie came from the test client.
	byIP := e.auditPage(t, c, "ip=192.0.2.10&action=login")
	if len(byIP.Entries) == 0 || byIP.Entries[0].IP != "192.0.2.10" || string(byIP.Entries[0].Diff) != "{}" {
		t.Errorf("ip filter: %+v", byIP.Entries)
	}

	if other := e.auditPage(t, c, "ip=::ffff:198.51.100.1"); len(other.Entries) != 0 {
		t.Errorf("other ip: %+v", other.Entries)
	}

	none := e.auditPage(t, c, "since=2099-01-01T00:00:00Z")
	if none.Entries == nil || len(none.Entries) != 0 {
		t.Errorf("future: %+v", none)
	}

	for _, q := range []string{
		"limit=0", "limit=201", "limit=x", "before=0", "before=-1",
		"action=Site", "action=site.", "action=site%25", "action=" + strings.Repeat("a", 129),
		"actor=" + strings.Repeat("a", 65), "actor=a%0Ab", "target=" + strings.Repeat("a", 201), "target=%00",
		"since=yesterday", "ip=192.0.2", "ip=host", "until=2026-11-21", "since=2026-11-21T00:00:00Z&until=2026-11-20T00:00:00Z",
		"since=2026-11-21T00:00:00Z&until=2026-11-21T00:00:00Z",
	} {
		rec := e.do(t, reqOpts{method: "GET", path: "/api/v1/audit?" + q, cookie: c})
		if rec.Code != http.StatusBadRequest || errCode(t, rec) != "invalid_query" {
			t.Errorf("%s: %d %s", q, rec.Code, rec.Body)
		}
	}

	rec := e.do(t, reqOpts{method: "GET", path: "/api/v1/audit/actions", cookie: c})

	var acts struct {
		Actions []string `json:"actions"`
	}
	if rec.Code != http.StatusOK || json.Unmarshal(rec.Body.Bytes(), &acts) != nil || !strings.Contains(strings.Join(acts.Actions, " "), "site.create") {
		t.Errorf("actions: %d %s", rec.Code, rec.Body)
	}

	// A token needs audit:read.
	other := e.createToken(t, c, `{"name":"o","scopes":["doctor:read"],"expiresInDays":1}`)
	if rec := e.do(t, reqOpts{method: "GET", path: "/api/v1/audit", header: bearerHeader(other.Token)}); rec.Code != http.StatusForbidden {
		t.Errorf("doctor:read: %d", rec.Code)
	}

	good := e.createToken(t, c, `{"name":"a","scopes":["audit:read"],"expiresInDays":1}`)
	if rec := e.do(t, reqOpts{method: "GET", path: "/api/v1/audit", header: bearerHeader(good.Token)}); rec.Code != http.StatusOK {
		t.Errorf("audit:read: %d", rec.Code)
	}
}

func TestAuditExport(t *testing.T) {
	e := newAPI(t)
	c := e.adminCookie(t)
	ctx := context.Background()

	// A crafted username of a failed login must not become a formula.
	if err := e.st.Audit(ctx, nil, "login.failed", "=HYPERLINK(\"http://x\")", map[string]string{"ip": "192.0.2.7"}); err != nil {
		t.Fatal(err)
	}

	if err := e.st.Audit(ctx, nil, "site.create", "shop,\"quoted\"\nline", nil); err != nil {
		t.Fatal(err)
	}

	rec := e.do(t, reqOpts{method: "GET", path: "/api/v1/audit/export?format=csv", cookie: c})
	if rec.Code != http.StatusOK || rec.Header().Get("Content-Type") != "text/csv; charset=utf-8" ||
		!strings.HasPrefix(rec.Header().Get("Content-Disposition"), `attachment; filename="hostashi-audit-`) ||
		!strings.HasSuffix(rec.Header().Get("Content-Disposition"), `.csv"`) || rec.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("csv: %d %v", rec.Code, rec.Header())
	}

	rows, err := csv.NewReader(strings.NewReader(rec.Body.String())).ReadAll()
	if err != nil {
		t.Fatal(err)
	}

	if strings.Join(rows[0], ",") != "id,at,user_id,actor,ip,action,target,detail,diff" {
		t.Errorf("header: %v", rows[0])
	}

	byAction := map[string][]string{}
	for _, r := range rows[1:] {
		byAction[r[5]] = r
	}

	if r := byAction["login.failed"]; r == nil || r[6] != `'=HYPERLINK("http://x")` || r[7] != `{"ip":"192.0.2.7"}` || r[2] != "" || r[4] != "" || r[8] != "{}" {
		t.Errorf("failed login row: %q", r)
	}

	if r := byAction["site.create"]; r == nil || r[6] != "shop,\"quoted\"\nline" {
		t.Errorf("quoted row: %q", r)
	}

	// The export wrote itself to the log, before its rows: it is the last
	// row of the file, with the filters.
	if r := rows[len(rows)-1]; r[5] != "audit.export" || r[3] != "kim" || r[4] != "192.0.2.10" || !strings.Contains(r[7], `"format":"csv"`) {
		t.Errorf("export entry: %q", r)
	}

	// Oldest first.
	if first, _ := strconv.Atoi(rows[1][0]); first != 1 {
		t.Errorf("order: first row is entry %s", rows[1][0])
	}

	q := url.Values{"format": {"json"}, "action": {"site"}}
	rec = e.do(t, reqOpts{method: "GET", path: "/api/v1/audit/export?" + q.Encode(), cookie: c})

	var entries []auditEntryJSON
	if rec.Code != http.StatusOK || rec.Header().Get("Content-Type") != "application/json" || json.Unmarshal(rec.Body.Bytes(), &entries) != nil {
		t.Fatalf("json: %d %s", rec.Code, rec.Body)
	}

	if len(entries) != 1 || entries[0].Target != "shop,\"quoted\"\nline" || entries[0].At.IsZero() {
		t.Errorf("json entries: %+v", entries)
	}

	// An empty export is still valid JSON.
	q.Set("action", "nothing")
	rec = e.do(t, reqOpts{method: "GET", path: "/api/v1/audit/export?" + q.Encode(), cookie: c})

	if err := json.Unmarshal(rec.Body.Bytes(), &entries); err != nil || len(entries) != 0 {
		t.Errorf("empty json: %v %s", err, rec.Body)
	}

	page := e.auditPage(t, c, "action=audit.export")
	if len(page.Entries) != 3 || !strings.Contains(string(page.Entries[0].Detail), `"action":"nothing"`) {
		t.Errorf("export entries: %+v", page.Entries)
	}

	for _, q := range []string{"", "format=xml", "format=csv&limit=0x", "format=csv&action=X"} {
		rec := e.do(t, reqOpts{method: "GET", path: "/api/v1/audit/export?" + q, cookie: c})
		if strings.Contains(q, "limit") {
			// Paging does not apply to the export.
			if rec.Code != http.StatusOK {
				t.Errorf("%s: %d", q, rec.Code)
			}

			continue
		}

		if rec.Code != http.StatusBadRequest || errCode(t, rec) != "invalid_query" {
			t.Errorf("%s: %d %s", q, rec.Code, rec.Body)
		}
	}
}

func TestCSVCell(t *testing.T) {
	for in, want := range map[string]string{
		"": "", "shop": "shop", "=1+1": "'=1+1", "+1": "'+1", "-1": "'-1", "@SUM": "'@SUM",
		"\tx": "'\tx", "\rx": "'\rx", "a=b": "a=b", "{\"a\":1}": "{\"a\":1}",
	} {
		if got := csvCell(in); got != want {
			t.Errorf("%q: %q, want %q", in, got, want)
		}
	}
}
