// SPDX-License-Identifier: AGPL-3.0-or-later

package serve

import "net/http"

// routeMux is an http.ServeMux that remembers every pattern registered on
// it, so a test can check that each route has an authorization entry
// (ADR-0035) and none is forgotten.
type routeMux struct {
	*http.ServeMux

	patterns []string
}

func newRouteMux() *routeMux {
	return &routeMux{ServeMux: http.NewServeMux()}
}

// HandleFunc registers h for pattern, like http.ServeMux.HandleFunc.
func (m *routeMux) HandleFunc(pattern string, h func(http.ResponseWriter, *http.Request)) {
	m.patterns = append(m.patterns, pattern)
	m.ServeMux.HandleFunc(pattern, h)
}

// Handle registers h for pattern, like http.ServeMux.Handle.
func (m *routeMux) Handle(pattern string, h http.Handler) {
	m.patterns = append(m.patterns, pattern)
	m.ServeMux.Handle(pattern, h)
}
