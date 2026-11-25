// SPDX-License-Identifier: AGPL-3.0-or-later

package serve

import (
	"encoding/json"
	"net/http"
	"reflect"
	"strings"
	"testing"

	"github.com/maki-it/hostashi/internal/agentapi"
)

func webHardeningOf(ag *stubAgent, user string) *agentapi.WebHardening {
	for _, s := range ag.web.Sites {
		if s.User == user {
			return s.Hardening
		}
	}

	return nil
}

func TestProfilesOverHTTP(t *testing.T) {
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

	var list profilesJSON

	rec := e.do(t, reqOpts{method: "GET", path: "/api/v1/profiles", cookie: c})
	if rec.Code != http.StatusOK || json.Unmarshal(rec.Body.Bytes(), &list) != nil || len(list.Profiles) != 9 || list.LimitPerMinute != agentapi.LimitRate {
		t.Fatalf("list: %d %s", rec.Code, rec.Body)
	}

	var got siteProfileJSON

	rec = e.do(t, reqOpts{method: "GET", path: "/api/v1/sites/blog/profile", cookie: c})
	if rec.Code != http.StatusOK || json.Unmarshal(rec.Body.Bytes(), &got) != nil || got.Profile != "php" || got.Changed || !got.Hardening.ConfigFiles || len(got.Choices.Profiles) != 8 {
		t.Fatalf("get: %d %s", rec.Code, rec.Body)
	}

	// WordPress, with xmlrpc.php allowed again.
	got = siteProfileJSON{}

	rec = e.do(t, reqOpts{method: "PUT", path: "/api/v1/sites/blog/profile", cookie: c, body: `{"profile":"wordpress","hardening":{"configFiles":true,"noPhpDirs":["wp-content/uploads"],"limitFiles":["wp-login.php"]}}`})
	if rec.Code != http.StatusOK || json.Unmarshal(rec.Body.Bytes(), &got) != nil || got.Profile != "wordpress" || !got.Changed || len(got.Defaults.DenyFiles) != 1 {
		t.Fatalf("set: %d %s", rec.Code, rec.Body)
	}

	want := &agentapi.WebHardening{ConfigFiles: true, NoPHPDirs: []string{"wp-content/uploads"}, LimitFiles: []string{"wp-login.php"}}
	if h := webHardeningOf(ag, "blog"); !reflect.DeepEqual(h, want) {
		t.Fatalf("web state: %+v", h)
	}

	// Without hardening, the profile's rules.
	rec = e.do(t, reqOpts{method: "PUT", path: "/api/v1/sites/blog/profile", cookie: c, body: `{"profile":"wordpress"}`})
	if rec.Code != http.StatusOK || strings.Contains(rec.Body.String(), `"changed":true`) {
		t.Fatalf("reset: %d %s", rec.Code, rec.Body)
	}

	if h := webHardeningOf(ag, "blog"); h == nil || len(h.DenyFiles) != 1 {
		t.Fatalf("web state after reset: %+v", h)
	}

	for name, c2 := range map[string]struct {
		site, body string
		code       int
	}{
		"static profile on PHP": {"blog", `{"profile":"static"}`, http.StatusUnprocessableEntity},
		"PHP profile on static": {"docs", `{"profile":"wordpress"}`, http.StatusUnprocessableEntity},
		"unknown":               {"blog", `{"profile":"magento2"}`, http.StatusUnprocessableEntity},
		"needs PHP":             {"docs", `{"profile":"static","hardening":{"limitFiles":["x.php"]}}`, http.StatusUnprocessableEntity},
		"hostile path":          {"blog", `{"profile":"php","hardening":{"denyFiles":["a;return 200"]}}`, http.StatusUnprocessableEntity},
		"absolute":              {"blog", `{"profile":"php","hardening":{"denyDirs":["/etc"]}}`, http.StatusUnprocessableEntity},
		"redirect site":         {"go", `{"profile":"php"}`, http.StatusConflict},
	} {
		if rec := e.do(t, reqOpts{method: "PUT", path: "/api/v1/sites/" + c2.site + "/profile", cookie: c, body: c2.body}); rec.Code != c2.code {
			t.Errorf("%s: %d %s", name, rec.Code, rec.Body)
		}
	}

	if rec := e.do(t, reqOpts{method: "GET", path: "/api/v1/sites/go/profile", cookie: c}); rec.Code != http.StatusConflict {
		t.Fatalf("redirect site get: %d", rec.Code)
	}

	if rec := e.do(t, reqOpts{method: "GET", path: "/api/v1/sites/none/profile", cookie: c}); rec.Code != http.StatusNotFound {
		t.Fatalf("unknown site: %d", rec.Code)
	}
}

func TestCreateSiteWithProfile(t *testing.T) {
	ag := &stubAgent{}
	e := newAPIWithAgent(t, ag)
	c := e.adminCookie(t)

	rec := e.do(t, reqOpts{method: "POST", path: "/api/v1/sites", cookie: c, body: `{"name":"app","php":"8.4","domains":["app.example.com"],"profile":"laravel"}`})
	if rec.Code != http.StatusCreated {
		t.Fatalf("create: %d %s", rec.Code, rec.Body)
	}

	for _, s := range ag.web.Sites {
		if s.User == "app" && s.WebDir != "public" {
			t.Fatalf("web dir: %+v", s)
		}
	}

	if rec := e.do(t, reqOpts{method: "GET", path: "/api/v1/sites/app/profile", cookie: c}); !strings.Contains(rec.Body.String(), `"profile":"laravel"`) {
		t.Fatalf("profile: %s", rec.Body)
	}

	for name, body := range map[string]string{
		"old PHP":            `{"name":"old","php":"8.1","domains":["old.example.com"],"profile":"laravel"}`,
		"PHP profile static": `{"name":"st","domains":["st.example.com"],"profile":"wordpress"}`,
		"unknown":            `{"name":"un","php":"8.4","domains":["un.example.com"],"profile":"magento2"}`,
		"redirect":           `{"name":"rd","type":"redirect","domains":["rd.example.com"],"redirect":{"target":"https://example.com"},"profile":"php"}`,
	} {
		if rec := e.do(t, reqOpts{method: "POST", path: "/api/v1/sites", cookie: c, body: body}); rec.Code != http.StatusUnprocessableEntity {
			t.Errorf("%s: %d %s", name, rec.Code, rec.Body)
		}
	}
}
