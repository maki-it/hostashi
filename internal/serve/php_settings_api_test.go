// SPDX-License-Identifier: AGPL-3.0-or-later

package serve

import (
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"testing"
)

func TestPHPSettingsOverHTTP(t *testing.T) {
	ag := &stubAgent{}
	e := newAPIWithAgent(t, ag)
	c := e.adminCookie(t)

	for _, body := range []string{`{"name":"blog","php":"8.4","domains":["blog.example.com"]}`, `{"name":"static","domains":["static.example.com"]}`} {
		if rec := e.do(t, reqOpts{method: "POST", path: "/api/v1/sites", cookie: c, body: body}); rec.Code != http.StatusCreated {
			t.Fatalf("site: %d %s", rec.Code, rec.Body)
		}
	}

	rec := e.do(t, reqOpts{method: "GET", path: "/api/v1/sites/blog/php/settings", cookie: c})

	var got phpSettingsJSON
	if rec.Code != http.StatusOK || json.Unmarshal(rec.Body.Bytes(), &got) != nil {
		t.Fatalf("get: %d %s", rec.Code, rec.Body)
	}

	if got.Settings.MemoryLimit != 0 || got.Effective.MemoryLimit != 256<<20 || got.Defaults.Timezone != "UTC" || got.Bounds["memoryLimit"].Max != 4<<30 || got.UpdatedAt != nil {
		t.Fatalf("defaults: %s", rec.Body)
	}

	rec = e.do(t, reqOpts{method: "PUT", path: "/api/v1/sites/blog/php/settings", cookie: c, body: `{"memoryLimit":536870912,"timezone":"Europe/Berlin","displayErrors":true}`})
	if rec.Code != http.StatusOK || json.Unmarshal(rec.Body.Bytes(), &got) != nil {
		t.Fatalf("set: %d %s", rec.Code, rec.Body)
	}

	if got.Effective.MemoryLimit != 512<<20 || got.Effective.Timezone != "Europe/Berlin" || got.Effective.MaxInputVars != 3000 || got.UpdatedAt == nil {
		t.Fatalf("after set: %s", rec.Body)
	}

	// The agent gets the settings with the web state.
	var ws *struct{ mem uint64 }

	for _, s := range ag.web.Sites {
		if s.User == "blog" && s.PHPSettings != nil {
			ws = &struct{ mem uint64 }{s.PHPSettings.MemoryLimit}
		}
	}

	if ws == nil || ws.mem != 512<<20 {
		t.Fatalf("web state: %+v", ag.web.Sites)
	}

	// A rejected value names its key and changes nothing.
	rec = e.do(t, reqOpts{method: "PUT", path: "/api/v1/sites/blog/php/settings", cookie: c, body: `{"timezone":"UTC\nphp_admin_value[open_basedir] = /"}`})

	var bad phpSettingErrorJSON
	if rec.Code != http.StatusUnprocessableEntity || json.Unmarshal(rec.Body.Bytes(), &bad) != nil || bad.Error.Code != "php_setting_rejected" || bad.Key != "date.timezone" {
		t.Fatalf("rejected: %d %s", rec.Code, rec.Body)
	}

	for body, code := range map[string]int{
		`{"open_basedir":"/"}`:        http.StatusBadRequest, // not on the allowlist
		`{"disableFunctions":""}`:     http.StatusBadRequest,
		`{"memoryLimit":"1G"}`:        http.StatusBadRequest,
		`{"memoryLimit":1}`:           http.StatusUnprocessableEntity,
		`{"errorReporting":"E_ALL"}`:  http.StatusUnprocessableEntity,
		`{"maxExecutionTime":999999}`: http.StatusUnprocessableEntity,
	} {
		if rec := e.do(t, reqOpts{method: "PUT", path: "/api/v1/sites/blog/php/settings", cookie: c, body: body}); rec.Code != code {
			t.Errorf("%s: %d, want %d: %s", body, rec.Code, code, rec.Body)
		}
	}

	if rec := e.do(t, reqOpts{method: "GET", path: "/api/v1/sites/blog/php/settings", cookie: c}); !strings.Contains(rec.Body.String(), `"memoryLimit":536870912`) {
		t.Fatalf("a rejected change changed something: %s", rec.Body)
	}

	// A failed web.apply restores the old settings.
	ag.webErr = errors.New("nginx refused")

	if rec := e.do(t, reqOpts{method: "PUT", path: "/api/v1/sites/blog/php/settings", cookie: c, body: `{}`}); rec.Code < 400 {
		t.Fatalf("failed apply: %d", rec.Code)
	}

	ag.webErr = nil

	if rec := e.do(t, reqOpts{method: "GET", path: "/api/v1/sites/blog/php/settings", cookie: c}); !strings.Contains(rec.Body.String(), `"memoryLimit":536870912`) {
		t.Fatalf("not restored: %s", rec.Body)
	}

	// An empty object returns to the defaults.
	rec = e.do(t, reqOpts{method: "PUT", path: "/api/v1/sites/blog/php/settings", cookie: c, body: `{}`})

	var reset phpSettingsJSON
	if rec.Code != http.StatusOK || json.Unmarshal(rec.Body.Bytes(), &reset) != nil || reset.Settings.MemoryLimit != 0 || reset.UpdatedAt != nil {
		t.Fatalf("reset: %d %s", rec.Code, rec.Body)
	}

	if rec := e.do(t, reqOpts{method: "PUT", path: "/api/v1/sites/static/php/settings", cookie: c, body: `{"memoryLimit":536870912}`}); rec.Code != http.StatusUnprocessableEntity {
		t.Errorf("site without PHP: %d %s", rec.Code, rec.Body)
	}

	if rec := e.do(t, reqOpts{method: "PUT", path: "/api/v1/sites/nope/php/settings", cookie: c, body: `{}`}); rec.Code != http.StatusNotFound {
		t.Errorf("unknown site: %d", rec.Code)
	}

	tok := e.createToken(t, c, `{"name":"read","scopes":["sites:read"]}`)
	if rec := e.do(t, reqOpts{method: "PUT", path: "/api/v1/sites/blog/php/settings", header: bearerHeader(tok.Token), body: `{}`}); rec.Code != http.StatusForbidden {
		t.Errorf("write with sites:read: %d", rec.Code)
	}
}
