// SPDX-License-Identifier: AGPL-3.0-or-later

package serve

import (
	"net/http"

	"github.com/maki-it/hostashi/api"
	"github.com/maki-it/hostashi/internal/openapi"
)

// healthJSON is the answer of GET /api/v1/health. A degraded panel answers
// 503 with the same body.
type healthJSON struct {
	Status  string `json:"status"` // "ok" or "degraded"
	Agent   string `json:"agent"`  // "ok" or "unavailable"
	DB      string `json:"db"`     // "ok" or "unavailable"
	Version string `json:"version"`
}

// registerOpenAPI serves the OpenAPI document (ADR-0058). It names routes,
// not data, but is only for logged-in callers (Kim, 2026-11-22).
func registerOpenAPI(mux *routeMux, a authn) {
	mux.Op("GET /api/v1/openapi.json", openapi.Operation{
		ID: "getOpenAPI", Tag: "system", Summary: "This OpenAPI document", OutContentType: "application/json",
	}, func(w http.ResponseWriter, r *http.Request) {
		if _, ok := a.principal(w, r); !ok {
			return
		}

		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Cache-Control", "no-cache")
		_, _ = w.Write(api.Spec)
	})
}
