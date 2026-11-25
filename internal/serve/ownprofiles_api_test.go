// SPDX-License-Identifier: AGPL-3.0-or-later

package serve

import (
	"encoding/json"
	"net/http"
	"reflect"
	"strconv"
	"strings"
	"testing"

	"github.com/maki-it/hostashi/internal/agentapi"
	"github.com/maki-it/hostashi/internal/profile"
)

const agencyBody = `{"name":"agency","title":"Agency WordPress","type":"php","minPhp":"8.2","webDir":"public",` +
	`"hardening":{"configFiles":true,"noPhpDirs":["wp-content/uploads"],"denyFiles":["xmlrpc.php"]},"pageCache":"wordpress",` +
	`"cron":[{"schedule":"*/5 * * * *","command":"php wp-cron.php"}],"checklist":["Check the uploads folder."]}`

func TestOwnProfilesOverHTTP(t *testing.T) {
	ag := &stubAgent{}
	e := newAPIWithAgent(t, ag)
	c := e.adminCookie(t)

	rec := e.do(t, reqOpts{method: "POST", path: "/api/v1/profiles", cookie: c, body: agencyBody})
	if rec.Code != http.StatusCreated || !strings.Contains(rec.Body.String(), `"builtin":false`) {
		t.Fatalf("create: %d %s", rec.Code, rec.Body)
	}

	if rec := e.do(t, reqOpts{method: "POST", path: "/api/v1/profiles", cookie: c, body: agencyBody}); rec.Code != http.StatusConflict {
		t.Fatalf("twice: %d %s", rec.Code, rec.Body)
	}

	// The list marks built-in and own profiles.
	var list profilesJSON

	rec = e.do(t, reqOpts{method: "GET", path: "/api/v1/profiles", cookie: c})
	if rec.Code != http.StatusOK || json.Unmarshal(rec.Body.Bytes(), &list) != nil || len(list.Profiles) != 10 {
		t.Fatalf("list: %d %s", rec.Code, rec.Body)
	}

	if last := list.Profiles[9]; last.Name != "agency" || last.Builtin || !list.Profiles[0].Builtin || last.WebDir != "public" {
		t.Fatalf("own in list: %+v", list.Profiles)
	}

	// A site created with it follows it, and the Hardening tab offers it.
	rec = e.do(t, reqOpts{method: "POST", path: "/api/v1/sites", cookie: c, body: `{"name":"blog","php":"8.4","domains":["blog.example.com"],"profile":"agency"}`})
	if rec.Code != http.StatusCreated {
		t.Fatalf("site: %d %s", rec.Code, rec.Body)
	}

	var sp siteProfileJSON

	rec = e.do(t, reqOpts{method: "GET", path: "/api/v1/sites/blog/profile", cookie: c})
	if rec.Code != http.StatusOK || json.Unmarshal(rec.Body.Bytes(), &sp) != nil || sp.Profile != "agency" || len(sp.Choices.Profiles) != 9 {
		t.Fatalf("site profile: %d %s", rec.Code, rec.Body)
	}

	// An edit reaches the site.
	edited := strings.Replace(agencyBody, `"denyFiles":["xmlrpc.php"]`, `"denyFiles":["xmlrpc.php","readme.html"]`, 1)
	if rec := e.do(t, reqOpts{method: "PUT", path: "/api/v1/profiles/agency", cookie: c, body: edited}); rec.Code != http.StatusOK {
		t.Fatalf("update: %d %s", rec.Code, rec.Body)
	}

	want := &agentapi.WebHardening{ConfigFiles: true, NoPHPDirs: []string{"wp-content/uploads"}, DenyFiles: []string{"xmlrpc.php", "readme.html"}}
	if h := webHardeningOf(ag, "blog"); !reflect.DeepEqual(h, want) {
		t.Fatalf("web state: %+v", h)
	}

	if d := e.lastAuditDiff(t, c, "profile.update"); d["hardening"].New == nil {
		t.Fatalf("audit: %+v", d)
	}

	// Export and import again under another name: the same values.
	rec = e.do(t, reqOpts{method: "GET", path: "/api/v1/profiles/agency/yaml", cookie: c})
	if rec.Code != http.StatusOK || !strings.HasPrefix(rec.Header().Get("Content-Type"), "application/yaml") ||
		rec.Header().Get("Content-Disposition") != `attachment; filename="hostashi-profile-agency.yaml"` || !strings.HasPrefix(rec.Body.String(), "version: 1\n") {
		t.Fatalf("export: %d %v %s", rec.Code, rec.Header(), rec.Body)
	}

	yaml := strings.Replace(rec.Body.String(), "name: agency", "name: agency-copy", 1)
	body, _ := json.Marshal(profileImportIn{YAML: yaml})

	var imported profile.Profile

	rec = e.do(t, reqOpts{method: "POST", path: "/api/v1/profiles/import", cookie: c, body: string(body)})
	if rec.Code != http.StatusCreated || json.Unmarshal(rec.Body.Bytes(), &imported) != nil {
		t.Fatalf("import: %d %s", rec.Code, rec.Body)
	}

	orig := list.Profiles[9]
	orig.Name, orig.Hardening.DenyFiles = "agency-copy", want.DenyFiles

	if !reflect.DeepEqual(imported, orig) {
		t.Fatalf("round trip:\n got %+v\nwant %+v", imported, orig)
	}

	if rec := e.do(t, reqOpts{method: "GET", path: "/api/v1/profiles/wordpress/yaml", cookie: c}); rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "name: wordpress") {
		t.Fatalf("export built-in: %d %s", rec.Code, rec.Body)
	}

	if rec := e.do(t, reqOpts{method: "GET", path: "/api/v1/profiles/nope/yaml", cookie: c}); rec.Code != http.StatusNotFound {
		t.Fatalf("export missing: %d", rec.Code)
	}

	// Delete waits for the sites.
	rec = e.do(t, reqOpts{method: "DELETE", path: "/api/v1/profiles/agency", cookie: c})
	if rec.Code != http.StatusConflict || !strings.Contains(rec.Body.String(), "profile_in_use") || !strings.Contains(rec.Body.String(), "blog") {
		t.Fatalf("delete in use: %d %s", rec.Code, rec.Body)
	}

	if rec := e.do(t, reqOpts{method: "PUT", path: "/api/v1/sites/blog/profile", cookie: c, body: `{"profile":"php"}`}); rec.Code != http.StatusOK {
		t.Fatalf("back to php: %d %s", rec.Code, rec.Body)
	}

	if rec := e.do(t, reqOpts{method: "DELETE", path: "/api/v1/profiles/agency", cookie: c}); rec.Code != http.StatusNoContent {
		t.Fatalf("delete: %d %s", rec.Code, rec.Body)
	}

	if n := e.auditCount(t, "profile.delete"); n != 1 {
		t.Fatalf("delete audit: %d", n)
	}
}

func TestOwnProfileRefusals(t *testing.T) {
	e := newAPIWithAgent(t, &stubAgent{})
	c := e.adminCookie(t)

	if rec := e.do(t, reqOpts{method: "POST", path: "/api/v1/profiles", cookie: c, body: agencyBody}); rec.Code != http.StatusCreated {
		t.Fatalf("create: %d %s", rec.Code, rec.Body)
	}

	set := func(field, value string) string {
		var m map[string]any
		if err := json.Unmarshal([]byte(agencyBody), &m); err != nil {
			t.Fatal(err)
		}

		var v any
		if err := json.Unmarshal([]byte(value), &v); err != nil {
			t.Fatal(err)
		}

		m["name"], m[field] = "other", v

		b, _ := json.Marshal(m)

		return string(b)
	}

	for name, c2 := range map[string]struct {
		method, path, body string
		code               int
	}{
		"built-in name":     {"POST", "/api/v1/profiles", strings.Replace(agencyBody, `"agency"`, `"wordpress"`, 1), http.StatusUnprocessableEntity},
		"bad name":          {"POST", "/api/v1/profiles", strings.Replace(agencyBody, `"agency"`, `"../x"`, 1), http.StatusUnprocessableEntity},
		"long name":         {"POST", "/api/v1/profiles", strings.Replace(agencyBody, `"agency"`, strconv.Quote(strings.Repeat("a", 41)), 1), http.StatusUnprocessableEntity},
		"nginx field":       {"POST", "/api/v1/profiles", strings.Replace(agencyBody, `"type"`, `"nginx":"location / {}","type"`, 1), http.StatusBadRequest},
		"builtin flag":      {"POST", "/api/v1/profiles", strings.Replace(agencyBody, `"type"`, `"builtin":true,"type"`, 1), http.StatusBadRequest},
		"nginx in web dir":  {"POST", "/api/v1/profiles", set("webDir", `"public; root /etc"`), http.StatusUnprocessableEntity},
		"nginx in path":     {"POST", "/api/v1/profiles", set("hardening", `{"denyFiles":["x { allow all; }"]}`), http.StatusUnprocessableEntity},
		"min php":           {"POST", "/api/v1/profiles", set("minPhp", `"eight"`), http.StatusUnprocessableEntity},
		"type":              {"POST", "/api/v1/profiles", set("type", `"proxy"`), http.StatusUnprocessableEntity},
		"page cache":        {"POST", "/api/v1/profiles", set("pageCache", `"all"`), http.StatusUnprocessableEntity},
		"long checklist":    {"POST", "/api/v1/profiles", set("checklist", `["`+strings.Repeat("x", 301)+`"]`), http.StatusUnprocessableEntity},
		"rename":            {"PUT", "/api/v1/profiles/agency", strings.Replace(agencyBody, `"agency"`, `"renamed"`, 1), http.StatusUnprocessableEntity},
		"change built-in":   {"PUT", "/api/v1/profiles/wordpress", strings.Replace(agencyBody, `"agency"`, `"wordpress"`, 1), http.StatusConflict},
		"change missing":    {"PUT", "/api/v1/profiles/nope", strings.Replace(agencyBody, `"agency"`, `"nope"`, 1), http.StatusNotFound},
		"delete built-in":   {"DELETE", "/api/v1/profiles/wordpress", "", http.StatusConflict},
		"delete missing":    {"DELETE", "/api/v1/profiles/nope", "", http.StatusNotFound},
		"import version 2":  {"POST", "/api/v1/profiles/import", `{"yaml":"version: 2\nname: x\ntitle: X\ntype: php\nhardening: {}\n"}`, http.StatusUnprocessableEntity},
		"import no version": {"POST", "/api/v1/profiles/import", `{"yaml":"name: x\ntitle: X\ntype: php\nhardening: {}\n"}`, http.StatusUnprocessableEntity},
		"import nginx":      {"POST", "/api/v1/profiles/import", `{"yaml":"version: 1\nname: x\ntitle: X\ntype: php\nhardening: {}\nnginx: |\n  location / { root /; }\n"}`, http.StatusUnprocessableEntity},
		"import nested":     {"POST", "/api/v1/profiles/import", `{"yaml":"version: 1\nname: x\ntitle: X\ntype: php\nhardening:\n  snippet: deny all;\n"}`, http.StatusUnprocessableEntity},
		"import bad path":   {"POST", "/api/v1/profiles/import", `{"yaml":"version: 1\nname: x\ntitle: X\ntype: php\nhardening:\n  denyDirs: [\"/etc\"]\n"}`, http.StatusUnprocessableEntity},
		"import built-in":   {"POST", "/api/v1/profiles/import", `{"yaml":"version: 1\nname: laravel\ntitle: X\ntype: php\nhardening: {}\n"}`, http.StatusUnprocessableEntity},
		"import extra key":  {"POST", "/api/v1/profiles/import", `{"yaml":"x","more":1}`, http.StatusBadRequest},
	} {
		if rec := e.do(t, reqOpts{method: c2.method, path: c2.path, cookie: c, body: c2.body}); rec.Code != c2.code {
			t.Errorf("%s: %d %s", name, rec.Code, rec.Body)
		}
	}

	if n := e.auditCount(t, "profile.import"); n != 0 {
		t.Fatalf("refused imports audited: %d", n)
	}
}

// Only admins change profiles; everyone may list them. A token needs the
// settings:write scope.
func TestOwnProfilesAccess(t *testing.T) {
	e := newAPIWithAgent(t, &stubAgent{})
	c := e.adminCookie(t)
	manager, _ := matrixLogin(t, e, c, "mona", "manager")
	read := e.createToken(t, c, `{"name":"read","scopes":["sites:read"]}`)
	write := e.createToken(t, c, `{"name":"write","scopes":["settings:write"]}`)

	for _, r := range []reqOpts{
		{method: "POST", path: "/api/v1/profiles", body: agencyBody},
		{method: "POST", path: "/api/v1/profiles/import", body: `{"yaml":"version: 1\n"}`},
		{method: "PUT", path: "/api/v1/profiles/agency", body: agencyBody},
		{method: "DELETE", path: "/api/v1/profiles/agency"},
	} {
		r.cookie = manager
		if rec := e.do(t, r); rec.Code != http.StatusForbidden {
			t.Errorf("manager %s %s: %d", r.method, r.path, rec.Code)
		}

		r.cookie, r.header = nil, bearerHeader(read.Token)
		if rec := e.do(t, r); rec.Code != http.StatusForbidden {
			t.Errorf("read token %s %s: %d", r.method, r.path, rec.Code)
		}
	}

	if rec := e.do(t, reqOpts{method: "GET", path: "/api/v1/profiles", cookie: manager}); rec.Code != http.StatusOK {
		t.Errorf("manager list: %d", rec.Code)
	}

	if rec := e.do(t, reqOpts{method: "POST", path: "/api/v1/profiles", header: bearerHeader(write.Token), body: agencyBody}); rec.Code != http.StatusCreated {
		t.Errorf("write token: %d %s", rec.Code, rec.Body)
	}

	if rec := e.do(t, reqOpts{method: "GET", path: "/api/v1/profiles/agency/yaml", header: bearerHeader(read.Token)}); rec.Code != http.StatusOK {
		t.Errorf("read token export: %d %s", rec.Code, rec.Body)
	}
}
