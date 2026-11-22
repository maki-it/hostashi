// SPDX-License-Identifier: AGPL-3.0-or-later

package web

import (
	"bytes"
	"context"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"text/template"
	"time"

	"github.com/maki-it/hostashi/internal/agentapi"
	"github.com/maki-it/hostashi/internal/nginxconf"
	"github.com/maki-it/hostashi/internal/snippet"
)

// Controller runs nginx. The production one (package webctl) uses the
// distribution's binary and systemd unit; tests start nginx themselves.
type Controller interface {
	// Test runs nginx -t on the config file conf without touching the server.
	Test(ctx context.Context, conf string) error
	// Reload makes the running server re-read its config file.
	Reload(ctx context.Context) error
	// Restart starts the server on the "current" config, or restarts it if it
	// runs a config that is not ours.
	Restart(ctx context.Context) error
}

// Files is what the driver renders for one generation. Content depends only
// on the desired state and is hashed for idempotency; Meta holds the
// generation number and is not part of the hash.
type Files struct {
	Content map[string][]byte
	Meta    map[string][]byte
}

// controlHost is the server_name of the control endpoint. The underscore
// makes it impossible as a site domain (agentapi.ValidateDomain).
const controlHost = "_hostashi_control"

// Nginx renders a self-contained config per generation. All includes of
// generated files are relative to nginx.conf, so the same bytes are tested
// in generations/<n> and served through the "current" symlink (spike S2).
type Nginx struct {
	Ctl Controller

	// Listen are the HTTP listen addresses of every site, e.g. "80" and
	// "[::]:80".
	Listen []string
	// ControlSocket is a Unix socket that every server block also listens
	// on. The agent reads the live generation and checks the sites through
	// it, so it needs no TCP access.
	ControlSocket string
	// User is the worker user; empty leaves the user directive out (tests).
	User      string
	PIDFile   string
	ErrorLog  string
	AccessLog string // empty: access_log off
	// SiteLogDir holds one directory per site, <SiteLogDir>/<site>/ with
	// access.log and error.log (ADR-0021, ADR-0053). The directories are
	// root:root 0750, so a site cannot write fake lines into them. Empty
	// keeps every site on ErrorLog and AccessLog (tests).
	SiteLogDir string
	// TempDir replaces the compiled-in temp paths (tests); empty keeps them.
	TempDir string
	// Modules is an include glob for load_module files, empty for none.
	Modules string
	// MimeTypes is the mime.types file of the nginx package.
	MimeTypes string
	// Brotli turns on the brotli filter and static modules (ADR-0020).
	Brotli bool

	// ListenTLS are the HTTPS listen addresses, e.g. "443" and "[::]:443";
	// empty serves no site over HTTPS.
	ListenTLS []string
	// ACMERoot holds /.well-known/acme-challenge/<token> files of HTTP-01
	// challenges; every site answers them on port 80. Empty: none.
	ACMERoot string
	// LegacyHTTP2 writes "listen ... ssl http2" instead of "http2 on" for
	// nginx before 1.25.1 (tests on older distributions only).
	LegacyHTTP2 bool
	// HTTP3 adds QUIC listeners (UDP) on the ListenTLS addresses and
	// announces them with Alt-Svc. It needs nginx built with
	// http_v3_module (1.25 and later).
	HTTP3 bool

	// PanelListen are the HTTPS addresses of the panel (ADR-0023), e.g.
	// "8443" and "[::]:8443"; empty serves no panel. The same addresses as
	// ListenTLS put the panel on the sites' port by host name (ADR-0042).
	PanelListen []string
	// PanelUpstream is the panel's Unix socket that nginx proxies to.
	PanelUpstream string

	// DBToolListen are the HTTPS addresses of the web database tool
	// (ADR-0038), served for the panel's name with its certificate; empty
	// serves no tool.
	DBToolListen []string
	// DBToolSocket is the FastCGI socket of the tool's PHP-FPM.
	DBToolSocket string
	// DBToolRoot holds the tool's index.php.
	DBToolRoot string

	VerifyTimeout time.Duration
}

var (
	listenRe   = regexp.MustCompile(`^((\[[0-9a-f:]+\]|[0-9.]+):)?[0-9]{1,5}$`)
	confPathRe = regexp.MustCompile(`^/[A-Za-z0-9_./*-]+$`)
)

func (n *Nginx) check() error {
	if n.Ctl == nil || len(n.Listen) == 0 {
		return errors.New("web: nginx driver needs a controller and listen addresses")
	}

	for _, l := range slices.Concat(n.Listen, n.ListenTLS, n.PanelListen, n.DBToolListen) {
		if !listenRe.MatchString(l) {
			return fmt.Errorf("web: invalid listen address %q", l)
		}
	}

	for _, p := range []string{n.ControlSocket, n.PIDFile, n.ErrorLog, n.MimeTypes} {
		if !confPathRe.MatchString(p) {
			return fmt.Errorf("web: invalid path %q", p)
		}
	}

	if len(n.PanelListen) > 0 && !confPathRe.MatchString(n.PanelUpstream) {
		return fmt.Errorf("web: invalid panel upstream %q", n.PanelUpstream)
	}

	if len(n.DBToolListen) > 0 && (!confPathRe.MatchString(n.DBToolSocket) || !confPathRe.MatchString(n.DBToolRoot)) {
		return fmt.Errorf("web: invalid database tool socket %q or root %q", n.DBToolSocket, n.DBToolRoot)
	}

	for _, p := range []string{n.AccessLog, n.SiteLogDir, n.TempDir, n.Modules, n.ACMERoot} {
		if p != "" && !confPathRe.MatchString(p) {
			return fmt.Errorf("web: invalid path %q", p)
		}
	}

	// On the sites' TLS port the panel shares their listeners; a partial
	// overlap would bind a port twice with different options.
	for _, l := range n.PanelListen {
		if slices.Contains(n.Listen, l) {
			return fmt.Errorf("web: panel address %q is an HTTP address of the sites", l)
		}
	}

	if len(n.PanelListen) > 0 && n.PanelPort() == n.TLSPort() && !slices.Equal(n.PanelListen, n.ListenTLS) {
		return errors.New("web: a panel on the sites' TLS port must use exactly their addresses")
	}

	if n.HTTP3 && len(n.ListenTLS) == 0 {
		return errors.New("web: HTTP/3 needs HTTPS listen addresses")
	}

	if n.User != "" && !regexp.MustCompile(`^[a-z_][a-z0-9_-]{0,31}$`).MatchString(n.User) {
		return fmt.Errorf("web: invalid worker user %q", n.User)
	}

	return nil
}

// TLSPort is the port of the first HTTPS address, for Alt-Svc.
func (n *Nginx) TLSPort() string { return firstPort(n.ListenTLS) }

// DBToolPort is the port of the first database tool address.
func (n *Nginx) DBToolPort() string { return firstPort(n.DBToolListen) }

// PanelPort is the port of the first panel address.
func (n *Nginx) PanelPort() string { return firstPort(n.PanelListen) }

// PanelShared reports whether the panel is served on the sites' TLS
// listeners (ADR-0042) instead of its own port.
func (n *Nginx) PanelShared() bool {
	return len(n.PanelListen) > 0 && slices.Equal(n.PanelListen, n.ListenTLS)
}

func firstPort(addrs []string) string {
	if len(addrs) == 0 {
		return ""
	}

	l := addrs[0]

	return l[strings.LastIndex(l, ":")+1:]
}

const compressTypes = "text/plain text/css text/xml text/javascript application/javascript application/json " +
	"application/xml application/rss+xml application/atom+xml application/manifest+json image/svg+xml " +
	"font/ttf font/otf application/vnd.ms-fontobject"

// PageCSP is the Content-Security-Policy of maintenance and parking pages
// (ADR-0027): the page's own files and inline code, nothing from other hosts.
const PageCSP = "default-src 'self'; script-src 'self' 'unsafe-inline'; style-src 'self' 'unsafe-inline'; " +
	"img-src 'self' data:; font-src 'self' data:; form-action 'none'; frame-ancestors 'none'; base-uri 'self'"

// pageTypes are the lines of the types block of a page location: the MIME
// type without parameters (charset adds them) and its extensions.
var pageTypes = func() []string {
	exts := map[string][]string{}

	for ext, ct := range agentapi.PageTypes {
		mime, _, _ := strings.Cut(ct, ";")
		exts[mime] = append(exts[mime], ext)
	}

	out := make([]string, 0, len(exts))
	for mime, e := range exts {
		slices.Sort(e)
		out = append(out, mime+" "+strings.Join(e, " "))
	}

	slices.Sort(out)

	return out
}()

var mainTmpl = template.Must(template.New("main").Parse(`# Generated by hostashi. Do not edit: changes are overwritten.
{{- if .N.User}}
user {{.N.User}};
{{- end}}
worker_processes auto;
pid {{.N.PIDFile}};
error_log {{.N.ErrorLog}};
{{- if .N.Modules}}
include {{.N.Modules}};
{{- end}}

events {
    worker_connections 1024;
}

http {
    include {{.N.MimeTypes}};
    # Retry-After of maintenance pages: only on their 503 answers.
    map $status $hostashi_retry_after {
        503 3600;
        default "";
    }
    # Page cache (ADR-0046): requests that never use the cache of any site.
    map $request_method $hostashi_cache_method {
        GET 0;
        HEAD 0;
        default 1;
    }
    map $http_authorization $hostashi_cache_auth {
        "" 0;
        default 1;
    }
    default_type application/octet-stream;
    sendfile on;
    tcp_nopush on;
    server_tokens off;
    {{- if .N.AccessLog}}
    access_log {{.N.AccessLog}};
    {{- else}}
    access_log off;
    {{- end}}
    {{- if .N.TempDir}}
    client_body_temp_path {{.N.TempDir}}/body;
    proxy_temp_path {{.N.TempDir}}/proxy;
    fastcgi_temp_path {{.N.TempDir}}/fastcgi;
    uwsgi_temp_path {{.N.TempDir}}/uwsgi;
    scgi_temp_path {{.N.TempDir}}/scgi;
    {{- end}}

    gzip on;
    gzip_vary on;
    gzip_proxied any;
    gzip_comp_level 5;
    gzip_min_length 256;
    gzip_types {{.Types}};
    {{- if .N.Brotli}}
    brotli on;
    brotli_static on;
    brotli_comp_level 5;
    brotli_min_length 256;
    brotli_types {{.Types}};
    {{- end}}

    {{- if .N.ListenTLS}}

    # TLS settings (Mozilla "intermediate"). Certificates are per site.
    ssl_protocols TLSv1.2 TLSv1.3;
    ssl_ciphers ECDHE-ECDSA-AES128-GCM-SHA256:ECDHE-RSA-AES128-GCM-SHA256:ECDHE-ECDSA-AES256-GCM-SHA384:ECDHE-RSA-AES256-GCM-SHA384:ECDHE-ECDSA-CHACHA20-POLY1305:ECDHE-RSA-CHACHA20-POLY1305;
    ssl_prefer_server_ciphers off;
    ssl_session_cache shared:hostashi_tls:10m;
    ssl_session_timeout 1d;
    ssl_session_tickets off;
    {{- end}}

    # Unknown host names get no answer.
    server {
        {{- range .N.Listen}}
        listen {{.}} default_server;
        {{- end}}
        listen unix:{{.N.ControlSocket}} default_server;
        server_name _;
        return 444;
    }
    {{- if .N.ListenTLS}}

    # Unknown host names over TLS: the handshake is refused, no certificate
    # of another site is shown.
    server {
        {{- range .N.ListenTLS}}
        listen {{.}} ssl default_server;
        {{- if $.N.HTTP3}}
        listen {{.}} quic reuseport default_server;
        {{- end}}
        {{- end}}
        server_name _;
        ssl_reject_handshake on;
    }
    {{- end}}

    # Control endpoint, reachable only through the agent's Unix socket.
    server {
        listen unix:{{.N.ControlSocket}};
        server_name {{.Control}};
        location = /.hostashi/generation {
            default_type text/plain;
            include generation.conf;
        }
        location / {
            return 404;
        }
    }

    include sites/*.conf;
}
`))

// panelTmpl serves the panel's address (ADR-0023): ACME challenges and a
// redirect on port 80, HTTPS on the panel port proxied to the panel's
// socket. On the sites' port (ADR-0042) the sites' default server refuses
// unknown names, so the panel's own one is left out. X-Real-IP is the only client address the panel trusts, and only
// from this socket.
var panelTmpl = template.Must(template.New("panel").Parse(`# Generated by hostashi for the panel. Do not edit.
server {
    {{- range .N.Listen}}
    listen {{.}};
    {{- end}}
    server_name {{.P.Domain}};
    {{- if .N.ACMERoot}}

    location ^~ /.well-known/acme-challenge/ {
        root {{.N.ACMERoot}};
        default_type text/plain;
        try_files $uri =404;
    }
    {{- end}}

    location / {
        {{- if .P.Cert}}
        return 301 https://$host{{if not .N.PanelShared}}:{{.N.PanelPort}}{{end}}$request_uri;
        {{- else}}
        return 404;
        {{- end}}
    }
}
{{- if .P.Cert}}
{{- if not .N.PanelShared}}

# Other names on the panel port get no certificate.
server {
    {{- range .N.PanelListen}}
    listen {{.}} ssl default_server;
    {{- if $.N.HTTP3}}
    listen {{.}} quic reuseport default_server;
    {{- end}}
    {{- end}}
    server_name _;
    ssl_reject_handshake on;
}
{{- end}}

server {
    {{- range .N.PanelListen}}
    listen {{.}} ssl{{if $.N.LegacyHTTP2}} http2{{end}};
    {{- if $.N.HTTP3}}
    listen {{.}} quic;
    {{- end}}
    {{- end}}
    {{- if not .N.LegacyHTTP2}}
    http2 on;
    {{- end}}
    server_name {{.P.Domain}};
    ssl_certificate {{.P.Cert}};
    ssl_certificate_key {{.P.Key}};
    {{- if .N.HTTP3}}
    add_header Alt-Svc 'h3=":{{.N.PanelPort}}"; ma=86400' always;
    {{- end}}
    add_header Strict-Transport-Security "max-age=31536000" always;
    client_max_body_size 16m;
    {{- if .P.Allow}}

    # The panel allowlist (ADR-0042): on the sites' port the firewall
    # cannot apply it.
    {{- range .P.Allow}}
    allow {{.}};
    {{- end}}
    deny all;
    {{- end}}

    location / {
        proxy_pass http://unix:{{.N.PanelUpstream}}:;
        proxy_http_version 1.1;
        proxy_set_header Host $http_host;
        proxy_set_header Connection "";
        proxy_set_header X-Real-IP $remote_addr;
        proxy_set_header X-Forwarded-For "";
        proxy_set_header X-Forwarded-Proto https;
        proxy_read_timeout 60s;
    }
}
{{- if and .P.DBTool .N.DBToolListen}}

# The web database tool (ADR-0038): Adminer on a port of its own, so it is
# another origin than the panel, with the panel's name and certificate.
# Only "/" reaches PHP; index.php takes a one-time ticket or its own
# session. adminer.css and its fonts are the only files served as they are.
server {
    {{- range .N.DBToolListen}}
    listen {{.}} ssl default_server;
    {{- end}}
    server_name _;
    ssl_reject_handshake on;
}

server {
    {{- range .N.DBToolListen}}
    listen {{.}} ssl{{if $.N.LegacyHTTP2}} http2{{end}};
    {{- end}}
    {{- if not .N.LegacyHTTP2}}
    http2 on;
    {{- end}}
    server_name {{.P.Domain}};
    ssl_certificate {{.P.Cert}};
    ssl_certificate_key {{.P.Key}};
    add_header Strict-Transport-Security "max-age=31536000" always;
    add_header Referrer-Policy no-referrer always;
    add_header X-Content-Type-Options nosniff always;
    add_header X-Frame-Options DENY always;
    client_max_body_size 64m;
    {{- if .P.Allow}}

    # The panel allowlist also covers the tool; the firewall applies it
    # to the tool port as well.
    {{- range .P.Allow}}
    allow {{.}};
    {{- end}}
    deny all;
    {{- end}}

    location = / {
        root {{.N.DBToolRoot}};
        fastcgi_pass unix:{{.N.DBToolSocket}};
        fastcgi_param SCRIPT_FILENAME $realpath_root/index.php;
        fastcgi_param DOCUMENT_ROOT $realpath_root;
        fastcgi_param SCRIPT_NAME /index.php;
        fastcgi_param QUERY_STRING $query_string;
        fastcgi_param REQUEST_METHOD $request_method;
        fastcgi_param CONTENT_TYPE $content_type;
        fastcgi_param CONTENT_LENGTH $content_length;
        fastcgi_param REQUEST_URI $request_uri;
        fastcgi_param DOCUMENT_URI /index.php;
        fastcgi_param SERVER_PROTOCOL $server_protocol;
        fastcgi_param REQUEST_SCHEME https;
        fastcgi_param HTTPS on;
        fastcgi_param GATEWAY_INTERFACE CGI/1.1;
        fastcgi_param SERVER_SOFTWARE nginx;
        fastcgi_param REMOTE_ADDR $remote_addr;
        fastcgi_param REMOTE_PORT $remote_port;
        fastcgi_param SERVER_ADDR $server_addr;
        fastcgi_param SERVER_PORT $server_port;
        fastcgi_param SERVER_NAME $server_name;
        fastcgi_param REDIRECT_STATUS 200;
        # Headers that would change what PHP sees as the request.
        fastcgi_param HTTP_PROXY "";
        fastcgi_param HTTP_X_FORWARDED_PREFIX "";
        fastcgi_read_timeout 300s;
    }

    # The tool's look (adminer.css and its fonts); nothing else in the
    # directory is served as a file.
    location ~ "^/(adminer\.css|fonts/[a-z0-9-]+\.woff2)$" {
        root {{.N.DBToolRoot}};
        try_files $uri =404;
    }

    location / {
        return 404;
    }
}
{{- end}}
{{- end}}
`))

// parkTmpl serves a parked domain (ADR-0027): its page with status 200 on
// every path, ACME challenges on port 80, HTTPS with its own certificate.
var parkTmpl = withPageLoc("park", `# Generated by hostashi for the parked domain {{.P.Domain}}. Do not edit.
server {
    {{- range .N.Listen}}
    listen {{.}};
    {{- end}}
    server_name {{.P.Domain}};
    {{- if .N.ACMERoot}}

    location ^~ /.well-known/acme-challenge/ {
        root {{.N.ACMERoot}};
        default_type text/plain;
        try_files $uri =404;
    }
    {{- end}}
    {{- if .P.Cert}}

    location / {
        return 301 https://$host$request_uri;
    }
}

server {
    {{- range .N.ListenTLS}}
    listen {{.}} ssl{{if $.N.LegacyHTTP2}} http2{{end}};
    {{- if $.N.HTTP3}}
    listen {{.}} quic;
    {{- end}}
    {{- end}}
    {{- if not .N.LegacyHTTP2}}
    http2 on;
    {{- end}}
    server_name {{.P.Domain}};
    ssl_certificate {{.P.Cert}};
    ssl_certificate_key {{.P.Key}};
    {{- end}}
    {{- template "pageloc" .PL}}

    location / {
        rewrite ^ {{.PL.Prefix}}{{.PL.Page}}/index.html last;
    }
}
`)

// pageLoc is the location of a page's files (ADR-0027) for pageLocSrc.
type pageLoc struct {
	Prefix, Page, Dir string
	Types             []string
	CSP               string
	AltSvc            string // port for Alt-Svc, empty for none
	HSTS              bool
	Retry             bool // Retry-After on the 503 answers (maintenance)
}

const pageLocSrc = `{{define "pageloc"}}
    location ^~ {{.Prefix}}{{.Page}}/ {
        alias {{.Dir}}/;
        index index.html;
        types {
            {{- range .Types}}
            {{.}};
            {{- end}}
        }
        default_type application/octet-stream;
        charset utf-8;
        charset_types text/css text/javascript image/svg+xml;
        add_header Content-Security-Policy "{{.CSP}}" always;
        add_header X-Content-Type-Options nosniff always;
        add_header Cache-Control no-store always;
        {{- if .Retry}}
        add_header Retry-After $hostashi_retry_after always;
        {{- end}}
        {{- if .AltSvc}}
        add_header Alt-Svc 'h3=":{{.AltSvc}}"; ma=86400' always;
        {{- end}}
        {{- if .HSTS}}
        add_header Strict-Transport-Security "max-age=31536000" always;
        {{- end}}
    }
{{- end}}`

// withPageLoc parses src and adds the "pageloc" template to it.
func withPageLoc(name, src string) *template.Template {
	return template.Must(template.Must(template.New(name).Parse(src)).Parse(pageLocSrc))
}

func (n *Nginx) pageLoc(page, dir string, tls, hsts, retry bool) pageLoc {
	pl := pageLoc{Prefix: agentapi.PagePathPrefix, Page: page, Dir: dir, Types: pageTypes, CSP: PageCSP, Retry: retry}
	if tls {
		pl.HSTS = hsts

		if n.HTTP3 {
			pl.AltSvc = n.TLSPort()
		}
	}

	return pl
}

var siteTmpl = template.Must(withPageLoc("site", `# Generated by hostashi for site {{.S.User}}. Do not edit.
{{- with .C}}
{{- template "cachezone" .}}
{{- end}}
{{- with .Expires}}

# Asset caching (ADR-0054): the listed file types for the chosen time,
# nothing for everything else.
map $uri ${{.Var}} {
    default off;
    "~*\.(?:{{.Exts}})$" {{.Seconds}}s;
}
{{- end}}
{{- if .M}}

# Maintenance: 1 for the addresses that still see the site.
geo ${{.Open}} {
    default 0;
    {{- range .M.AllowIPs}}
    {{.}} 1;
    {{- end}}
}
{{- end}}
server {
    {{- range .N.Listen}}
    listen {{.}};
    {{- end}}
    listen unix:{{.N.ControlSocket}};
    server_name{{range .S.Domains}} {{.}}{{end}};
    {{- if .N.SiteLogDir}}
    access_log {{.N.SiteLogDir}}/{{.S.User}}/access.log;
    error_log {{.N.SiteLogDir}}/{{.S.User}}/error.log warn;
    {{- end}}
    {{- if .N.ACMERoot}}

    # HTTP-01 challenges of the panel's ACME client.
    location ^~ /.well-known/acme-challenge/ {
        root {{.N.ACMERoot}};
        default_type text/plain;
        try_files $uri =404;
    }
    {{- end}}

    # Health check of the agent after a reload, only through its Unix socket.
    # It never reaches the site's application, so a broken application does
    # not block configuration changes of other sites.
    location = /.hostashi/health {
        if ($remote_addr != "unix:") {
            return 404;
        }
        access_log off;
        default_type text/plain;
        return 200 "{{.S.User}}";
    }
    {{- if .S.Cert}}

    location / {
        return 301 https://$host$request_uri;
    }
}

server {
    {{- range .N.ListenTLS}}
    listen {{.}} ssl{{if $.N.LegacyHTTP2}} http2{{end}};
    {{- if $.N.HTTP3}}
    listen {{.}} quic;
    {{- end}}
    {{- end}}
    {{- if not .N.LegacyHTTP2}}
    http2 on;
    {{- end}}
    server_name{{range .S.Domains}} {{.}}{{end}};
    {{- if .N.SiteLogDir}}
    access_log {{.N.SiteLogDir}}/{{.S.User}}/access.log;
    error_log {{.N.SiteLogDir}}/{{.S.User}}/error.log warn;
    {{- end}}
    ssl_certificate {{.S.Cert}};
    ssl_certificate_key {{.S.Key}};
    # Headers here apply to every response of the site; a location with its
    # own add_header would drop them, so locations set none.
    {{- if .N.HTTP3}}
    add_header Alt-Svc 'h3=":{{.N.TLSPort}}"; ma=86400' always;
    {{- end}}
    {{- if .S.HSTS}}
    add_header Strict-Transport-Security "max-age=31536000" always;
    {{- end}}
    {{- end}}
    {{- if .S.Disabled}}
    {{- with .D}}

    # Disabled in the panel (ADR-0047): the page's files and, for every
    # other URL, the page with 503. Nothing of the site is served.
    error_page 503 {{$.Prefix}}{{.Page}}/index.html;

    {{- template "pageloc" $.PL}}

    location / {
        return 503;
    }
    {{- else}}

    # Disabled in the panel: nothing of the site is served.
    location / {
        default_type text/html;
        return 503 "<!doctype html><title>Temporarily unavailable</title><h1>Temporarily unavailable</h1><p>This site is not available at the moment. Please try again later.</p>\n";
    }
    {{- end}}
    {{- else}}
    {{- if .C}}
    # HIT, MISS, BYPASS, EXPIRED, STALE or UPDATING on PHP answers.
    add_header X-Cache $upstream_cache_status always;
    {{- end}}
    root {{.S.Root}};
    {{- with $.Body}}
    # Follows the site's post_max_size (ADR-0049).
    client_max_body_size {{.}};
    {{- end}}
    {{- with .M}}

    # Maintenance (ADR-0027): the page's files for everyone, and the page
    # with 503 instead of the site for addresses outside the allowlist.
    error_page 503 {{$.Prefix}}{{.Page}}/index.html;

    {{- template "pageloc" $.PL}}
    {{- end}}
    {{- if .S.PHPSocket}}
    index index.php index.html index.htm;

    location / {
        {{- if .M}}
        if (${{.Open}} = 0) {
            return 503;
        }
        {{- end}}
        {{- with $.SnipRoot}}
        include {{.}};
        {{- end}}
        try_files $uri $uri/ /index.php$is_args$args;
    }

    location ~ \.php$ {
        {{- if .M}}
        if (${{.Open}} = 0) {
            return 503;
        }
        {{- end}}
        try_files $fastcgi_script_name =404;
        fastcgi_pass unix:{{.S.PHPSocket}};
        fastcgi_index index.php;
        fastcgi_param SCRIPT_FILENAME $realpath_root$fastcgi_script_name;
        fastcgi_param DOCUMENT_ROOT $realpath_root;
        fastcgi_param SCRIPT_NAME $fastcgi_script_name;
        fastcgi_param QUERY_STRING $query_string;
        fastcgi_param REQUEST_METHOD $request_method;
        fastcgi_param CONTENT_TYPE $content_type;
        fastcgi_param CONTENT_LENGTH $content_length;
        fastcgi_param REQUEST_URI $request_uri;
        fastcgi_param DOCUMENT_URI $document_uri;
        fastcgi_param SERVER_PROTOCOL $server_protocol;
        fastcgi_param REQUEST_SCHEME $scheme;
        fastcgi_param HTTPS $https if_not_empty;
        fastcgi_param GATEWAY_INTERFACE CGI/1.1;
        fastcgi_param SERVER_SOFTWARE nginx;
        fastcgi_param REMOTE_ADDR $remote_addr;
        fastcgi_param REMOTE_PORT $remote_port;
        fastcgi_param SERVER_ADDR $server_addr;
        fastcgi_param SERVER_PORT $server_port;
        fastcgi_param SERVER_NAME $server_name;
        fastcgi_param REDIRECT_STATUS 200;
        # httpoxy: never pass a client's Proxy header on as HTTP_PROXY.
        fastcgi_param HTTP_PROXY "";
        {{- with $.ReadTimeout}}
        # Follows the site's max_execution_time (ADR-0049).
        fastcgi_read_timeout {{.}};
        {{- end}}
        {{- with $.C}}
        {{- template "cacheloc" .}}
        {{- end}}
    }
    {{- else}}
    index index.html index.htm;
    {{- with $.Expires}}
    expires ${{.Var}};
    {{- end}}

    location / {
        {{- if .M}}
        if (${{.Open}} = 0) {
            return 503;
        }
        {{- end}}
        {{- with $.SnipRoot}}
        include {{.}};
        {{- end}}
        {{- if .S.SPA}}
        # Single-page app (ADR-0054): unknown paths get the app's index.html.
        try_files $uri $uri/ /index.html;
        {{- else}}
        try_files $uri $uri/ =404;
        {{- end}}
    }

    # Without PHP, PHP files are never sent as text: they may hold secrets
    # such as database passwords.
    location ~ \.php$ {
        deny all;
    }
    {{- end}}

    # Hidden files stay hidden, except /.well-known (RFC 8615).
    location ~ /\.(?!well-known/) {
        deny all;
    }
    {{- with $.SnipServer}}

    # Custom snippet (ADR-0029), after Hostashi's own locations.
    include {{.}};
    {{- end}}
    {{- end}}
}
`).Parse(cacheSrc))

// phpLimits sets the site's body size and read timeout (ADR-0049) unless
// its server snippet, which is rendered at the server level, sets the same
// directive: nginx refuses a duplicate, and the snippet is the site's own
// choice.
func phpLimits(data map[string]any, s Site) {
	if s.PHPSocket == "" || s.Disabled {
		return
	}

	set := map[string]bool{}

	if s.Maintenance == nil {
		if server, _, err := s.checkSnippets(); err == nil {
			for _, d := range server {
				set[d.Name] = true
			}
		}
	}

	if s.BodyMax != 0 && !set["client_max_body_size"] {
		data["Body"] = strconv.FormatUint(s.BodyMax>>20, 10) + "m"
	}

	if s.FastCGIReadTimeout != 0 && !set["fastcgi_read_timeout"] {
		data["ReadTimeout"] = strconv.Itoa(s.FastCGIReadTimeout) + "s"
	}
}

// expiresView is the asset cache of a site for the template: the map
// variable, the extensions joined for the regular expression (checked by
// agentapi.ValidateAssetCache: letters and digits only) and the time.
type expiresView struct {
	Var, Exts string
	Seconds   int
}

func assetCacheView(s Site) expiresView {
	exts, secs := s.AssetCacheExts, s.AssetCacheSeconds
	if len(exts) == 0 {
		exts = agentapi.DefaultAssetCacheExts
	}

	if secs == 0 {
		secs = agentapi.DefaultAssetCacheSeconds
	}

	return expiresView{Var: "hostashi_expires_" + hex.EncodeToString([]byte(s.User)), Exts: strings.Join(exts, "|"), Seconds: secs}
}

// Render turns desired state into the files of generation gen.
func (n *Nginx) Render(d Desired, gen int) (Files, error) {
	if err := n.check(); err != nil {
		return Files{}, err
	}

	f := Files{Content: map[string][]byte{}, Meta: map[string][]byte{}}

	var b bytes.Buffer
	if err := mainTmpl.Execute(&b, map[string]any{"N": n, "Types": compressTypes, "Control": controlHost}); err != nil {
		return Files{}, fmt.Errorf("web: render nginx.conf: %w", err)
	}

	f.Content["nginx.conf"] = bytes.Clone(b.Bytes())

	for _, s := range d.Sites {
		if s.Cert != "" && len(n.ListenTLS) == 0 {
			return Files{}, fmt.Errorf("web: site %s has a certificate but there is no HTTPS listener", s.User)
		}

		b.Reset()

		data := map[string]any{"N": n, "S": s, "M": (*Maintenance)(nil), "D": (*PageRef)(nil), "C": (*cacheView)(nil)}
		if s.AssetCache && !s.Disabled && s.PHPSocket == "" {
			data["Expires"] = assetCacheView(s)
		}

		if s.Maintenance != nil && !s.Disabled {
			data["M"] = s.Maintenance
			data["Open"] = "hostashi_open_" + hex.EncodeToString([]byte(s.User))
			data["Prefix"] = agentapi.PagePathPrefix
			data["PL"] = n.pageLoc(s.Maintenance.Page, s.Maintenance.Dir, s.Cert != "", s.HSTS, true)
		}

		// A disabled site has no allowlist and no Retry-After: nobody knows
		// when it comes back (ADR-0047).
		if p := s.DisabledPage; p != nil && s.Disabled {
			data["D"] = p
			data["Prefix"] = agentapi.PagePathPrefix
			data["PL"] = n.pageLoc(p.Page, p.Dir, s.Cert != "", s.HSTS, false)
		}

		// No cache while the site is disabled or in maintenance: the
		// addresses on the allowlist must not fill it for the time after.
		if c := s.PageCache; c != nil && !s.Disabled && s.Maintenance == nil && s.PHPSocket != "" {
			v, err := newCacheView(s.User, *c)
			if err != nil {
				return Files{}, fmt.Errorf("web: site %s: %w", s.User, err)
			}

			data["C"] = &v
		}

		if !s.Disabled && s.Maintenance == nil {
			server, root, err := s.checkSnippets()
			if err != nil {
				return Files{}, fmt.Errorf("web: site %s: %w", s.User, err)
			}

			// Snippets live in their own files outside sites/, so the
			// sites/*.conf glob never picks them up, and a stray brace
			// cannot leave its block (S2).
			for slot, ds := range map[string][]nginxconf.Directive{"SnipServer": server, "SnipRoot": root} {
				if len(ds) == 0 {
					continue
				}

				name := "snippets/" + s.User + "-" + strings.ToLower(strings.TrimPrefix(slot, "Snip")) + ".conf"
				f.Content[name] = []byte("# Custom snippet of site " + s.User + ", checked by hostashi (ADR-0029).\n" + snippet.Render(ds, ""))
				data[slot] = name
			}
		}

		phpLimits(data, s)

		if err := siteTmpl.Execute(&b, data); err != nil {
			return Files{}, fmt.Errorf("web: render site %s: %w", s.User, err)
		}

		f.Content["sites/"+s.User+".conf"] = bytes.Clone(b.Bytes())
	}

	for _, p := range d.Parked {
		if p.Cert != "" && len(n.ListenTLS) == 0 {
			return Files{}, fmt.Errorf("web: parked %s has a certificate but there is no HTTPS listener", p.Domain)
		}

		b.Reset()

		data := map[string]any{"N": n, "P": p, "PL": n.pageLoc(p.Page, p.Dir, p.Cert != "", false, true)}
		if err := parkTmpl.Execute(&b, data); err != nil {
			return Files{}, fmt.Errorf("web: render parked %s: %w", p.Domain, err)
		}

		// Site users never start with an underscore.
		f.Content["sites/"+p.Name+".conf"] = bytes.Clone(b.Bytes())
	}

	if p := d.Panel; p != nil {
		if len(n.PanelListen) == 0 {
			return Files{}, errors.New("web: the panel has an address but there is no panel listener")
		}

		b.Reset()

		if err := panelTmpl.Execute(&b, map[string]any{"N": n, "P": p}); err != nil {
			return Files{}, fmt.Errorf("web: render panel: %w", err)
		}

		// Site users never start with an underscore.
		f.Content["sites/_panel.conf"] = bytes.Clone(b.Bytes())
	}

	f.Meta["generation.conf"] = []byte("return 200 \"" + strconv.Itoa(gen) + "\";\n")

	return f, nil
}

// Validate runs nginx -t on a complete generation directory. nginx -t
// binds Unix listen sockets for real, so the control socket's directory must
// exist (root, 0700; the nginx unit recreates it empty on every start).
func (n *Nginx) Validate(ctx context.Context, genDir string) error {
	if err := os.MkdirAll(filepath.Dir(n.ControlSocket), 0o700); err != nil {
		return fmt.Errorf("web: control socket directory: %w", err)
	}

	return n.Ctl.Test(ctx, genDir+"/nginx.conf")
}

// Activate reloads a server that runs one of our generations, and starts or
// restarts it otherwise (first apply, or nginx still on the package config).
// A reload that nginx rejects later is not visible here; Verify finds it.
func (n *Nginx) Activate(ctx context.Context) error {
	if _, err := n.liveGeneration(ctx); err == nil {
		return n.Ctl.Reload(ctx)
	}

	return n.Ctl.Restart(ctx)
}

func (n *Nginx) client() *http.Client {
	sock := n.ControlSocket

	return &http.Client{
		Timeout: 3 * time.Second,
		Transport: &http.Transport{
			DisableKeepAlives: true,
			DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
				var d net.Dialer

				return d.DialContext(ctx, "unix", sock)
			},
		},
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}
}

func (n *Nginx) get(ctx context.Context, host, p string) (int, string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, "http://localhost"+p, nil)
	if err != nil {
		return 0, "", fmt.Errorf("web: %w", err)
	}

	req.Host = host

	resp, err := n.client().Do(req)
	if err != nil {
		return 0, "", fmt.Errorf("web: %w", err)
	}

	defer resp.Body.Close()

	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<12))
	if err != nil {
		return 0, "", fmt.Errorf("web: %w", err)
	}

	return resp.StatusCode, string(body), nil
}

func (n *Nginx) liveGeneration(ctx context.Context) (int, error) {
	code, body, err := n.get(ctx, controlHost, "/.hostashi/generation")
	if err != nil {
		return 0, err
	}

	if code != http.StatusOK {
		return 0, fmt.Errorf("web: control endpoint answered %d", code)
	}

	g, err := strconv.Atoi(strings.TrimSpace(body))
	if err != nil {
		return 0, fmt.Errorf("web: control endpoint answered %q", body)
	}

	return g, nil
}

// stableAnswers is how many answers in a row must name the new generation.
// After a reload, old workers still accept connections for a short time, so
// one matching answer proves nothing (spike S2, finding 2).
const stableAnswers = 8

// Verify waits until the control endpoint reports gen several times in a
// row, then asks every site's health location on its primary domain: it
// must answer 200 with the site's name, which proves that the domain reaches
// the right server block. A transport error or any other answer fails the
// generation.
func (n *Nginx) Verify(ctx context.Context, gen int, d Desired) error {
	timeout := n.VerifyTimeout
	if timeout == 0 {
		timeout = 5 * time.Second
	}

	deadline := time.Now().Add(timeout)
	last := ""

	for stable := 0; stable < stableAnswers; {
		g, err := n.liveGeneration(ctx)

		switch {
		case err != nil:
			stable, last = 0, err.Error()
		case g != gen:
			stable, last = 0, "generation "+strconv.Itoa(g)+" is live"
		default:
			stable++
		}

		if time.Now().After(deadline) {
			return fmt.Errorf("web: generation %d did not become live: %s", gen, last)
		}

		select {
		case <-ctx.Done():
			return fmt.Errorf("web: %w", ctx.Err())
		case <-time.After(10 * time.Millisecond):
		}
	}

	for _, s := range d.Sites {
		code, body, err := n.get(ctx, s.Domains[0], "/.hostashi/health")
		if err != nil {
			return fmt.Errorf("web: site %s: %w", s.User, err)
		}

		if code != http.StatusOK || body != s.User {
			return fmt.Errorf("web: site %s: health check answered %d", s.User, code)
		}
	}

	return nil
}
