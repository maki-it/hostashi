// SPDX-License-Identifier: AGPL-3.0-or-later

package web

import "github.com/maki-it/hostashi/internal/agentapi"

// www redirect of a site (ADR-0065): each name that redirects to its
// partner gets its own server blocks, with the site's listeners,
// certificate, logs and headers, the ACME location, and a 301. Snippets,
// maintenance, the disabled page and caches stay in the site's own server,
// which serves the other names.

const wwwSrc = `{{define "www"}}
{{- $s := .S}}
{{- range .WWW}}

# www redirect (ADR-0065): {{.From}} goes to {{.To}}.
server {
    {{- range $.N.Listen}}
    listen {{.}};
    {{- end}}
    server_name {{.From}};
    {{- template "realip" $.RealIP}}
    {{- if $.N.SiteLogDir}}
    access_log {{$.N.SiteLogDir}}/{{$s.User}}/access.log{{if $.RealIP}} hostashi_realip{{end}};
    access_log {{$.N.SiteLogDir}}/{{$s.User}}/traffic.log hostashi_traffic buffer=32k flush=5s;
    error_log {{$.N.SiteLogDir}}/{{$s.User}}/error.log warn;
    {{- end}}
    {{- if not $s.Cert}}
    {{- template "sitehdrs" $.WWWHdrs}}
    {{- end}}
    {{- if $.N.ACMERoot}}

    location ^~ /.well-known/acme-challenge/ {
        root {{$.N.ACMERoot}};
        default_type text/plain;
        try_files $uri =404;
    }
    {{- end}}

    location / {
        return 301 {{if $s.Cert}}https{{else}}http{{end}}://{{.To}}$request_uri;
    }
}
{{- if $s.Cert}}

server {
    {{- range $.N.ListenTLS}}
    listen {{.}} ssl{{if $.N.LegacyHTTP2}} http2{{end}};
    {{- if $.N.HTTP3}}
    listen {{.}} quic;
    {{- end}}
    {{- end}}
    {{- if not $.N.LegacyHTTP2}}
    http2 on;
    {{- end}}
    server_name {{.From}};
    {{- template "realip" $.RealIP}}
    {{- if $.N.SiteLogDir}}
    access_log {{$.N.SiteLogDir}}/{{$s.User}}/access.log{{if $.RealIP}} hostashi_realip{{end}};
    access_log {{$.N.SiteLogDir}}/{{$s.User}}/traffic.log hostashi_traffic buffer=32k flush=5s;
    error_log {{$.N.SiteLogDir}}/{{$s.User}}/error.log warn;
    {{- end}}
    ssl_certificate {{$s.Cert}};
    ssl_certificate_key {{$s.Key}};
    {{- template "sitehdrs" $.WWWHdrs}}

    location / {
        return 301 https://{{.To}}$request_uri;
    }
}
{{- end}}
{{- end}}
{{- end}}`

// wwwData fills "Serve" (the names of the site's own server blocks), "WWW"
// (the names that redirect) and "WWWHdrs" (the headers of their blocks,
// without the page cache's X-Cache).
func (n *Nginx) wwwData(data map[string]any, s Site) {
	serve, redirect := agentapi.SplitWWW(s.Domains, s.WWWRedirect)
	data["Serve"], data["WWW"] = serve, redirect
	data["WWWHdrs"] = &hdrView{Indent: "    ", Lines: n.siteHeaderLines(s, false)}
}

// healthDomain is the name the health check asks for: the first domain
// the site serves itself.
func (s Site) healthDomain() string {
	serve, _ := agentapi.SplitWWW(s.Domains, s.WWWRedirect)
	return serve[0]
}
