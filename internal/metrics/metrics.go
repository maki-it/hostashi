// SPDX-License-Identifier: AGPL-3.0-or-later

// Package metrics holds the panel's Prometheus metrics (ADR-0022). Every
// series starts with "hostashi_" and every label is a closed set: never a raw
// path, a user name, an IP address or a domain.
package metrics

import (
	"bytes"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/collectors"
	"github.com/prometheus/common/expfmt"
)

// CacheFor is how long one scrape result is served again, so a fast scrape
// interval or many scrapers cannot drive load (ADR-0022).
const CacheFor = 15 * time.Second

// Route labels for requests that have no registered API pattern.
const (
	RouteUI        = "ui"        // the embedded frontend
	RouteUnmatched = "unmatched" // an API path without a route (404 or 405)
)

// Metrics is the registry of the web process and the series it updates
// itself. Other packages add their series through Registerer.
type Metrics struct {
	reg *prometheus.Registry

	httpRequests *prometheus.CounterVec
	httpDuration *prometheus.HistogramVec

	now func() time.Time

	mu       sync.Mutex
	cached   []byte
	cachedAt time.Time
}

// New returns a registry with the Go runtime and process collectors and the
// panel's HTTP series.
func New() *Metrics {
	m := &Metrics{
		reg: prometheus.NewRegistry(),
		httpRequests: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "hostashi_http_requests_total",
			Help: "Panel HTTP requests by registered route and status class.",
		}, []string{"route", "code"}),
		// Only the route as label, and few buckets, to keep the series count
		// near the ADR's budget of about 300 for the panel.
		httpDuration: prometheus.NewHistogramVec(prometheus.HistogramOpts{
			Name:    "hostashi_http_request_duration_seconds",
			Help:    "Panel HTTP request duration by registered route.",
			Buckets: []float64{0.005, 0.025, 0.1, 0.25, 1, 2.5, 10},
		}, []string{"route"}),
		now: time.Now,
	}

	m.reg.MustRegister(
		collectors.NewGoCollector(),
		collectors.NewProcessCollector(collectors.ProcessCollectorOpts{}),
		m.httpRequests,
		m.httpDuration,
	)

	return m
}

// Registerer is where other packages add their collectors.
func (m *Metrics) Registerer() prometheus.Registerer { return m.reg }

// Gatherer reads the registry (tests).
func (m *Metrics) Gatherer() prometheus.Gatherer { return m.reg }

// statusClass turns a status code into "2xx" and so on. Codes outside
// 100 to 599 count as "5xx", because a handler that writes them is broken.
func statusClass(code int) string {
	if code < 100 || code > 599 {
		return "5xx"
	}

	return [...]string{"1xx", "2xx", "3xx", "4xx", "5xx"}[code/100-1]
}

// RouteOf returns the route label of a request after the mux has seen it:
// the registered pattern for API routes, RouteUI for the frontend and
// RouteUnmatched for an API path without a route.
func RouteOf(r *http.Request) string {
	if r.Pattern != "" {
		return r.Pattern
	}

	if r.URL.Path == "/api" || strings.HasPrefix(r.URL.Path, "/api/") {
		return RouteUnmatched
	}

	return RouteUI
}

// Instrument counts and times every request. It must wrap the handler that
// contains the ServeMux, which sets the request's Pattern in place.
func (m *Metrics) Instrument(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := m.now()
		sw := &statusWriter{ResponseWriter: w}

		next.ServeHTTP(sw, r)

		code := sw.code
		if code == 0 {
			code = http.StatusOK
		}

		route := RouteOf(r)
		m.httpRequests.WithLabelValues(route, statusClass(code)).Inc()
		m.httpDuration.WithLabelValues(route).Observe(m.now().Sub(start).Seconds())
	})
}

type statusWriter struct {
	http.ResponseWriter

	code int
}

func (s *statusWriter) WriteHeader(code int) {
	if s.code == 0 {
		s.code = code
	}

	s.ResponseWriter.WriteHeader(code)
}

func (s *statusWriter) Write(b []byte) (int, error) {
	if s.code == 0 {
		s.code = http.StatusOK
	}

	return s.ResponseWriter.Write(b)
}

// Unwrap lets http.ResponseController reach the real writer.
func (s *statusWriter) Unwrap() http.ResponseWriter { return s.ResponseWriter }

// textFormat is the classic Prometheus text format, which every scraper reads.
var textFormat = expfmt.NewFormat(expfmt.TypeTextPlain)

// Exposition returns the registry in the text format, at most CacheFor old.
func (m *Metrics) Exposition() ([]byte, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	now := m.now()
	if m.cached != nil && now.Sub(m.cachedAt) < CacheFor {
		return m.cached, nil
	}

	mfs, err := m.reg.Gather()
	if err != nil {
		return nil, err
	}

	var buf bytes.Buffer

	enc := expfmt.NewEncoder(&buf, textFormat)
	for _, mf := range mfs {
		if err := enc.Encode(mf); err != nil {
			return nil, err
		}
	}

	m.cached, m.cachedAt = buf.Bytes(), now

	return m.cached, nil
}

// ContentType is the media type of Exposition.
func ContentType() string { return string(textFormat) }
