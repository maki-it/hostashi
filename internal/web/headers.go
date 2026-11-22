// SPDX-License-Identifier: AGPL-3.0-or-later

package web

import (
	"github.com/maki-it/hostashi/internal/agentapi"
	"github.com/maki-it/hostashi/internal/nginxconf"
)

// Response headers of a site (ADR-0056). nginx inherits add_header from the
// server level only into locations without an add_header of their own, so
// the server-level set is one template, "sitehdrs", repeated in every
// Hostashi location that adds headers of its own (today the "location /"
// with a custom snippet that sets add_header). The page locations of
// maintenance and disabled sites keep their own fixed set.

const siteHdrsSrc = `{{define "sitehdrs"}}
{{- range .Lines}}
{{$.Indent}}{{.}}
{{- end}}
{{- end}}`

// hdrView is the server-level header set of a site, rendered with Indent.
type hdrView struct {
	Indent string
	Lines  []string
}

// siteHeaderLines returns the add_header lines that apply to every answer
// of a site: Alt-Svc and HSTS on HTTPS, X-Cache with the page cache, and
// the security headers.
func (n *Nginx) siteHeaderLines(s Site, cache bool) []string {
	var l []string

	if s.Cert != "" {
		if n.HTTP3 {
			l = append(l, `add_header Alt-Svc 'h3=":`+n.TLSPort()+`"; ma=86400' always;`)
		}

		if s.HSTS {
			l = append(l, `add_header Strict-Transport-Security "max-age=31536000" always;`)
		}
	}

	if cache {
		l = append(l, "# HIT, MISS, BYPASS, EXPIRED, STALE or UPDATING on PHP answers.", "add_header X-Cache $upstream_cache_status always;")
	}

	for _, h := range s.Headers.List() {
		l = append(l, headerLine(h))
	}

	return l
}

// headerLine quotes the value with the quote that agentapi.WebHeaders
// refused inside it: single quotes for Permissions-Policy (its origins
// use double quotes), double quotes otherwise (CSP keywords use single
// quotes). The values hold no backslash, "$" or braces.
func headerLine(h agentapi.WebHeader) string {
	q := `"`
	if h.Name == "Permissions-Policy" {
		q = `'`
	}

	return "add_header " + h.Name + " " + q + h.Value + q + " always;"
}

// hideLines returns the fastcgi_hide_header lines that drop the PHP
// application's own copy of every header Hostashi sends, so the answer
// carries Hostashi's value only.
func hideLines(s Site) []string {
	var l []string
	for _, h := range s.Headers.List() {
		l = append(l, "fastcgi_hide_header "+h.Name+";")
	}

	return l
}

// checkHeaders validates the security headers and refuses a snippet that
// sets one of them too: the browser would get two values.
func (s Site) checkHeaders() error {
	if err := s.Headers.Validate(); err != nil {
		return err
	}

	return s.Headers.Conflict(s.Snippets.Server, s.Snippets.Root)
}

// addsHeaders reports whether directives set response headers themselves,
// which stops nginx from inheriting the server-level ones.
func addsHeaders(ds []nginxconf.Directive) bool {
	for _, d := range ds {
		if d.Name == "add_header" {
			return true
		}
	}

	return false
}

// headers fills the template data of a site's headers: "H" at the server
// level, "RootHdrs" inside "location /" when its snippet adds headers,
// "Hide" in the PHP location.
func (n *Nginx) headers(data map[string]any, s Site) {
	c, _ := data["C"].(*cacheView)
	lines := n.siteHeaderLines(s, c != nil)
	data["H"] = &hdrView{Indent: "    ", Lines: lines}
	data["RootHdrs"] = (*hdrView)(nil)

	if s.PHPSocket != "" && !s.Disabled {
		data["Hide"] = hideLines(s)
	}

	if s.Disabled || s.Maintenance != nil || len(lines) == 0 {
		return
	}

	if _, root, err := s.checkSnippets(); err == nil && addsHeaders(root) {
		data["RootHdrs"] = &hdrView{Indent: "        ", Lines: lines}
	}
}

// proxyHideLines drops the target's copy of every header Hostashi adds to
// the answers of a reverse proxy site (ADR-0056), so the visitor gets one.
func proxyHideLines(s Site) []string {
	var l []string

	if s.Cert != "" && s.HSTS {
		l = append(l, "proxy_hide_header Strict-Transport-Security;")
	}

	for _, h := range s.Headers.List() {
		l = append(l, "proxy_hide_header "+h.Name+";")
	}

	return l
}
