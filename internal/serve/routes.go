// SPDX-License-Identifier: AGPL-3.0-or-later

package serve

import (
	"net/http"

	"github.com/maki-it/hostashi/internal/openapi"
)

// routeMux is an http.ServeMux that remembers every pattern registered on
// it, so a test can check that each route has an authorization entry
// (ADR-0035) and none is forgotten, and its operation record for the
// OpenAPI document (ADR-0058).
type routeMux struct {
	*http.ServeMux

	patterns []string
	ops      map[string]openapi.Operation
}

func newRouteMux() *routeMux {
	return &routeMux{ServeMux: http.NewServeMux(), ops: map[string]openapi.Operation{}}
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

// Op registers h for pattern together with its operation record, which the
// OpenAPI document is built from (ADR-0058). Every new route uses Op.
func (m *routeMux) Op(pattern string, op openapi.Operation, h http.HandlerFunc) {
	m.ops[pattern] = op
	m.HandleFunc(pattern, h)
}
