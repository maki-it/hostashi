// SPDX-License-Identifier: AGPL-3.0-or-later

package web

import (
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/maki-it/hostashi/internal/agentapi"
)

func TestRealIPRender(t *testing.T) {
	n := &Nginx{Ctl: &testCtl{}, Listen: []string{"80"}, ListenTLS: []string{"443"}, ControlSocket: "/run/x.sock", PIDFile: "/run/x.pid", ErrorLog: "/var/log/x", MimeTypes: "/etc/x"}

	f, err := n.Render(Desired{Sites: []Site{
		{User: "cf", Domains: []string{"cf.example.com"}, Root: "/srv/cf", Cert: "/c/cf.pem", Key: "/c/cf.key", RealIP: agentapi.WebRealIP{Cloudflare: true}},
		{User: "lb", Domains: []string{"lb.example.com"}, Root: "/srv/lb", RealIP: agentapi.WebRealIP{Ranges: []string{"10.0.0.7/24", "10.0.0.9"}, Header: agentapi.RealIPXRealIP}},
		{User: "plain", Domains: []string{"plain.example.com"}, Root: "/srv/plain"},
		{User: "go", Domains: []string{"go.example.com"}, Redirect: &agentapi.WebRedirect{Target: "https://new.example.com/", Status: 301}, RealIP: agentapi.WebRealIP{Cloudflare: true, Header: agentapi.RealIPCFConnectingIP}},
	}}, 1)
	if err != nil {
		t.Fatal(err)
	}

	cf := string(f.Content["sites/cf.conf"])
	// Both server blocks (the HTTP redirect and HTTPS) trust Cloudflare.
	if got := strings.Count(cf, "set_real_ip_from 173.245.48.0/20;"); got != 2 {
		t.Errorf("cf.conf trusts Cloudflare in %d server blocks:\n%s", got, cf)
	}

	for _, want := range []string{"set_real_ip_from 2a06:98c0::/29;", "real_ip_header X-Forwarded-For;", "real_ip_recursive on;"} {
		if !strings.Contains(cf, want) {
			t.Errorf("cf.conf lacks %q", want)
		}
	}

	lb := string(f.Content["sites/lb.conf"])
	for _, want := range []string{"set_real_ip_from 10.0.0.0/24;", "set_real_ip_from 10.0.0.9/32;", "real_ip_header X-Real-IP;"} {
		if !strings.Contains(lb, want) {
			t.Errorf("lb.conf lacks %q:\n%s", want, lb)
		}
	}

	if strings.Contains(lb, "real_ip_recursive") || strings.Contains(lb, "173.245.48.0/20") {
		t.Errorf("lb.conf:\n%s", lb)
	}

	if g := string(f.Content["sites/go.conf"]); !strings.Contains(g, "real_ip_header CF-Connecting-IP;") {
		t.Errorf("go.conf:\n%s", g)
	}

	n.SiteLogDir = "/var/log/nginx/sites"

	f, err = n.Render(Desired{Sites: []Site{
		{User: "cf", Domains: []string{"cf.example.com"}, Root: "/srv/cf", RealIP: agentapi.WebRealIP{Cloudflare: true}},
		{User: "plain", Domains: []string{"plain.example.com"}, Root: "/srv/plain"},
	}}, 2)
	if err != nil {
		t.Fatal(err)
	}

	if cf := string(f.Content["sites/cf.conf"]); !strings.Contains(cf, "access_log /var/log/nginx/sites/cf/access.log hostashi_realip;") {
		t.Errorf("cf.conf logs without the proxy:\n%s", cf)
	}

	if p := string(f.Content["sites/plain.conf"]); strings.Contains(p, "real_ip") || !strings.Contains(p, "access_log /var/log/nginx/sites/plain/access.log;") {
		t.Errorf("plain.conf trusts a proxy:\n%s", p)
	}

	if m := string(f.Content["nginx.conf"]); strings.Contains(m, "real_ip") {
		t.Errorf("nginx.conf trusts a proxy for every site:\n%s", m)
	}

	for name, r := range map[string]agentapi.WebRealIP{
		"everything": {Ranges: []string{"0.0.0.0/0"}},
		"loopback":   {Ranges: []string{"127.0.0.1"}},
		"header":     {Cloudflare: true, Header: "X-Forwarded-For;"},
	} {
		s := Site{User: "a", Domains: []string{"a.example.com"}, Root: "/srv/a", RealIP: r}
		if err := (Desired{Sites: []Site{s}}).Validate(); err == nil {
			t.Errorf("%s accepted", name)
		}
	}
}

// TestRealIPServed checks with the real nginx that the directives load and
// that a forged header from an untrusted peer changes nothing: the access
// log keeps the peer.
func TestRealIPServed(t *testing.T) {
	e := newEnv(t)
	ctx := t.Context()

	logs := filepath.Join(e.dir, "logs")
	e.r.Driver.SiteLogDir = logs

	if _, err := e.r.Recover(ctx); err != nil {
		t.Fatal(err)
	}

	cf := e.site(t, "cf", "cf index", "cf.example.com")
	cf.RealIP = agentapi.WebRealIP{Cloudflare: true, Ranges: []string{"10.0.0.2"}}

	if err := e.r.Driver.PrepareLogs(map[string]bool{"cf": true}); err != nil {
		t.Fatal(err)
	}

	if _, err := e.r.Apply(ctx, Desired{Sites: []Site{cf}}); err != nil {
		t.Fatal(err)
	}

	conn, err := net.DialTimeout("tcp", "127.0.0.1:"+strconv.Itoa(e.port), 2*time.Second)
	if err != nil {
		t.Fatal(err)
	}

	_ = conn.SetDeadline(time.Now().Add(3 * time.Second))
	_, _ = conn.Write([]byte("GET / HTTP/1.1\r\nHost: cf.example.com\r\nX-Forwarded-For: 198.51.100.77\r\nConnection: close\r\n\r\n"))
	b := make([]byte, 4096)
	n, _ := conn.Read(b)
	_ = conn.Close()

	if !strings.Contains(string(b[:n]), "cf index") {
		t.Fatalf("answer: %q", b[:n])
	}

	var line string

	for range 50 {
		raw, _ := os.ReadFile(filepath.Join(logs, "cf", "access.log"))
		if line = string(raw); line != "" {
			break
		}

		time.Sleep(20 * time.Millisecond)
	}

	if !strings.HasPrefix(line, "127.0.0.1 ") || !strings.HasSuffix(line, " proxy=127.0.0.1\n") {
		t.Errorf("access log: %q", line)
	}
}

// The panel's own setting (ADR-0059) goes into each of its server blocks:
// port 80, HTTPS and the database tool. Without it the panel trusts no
// proxy.
func TestRealIPPanelRender(t *testing.T) {
	n := &Nginx{
		Ctl: &testCtl{}, Listen: []string{"80"}, ListenTLS: []string{"443"},
		PanelListen: []string{"8443"}, PanelUpstream: "/run/hostashi-panel.sock", DBToolListen: []string{"8444"}, DBToolRoot: "/usr/share/hostashi/adminer", DBToolSocket: "/run/php/hostashi-dbtool.sock",
		ControlSocket: "/run/x.sock", PIDFile: "/run/x.pid", ErrorLog: "/var/log/x", MimeTypes: "/etc/x",
	}

	panel := &Panel{Domain: "cp.example.com", Cert: "/c.pem", Key: "/k.pem", DBTool: true, RealIP: agentapi.WebRealIP{Cloudflare: true, Ranges: []string{"10.0.0.9"}}}

	f, err := n.Render(Desired{Panel: panel}, 1)
	if err != nil {
		t.Fatal(err)
	}

	p := string(f.Content["sites/_panel.conf"])
	if got := strings.Count(p, "set_real_ip_from 10.0.0.9/32;"); got != 3 {
		t.Errorf("the panel trusts its proxy in %d server blocks:\n%s", got, p)
	}

	for _, want := range []string{"set_real_ip_from 173.245.48.0/20;", "real_ip_header X-Forwarded-For;", "real_ip_recursive on;", "proxy_set_header X-Real-IP $remote_addr;", `proxy_set_header X-Forwarded-For "";`} {
		if !strings.Contains(p, want) {
			t.Errorf("_panel.conf lacks %q", want)
		}
	}

	panel.RealIP = agentapi.WebRealIP{}

	if f, err = n.Render(Desired{Panel: panel}, 2); err != nil {
		t.Fatal(err)
	}

	if p := string(f.Content["sites/_panel.conf"]); strings.Contains(p, "real_ip") {
		t.Errorf("the panel trusts a proxy without a setting:\n%s", p)
	}

	bad := &Panel{Domain: "cp.example.com", RealIP: agentapi.WebRealIP{Ranges: []string{"::1"}}}
	if err := (Desired{Panel: bad}).Validate(); err == nil {
		t.Error("loopback accepted for the panel")
	}
}
