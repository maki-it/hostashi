// SPDX-License-Identifier: AGPL-3.0-or-later

package web

import (
	"fmt"
	"net"
	"net/http"
	"strconv"
	"strings"
	"testing"

	"github.com/maki-it/hostashi/internal/agentapi"
)

func TestAppRender(t *testing.T) {
	n := &Nginx{Ctl: &testCtl{}, Listen: []string{"80"}, ControlSocket: "/run/x.sock", PIDFile: "/run/x.pid", ErrorLog: "/var/log/x", MimeTypes: "/etc/x"}

	f, err := n.Render(Desired{Sites: []Site{{
		User: "node", Domains: []string{"node.example.com"}, Root: "/home/node/htdocs", App: &agentapi.WebApp{Port: 20001},
		Headers: agentapi.WebHeaders{NoSniff: true},
	}}}, 1)
	if err != nil {
		t.Fatal(err)
	}

	conf := string(f.Content["sites/node.conf"])
	for _, want := range []string{
		"proxy_pass http://127.0.0.1:20001;", "proxy_set_header Host $host;", "proxy_set_header Connection $hostashi_connection_upgrade;",
		"proxy_hide_header X-Content-Type-Options;", "client_max_body_size 0;", "location = /.hostashi/health",
	} {
		if !strings.Contains(conf, want) {
			t.Errorf("node.conf lacks %q:\n%s", want, conf)
		}
	}

	if strings.Contains(conf, "fastcgi") || strings.Contains(conf, "try_files") {
		t.Errorf("node.conf serves files or PHP:\n%s", conf)
	}

	a := func(port int, f func(*Site)) Site {
		s := Site{User: "a", Domains: []string{"a.example.com"}, Root: "/home/a/htdocs", App: &agentapi.WebApp{Port: port}}
		if f != nil {
			f(&s)
		}

		return s
	}

	for name, s := range map[string]Site{
		"port low":  a(8080, nil),
		"port high": a(30000, nil),
		"php":       a(20001, func(s *Site) { s.PHPSocket = "/run/php/a.sock" }),
		"spa":       a(20001, func(s *Site) { s.SPA = true }),
		"proxy":     a(20001, func(s *Site) { s.Root = ""; s.Proxy = &agentapi.WebProxy{Target: "http://10.0.0.5"} }),
		"asset":     a(20001, func(s *Site) { s.AssetCache = true }),
		"no root":   a(20001, func(s *Site) { s.Root = "" }),
	} {
		if err := (Desired{Sites: []Site{s}}).Validate(); err == nil {
			t.Errorf("%s accepted", name)
		}
	}
}

// TestAppServed passes requests through the real nginx to an app on a
// loopback port of the app range.
func TestAppServed(t *testing.T) {
	e := newEnv(t)
	ctx := t.Context()

	var (
		ln   net.Listener
		port int
	)

	for p := agentapi.MaxAppPort; p > agentapi.MaxAppPort-50; p-- {
		l, err := net.Listen("tcp", "127.0.0.1:"+strconv.Itoa(p))
		if err == nil {
			ln, port = l, p

			break
		}
	}

	if ln == nil {
		t.Skip("no free port in the app range")
	}

	srv := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = fmt.Fprintf(w, "%s %s %s", r.Host, r.URL.RequestURI(), r.Header.Get("X-Forwarded-Proto"))
	})}

	go func() { _ = srv.Serve(ln) }()

	t.Cleanup(func() { _ = srv.Close() })

	if _, err := e.r.Recover(ctx); err != nil {
		t.Fatal(err)
	}

	app := e.site(t, "node", "files are not served", "node.example.com")
	app.App = &agentapi.WebApp{Port: port}

	if _, err := e.r.Apply(ctx, Desired{Sites: []Site{app}}); err != nil {
		t.Fatal(err)
	}

	if code, _, body := e.fetchFull(t, "node.example.com", "/index.html?x=1"); code != http.StatusOK || body != "node.example.com /index.html?x=1 http" {
		t.Errorf("app: %d %q", code, body)
	}

	_ = srv.Close()

	if code, _, _ := e.fetchFull(t, "node.example.com", "/"); code != http.StatusBadGateway {
		t.Errorf("app down: %d", code)
	}
}
