// SPDX-License-Identifier: AGPL-3.0-or-later

package web

import (
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestStaticOptionsRender(t *testing.T) {
	n := &Nginx{Ctl: &testCtl{}, Listen: []string{"80"}, ControlSocket: "/run/x.sock", PIDFile: "/run/x.pid", ErrorLog: "/var/log/x", MimeTypes: "/etc/x"}

	f, err := n.Render(Desired{Sites: []Site{
		{User: "app", Domains: []string{"app.example.com"}, Root: "/srv/app/htdocs/dist", SPA: true, AssetCache: true},
		{User: "plain", Domains: []string{"plain.example.com"}, Root: "/srv/plain/htdocs"},
		{User: "own", Domains: []string{"own.example.com"}, Root: "/srv/own/htdocs", AssetCache: true, AssetCacheSeconds: 3600, AssetCacheExts: []string{"html", "json"}},
		{User: "wp", Domains: []string{"wp.example.com"}, Root: "/srv/wp/htdocs", PHPSocket: "/run/wp.sock", AssetCache: true},
		{User: "off", Domains: []string{"off.example.com"}, Root: "/srv/off/htdocs", SPA: true, AssetCache: true, Disabled: true},
	}}, 1)
	if err != nil {
		t.Fatal(err)
	}

	app := string(f.Content["sites/app.conf"])
	for _, want := range []string{
		"root /srv/app/htdocs/dist;", "try_files $uri $uri/ /index.html;", "map $uri $hostashi_expires_617070 {", "expires $hostashi_expires_617070;",
		`"~*\.(?:css|js|mjs|woff|woff2|ttf|otf|eot|svg|png|jpg|jpeg|gif|webp|avif|ico|svgz|mp4|webm|ogg|ogv)$" 2592000s;`,
	} {
		if !strings.Contains(app, want) {
			t.Errorf("app.conf lacks %q:\n%s", want, app)
		}
	}

	if own := string(f.Content["sites/own.conf"]); !strings.Contains(own, `"~*\.(?:html|json)$" 3600s;`) {
		t.Errorf("own.conf:\n%s", own)
	}

	// A PHP site caches assets too (ADR-0054 addendum 4); PHP answers
	// never match the map.
	if wp := string(f.Content["sites/wp.conf"]); !strings.Contains(wp, "expires $hostashi_expires_7770;") || !strings.Contains(wp, "fastcgi_pass unix:/run/wp.sock;") {
		t.Errorf("wp.conf:\n%s", wp)
	}

	plain := string(f.Content["sites/plain.conf"])
	if !strings.Contains(plain, "try_files $uri $uri/ =404;") || strings.Contains(plain, "expires") || strings.Contains(plain, "/index.html;") {
		t.Errorf("plain.conf:\n%s", plain)
	}

	// A disabled site serves nothing of its own: no map, no fallback.
	if off := string(f.Content["sites/off.conf"]); strings.Contains(off, "expires") || strings.Contains(off, "try_files") {
		t.Errorf("off.conf:\n%s", off)
	}

	for name, s := range map[string]Site{
		"php spa":       {User: "p", Domains: []string{"p.example.com"}, Root: "/srv/p", PHPSocket: "/run/p.sock", SPA: true},
		"bad extension": {User: "r", Domains: []string{"r.example.com"}, Root: "/srv/r", AssetCache: true, AssetCacheExts: []string{`css)$" 1d; } server { #`}},
		"short time":    {User: "u", Domains: []string{"u.example.com"}, Root: "/srv/u", AssetCache: true, AssetCacheSeconds: 1},
	} {
		if err := (Desired{Sites: []Site{s}}).Validate(); err == nil {
			t.Errorf("%s accepted", name)
		}
	}
}

// TestStaticOptionsServed checks the fallback and the cache headers with
// the real nginx.
func TestStaticOptionsServed(t *testing.T) {
	e := newEnv(t)
	ctx := t.Context()

	if _, err := e.r.Recover(ctx); err != nil {
		t.Fatal(err)
	}

	spa := e.site(t, "spa", "app shell", "spa.example.com")
	if err := os.WriteFile(filepath.Join(spa.Root, "app.js"), []byte("js"), 0o644); err != nil {
		t.Fatal(err)
	}

	spa.SPA, spa.AssetCache = true, true

	own := e.site(t, "own", "own index", "own.example.com")
	if err := os.WriteFile(filepath.Join(own.Root, "data.json"), []byte("{}"), 0o644); err != nil {
		t.Fatal(err)
	}

	if err := os.WriteFile(filepath.Join(own.Root, "app.js"), []byte("js"), 0o644); err != nil {
		t.Fatal(err)
	}

	own.AssetCache, own.AssetCacheSeconds, own.AssetCacheExts = true, 3600, []string{"json"}
	plain := e.site(t, "plain", "plain index", "plain.example.com")

	if _, err := e.r.Apply(ctx, Desired{Sites: []Site{spa, plain, own}}); err != nil {
		t.Fatal(err)
	}

	if code, body := e.fetchPath(t, "spa.example.com", "/some/route"); code != http.StatusOK || body != "app shell" {
		t.Errorf("spa route: %d %q", code, body)
	}

	if code, h, body := e.fetchFull(t, "spa.example.com", "/app.js"); code != http.StatusOK || body != "js" || h.Get("Cache-Control") != "max-age=2592000" {
		t.Errorf("asset: %d %q %v", code, body, h)
	}

	if code, h, _ := e.fetchFull(t, "spa.example.com", "/"); code != http.StatusOK || h.Get("Cache-Control") != "" {
		t.Errorf("index cached: %d %v", code, h)
	}

	if _, h, _ := e.fetchFull(t, "own.example.com", "/data.json"); h.Get("Cache-Control") != "max-age=3600" {
		t.Errorf("own json: %v", h)
	}

	if _, h, _ := e.fetchFull(t, "own.example.com", "/app.js"); h.Get("Cache-Control") != "" {
		t.Errorf("own js cached: %v", h)
	}

	if code, _ := e.fetchPath(t, "plain.example.com", "/some/route"); code != http.StatusNotFound {
		t.Errorf("plain route: %d", code)
	}

	// Hidden files stay hidden with the fallback.
	if code, _ := e.fetchPath(t, "spa.example.com", "/.env"); code != http.StatusNotFound {
		t.Errorf("hidden file: %d", code)
	}
}
