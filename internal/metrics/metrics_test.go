// SPDX-License-Identifier: AGPL-3.0-or-later

package metrics

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	dto "github.com/prometheus/client_model/go"
)

func TestStatusClass(t *testing.T) {
	cases := map[int]string{100: "1xx", 200: "2xx", 204: "2xx", 302: "3xx", 404: "4xx", 499: "4xx", 500: "5xx", 599: "5xx", 0: "5xx", 99: "5xx", 600: "5xx", -1: "5xx"}

	for code, want := range cases {
		if got := statusClass(code); got != want {
			t.Errorf("%d: %q, want %q", code, got, want)
		}
	}
}

func TestInstrumentUsesPatternNotPath(t *testing.T) {
	m := New()

	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/v1/sites/{name}", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusTeapot)
	})
	mux.HandleFunc("GET /api/v1/ok", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("ok")) // implicit 200
	})
	mux.HandleFunc("/", func(http.ResponseWriter, *http.Request) {}) // no write at all

	h := m.Instrument(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/api/v1/nothing") {
			http.NotFound(w, r) // like an API path without a route

			return
		}

		mux.ServeHTTP(w, r)
	}))

	for _, p := range []string{"/api/v1/sites/secret-customer", "/api/v1/sites/other", "/api/v1/ok", "/api/v1/nothing/here", "/dashboard"} {
		h.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, p, nil))
	}

	checks := []struct {
		route, code string
		want        float64
	}{
		{"GET /api/v1/sites/{name}", "4xx", 2},
		{"GET /api/v1/ok", "2xx", 1},
		{RouteUnmatched, "4xx", 1},
		{"/", "2xx", 1},
	}

	for _, c := range checks {
		if got := counterValue(t, m, c.route, c.code); got != c.want {
			t.Errorf("%s %s: %v, want %v", c.route, c.code, got, c.want)
		}
	}

	body, err := m.Exposition()
	if err != nil {
		t.Fatal(err)
	}

	// Raw path segments never become label values.
	for _, leak := range []string{"secret-customer", "nothing/here", "/dashboard"} {
		if strings.Contains(string(body), leak) {
			t.Errorf("exposition contains %q", leak)
		}
	}
}

func TestRouteOfUI(t *testing.T) {
	if got := RouteOf(httptest.NewRequest(http.MethodGet, "/sites/x", nil)); got != RouteUI {
		t.Fatal(got)
	}

	if got := RouteOf(httptest.NewRequest(http.MethodGet, "/api", nil)); got != RouteUnmatched {
		t.Fatal(got)
	}
}

func TestExpositionIsCached(t *testing.T) {
	m := New()
	now := time.Date(2026, 11, 19, 9, 0, 0, 0, time.UTC)
	m.now = func() time.Time { return now }

	first, err := m.Exposition()
	if err != nil {
		t.Fatal(err)
	}

	if !strings.Contains(string(first), "go_goroutines") {
		t.Fatalf("missing series:\n%s", first)
	}

	m.httpRequests.WithLabelValues("GET /x", "2xx").Inc()

	now = now.Add(CacheFor - time.Second)

	again, _ := m.Exposition()
	if strings.Contains(string(again), `route="GET /x"`) {
		t.Fatal("cache not used")
	}

	now = now.Add(time.Second)

	fresh, _ := m.Exposition()
	if !strings.Contains(string(fresh), `hostashi_http_requests_total{code="2xx",route="GET /x"} 1`) {
		t.Fatalf("cache not refreshed:\n%s", fresh)
	}
}

func TestContentType(t *testing.T) {
	if !strings.HasPrefix(ContentType(), "text/plain; version=0.0.4") {
		t.Fatal(ContentType())
	}
}

func counterValue(t *testing.T, m *Metrics, route, code string) float64 {
	t.Helper()

	var d dto.Metric
	if err := m.httpRequests.WithLabelValues(route, code).Write(&d); err != nil {
		t.Fatal(err)
	}

	return d.GetCounter().GetValue()
}
