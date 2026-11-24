// SPDX-License-Identifier: AGPL-3.0-or-later

package web

import (
	"context"
	"crypto/tls"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/maki-it/hostashi/internal/agentapi"
)

func TestWWWRender(t *testing.T) {
	n := &Nginx{Ctl: &testCtl{}, Listen: []string{"80"}, ListenTLS: []string{"443"}, ControlSocket: "/run/x.sock", PIDFile: "/run/x.pid", ErrorLog: "/var/log/x", MimeTypes: "/etc/x", ACMERoot: "/var/lib/acme"}

	s := Site{
		User: "shop", Domains: []string{"example.com", "www.example.com", "shop.example.com"}, Root: "/srv/shop",
		Cert: "/c.pem", Key: "/k.pem", HSTS: true, WWWRedirect: agentapi.WWWRedirectToWWW,
		Snippets: Snippets{Server: "client_max_body_size 5m;"}, Home: "/srv",
	}

	f, err := n.Render(Desired{Sites: []Site{s}}, 1)
	if err != nil {
		t.Fatal(err)
	}

	c := string(f.Content["sites/shop.conf"])

	if strings.Count(c, "server_name www.example.com shop.example.com;") != 2 {
		t.Errorf("own servers do not serve exactly the other names:\n%s", c)
	}

	if strings.Count(c, "server_name example.com;") != 2 || strings.Count(c, "return 301 https://www.example.com$request_uri;") != 2 {
		t.Errorf("no HTTP and HTTPS redirect for example.com:\n%s", c)
	}

	// The redirect server has the ACME location, the certificate and HSTS.
	_, redirect, _ := strings.Cut(c, "# www redirect (ADR-0065)")
	for _, want := range []string{"/.well-known/acme-challenge/", "ssl_certificate /c.pem;", "Strict-Transport-Security"} {
		if !strings.Contains(redirect, want) {
			t.Errorf("redirect servers lack %q:\n%s", want, redirect)
		}
	}

	// The snippet stays in the site's own server.
	if strings.Count(c, "snippets/shop-server.conf") != 1 || strings.Contains(redirect, "snippets/") {
		t.Errorf("snippet:\n%s", c)
	}

	// Without a certificate the redirect stays on http.
	s.Cert, s.Key, s.WWWRedirect = "", "", agentapi.WWWRedirectToNonWWW

	f, err = n.Render(Desired{Sites: []Site{s}}, 1)
	if err != nil {
		t.Fatal(err)
	}

	c = string(f.Content["sites/shop.conf"])
	if !strings.Contains(c, "server_name www.example.com;") || !strings.Contains(c, "return 301 http://example.com$request_uri;") || strings.Contains(c, "ssl_certificate") {
		t.Errorf("http redirect:\n%s", c)
	}

	// Off renders no redirect server.
	s.WWWRedirect = ""

	f, err = n.Render(Desired{Sites: []Site{s}}, 1)
	if err != nil {
		t.Fatal(err)
	}

	if c := string(f.Content["sites/shop.conf"]); strings.Contains(c, "www redirect") || !strings.Contains(c, "server_name example.com www.example.com shop.example.com;") {
		t.Errorf("off:\n%s", c)
	}
}

func TestWWWValidate(t *testing.T) {
	for name, s := range map[string]Site{
		"bad mode":      {User: "alpha", Domains: []string{"a.example.com"}, Root: "/srv/a", WWWRedirect: "both"},
		"redirect site": {User: "alpha", Domains: []string{"a.example.com", "www.a.example.com"}, WWWRedirect: "to-www", Redirect: &agentapi.WebRedirect{Target: "https://b.example.com", Status: 301}},
	} {
		if err := (Desired{Sites: []Site{s}}).Validate(); err == nil {
			t.Errorf("%s accepted", name)
		}
	}

	ok := Site{User: "alpha", Domains: []string{"a.example.com"}, Root: "/srv/a", WWWRedirect: "to-www"}
	if err := (Desired{Sites: []Site{ok}}).Validate(); err != nil {
		t.Errorf("mode without a pair: %v", err)
	}
}

// TestWWWServed checks the redirect with the real nginx over HTTP and
// HTTPS, with the primary domain being the redirected one, so the health
// check must use the other name.
func TestWWWServed(t *testing.T) {
	e := newEnv(t)
	ctx := t.Context()

	tlsPort := freePort(t)
	e.r.Driver.ListenTLS = []string{"127.0.0.1:" + strconv.Itoa(tlsPort)}
	e.r.Driver.LegacyHTTP2 = legacyHTTP2(t, e.ctl.bin)
	e.r.Driver.ACMERoot = filepath.Join(e.dir, "acme")

	challenge := filepath.Join(e.r.Driver.ACMERoot, ".well-known", "acme-challenge")
	if err := os.MkdirAll(challenge, 0o755); err != nil {
		t.Fatal(err)
	}

	if err := os.WriteFile(filepath.Join(challenge, "tok"), []byte("tok.thumb"), 0o644); err != nil {
		t.Fatal(err)
	}

	certDir := filepath.Join(e.dir, "certs")
	if err := os.MkdirAll(certDir, 0o700); err != nil {
		t.Fatal(err)
	}

	site := e.site(t, "shop", "shop index", "example.com", "www.example.com", "shop.example.com")
	site.Cert, site.Key, _ = selfSigned(t, certDir, site.Domains...)
	site.HSTS = true
	site.WWWRedirect = agentapi.WWWRedirectToWWW
	plain := e.site(t, "plain", "plain index", "plain.example.com", "www.plain.example.com")
	plain.WWWRedirect = agentapi.WWWRedirectToNonWWW

	if _, err := e.r.Recover(ctx); err != nil {
		t.Fatal(err)
	}

	if _, err := e.r.Apply(ctx, Desired{Sites: []Site{site, plain}}); err != nil {
		t.Fatal(err)
	}

	// HTTP: one hop straight to https://www for the TLS site.
	if code, h, _ := e.fetchFull(t, "example.com", "/a?b=1"); code != http.StatusMovedPermanently || h.Get("Location") != "https://www.example.com/a?b=1" {
		t.Errorf("http example.com: %d %v", code, h)
	}

	if code, body := e.fetchPath(t, "example.com", "/.well-known/acme-challenge/tok"); code != http.StatusOK || body != "tok.thumb" {
		t.Errorf("challenge on the redirected name: %d %q", code, body)
	}

	if code, h, _ := e.fetchFull(t, "www.plain.example.com", "/x"); code != http.StatusMovedPermanently || h.Get("Location") != "http://plain.example.com/x" {
		t.Errorf("http www.plain: %d %v", code, h)
	}

	if code, body := e.fetch(t, "plain.example.com"); code != http.StatusOK || body != "plain index" {
		t.Errorf("plain: %d %q", code, body)
	}

	addr := "127.0.0.1:" + strconv.Itoa(tlsPort)
	client := &http.Client{
		Timeout:       3 * time.Second,
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
		Transport: &http.Transport{
			TLSClientConfig: &tls.Config{InsecureSkipVerify: true, MinVersion: tls.VersionTLS12},
			DialContext: func(ctx context.Context, network, _ string) (net.Conn, error) {
				var d net.Dialer

				return d.DialContext(ctx, network, addr)
			},
		},
	}

	get := func(u string) (int, http.Header) {
		t.Helper()

		resp, err := client.Get(u)
		if err != nil {
			t.Fatal(err)
		}

		_ = resp.Body.Close()

		return resp.StatusCode, resp.Header
	}

	if code, h := get("https://example.com/p"); code != http.StatusMovedPermanently || h.Get("Location") != "https://www.example.com/p" || h.Get("Strict-Transport-Security") == "" {
		t.Errorf("https example.com: %d %v", code, h)
	}

	for _, u := range []string{"https://www.example.com/", "https://shop.example.com/"} {
		if code, _ := get(u); code != http.StatusOK {
			t.Errorf("%s: %d", u, code)
		}
	}
}
