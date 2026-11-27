// SPDX-License-Identifier: AGPL-3.0-or-later

package serve

import (
	"net/http"
	"strings"
	"testing"
)

func TestColorSchemePerUser(t *testing.T) {
	e := newAPI(t)
	c := e.adminCookie(t)

	rec := e.do(t, reqOpts{method: "GET", path: "/api/v1/auth/me", cookie: c})
	if !strings.Contains(rec.Body.String(), `"colorScheme":"system","theme":"default","language":"auto"`) {
		t.Fatalf("default: %s", rec.Body)
	}

	rec = e.do(t, reqOpts{method: "PATCH", path: "/api/v1/me", cookie: c, body: `{"colorScheme":"dark"}`})
	if rec.Code != http.StatusOK || strings.TrimSpace(rec.Body.String()) != `{"colorScheme":"dark","theme":"default","language":"auto"}` {
		t.Fatalf("patch: %d %s", rec.Code, rec.Body)
	}

	rec = e.do(t, reqOpts{method: "GET", path: "/api/v1/auth/me", cookie: c})
	if !strings.Contains(rec.Body.String(), `"colorScheme":"dark"`) {
		t.Fatalf("stored: %s", rec.Body)
	}

	// The language changes on its own and keeps the colour scheme (ADR-0071).
	rec = e.do(t, reqOpts{method: "PATCH", path: "/api/v1/me", cookie: c, body: `{"language":"de"}`})
	if rec.Code != http.StatusOK || strings.TrimSpace(rec.Body.String()) != `{"colorScheme":"dark","theme":"default","language":"de"}` {
		t.Fatalf("language: %d %s", rec.Code, rec.Body)
	}

	rec = e.do(t, reqOpts{method: "GET", path: "/api/v1/auth/me", cookie: c})
	if !strings.Contains(rec.Body.String(), `"language":"de"`) {
		t.Fatalf("stored language: %s", rec.Body)
	}

	for _, body := range []string{`{"colorScheme":"blue"}`, `{"theme":"konsole"}`, `{"language":"fr"}`, `{"language":"DE"}`} {
		rec = e.do(t, reqOpts{method: "PATCH", path: "/api/v1/me", cookie: c, body: body})
		if rec.Code != http.StatusUnprocessableEntity || errCode(t, rec) != "invalid_argument" {
			t.Fatalf("%s: %d %s", body, rec.Code, rec.Body)
		}
	}

	rec = e.do(t, reqOpts{method: "PATCH", path: "/api/v1/me", cookie: c, body: `{"colorScheme":"light","font":"comic"}`})
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("unknown field: %d", rec.Code)
	}

	// An API token cannot change it.
	tok := e.createToken(t, c, `{"name":"ci","scopes":["sites:read"]}`)

	rec = e.do(t, reqOpts{method: "PATCH", path: "/api/v1/me", header: bearerHeader(tok.Token), body: `{"colorScheme":"light"}`})
	if rec.Code != http.StatusForbidden || errCode(t, rec) != "session_required" {
		t.Fatalf("token: %d %s", rec.Code, rec.Body)
	}

	rec = e.do(t, reqOpts{method: "PATCH", path: "/api/v1/me", body: `{"colorScheme":"light"}`})
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("anonymous: %d", rec.Code)
	}
}
