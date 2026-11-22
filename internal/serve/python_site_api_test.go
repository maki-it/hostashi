// SPDX-License-Identifier: AGPL-3.0-or-later

package serve

import (
	"encoding/json"
	"net/http"
	"reflect"
	"slices"
	"testing"

	"github.com/maki-it/hostashi/internal/agentapi"
)

func TestPythonSiteOverHTTP(t *testing.T) {
	ag := &stubAgent{}
	e := newAPIWithAgent(t, ag)
	c := e.adminCookie(t)

	apps := &stubApps{specs: map[string]agentapi.AppSpec{}}
	e.sites.EnableApps(apps)

	for body, want := range map[string]int{
		`{"name":"shop","domains":["shop.example.com"],"type":"python"}`:                                                                                              http.StatusUnprocessableEntity,
		`{"name":"shop","domains":["shop.example.com"],"python":{"version":"3.13"}}`:                                                                                  http.StatusUnprocessableEntity,
		`{"name":"shop","domains":["shop.example.com"],"type":"python","python":{}}`:                                                                                  http.StatusUnprocessableEntity,
		`{"name":"shop","domains":["shop.example.com"],"type":"python","python":{"version":"3.13"},"node":{"version":"24"}}`:                                          http.StatusUnprocessableEntity,
		`{"name":"shop","domains":["shop.example.com"],"type":"python","python":{"version":"3.12"}}`:                                                                  http.StatusConflict,
		`{"name":"shop","domains":["shop.example.com"],"type":"python","python":{"version":"3.13","startKind":"wsgi","target":"x"}}`:                                  http.StatusUnprocessableEntity,
		`{"name":"shop","domains":["shop.example.com"],"type":"python","python":{"version":"3.13","startKind":"wsgi","target":"a:b","args":["--bind","0.0.0.0:80"]}}`: http.StatusUnprocessableEntity,
	} {
		if rec := e.do(t, reqOpts{method: "POST", path: "/api/v1/sites", cookie: c, body: body}); rec.Code != want {
			t.Errorf("%s: %d %s", body, rec.Code, rec.Body)
		}
	}

	rec := e.do(t, reqOpts{method: "POST", path: "/api/v1/sites", cookie: c, body: `{"name":"shop","domains":["shop.example.com"],"type":"python","python":{"version":"3.13"}}`})
	if rec.Code != http.StatusCreated {
		t.Fatalf("create: %d %s", rec.Code, rec.Body)
	}

	var site siteJSON
	if err := json.Unmarshal(rec.Body.Bytes(), &site); err != nil || site.Type != "python" || !site.Account || site.Python == nil || site.Node != nil || site.App != nil ||
		!reflect.DeepEqual(*site.Python, pythonAppJSON{Version: "3.13", Workdir: "htdocs", Args: []string{}, Port: agentapi.MinAppPort}) {
		t.Fatalf("site %+v %+v %v", site, site.Python, err)
	}

	rec = e.do(t, reqOpts{method: "PUT", path: "/api/v1/sites/shop/python", cookie: c, body: `{"version":"3.13","startKind":"asgi","target":"main:app","args":["--workers","2"]}`})
	if rec.Code != http.StatusOK {
		t.Fatalf("set: %d %s", rec.Code, rec.Body)
	}

	if a := apps.specs["shop"]; a.Python != "3.13" || a.StartKind != agentapi.AppStartASGI || a.Start != "main:app" || !slices.Equal(a.Args, []string{"--workers", "2"}) || a.Stopped {
		t.Errorf("spec %+v", a)
	}

	// A new version needs the site's name, then rebuilds .venv.
	if rec := e.do(t, reqOpts{method: "PUT", path: "/api/v1/sites/shop/python", cookie: c, body: `{"version":"3.14","startKind":"asgi","target":"main:app"}`}); rec.Code != http.StatusUnprocessableEntity {
		t.Errorf("without confirm: %d %s", rec.Code, rec.Body)
	}

	if rec := e.do(t, reqOpts{method: "PUT", path: "/api/v1/sites/shop/python", cookie: c, body: `{"version":"3.14","startKind":"asgi","target":"main:app","confirm":"shop"}`}); rec.Code != http.StatusOK {
		t.Errorf("with confirm: %d %s", rec.Code, rec.Body)
	}

	if !slices.Equal(apps.venvs, []agentapi.PythonVenvRequest{{Site: "shop", Python: "3.14", Clear: true}}) {
		t.Errorf("venvs %v", apps.venvs)
	}

	for body, want := range map[string]int{`{}`: http.StatusUnprocessableEntity, `{"confirm":"Shop"}`: http.StatusUnprocessableEntity, `{"confirm":"shop"}`: http.StatusOK} {
		if rec := e.do(t, reqOpts{method: "POST", path: "/api/v1/sites/shop/python/venv", cookie: c, body: body}); rec.Code != want {
			t.Errorf("venv %s: %d %s", body, rec.Code, rec.Body)
		}
	}

	if len(apps.venvs) != 2 {
		t.Errorf("venvs %v", apps.venvs)
	}

	// Not for other types.
	if rec := e.do(t, reqOpts{method: "POST", path: "/api/v1/sites", cookie: c, body: `{"name":"docs","domains":["docs.example.com"]}`}); rec.Code != http.StatusCreated {
		t.Fatalf("static: %d %s", rec.Code, rec.Body)
	}

	if rec := e.do(t, reqOpts{method: "PUT", path: "/api/v1/sites/docs/python", cookie: c, body: `{"version":"3.13"}`}); rec.Code != http.StatusConflict {
		t.Errorf("python of a static site: %d %s", rec.Code, rec.Body)
	}

	if rec := e.do(t, reqOpts{method: "POST", path: "/api/v1/sites/docs/python/venv", cookie: c, body: `{"confirm":"docs"}`}); rec.Code != http.StatusConflict {
		t.Errorf("venv of a static site: %d %s", rec.Code, rec.Body)
	}

	// The type change takes python.
	rec = e.do(t, reqOpts{method: "POST", path: "/api/v1/sites/docs/type", cookie: c, body: `{"type":"python","python":{"version":"3.13","startKind":"script","target":"run.py"}}`})
	if rec.Code != http.StatusOK {
		t.Fatalf("type: %d %s", rec.Code, rec.Body)
	}

	if rec := e.do(t, reqOpts{method: "POST", path: "/api/v1/sites/docs/type", cookie: c, body: `{"type":"node","python":{"version":"3.13"}}`}); rec.Code != http.StatusUnprocessableEntity {
		t.Errorf("python for node: %d %s", rec.Code, rec.Body)
	}
}
