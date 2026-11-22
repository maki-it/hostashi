// SPDX-License-Identifier: AGPL-3.0-or-later

package serve

import (
	"bytes"
	"context"
	"encoding/json"
	"flag"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/getkin/kin-openapi/openapi3"

	"github.com/maki-it/hostashi/api"
	"github.com/maki-it/hostashi/internal/authz"
	"github.com/maki-it/hostashi/internal/openapi"
)

var updateOpenAPI = flag.Bool("update", false, "rewrite api/openapi.json from the route records")

// openAPIDocument builds the OpenAPI document from every /api/v1 route of
// the full mux, with its access from the route matrix (ADR-0035).
func openAPIDocument(t *testing.T) []byte {
	t.Helper()

	_, mux := newFullAPIMux(t)

	var routes []openapi.Route

	for _, p := range mux.patterns {
		if _, path, _ := strings.Cut(p, " "); !strings.HasPrefix(path, "/api/v1/") {
			continue // /metrics is Prometheus text, not part of the API
		}

		rt := openapi.Route{Pattern: p, Access: openAPIAccess(routeAccess[p])}
		if op, ok := mux.ops[p]; ok {
			rt.Op = &op
		}

		routes = append(routes, rt)
	}

	openapi.SortRoutes(routes)

	doc, err := openapi.Build(openapi.Info{
		Title:   "Hostashi API",
		Version: "v1",
		Description: "The API of one Hostashi panel. The panel and the hostashi CLI use it as well (ADR-0018, ADR-0058). " +
			"Operations marked x-hostashi-undescribed have no schema yet.",
		SessionCookie: sessionCookie,
		Error:         apiError{},
	}, routes)
	if err != nil {
		t.Fatal(err)
	}

	return doc
}

func openAPIAccess(a authz.Action) openapi.Access {
	if a == accessPublic || a == accessSession || a == accessAny {
		return openapi.Access{Kind: string(a)}
	}

	return openapi.Access{Kind: "action", Action: string(a), Scope: authz.ScopeOf(a)}
}

// TestOpenAPIDocument fails when api/openapi.json is not what the route
// records produce. Regenerate it with
//
//	go test ./internal/serve -run TestOpenAPIDocument -update
func TestOpenAPIDocument(t *testing.T) {
	doc := openAPIDocument(t)

	if *updateOpenAPI {
		if err := os.WriteFile(filepath.Join("..", "..", "api", "openapi.json"), doc, 0o644); err != nil {
			t.Fatal(err)
		}

		return
	}

	if !bytes.Equal(doc, api.Spec) {
		t.Fatal("api/openapi.json is out of date; run: go test ./internal/serve -run TestOpenAPIDocument -update")
	}
}

// TestOpenAPIDescribed fails for an API route without an operation record:
// every route is in the document with its schemas, so the CLI can reach it
// (ADR-0058). Register API routes with mux.Op.
func TestOpenAPIDescribed(t *testing.T) {
	_, mux := newFullAPIMux(t)

	for _, p := range mux.patterns {
		if _, ok := mux.ops[p]; !ok && strings.Contains(p, "/api/v1/") {
			t.Errorf("%s has no operation record; register it with mux.Op", p)
		}
	}
}

func TestOpenAPIServed(t *testing.T) {
	e, _ := newFullAPI(t)

	if rec := e.do(t, reqOpts{method: http.MethodGet, path: "/api/v1/openapi.json"}); rec.Code != http.StatusUnauthorized {
		t.Fatalf("without login: %d, want 401", rec.Code)
	}

	c := e.adminCookie(t)

	rec := e.do(t, reqOpts{method: http.MethodGet, path: "/api/v1/openapi.json", cookie: c})
	if rec.Code != http.StatusOK || rec.Header().Get("Content-Type") != "application/json" {
		t.Fatalf("with login: %d %q", rec.Code, rec.Header().Get("Content-Type"))
	}

	var doc struct {
		OpenAPI string         `json:"openapi"`
		Paths   map[string]any `json:"paths"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &doc); err != nil || doc.OpenAPI != "3.1.0" || doc.Paths["/api/v1/openapi.json"] == nil {
		t.Fatalf("document: %v %q", err, doc.OpenAPI)
	}
}

// TestOpenAPIValid checks the document against the OpenAPI 3.1 rules.
func TestOpenAPIValid(t *testing.T) {
	doc, err := openapi3.NewLoader().LoadFromData(openAPIDocument(t))
	if err != nil {
		t.Fatal(err)
	}

	if err := doc.Validate(context.Background()); err != nil {
		t.Fatal(err)
	}
}
