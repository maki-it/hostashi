// SPDX-License-Identifier: AGPL-3.0-or-later

package scan

import (
	"fmt"
	"net/url"
	"path"
	"strings"

	"github.com/maki-it/hostashi/internal/cloudpanel/nginxconf"
)

// server is the analyzed form of one nginx server block.
type server struct {
	Line         int
	Names        []string
	Ports        []string
	SSL          bool
	Panel        bool // listens on the CloudPanel admin port
	Default      bool // default_server
	Root         string
	Cert, Key    string
	FastCGI      string // fastcgi_pass target of the PHP location
	ProxyPass    string // proxy_pass of location /
	Return       []string
	RedirectOnly bool
	MaxBody      string
	Custom       []Custom
	Findings     []Finding
}

// Directives in a server block that the model covers, or that Hostashi
// regenerates with its own defaults so dropping them loses nothing.
var serverMapped = map[string]bool{
	"listen": true, "server_name": true, "root": true, "index": true,
	"ssl_certificate": true, "ssl_certificate_key": true, "return": true,
	"access_log": true, "error_log": true, "charset": true, "http2": true, "http3": true,
	"client_max_body_size": true, "location": true, "include": true,
	"ssl_protocols": true, "ssl_ciphers": true, "ssl_prefer_server_ciphers": true,
	"ssl_session_cache": true, "ssl_session_timeout": true, "ssl_session_tickets": true,
	"ssl_stapling": true, "ssl_stapling_verify": true, "ssl_dhparam": true, "ssl_ecdh_curve": true,
	"gzip": true, "gzip_types": true, "gzip_vary": true, "gzip_comp_level": true, "gzip_proxied": true,
	"gzip_min_length": true, "gzip_disable": true, "quic_retry": true, "server_tokens": true,
}

// Directives that are part of a standard PHP or proxy handler location.
var handlerMapped = map[string]bool{
	"fastcgi_pass": true, "fastcgi_index": true, "fastcgi_param": true, "include": true,
	"fastcgi_split_path_info": true, "fastcgi_intercept_errors": true, "fastcgi_read_timeout": true,
	"fastcgi_send_timeout": true, "fastcgi_connect_timeout": true, "fastcgi_buffers": true,
	"fastcgi_buffer_size": true, "fastcgi_busy_buffers_size": true, "fastcgi_keep_conn": true,
	"try_files": true, "proxy_pass": true, "proxy_http_version": true, "proxy_set_header": true,
	"proxy_read_timeout": true, "proxy_send_timeout": true, "proxy_connect_timeout": true,
	"proxy_buffering": true, "proxy_redirect": true, "proxy_cache_bypass": true,
	"access_log": true, "log_not_found": true,
}

// includeMapped are include files that only carry standard FastCGI or proxy
// parameters shipped by the nginx package.
var includeMapped = map[string]bool{
	"fastcgi_params": true, "fastcgi.conf": true, "snippets/fastcgi-php.conf": true,
	"proxy_params": true, "/etc/nginx/fastcgi_params": true, "/etc/nginx/fastcgi.conf": true,
	"/etc/nginx/snippets/fastcgi-php.conf": true, "/etc/nginx/proxy_params": true,
}

const panelPort = "8443"

func analyseServer(d nginxconf.Directive, source string, inc includer) server {
	s := server{Line: d.Line}

	body := inc.expand(d.Block, source, &s.Findings, 0)
	for _, x := range body {
		src := source
		if x.File != "" {
			src = x.File
		}

		switch x.Name {
		case "listen":
			if len(x.Args) == 0 {
				continue
			}

			port := x.Args[0]
			if i := strings.LastIndex(port, ":"); i >= 0 {
				port = port[i+1:]
			}

			s.Ports = append(s.Ports, port)

			for _, a := range x.Args[1:] {
				switch a {
				case "ssl", "quic":
					s.SSL = true
				case "default_server", "default":
					s.Default = true
				}
			}

			if port == "443" {
				s.SSL = true
			}

			if port == panelPort {
				s.Panel = true
			}
		case "server_name":
			s.Names = append(s.Names, x.Args...)
		case "root":
			if len(x.Args) == 1 {
				s.Root = x.Args[0]
			}
		case "ssl_certificate":
			if len(x.Args) == 1 {
				s.Cert = x.Args[0]
			}
		case "ssl_certificate_key":
			if len(x.Args) == 1 {
				s.Key = x.Args[0]
			}
		case "return":
			s.Return = x.Args
		case "client_max_body_size":
			if len(x.Args) == 1 {
				s.MaxBody = x.Args[0]
			}
		case "location":
			s.analyseLocation(x, src)
		default:
			if !serverMapped[x.Name] {
				s.Custom = append(s.Custom, Custom{Context: "server: " + x.Name, Raw: x.Raw, Source: src, Line: x.Line})
			}
		}
	}

	s.RedirectOnly = len(s.Return) > 0 && s.Root == "" && s.FastCGI == "" && s.ProxyPass == "" && len(s.Custom) == 0

	return s
}

func (s *server) analyseLocation(loc nginxconf.Directive, source string) {
	args := strings.Join(loc.Args, " ")
	ctx := "location " + args

	var (
		fcgi, proxy string
		deny, other bool
	)

	for _, x := range loc.Block {
		switch {
		case x.Name == "fastcgi_pass" && len(x.Args) == 1:
			fcgi = x.Args[0]
		case x.Name == "proxy_pass" && len(x.Args) == 1:
			proxy = x.Args[0]
		case x.Name == "deny" && len(x.Args) == 1 && x.Args[0] == "all":
			deny = true
		case x.Name == "include" && len(x.Args) == 1 && includeMapped[x.Args[0]]:
		case handlerMapped[x.Name] && x.Name != "include":
		default:
			other = true
		}
	}

	switch {
	case fcgi != "" && !other && isPHPLocation(loc.Args):
		if s.FastCGI != "" && s.FastCGI != fcgi {
			s.Findings = append(s.Findings, Finding{SevReview, "php_two_backends", "two PHP locations with different backends", source, loc.Line})
		}

		s.FastCGI = fcgi
	case proxy != "" && !other && args == "/":
		s.ProxyPass = proxy
	case deny && !other && fcgi == "" && proxy == "" && isDotfileLocation(loc.Args):
		// standard "deny hidden files" rule, Hostashi has its own
	case !other && fcgi == "" && proxy == "" && args == "/":
		// location / { try_files ...; } is the static default
	default:
		s.Custom = append(s.Custom, Custom{Context: ctx, Raw: loc.Raw, Source: source, Line: loc.Line})
		if fcgi != "" && s.FastCGI == "" {
			s.FastCGI = fcgi // still a PHP site, but the handler is customized
		}

		if proxy != "" && args == "/" && s.ProxyPass == "" {
			s.ProxyPass = proxy
		}
	}
}

func isPHPLocation(a []string) bool {
	return len(a) == 2 && (a[0] == "~" || a[0] == "~*") && (a[1] == `\.php$` || a[1] == `[^/]\.php(/|$)` || a[1] == `\.php(/|$)`)
}

func isDotfileLocation(a []string) bool {
	return len(a) == 2 && (a[0] == "~" || a[0] == "~*") && (a[1] == `/\.` || a[1] == `/\.(?!well-known)`)
}

// includer resolves include directives inside server blocks. Only plain
// file names below /etc/nginx are followed; globs and other paths are custom.
type includer struct {
	r *reader
}

const maxIncludeDepth = 3

func (inc includer) expand(ds []nginxconf.Directive, source string, fs *[]Finding, depth int) []nginxconf.Directive {
	var out []nginxconf.Directive

	for _, d := range ds {
		if d.Name != "include" || len(d.Args) != 1 || includeMapped[d.Args[0]] {
			out = append(out, d)
			continue
		}

		p := d.Args[0]
		if !strings.HasPrefix(p, "/") {
			p = "/etc/nginx/" + p
		}

		p = path.Clean(p)
		switch {
		case strings.ContainsAny(p, "*?["):
			*fs = append(*fs, Finding{SevReview, "include_glob", fmt.Sprintf("include %s uses a glob, not followed", d.Args[0]), source, d.Line})
			out = append(out, nginxconf.Directive{Name: "x-include", Raw: d.Raw, Line: d.Line})

			continue
		case !strings.HasPrefix(p, "/etc/nginx/"):
			*fs = append(*fs, Finding{SevBlocker, "include_outside", fmt.Sprintf("include %s points outside /etc/nginx", d.Args[0]), source, d.Line})
			continue
		case depth >= maxIncludeDepth:
			*fs = append(*fs, Finding{SevBlocker, "include_depth", "includes nested too deeply", source, d.Line})
			continue
		}

		b, err := inc.r.ReadFile(p, nginxconf.MaxBytes)
		if err != nil {
			*fs = append(*fs, Finding{SevBlocker, "include_unreadable", fmt.Sprintf("include %s: %v", d.Args[0], err), source, d.Line})
			continue
		}

		sub, err := nginxconf.Parse(string(b))
		if err != nil {
			*fs = append(*fs, Finding{SevBlocker, "include_syntax", fmt.Sprintf("include %s: %v", d.Args[0], err), p, 0})
			continue
		}

		*fs = append(*fs, Finding{SevInfo, "include_inlined", "include " + d.Args[0] + " was read and analyzed inline", source, d.Line})
		for _, x := range inc.expand(sub, p, fs, depth+1) {
			if x.File == "" {
				x.File = p
			}

			out = append(out, x)
		}
	}

	return out
}

// redirectTarget returns the host a "return 301 https://host..." points to,
// and whether the target keeps the request host ($host / $server_name).
func redirectTarget(ret []string) (host string, sameHost bool, ok bool) {
	if len(ret) != 2 || (ret[0] != "301" && ret[0] != "302" && ret[0] != "307" && ret[0] != "308") {
		return "", false, false
	}

	t := ret[1]
	if strings.HasPrefix(t, "https://$host") || strings.HasPrefix(t, "https://$server_name") || strings.HasPrefix(t, "https://$http_host") {
		return "", true, true
	}

	u, err := url.Parse(strings.Replace(t, "$request_uri", "", 1))
	if err != nil || u.Host == "" || strings.Contains(u.Host, "$") {
		return "", false, false
	}

	return u.Hostname(), false, true
}
