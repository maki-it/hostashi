// SPDX-License-Identifier: AGPL-3.0-or-later

package web

import (
	"errors"
	"strings"
	"testing"

	"github.com/maki-it/hostashi/internal/agentapi"
	"github.com/maki-it/hostashi/internal/profile"
)

func wordPressPreviewSite() agentapi.WebSite {
	p, _ := profile.Builtin(profile.WordPress)
	h := profile.Effective(p, profile.Overrides{}, true)

	return agentapi.WebSite{User: "shop", Domains: []string{"shop.example.com"}, PHP: "8.3", TLS: true, HSTS: true, Hardening: &h}
}

// TestPreviewSameRenderer: the preview is the server block Render writes
// for the same site with the placeholder paths.
func TestPreviewSameRenderer(t *testing.T) {
	ws := wordPressPreviewSite()

	got, err := Preview(ws)
	if err != nil {
		t.Fatal(err)
	}

	n := previewNginx

	f, err := n.Render(Desired{Sites: []Site{{
		User: "shop", Domains: ws.Domains, Root: PreviewDir + "/home/shop/htdocs", PHPSocket: PreviewDir + "/php/shop.sock",
		Cert: PreviewDir + "/certs/shop/fullchain.pem", Key: PreviewDir + "/certs/shop/privkey.pem", HSTS: true, Hardening: *ws.Hardening,
		BodyMax: agentapi.PHPSettings{}.ClientMaxBodySize(), FastCGIReadTimeout: agentapi.PHPSettings{}.FastCGIReadTimeout(),
	}}}, 0)
	if err != nil {
		t.Fatal(err)
	}

	if !strings.HasSuffix(got, string(f.Content["sites/shop.conf"])) {
		t.Errorf("preview is not the rendered server block:\n%s", got)
	}

	for _, want := range []string{
		"location = /xmlrpc.php", "location = /wp-login.php", "limit_req zone=hostashi_limit_",
		`wp-content/uploads)/.*\.(?:php|phtml|phar)$`, "fastcgi_pass unix:" + PreviewDir + "/php/shop.sock",
		"ssl_certificate_key " + PreviewDir + "/certs/shop/privkey.pem",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("preview misses %q:\n%s", want, got)
		}
	}
}

// TestPreviewNoSecrets: password hashes and user names never reach the
// preview, the password file is a placeholder.
func TestPreviewNoSecrets(t *testing.T) {
	ws := wordPressPreviewSite()
	hash := "$2y$10$abcdefghijklmnopqrstuuJ0Zr4xgYc5Hh0b0d0V9w5yQe8a7xW1C"
	ws.Access = &agentapi.WebAccess{Auth: &agentapi.WebAuth{Users: []agentapi.WebAuthUser{{Name: "kim-login", Hash: hash}}}}

	if err := ws.Access.Validate(); err != nil {
		t.Fatalf("test access is invalid: %v", err)
	}

	got, err := Preview(ws)
	if err != nil {
		t.Fatal(err)
	}

	for _, secret := range []string{hash, "kim-login", AuthFileName(Site{User: "shop", Access: *ws.Access})} {
		if strings.Contains(got, secret) {
			t.Errorf("preview holds %q", secret)
		}
	}

	if !strings.Contains(got, "auth_basic_user_file "+PreviewDir+"/auth/") {
		t.Errorf("no placeholder password file:\n%s", got)
	}
}

// TestPreviewRefused: sites without files and invalid state are refused.
func TestPreviewRefused(t *testing.T) {
	for name, ws := range map[string]agentapi.WebSite{
		"redirect": {User: "r", Domains: []string{"r.example.com"}, Redirect: &agentapi.WebRedirect{Target: "https://example.org", Status: 301}},
		"app":      {User: "a", Domains: []string{"a.example.com"}, App: &agentapi.WebApp{Port: 3000}},
	} {
		if _, err := Preview(ws); !errors.Is(err, ErrPreview) {
			t.Errorf("%s: %v", name, err)
		}
	}

	bad := map[string]func(*agentapi.WebSite){
		"web dir":   func(ws *agentapi.WebSite) { ws.WebDir = "../etc" },
		"hardening": func(ws *agentapi.WebSite) { ws.Hardening = &agentapi.WebHardening{DenyFiles: []string{"a;b"}} },
		"domain":    func(ws *agentapi.WebSite) { ws.Domains = []string{"bad domain"} },
		"user":      func(ws *agentapi.WebSite) { ws.User = "../x" },
	}

	for name, change := range bad {
		ws := wordPressPreviewSite()
		change(&ws)

		if _, err := Preview(ws); !errors.Is(err, ErrInvalidDesired) {
			t.Errorf("%s: %v", name, err)
		}
	}
}

// TestPreviewSnippets: snippets that fit are shown in their own file,
// others are left out with a note instead of failing the preview.
func TestPreviewSnippets(t *testing.T) {
	ws := wordPressPreviewSite()
	ws.Snippets = &agentapi.WebSnippets{Server: "client_max_body_size 8m;"}

	got, err := Preview(ws)
	if err != nil {
		t.Fatal(err)
	}

	if !strings.Contains(got, "# File snippets/shop-server.conf:") || !strings.Contains(got, "client_max_body_size 8m;") {
		t.Errorf("snippet missing:\n%s", got)
	}

	ws.Snippets = &agentapi.WebSnippets{Server: "location /x { alias /srv/real/home/shop/x; }"}

	got, err = Preview(ws)
	if err != nil {
		t.Fatal(err)
	}

	if strings.Contains(got, "/srv/real") || !strings.Contains(got, "custom snippets are left out") {
		t.Errorf("unfit snippet:\n%s", got)
	}
}

// TestPreviewServed: maintenance and the disabled state do not hide the
// rules in a preview.
func TestPreviewServed(t *testing.T) {
	ws := wordPressPreviewSite()
	ws.Disabled = true
	ws.Maintenance = &agentapi.WebMaintenance{Page: "x", Digest: "y"}

	got, err := Preview(ws)
	if err != nil {
		t.Fatal(err)
	}

	if !strings.Contains(got, "location = /xmlrpc.php") {
		t.Errorf("rules missing:\n%s", got)
	}
}
