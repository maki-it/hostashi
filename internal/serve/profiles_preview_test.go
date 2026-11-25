// SPDX-License-Identifier: AGPL-3.0-or-later

package serve

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/maki-it/hostashi/internal/agentapi"
	"github.com/maki-it/hostashi/internal/web"
)

func TestSiteProfilePreviewOverHTTP(t *testing.T) {
	ag := &stubAgent{}
	e := newAPIWithAgent(t, ag)
	c := e.adminCookie(t)

	for _, body := range []string{
		`{"name":"blog","php":"8.4","domains":["blog.example.com"]}`,
		`{"name":"docs","domains":["docs.example.com"]}`,
		`{"name":"go","type":"redirect","domains":["go.example.com"],"redirect":{"target":"https://example.com","status":301}}`,
	} {
		if rec := e.do(t, reqOpts{method: "POST", path: "/api/v1/sites", cookie: c, body: body}); rec.Code != http.StatusCreated {
			t.Fatalf("site: %d %s", rec.Code, rec.Body)
		}
	}

	if rec := e.do(t, reqOpts{method: "POST", path: "/api/v1/sites/blog/access/users", cookie: c, body: `{"name":"office","password":"correct horse battery"}`}); rec.Code != http.StatusCreated {
		t.Fatalf("access user: %d %s", rec.Code, rec.Body)
	}

	if rec := e.do(t, reqOpts{method: "PUT", path: "/api/v1/sites/blog/access", cookie: c, body: `{"password":true,"paths":["/wp-admin"]}`}); rec.Code != http.StatusOK {
		t.Fatalf("access: %d %s", rec.Code, rec.Body)
	}

	preview := func(site, body string) (int, string) {
		t.Helper()

		rec := e.do(t, reqOpts{method: "POST", path: "/api/v1/sites/" + site + "/profile/preview", cookie: c, body: body})

		var got siteProfilePreviewJSON
		if rec.Code == http.StatusOK && json.Unmarshal(rec.Body.Bytes(), &got) != nil {
			t.Fatalf("preview: %s", rec.Body)
		}

		return rec.Code, got.Config
	}

	// The site as it is: the same render as the state the agent got.
	code, conf := preview("blog", `{}`)

	var sent agentapi.WebSite

	for _, s := range ag.web.Sites {
		if s.User == "blog" {
			sent = s
		}
	}

	want, err := web.Preview(sent)
	if err != nil || code != http.StatusOK || conf != want {
		t.Fatalf("current: %d %v\n%s", code, err, conf)
	}

	if strings.Contains(conf, "xmlrpc.php") || !strings.Contains(conf, "auth_basic_user_file "+web.PreviewDir+"/auth/") {
		t.Fatalf("current rules:\n%s", conf)
	}

	// No password hash and no user name of the password file.
	if sent.Access == nil || sent.Access.Auth == nil {
		t.Fatalf("no password protection sent: %+v", sent.Access)
	}

	for _, u := range sent.Access.Auth.Users {
		if strings.Contains(conf, u.Hash) || strings.Contains(conf, u.Name) {
			t.Fatalf("preview holds a password user:\n%s", conf)
		}
	}

	// Another profile before saving, with and without changed rules.
	code, conf = preview("blog", `{"profile":"wordpress"}`)
	if code != http.StatusOK || !strings.Contains(conf, "location = /xmlrpc.php") || !strings.Contains(conf, "location = /wp-login.php") || !strings.Contains(conf, "wp-content/uploads") {
		t.Fatalf("wordpress: %d\n%s", code, conf)
	}

	code, conf = preview("blog", `{"profile":"wordpress","hardening":{"configFiles":true,"noPhpDirs":["wp-content/uploads"]}}`)
	if code != http.StatusOK || strings.Contains(conf, "xmlrpc.php") || strings.Contains(conf, "wp-login.php") || !strings.Contains(conf, "wp-content/uploads") {
		t.Fatalf("wordpress changed: %d\n%s", code, conf)
	}

	// Nothing was stored.
	if rec := e.do(t, reqOpts{method: "GET", path: "/api/v1/sites/blog/profile", cookie: c}); !strings.Contains(rec.Body.String(), `"profile":"php"`) {
		t.Fatalf("stored: %s", rec.Body)
	}

	for name, c2 := range map[string]struct {
		site, body string
		code       int
	}{
		"unknown profile":       {"blog", `{"profile":"magento2"}`, http.StatusUnprocessableEntity},
		"PHP profile on static": {"docs", `{"profile":"wordpress"}`, http.StatusUnprocessableEntity},
		"needs PHP":             {"docs", `{"hardening":{"limitFiles":["x.php"]}}`, http.StatusUnprocessableEntity},
		"hostile path":          {"blog", `{"profile":"php","hardening":{"denyFiles":["a;return 200"]}}`, http.StatusUnprocessableEntity},
		"redirect site":         {"go", `{}`, http.StatusConflict},
		"unknown site":          {"none", `{}`, http.StatusNotFound},
		"bad body":              {"blog", `{"profile":1}`, http.StatusBadRequest},
	} {
		if code, _ := preview(c2.site, c2.body); code != c2.code {
			t.Errorf("%s: %d", name, code)
		}
	}
}
