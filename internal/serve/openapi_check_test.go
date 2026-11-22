// SPDX-License-Identifier: AGPL-3.0-or-later

package serve

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"

	"github.com/getkin/kin-openapi/openapi3"
	"github.com/getkin/kin-openapi/openapi3filter"
	"github.com/getkin/kin-openapi/routers"
	"github.com/getkin/kin-openapi/routers/legacy"

	"github.com/maki-it/hostashi/api"
)

// The checked-in OpenAPI document, loaded once for checkResponse.
var (
	specOnce   sync.Once
	specRouter routers.Router
	errSpec    error
)

func specRoutes() (routers.Router, error) {
	specOnce.Do(func() {
		doc, err := openapi3.NewLoader().LoadFromData(api.Spec)
		if err != nil {
			errSpec = err

			return
		}

		// Downloads and streams are checked by content type only.
		for _, ct := range []string{"application/zip", "application/x-ndjson"} {
			if openapi3filter.RegisteredBodyDecoder(ct) == nil {
				openapi3filter.RegisterBodyDecoder(ct, openapi3filter.FileBodyDecoder)
			}
		}

		doc.Servers = openapi3.Servers{{URL: "/"}}
		specRouter, errSpec = legacy.NewRouter(doc)
	})

	return specRouter, errSpec
}

// checkResponse validates an answer of a described operation against
// api/openapi.json (ADR-0058), so a wrong schema fails the feature test
// that produced the answer. Undescribed operations and non-API paths are
// skipped; request bodies are not checked, as tests send wrong ones on
// purpose.
func checkResponse(t *testing.T, r *http.Request, rec *httptest.ResponseRecorder) {
	t.Helper()

	if err := responseMismatch(r, rec); err != nil {
		t.Errorf("%s %s answered %d, which does not match api/openapi.json: %v\n%s", r.Method, r.URL.Path, rec.Code, err, rec.Body)
	}
}

// responseMismatch is checkResponse without the test: nil when the answer
// fits the document or the route is not described.
func responseMismatch(r *http.Request, rec *httptest.ResponseRecorder) error {
	rt, err := specRoutes()
	if err != nil {
		return fmt.Errorf("load api/openapi.json: %w", err)
	}

	route, params, err := rt.FindRoute(r)
	if err != nil || route.Operation == nil || route.Operation.Extensions["x-hostashi-undescribed"] == true {
		return nil //nolint:nilerr // a path outside the document is not checked
	}

	in := &openapi3filter.RequestValidationInput{
		Request: r, PathParams: params, Route: route,
		Options: &openapi3filter.Options{ExcludeRequestBody: true, AuthenticationFunc: openapi3filter.NoopAuthenticationFunc},
	}

	return openapi3filter.ValidateResponse(context.Background(), &openapi3filter.ResponseValidationInput{
		RequestValidationInput: in,
		Status:                 rec.Code,
		Header:                 rec.Header(),
		Body:                   io.NopCloser(bytes.NewReader(rec.Body.Bytes())),
		Options:                &openapi3filter.Options{IncludeResponseStatus: true},
	})
}

func TestResponseMismatch(t *testing.T) {
	for _, c := range []struct {
		name, path, body string
		code             int
		bad              bool
	}{
		{"fits", "/api/v1/users", `{"users":[]}`, http.StatusOK, false},
		{"wrong type", "/api/v1/users", `{"users":"x"}`, http.StatusOK, true},
		{"unknown field", "/api/v1/users", `{"users":[],"extra":1}`, http.StatusOK, true},
		{"error body", "/api/v1/users", `{"error":{"code":"forbidden","message":"no"}}`, http.StatusForbidden, false},
		{"bad error body", "/api/v1/users", `{"oops":1}`, http.StatusForbidden, true},
		{"unknown route", "/api/v1/no-such-route", `"anything"`, http.StatusOK, false},
		{"not the API", "/index.html", `<html>`, http.StatusOK, false},
	} {
		t.Run(c.name, func(t *testing.T) {
			r := httptest.NewRequest(http.MethodGet, c.path, nil)
			rec := httptest.NewRecorder()
			rec.Header().Set("Content-Type", "application/json")
			rec.WriteHeader(c.code)
			_, _ = rec.WriteString(c.body)

			if err := responseMismatch(r, rec); (err != nil) != c.bad {
				t.Errorf("mismatch = %v, want bad=%v", err, c.bad)
			}
		})
	}
}
