// SPDX-License-Identifier: AGPL-3.0-or-later

package serve

import (
	"encoding/json"
	"net/http"
	"reflect"
	"testing"

	"github.com/maki-it/hostashi/internal/agentapi"
)

func TestSiteTypeOverHTTP(t *testing.T) {
	ag := &stubAgent{}
	e := newAPIWithAgent(t, ag)
	c := e.adminCookie(t)
	e.sites.EnableApps(&stubApps{specs: map[string]agentapi.AppSpec{}})

	rec := e.do(t, reqOpts{method: "POST", path: "/api/v1/sites", cookie: c, body: `{"name":"shop","domains":["shop.example.com"],"php":"8.4"}`})
	if rec.Code != http.StatusCreated {
		t.Fatalf("create: %d %s", rec.Code, rec.Body)
	}

	for body, want := range map[string]int{
		`{"type":"php","php":"8.3"}`:                                           http.StatusUnprocessableEntity,
		`{"type":"static","php":"8.3"}`:                                        http.StatusUnprocessableEntity,
		`{"type":"node"}`:                                                      http.StatusUnprocessableEntity,
		`{"type":"node","node":{}}`:                                            http.StatusUnprocessableEntity,
		`{"type":"app","node":{"version":"24"}}`:                               http.StatusUnprocessableEntity,
		`{"type":"node","node":{"version":"24"},"app":{}}`:                     http.StatusUnprocessableEntity,
		`{"type":"node","node":{"version":"22"}}`:                              http.StatusConflict,
		`{"type":"redirect","redirect":{"target":"https://shop.example.com"}}`: http.StatusUnprocessableEntity,
		`{"type":"python"}`:                                                    http.StatusUnprocessableEntity,
	} {
		if rec := e.do(t, reqOpts{method: "POST", path: "/api/v1/sites/shop/type", cookie: c, body: body}); rec.Code != want {
			t.Errorf("%s: %d %s", body, rec.Code, rec.Body)
		}
	}

	if rec := e.do(t, reqOpts{method: "POST", path: "/api/v1/sites/nope/type", cookie: c, body: `{"type":"static"}`}); rec.Code != http.StatusNotFound {
		t.Errorf("unknown site: %d", rec.Code)
	}

	rec = e.do(t, reqOpts{method: "POST", path: "/api/v1/sites/shop/type", cookie: c, body: `{"type":"app","app":{"program":"bin/server","args":["-p","1"]}}`})
	if rec.Code != http.StatusOK {
		t.Fatalf("to app: %d %s", rec.Code, rec.Body)
	}

	var site siteJSON
	if err := json.Unmarshal(rec.Body.Bytes(), &site); err != nil || site.Type != "app" || site.PHP != "" || site.App == nil ||
		!reflect.DeepEqual(*site.App, programAppJSON{Workdir: "htdocs", Program: "bin/server", Args: []string{"-p", "1"}, Port: agentapi.MinAppPort}) {
		t.Fatalf("site %+v %+v %v", site, site.App, err)
	}

	rec = e.do(t, reqOpts{method: "POST", path: "/api/v1/sites/shop/type", cookie: c, body: `{"type":"redirect","redirect":{"target":"https://example.org","status":308}}`})
	if rec.Code != http.StatusOK {
		t.Fatalf("to redirect: %d %s", rec.Code, rec.Body)
	}

	site = siteJSON{}
	if err := json.Unmarshal(rec.Body.Bytes(), &site); err != nil || site.Type != "redirect" || !site.Account || site.App != nil ||
		site.Redirect == nil || site.Redirect.Status != 308 {
		t.Fatalf("site %+v %v", site, err)
	}

	if len(ag.deleted) != 0 {
		t.Fatalf("account removed: %v", ag.deleted)
	}

	rec = e.do(t, reqOpts{method: "DELETE", path: "/api/v1/sites/shop/account", cookie: c})
	if rec.Code != http.StatusOK {
		t.Fatalf("remove account: %d %s", rec.Code, rec.Body)
	}

	site = siteJSON{}
	if err := json.Unmarshal(rec.Body.Bytes(), &site); err != nil || site.Account || site.Type != "redirect" || len(ag.deleted) != 1 {
		t.Fatalf("site %+v %v deleted %v", site, err, ag.deleted)
	}

	if rec := e.do(t, reqOpts{method: "DELETE", path: "/api/v1/sites/shop/account", cookie: c}); rec.Code != http.StatusUnprocessableEntity {
		t.Errorf("second removal: %d %s", rec.Code, rec.Body)
	}
}
