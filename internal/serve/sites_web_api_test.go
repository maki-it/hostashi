// SPDX-License-Identifier: AGPL-3.0-or-later

package serve

import (
	"encoding/json"
	"fmt"
	"net/http"
	"reflect"
	"testing"

	"github.com/maki-it/hostashi/internal/agentapi"
)

func TestSiteWebOptionsOverHTTP(t *testing.T) {
	ag := &stubAgent{}
	e := newAPIWithAgent(t, ag)
	c := e.adminCookie(t)

	if rec := e.do(t, reqOpts{method: "POST", path: "/api/v1/sites", cookie: c, body: `{"name":"docs","domains":["docs.example.com"]}`}); rec.Code != http.StatusCreated {
		t.Fatalf("create: %d %s", rec.Code, rec.Body)
	}

	rec := e.do(t, reqOpts{method: "GET", path: "/api/v1/sites/docs", cookie: c})

	var fresh siteJSON
	if err := json.Unmarshal(rec.Body.Bytes(), &fresh); err != nil || fresh.Web.AssetCacheSeconds != 2592000 || len(fresh.Web.AssetCacheExts) != len(agentapi.DefaultAssetCacheExts) {
		t.Fatalf("defaults %+v %v", fresh.Web, err)
	}

	rec = e.do(t, reqOpts{method: "PUT", path: "/api/v1/sites/docs/web", cookie: c, body: `{"dir":"dist","spa":true,"assetCache":true,"assetCacheSeconds":3600,"assetCacheExts":["css","json"]}`})
	if rec.Code != http.StatusOK {
		t.Fatalf("set: %d %s", rec.Code, rec.Body)
	}

	var site siteJSON
	if err := json.Unmarshal(rec.Body.Bytes(), &site); err != nil || site.Type != "static" || !reflect.DeepEqual(site.Web, webJSON{Dir: "dist", SPA: true, AssetCache: true, AssetCacheSeconds: 3600, AssetCacheExts: []string{"css", "json"}}) {
		t.Fatalf("site %+v %v", site, err)
	}

	if ws := ag.web.Sites[0]; ws.WebDir != "dist" || !ws.SPA || !ws.AssetCache || ws.AssetCacheSeconds != 3600 || fmt.Sprint(ws.AssetCacheExts) != "[css json]" {
		t.Fatalf("web state %+v", ws)
	}

	for body, want := range map[string]string{
		`{"dir":"../x","spa":false,"assetCache":false}`: "invalid_argument",
		`{"dir":"/etc","spa":false,"assetCache":false}`: "invalid_argument",
		`{"dir":"dist"}`: "invalid_argument",
		`{"dir":"","spa":false,"assetCache":true,"assetCacheSeconds":10}`:         "invalid_argument",
		`{"dir":"","spa":false,"assetCache":true,"assetCacheExts":[".css"]}`:      "invalid_argument",
		`{"dir":"","spa":false,"assetCache":true,"assetCacheExts":["css","css"]}`: "invalid_argument",
	} {
		if rec := e.do(t, reqOpts{method: "PUT", path: "/api/v1/sites/docs/web", cookie: c, body: body}); rec.Code != http.StatusUnprocessableEntity || errCode(t, rec) != want {
			t.Errorf("%s: %d %s", body, rec.Code, rec.Body)
		}
	}

	// With PHP the folder and the asset cache stay and are sent; SPA is
	// kept but not sent, and cannot be turned on (ADR-0054 addendum 4).
	if rec := e.do(t, reqOpts{method: "PUT", path: "/api/v1/sites/docs/php", cookie: c, body: `{"php":"8.4"}`}); rec.Code != http.StatusOK {
		t.Fatalf("php: %d %s", rec.Code, rec.Body)
	}

	if ws := ag.web.Sites[0]; ws.WebDir != "dist" || ws.SPA || !ws.AssetCache || ws.AssetCacheSeconds != 3600 || ws.PHP != "8.4" {
		t.Fatalf("php web state %+v", ws)
	}

	rec = e.do(t, reqOpts{method: "PUT", path: "/api/v1/sites/docs/web", cookie: c, body: `{"dir":"public","spa":true,"assetCache":false}`})
	if rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("spa with php: %d %s", rec.Code, rec.Body)
	}

	if rec := e.do(t, reqOpts{method: "PUT", path: "/api/v1/sites/docs/web", cookie: c, body: `{"dir":"public","spa":false,"assetCache":false}`}); rec.Code != http.StatusOK || ag.web.Sites[0].WebDir != "public" || ag.web.Sites[0].AssetCache {
		t.Fatalf("php folder: %d %s", rec.Code, rec.Body)
	}

	if rec := e.do(t, reqOpts{method: "PUT", path: "/api/v1/sites/nope/web", cookie: c, body: `{"dir":"","spa":false,"assetCache":false}`}); rec.Code != http.StatusNotFound {
		t.Fatalf("unknown site: %d", rec.Code)
	}
}
