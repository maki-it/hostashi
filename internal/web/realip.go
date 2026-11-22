// SPDX-License-Identifier: AGPL-3.0-or-later

package web

import "github.com/maki-it/hostashi/internal/agentapi"

// Real client address (ADR-0059). The realip directives go into every
// server block of a site that has the setting on, so nginx replaces
// $remote_addr only for connections from the trusted ranges and only for
// that site. Logs, PHP's REMOTE_ADDR, the X-Real-IP of proxied apps and
// the maintenance allowlist all read $remote_addr. The health check's
// "unix:" peer is never in a trusted range.

const realIPSrc = `{{define "realip"}}
{{- with .}}
    # Client address from trusted proxies only (ADR-0059).
    {{- range .From}}
    set_real_ip_from {{.}};
    {{- end}}
    real_ip_header {{.Header}};
    {{- if .Recursive}}
    real_ip_recursive on;
    {{- end}}
{{- end}}
{{- end}}`

// realIPView is the rendered form of a site's setting; every value comes
// from a parsed prefix or the fixed header list.
type realIPView struct {
	From      []string
	Header    string
	Recursive bool
}

func newRealIPView(r agentapi.WebRealIP) *realIPView {
	if r.IsZero() {
		return nil
	}

	v := &realIPView{Header: r.HeaderName(), Recursive: r.Recursive()}
	for _, p := range r.Prefixes() {
		v.From = append(v.From, p.String())
	}

	return v
}
