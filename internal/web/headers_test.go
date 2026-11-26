// SPDX-License-Identifier: AGPL-3.0-or-later

package web

import (
	"errors"
	"net"
	"net/http"
	"net/http/fcgi"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/maki-it/hostashi/internal/agentapi"
)

var allHeaders = agentapi.WebHeaders{
	NoSniff: true, FrameOptions: agentapi.FrameDeny, ReferrerPolicy: "same-origin",
	PermissionsPolicy: `geolocation=(self "https://maps.example.com"), camera=()`,
	CSP:               "default-src 'self'; img-src 'self' data:",
	NoIndex:           true,
}

// appServer is a PHP application that sends its own security headers.
func appServer(t *testing.T, sock string) {
	t.Helper()

	l, err := net.Listen("unix", sock)
	if err != nil {
		t.Fatal(err)
	}

	t.Cleanup(func() { _ = l.Close() })

	go func() {
		_ = fcgi.Serve(l, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("X-Frame-Options", "SAMEORIGIN")
			w.Header().Set("Content-Security-Policy", "default-src *")
			w.Header().Set("X-App", "kept")
			_, _ = w.Write([]byte("php"))
		}))
	}()
}

// checkOnce asserts that every switched-on header comes exactly once with
// Hostashi's value.
func checkOnce(t *testing.T, what string, h http.Header, want agentapi.WebHeaders) {
	t.Helper()

	for _, x := range want.List() {
		if got := h.Values(x.Name); len(got) != 1 || got[0] != x.Value {
			t.Errorf("%s: %s = %q, want once %q", what, x.Name, got, x.Value)
		}
	}
}

func TestSecurityHeadersStaticAndPHP(t *testing.T) {
	e := newEnv(t)
	ctx := t.Context()

	static := e.site(t, "alpha", "hello alpha", "alpha.example.com")
	static.Headers = allHeaders

	php := e.site(t, "beta", "static beta", "beta.example.com")
	php.PHPSocket = filepath.Join(e.dir, "php.sock")
	php.Headers = allHeaders
	appServer(t, php.PHPSocket)

	if err := os.WriteFile(filepath.Join(php.Root, "index.php"), []byte("<?php"), 0o644); err != nil {
		t.Fatal(err)
	}

	plain := e.site(t, "gamma", "hello gamma", "gamma.example.com")

	if _, err := e.r.Recover(ctx); err != nil {
		t.Fatal(err)
	}

	if _, err := e.r.Apply(ctx, Desired{Sites: []Site{static, php, plain}}); err != nil {
		t.Fatal(err)
	}

	code, h, body := e.fetchFull(t, "alpha.example.com", "/")
	if code != http.StatusOK || body != "hello alpha" {
		t.Fatalf("static: %d %q", code, body)
	}

	checkOnce(t, "static", h, allHeaders)

	// Error answers carry them too ("always").
	code, h, _ = e.fetchFull(t, "alpha.example.com", "/missing")
	if code != http.StatusNotFound {
		t.Fatalf("missing: %d", code)
	}

	checkOnce(t, "404", h, allHeaders)

	// The application's own copies are replaced, other headers stay.
	code, h, body = e.fetchFull(t, "beta.example.com", "/")
	if code != http.StatusOK || body != "php" {
		t.Fatalf("php: %d %q", code, body)
	}

	checkOnce(t, "php", h, allHeaders)

	if h.Get("X-App") != "kept" {
		t.Errorf("php: X-App lost: %v", h)
	}

	_, h, _ = e.fetchFull(t, "gamma.example.com", "/")
	for _, x := range allHeaders.List() {
		if h.Get(x.Name) != "" {
			t.Errorf("site without headers sends %s", x.Name)
		}
	}
}

func TestSecurityHeadersReportOnlyAndOff(t *testing.T) {
	e := newEnv(t)
	ctx := t.Context()

	s := e.site(t, "alpha", "hello", "alpha.example.com")
	s.PHPSocket = filepath.Join(e.dir, "php.sock")
	s.Headers = agentapi.WebHeaders{CSP: "default-src 'self'", CSPReportOnly: true}
	appServer(t, s.PHPSocket)

	if err := os.WriteFile(filepath.Join(s.Root, "index.php"), []byte("<?php"), 0o644); err != nil {
		t.Fatal(err)
	}

	if _, err := e.r.Recover(ctx); err != nil {
		t.Fatal(err)
	}

	if _, err := e.r.Apply(ctx, Desired{Sites: []Site{s}}); err != nil {
		t.Fatal(err)
	}

	// Report only: Hostashi's report-only policy, the application keeps
	// its own enforced policy and X-Frame-Options (switched off here).
	_, h, _ := e.fetchFull(t, "alpha.example.com", "/")
	checkOnce(t, "report only", h, s.Headers)

	if h.Get("Content-Security-Policy") != "default-src *" || h.Get("X-Frame-Options") != "SAMEORIGIN" {
		t.Errorf("application headers lost: %v", h)
	}
}

func TestSecurityHeadersWithRootSnippet(t *testing.T) {
	e := newEnv(t)
	ctx := t.Context()

	s := e.site(t, "alpha", "hello", "alpha.example.com")
	s.Home = s.Root
	s.Headers = agentapi.DefaultWebHeaders
	s.Snippets = Snippets{Root: "add_header X-Custom yes;\n"}

	if _, err := e.r.Recover(ctx); err != nil {
		t.Fatal(err)
	}

	if _, err := e.r.Apply(ctx, Desired{Sites: []Site{s}}); err != nil {
		t.Fatal(err)
	}

	_, h, _ := e.fetchFull(t, "alpha.example.com", "/")
	checkOnce(t, "root snippet", h, s.Headers)

	if h.Get("X-Custom") != "yes" {
		t.Errorf("snippet header lost: %v", h)
	}
}

func TestSecurityHeadersSnippetConflict(t *testing.T) {
	for name, sn := range map[string]Snippets{
		"server": {Server: "add_header x-frame-options DENY;\n"},
		"root":   {Root: "add_header X-Content-Type-Options nosniff always;\n"},
	} {
		s := Site{User: "alpha", Domains: []string{"alpha.example.com"}, Root: "/srv/alpha/htdocs", Home: "/srv/alpha", Snippets: sn, Headers: agentapi.DefaultWebHeaders}
		if err := s.checkHeaders(); !errors.Is(err, agentapi.ErrHeaderConflict) {
			t.Errorf("%s: got %v", name, err)
		}

		// Without the switch the snippet is fine.
		s.Headers = agentapi.WebHeaders{ReferrerPolicy: "origin"}
		if err := s.checkHeaders(); err != nil {
			t.Errorf("%s without conflict: %v", name, err)
		}
	}
}

func TestSecurityHeadersRender(t *testing.T) {
	n := &Nginx{
		Ctl: &testCtl{}, Listen: []string{"80"}, ListenTLS: []string{"443"}, HTTP3: true,
		ControlSocket: "/run/x.sock", PIDFile: "/run/x.pid", ErrorLog: "/var/log/x", MimeTypes: "/etc/x",
	}

	tls := Site{User: "alpha", Domains: []string{"a.example.com"}, Root: "/srv/a", Cert: "/c.pem", Key: "/k.pem", HSTS: true, Headers: allHeaders}
	disabled := Site{User: "beta", Domains: []string{"b.example.com"}, Root: "/srv/b", Disabled: true, Headers: allHeaders}

	f, err := n.Render(Desired{Sites: []Site{tls, disabled}}, 1)
	if err != nil {
		t.Fatal(err)
	}

	alpha := string(f.Content["sites/alpha.conf"])
	httpBlock, tlsBlock, _ := strings.Cut(alpha, "server {\n    listen 443")

	// The HTTP server only redirects: no headers there.
	if strings.Contains(httpBlock, "add_header") {
		t.Errorf("headers in the redirecting HTTP server:\n%s", httpBlock)
	}

	for _, want := range []string{
		`add_header Permissions-Policy 'geolocation=(self "https://maps.example.com"), camera=()' always;`,
		`add_header Content-Security-Policy "default-src 'self'; img-src 'self' data:" always;`,
		`add_header X-Frame-Options "DENY" always;`,
		`add_header Strict-Transport-Security "max-age=31536000" always;`,
	} {
		if strings.Count(tlsBlock, want) != 1 {
			t.Errorf("HTTPS server lacks %q once:\n%s", want, tlsBlock)
		}
	}

	// A disabled site sends them on its 503 but runs no PHP to hide from.
	beta := string(f.Content["sites/beta.conf"])
	if !strings.Contains(beta, `add_header X-Content-Type-Options "nosniff" always;`) || strings.Contains(beta, "fastcgi_hide_header") {
		t.Errorf("disabled site:\n%s", beta)
	}
}
