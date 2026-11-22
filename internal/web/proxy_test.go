// SPDX-License-Identifier: AGPL-3.0-or-later

package web

import (
	"fmt"
	"net"
	"net/http"
	"strings"
	"testing"

	"github.com/maki-it/hostashi/internal/agentapi"
)

func TestProxyRender(t *testing.T) {
	n := &Nginx{Ctl: &testCtl{}, Listen: []string{"80"}, ControlSocket: "/run/x.sock", PIDFile: "/run/x.pid", ErrorLog: "/var/log/x", MimeTypes: "/etc/x"}

	f, err := n.Render(Desired{Sites: []Site{
		{
			User: "app", Domains: []string{"app.example.com"}, Proxy: &agentapi.WebProxy{Target: "https://backend.example.net/base"},
			ProxyAddrs: []string{"10.0.0.5:443", "[2001:db8::5]:443"}, Headers: agentapi.WebHeaders{NoSniff: true},
		},
		{User: "raw", Domains: []string{"raw.example.com"}, Proxy: &agentapi.WebProxy{Target: "https://10.0.0.6:8443", TargetHost: true, Insecure: true}, ProxyAddrs: []string{"10.0.0.6:8443"}},
		{User: "gone", Domains: []string{"gone.example.com"}, Proxy: &agentapi.WebProxy{Target: "http://backend.example.net"}},
	}}, 1)
	if err != nil {
		t.Fatal(err)
	}

	app := string(f.Content["sites/app.conf"])
	for _, want := range []string{
		"upstream hostashi_proxy_617070 {\n    server 10.0.0.5:443;\n    server [2001:db8::5]:443;\n}",
		"proxy_pass https://hostashi_proxy_617070/base/;", "proxy_set_header Host $host;", "proxy_ssl_name backend.example.net;",
		"proxy_ssl_verify on;", "proxy_ssl_trusted_certificate /etc/ssl/certs/ca-certificates.crt;", "proxy_hide_header X-Content-Type-Options;",
		"proxy_set_header Connection $hostashi_connection_upgrade;", "location = /.hostashi/health", "client_max_body_size 0;",
	} {
		if !strings.Contains(app, want) {
			t.Errorf("app.conf lacks %q:\n%s", want, app)
		}
	}

	if strings.Contains(app, "root ") || strings.Contains(app, "fastcgi") {
		t.Errorf("app.conf serves files:\n%s", app)
	}

	raw := string(f.Content["sites/raw.conf"])
	for _, want := range []string{"proxy_pass https://hostashi_proxy_726177;", "proxy_set_header Host 10.0.0.6:8443;", "proxy_ssl_verify off;"} {
		if !strings.Contains(raw, want) {
			t.Errorf("raw.conf lacks %q:\n%s", want, raw)
		}
	}

	if gone := string(f.Content["sites/gone.conf"]); !strings.Contains(gone, "return 502;") || strings.Contains(gone, "upstream") {
		t.Errorf("gone.conf:\n%s", gone)
	}

	if main := string(f.Content["nginx.conf"]); !strings.Contains(main, "map $http_upgrade $hostashi_connection_upgrade") {
		t.Errorf("nginx.conf lacks the upgrade map")
	}

	p := func(target string, addrs ...string) Site {
		return Site{User: "a", Domains: []string{"a.example.com"}, Proxy: &agentapi.WebProxy{Target: target}, ProxyAddrs: addrs}
	}

	for name, s := range map[string]Site{
		"loopback target": p("http://127.0.0.1:8444"),
		"metadata":        p("http://169.254.169.254"),
		"loopback addr":   p("http://backend.example.net", "127.0.0.1:80"),
		"bad addr":        p("http://backend.example.net", "10.0.0.5"),
		"injected addr":   p("http://backend.example.net", "10.0.0.5:80; server 127.0.0.1:80"),
		"loop":            p("https://a.example.com"),
		"injection":       p("http://b.example.com/x;return 200"),
		"addrs only":      {User: "a", Domains: []string{"a.example.com"}, Root: "/srv/a", ProxyAddrs: []string{"10.0.0.5:80"}},
		"with root":       {User: "a", Domains: []string{"a.example.com"}, Root: "/srv/a", Proxy: &agentapi.WebProxy{Target: "http://10.0.0.5"}},
		"with redirect": {
			User: "a", Domains: []string{"a.example.com"}, Proxy: &agentapi.WebProxy{Target: "http://10.0.0.5"},
			Redirect: &agentapi.WebRedirect{Target: "https://b.example.com", Status: 301},
		},
	} {
		if err := (Desired{Sites: []Site{s}}).Validate(); err == nil {
			t.Errorf("%s accepted", name)
		}
	}
}

// outsideAddr is an address of this host that is not loopback, which a
// proxy site may reach.
func outsideAddr(t *testing.T) string {
	t.Helper()

	addrs, err := net.InterfaceAddrs()
	if err != nil {
		t.Skip(err)
	}

	for _, a := range addrs {
		if n, ok := a.(*net.IPNet); ok && n.IP.To4() != nil && !n.IP.IsLoopback() && !n.IP.IsLinkLocalUnicast() {
			return n.IP.String()
		}
	}

	t.Skip("no address besides loopback")

	return ""
}

// TestProxyServed passes requests through the real nginx to a backend.
func TestProxyServed(t *testing.T) {
	e := newEnv(t)
	ctx := t.Context()

	ln, err := net.Listen("tcp", outsideAddr(t)+":0")
	if err != nil {
		t.Skip(err)
	}

	srv := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Content-Type-Options", "from-backend")
		_, _ = fmt.Fprintf(w, "%s %s %s %s", r.Host, r.URL.RequestURI(), r.Header.Get("X-Forwarded-Host"), r.Header.Get("X-Forwarded-Proto"))
	})}

	go func() { _ = srv.Serve(ln) }()

	t.Cleanup(func() { _ = srv.Close() })

	addr := ln.Addr().String()

	if _, err := e.r.Recover(ctx); err != nil {
		t.Fatal(err)
	}

	plain := e.site(t, "plain", "plain index", "plain.example.com")
	app := Site{
		User: "app", Domains: []string{"app.example.com"}, Proxy: &agentapi.WebProxy{Target: "http://backend.example.net/base"},
		ProxyAddrs: []string{addr}, Headers: agentapi.WebHeaders{NoSniff: true},
	}
	own := Site{User: "own", Domains: []string{"own.example.com"}, Proxy: &agentapi.WebProxy{Target: "http://" + addr, TargetHost: true}, ProxyAddrs: []string{addr}}
	gone := Site{User: "gone", Domains: []string{"gone.example.com"}, Proxy: &agentapi.WebProxy{Target: "http://backend.example.net"}}

	if _, err := e.r.Apply(ctx, Desired{Sites: []Site{plain, app, own, gone}}); err != nil {
		t.Fatal(err)
	}

	code, h, body := e.fetchFull(t, "app.example.com", "/a/b?x=1")
	if code != http.StatusOK || body != "app.example.com /base/a/b?x=1 app.example.com http" {
		t.Errorf("app: %d %q", code, body)
	}

	if got := h.Values("X-Content-Type-Options"); len(got) != 1 || got[0] != "nosniff" {
		t.Errorf("app: X-Content-Type-Options %v", got)
	}

	if code, _, body := e.fetchFull(t, "own.example.com", "/x"); code != http.StatusOK || !strings.HasPrefix(body, addr+" /x ") {
		t.Errorf("own: %d %q", code, body)
	}

	if code, _, _ := e.fetchFull(t, "gone.example.com", "/"); code != http.StatusBadGateway {
		t.Errorf("gone: %d", code)
	}

	if code, body := e.fetch(t, "plain.example.com"); code != http.StatusOK || body != "plain index" {
		t.Errorf("plain: %d %q", code, body)
	}
}
