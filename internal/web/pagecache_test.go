// SPDX-License-Identifier: AGPL-3.0-or-later

package web

import (
	"bufio"
	"context"
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/fcgi"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/maki-it/hostashi/internal/agentapi"
)

// countingFCGI answers every request with a running number, so a cached
// answer shows the number of an earlier one. /cookie.php sets a cookie and
// /private.php sends Cache-Control: private.
func countingFCGI(t *testing.T, sock string) *atomic.Int64 {
	t.Helper()

	l, err := net.Listen("unix", sock)
	if err != nil {
		t.Fatal(err)
	}

	t.Cleanup(func() { _ = l.Close() })

	var n atomic.Int64

	go func() {
		_ = fcgi.Serve(l, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			switch filepath.Base(fcgi.ProcessEnv(r)["SCRIPT_FILENAME"]) {
			case "cookie.php":
				w.Header().Set("Set-Cookie", "session=secret")
			case "private.php":
				w.Header().Set("Cache-Control", "private")
			}

			_, _ = fmt.Fprintf(w, "answer %d", n.Add(1))
		}))
	}()

	return &n
}

// request sends one request over TCP and returns the status, the X-Cache
// header and the body.
func (e *env) request(t *testing.T, method, host, p string, header map[string]string) (int, string, string) {
	t.Helper()

	conn, err := net.DialTimeout("tcp", "127.0.0.1:"+strconv.Itoa(e.port), 2*time.Second)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}

	defer conn.Close()

	_ = conn.SetDeadline(time.Now().Add(3 * time.Second))

	var b strings.Builder
	fmt.Fprintf(&b, "%s %s HTTP/1.1\r\nHost: %s\r\nConnection: close\r\n", method, p, host)

	for k, v := range header {
		fmt.Fprintf(&b, "%s: %s\r\n", k, v)
	}

	if method == http.MethodPost {
		b.WriteString("Content-Length: 0\r\n")
	}

	b.WriteString("\r\n")
	_, _ = io.WriteString(conn, b.String())

	req := &http.Request{Method: method}

	resp, err := http.ReadResponse(bufio.NewReader(conn), req)
	if err != nil {
		t.Fatalf("%s %s: %v", method, p, err)
	}

	defer resp.Body.Close()

	body, _ := io.ReadAll(resp.Body)

	return resp.StatusCode, resp.Header.Get("X-Cache"), string(body)
}

// cacheSite is a PHP site with index.php, cookie.php and private.php and a
// page cache in its own directory.
func (e *env) cacheSite(t *testing.T, user, preset string, exclude ...string) Site {
	t.Helper()

	s := e.site(t, user, "static", user+".example.com")
	s.PHPSocket = filepath.Join(e.dir, user+".sock")

	if err := os.MkdirAll(filepath.Join(s.Root, "wp-admin"), 0o755); err != nil {
		t.Fatal(err)
	}

	for _, name := range []string{"index.php", "cookie.php", "private.php", "wp-login.php", "xmlrpc.php", "wp-admin/edit.php"} {
		if err := os.WriteFile(filepath.Join(s.Root, name), []byte("<?php"), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	dir := filepath.Join(e.dir, "cache", user)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}

	s.PageCache = &PageCache{Dir: dir, Preset: preset, Minutes: 10, SizeMB: 64, Exclude: exclude}

	return s
}

func TestPageCacheWordPress(t *testing.T) {
	e := newEnv(t)
	ctx := t.Context()

	s := e.cacheSite(t, "alpha", agentapi.PageCacheWordPress, "/shop", "/blog*")
	countingFCGI(t, s.PHPSocket)

	if _, err := e.r.Recover(ctx); err != nil {
		t.Fatal(err)
	}

	if _, err := e.r.Apply(ctx, Desired{Sites: []Site{s}}); err != nil {
		t.Fatal(err)
	}

	host := "alpha.example.com"

	// The first answer fills the cache, the second comes from it.
	code, xc, first := e.request(t, http.MethodGet, host, "/", nil)
	if code != http.StatusOK || xc != "MISS" {
		t.Fatalf("first GET: %d %q %q", code, xc, first)
	}

	if code, xc, body := e.request(t, http.MethodGet, host, "/", nil); code != http.StatusOK || xc != "HIT" || body != first {
		t.Fatalf("second GET: %d %q %q, want HIT %q", code, xc, body, first)
	}

	// HEAD has its own key, so a cached HEAD never answers a GET.
	if _, xc, _ := e.request(t, http.MethodHead, host, "/", nil); xc != "MISS" {
		t.Fatalf("first HEAD: %q", xc)
	}

	// Static files never pass the cache.
	if code, xc, body := e.request(t, http.MethodGet, host, "/index.html", nil); code != http.StatusOK || xc != "" || body != "static" {
		t.Fatalf("static file: %d %q %q", code, xc, body)
	}

	for name, c := range map[string]struct {
		method, path string
		header       map[string]string
	}{
		"logged in":        {http.MethodGet, "/", map[string]string{"Cookie": "wordpress_logged_in_abc=1"}},
		"second cookie":    {http.MethodGet, "/", map[string]string{"Cookie": "a=1; wordpress_sec_x=2"}},
		"post password":    {http.MethodGet, "/", map[string]string{"Cookie": "wp-postpass_1=x"}},
		"comment author":   {http.MethodGet, "/", map[string]string{"Cookie": "comment_author_1=x"}},
		"authorization":    {http.MethodGet, "/", map[string]string{"Authorization": "Basic eDp5"}},
		"admin page":       {http.MethodGet, "/wp-admin/edit.php?x=1", nil},
		"login":            {http.MethodGet, "/wp-login.php", nil},
		"rest api":         {http.MethodGet, "/wp-json/wp/v2/posts", nil},
		"xmlrpc":           {http.MethodGet, "/xmlrpc.php", nil},
		"preview":          {http.MethodGet, "/?p=1&preview=true", nil},
		"excluded path":    {http.MethodGet, "/shop", nil},
		"excluded + query": {http.MethodGet, "/shop?page=2", nil},
		"excluded prefix":  {http.MethodGet, "/blog/post-1", nil},
		"excluded prefix2": {http.MethodGet, "/blogroll", nil},
	} {
		for range 2 {
			if _, xc, _ := e.request(t, c.method, host, c.path, c.header); xc != "BYPASS" {
				t.Errorf("%s: X-Cache %q, want BYPASS", name, xc)
			}
		}
	}

	// Other methods never look into the cache at all.
	for range 2 {
		if _, xc, body := e.request(t, http.MethodPost, host, "/", nil); xc != "" || body == first {
			t.Errorf("POST: X-Cache %q, body %q", xc, body)
		}
	}

	// Near misses are cached.
	for name, c := range map[string]struct {
		path   string
		header map[string]string
	}{
		"other cookie":         {"/", map[string]string{"Cookie": "_ga=1"}},
		"cookie name contains": {"/", map[string]string{"Cookie": "my_wordpress_logged_in_x=1"}},
		"path after excluded":  {"/shopping", nil},
		"subpath of exact":     {"/shop/item", nil},
		"admin elsewhere":      {"/page/wp-admin/", nil},
	} {
		e.request(t, http.MethodGet, host, c.path, c.header)

		if _, xc, _ := e.request(t, http.MethodGet, host, c.path, c.header); xc != "HIT" {
			t.Errorf("%s: X-Cache %q, want HIT", name, xc)
		}
	}

	// The application's own headers decide: no cache for Set-Cookie or
	// Cache-Control: private.
	for _, p := range []string{"/cookie.php", "/private.php"} {
		for range 2 {
			if _, xc, _ := e.request(t, http.MethodGet, host, p, nil); xc != "MISS" {
				t.Errorf("%s: X-Cache %q, want MISS", p, xc)
			}
		}
	}

	// Purging means an empty directory (pagecache.purge in the agent): the
	// next request is a miss, never an error.
	if err := os.RemoveAll(s.PageCache.Dir); err != nil {
		t.Fatal(err)
	}

	if err := os.Mkdir(s.PageCache.Dir, 0o700); err != nil {
		t.Fatal(err)
	}

	if code, xc, body := e.request(t, http.MethodGet, host, "/", nil); code != http.StatusOK || xc != "MISS" || body == first {
		t.Fatalf("after purge: %d %q %q", code, xc, body)
	}

	if _, xc, _ := e.request(t, http.MethodGet, host, "/", nil); xc != "HIT" {
		t.Fatalf("after purge, second GET: %q", xc)
	}
}

func TestPageCacheWooCommerce(t *testing.T) {
	e := newEnv(t)
	ctx := t.Context()

	s := e.cacheSite(t, "shop", agentapi.PageCacheWooCommerce)
	countingFCGI(t, s.PHPSocket)

	if _, err := e.r.Recover(ctx); err != nil {
		t.Fatal(err)
	}

	if _, err := e.r.Apply(ctx, Desired{Sites: []Site{s}}); err != nil {
		t.Fatal(err)
	}

	host := "shop.example.com"

	for name, c := range map[string]struct {
		path   string
		header map[string]string
	}{
		"cart cookie":    {"/", map[string]string{"Cookie": "woocommerce_items_in_cart=1"}},
		"session cookie": {"/", map[string]string{"Cookie": "wp_woocommerce_session_abc=1"}},
		"logged in":      {"/", map[string]string{"Cookie": "wordpress_logged_in_x=1"}},
		"cart":           {"/cart/", nil},
		"checkout":       {"/checkout/", nil},
		"account":        {"/my-account/orders/", nil},
		"add to cart":    {"/?add-to-cart=12", nil},
		"ajax":           {"/?wc-ajax=get_refreshed_fragments", nil},
		"admin":          {"/wp-admin/edit.php", nil},
	} {
		if _, xc, _ := e.request(t, http.MethodGet, host, c.path, c.header); xc != "BYPASS" {
			t.Errorf("%s: X-Cache %q, want BYPASS", name, xc)
		}
	}

	e.request(t, http.MethodGet, host, "/product/x/", nil)

	if _, xc, _ := e.request(t, http.MethodGet, host, "/product/x/", nil); xc != "HIT" {
		t.Fatalf("product page: %q, want HIT", xc)
	}
}

func TestPageCacheGenericPHPSkipsEveryCookie(t *testing.T) {
	e := newEnv(t)
	ctx := t.Context()

	s := e.cacheSite(t, "app", agentapi.PageCachePHP)
	countingFCGI(t, s.PHPSocket)

	if _, err := e.r.Recover(ctx); err != nil {
		t.Fatal(err)
	}

	if _, err := e.r.Apply(ctx, Desired{Sites: []Site{s}}); err != nil {
		t.Fatal(err)
	}

	host := "app.example.com"

	if _, xc, _ := e.request(t, http.MethodGet, host, "/", map[string]string{"Cookie": "_ga=1"}); xc != "BYPASS" {
		t.Fatalf("cookie: %q, want BYPASS", xc)
	}

	// The WordPress paths mean nothing here.
	e.request(t, http.MethodGet, host, "/wp-admin/edit.php", nil)

	if _, xc, _ := e.request(t, http.MethodGet, host, "/wp-admin/edit.php", nil); xc != "HIT" {
		t.Fatalf("no cookie: %q, want HIT", xc)
	}
}

// Cached answers keep the site's security headers: X-Cache sits at the
// server level next to them, not in the PHP location.
func TestPageCacheKeepsSecurityHeaders(t *testing.T) {
	e := newEnv(t)
	ctx := t.Context()

	tlsPort := freePort(t)
	e.r.Driver.ListenTLS = []string{"127.0.0.1:" + strconv.Itoa(tlsPort)}
	e.r.Driver.LegacyHTTP2 = legacyHTTP2(t, e.ctl.bin)

	s := e.cacheSite(t, "alpha", agentapi.PageCacheWordPress)
	countingFCGI(t, s.PHPSocket)

	certDir := filepath.Join(e.dir, "certs")
	if err := os.MkdirAll(certDir, 0o700); err != nil {
		t.Fatal(err)
	}

	var pool *x509.CertPool

	s.Cert, s.Key, pool = selfSigned(t, certDir, "alpha.example.com")
	s.HSTS = true

	if _, err := e.r.Recover(ctx); err != nil {
		t.Fatal(err)
	}

	if _, err := e.r.Apply(ctx, Desired{Sites: []Site{s}}); err != nil {
		t.Fatal(err)
	}

	addr := "127.0.0.1:" + strconv.Itoa(tlsPort)
	client := &http.Client{Timeout: 3 * time.Second, Transport: &http.Transport{
		TLSClientConfig: &tls.Config{RootCAs: pool, MinVersion: tls.VersionTLS12},
		DialContext: func(ctx context.Context, network, _ string) (net.Conn, error) {
			var d net.Dialer

			return d.DialContext(ctx, network, addr)
		},
	}}

	for i, want := range []string{"MISS", "HIT"} {
		resp, err := client.Get("https://alpha.example.com/")
		if err != nil {
			t.Fatal(err)
		}

		_, _ = io.Copy(io.Discard, resp.Body)
		_ = resp.Body.Close()

		if got := resp.Header.Get("X-Cache"); got != want {
			t.Fatalf("request %d: X-Cache %q, want %q", i, got, want)
		}

		if h := resp.Header.Get("Strict-Transport-Security"); h != "max-age=31536000" {
			t.Fatalf("request %d (%s): HSTS header %q", i, want, h)
		}
	}
}

func TestPageCacheRender(t *testing.T) {
	n := &Nginx{
		Ctl: &testCtl{}, Listen: []string{"80"},
		ControlSocket: "/run/x.sock", PIDFile: "/run/x.pid", ErrorLog: "/var/log/x", MimeTypes: "/etc/x",
	}

	cache := &PageCache{Dir: "/srv/.hostashi-cache/my-site", Preset: agentapi.PageCacheWordPress, Minutes: 30, SizeMB: 512, Exclude: []string{"/a.b", "/c*"}}
	site := Site{User: "my-site", Domains: []string{"a.example.com"}, Root: "/srv/a", PHPSocket: "/run/php.sock", PageCache: cache}

	f, err := n.Render(Desired{Sites: []Site{site}}, 1)
	if err != nil {
		t.Fatal(err)
	}

	conf := string(f.Content["sites/my-site.conf"])
	id := "6d792d73697465" // hex of "my-site"

	for _, want := range []string{
		"fastcgi_cache_path /srv/.hostashi-cache/my-site levels=1:2 keys_zone=hostashi_cache_" + id + ":2m max_size=512m inactive=1d use_temp_path=off;",
		"fastcgi_cache hostashi_cache_" + id + ";",
		"fastcgi_cache_valid 200 301 302 30m;",
		`"~^/a\.b([?]|$)" 1;`,
		`"~^/c" 1;`,
		`"~(^|;[ ]*)wordpress_logged_in_" 1;`,
		"$hostashi_cache_cookie_" + id + " $hostashi_cache_path_" + id + ";",
	} {
		if !strings.Contains(conf, want) {
			t.Errorf("missing %q in\n%s", want, conf)
		}
	}

	// No add_header inside the PHP location: it would drop the server's.
	_, php, _ := strings.Cut(conf, `location ~ \.php$ {`)
	php, _, _ = strings.Cut(php, "\n    }")

	if strings.Contains(php, "add_header") {
		t.Errorf("add_header in the PHP location:\n%s", php)
	}

	// Disabled, in maintenance or without PHP: no cache at all.
	for name, s := range map[string]Site{
		"disabled":    {User: "my-site", Domains: site.Domains, Root: site.Root, PageCache: cache, Disabled: true},
		"maintenance": {User: "my-site", Domains: site.Domains, Root: site.Root, PHPSocket: site.PHPSocket, PageCache: cache, Maintenance: &Maintenance{Page: "p1", Digest: strings.Repeat("a", 64), Dir: "/srv/pages/p1"}},
	} {
		f, err := n.Render(Desired{Sites: []Site{s}}, 1)
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}

		if c := string(f.Content["sites/my-site.conf"]); strings.Contains(c, "fastcgi_cache") || strings.Contains(c, "X-Cache") {
			t.Errorf("%s: cache rendered:\n%s", name, c)
		}
	}
}

func TestPageCacheValidate(t *testing.T) {
	good := PageCache{Dir: "/srv/.hostashi-cache/alpha", Preset: agentapi.PageCachePHP, Minutes: 10, SizeMB: 256}
	site := Site{User: "alpha", Domains: []string{"a.example.com"}, Root: "/srv/a", PHPSocket: "/run/php.sock"}

	for name, tc := range map[string]struct {
		mod func(*Site)
		ok  bool
	}{
		"fine":            {func(*Site) {}, true},
		"no php":          {func(s *Site) { s.PHPSocket = "" }, false},
		"relative dir":    {func(s *Site) { s.PageCache.Dir = "cache" }, false},
		"dir with space":  {func(s *Site) { s.PageCache.Dir = "/srv/a b" }, false},
		"dir with dots":   {func(s *Site) { s.PageCache.Dir = "/srv/../etc" }, false},
		"dir with semi":   {func(s *Site) { s.PageCache.Dir = "/srv/a;b" }, false},
		"unknown preset":  {func(s *Site) { s.PageCache.Preset = "x" }, false},
		"bad minutes":     {func(s *Site) { s.PageCache.Minutes = 0 }, false},
		"bad size":        {func(s *Site) { s.PageCache.SizeMB = 1 }, false},
		"bad exclude":     {func(s *Site) { s.PageCache.Exclude = []string{`/a"; deny all; "`} }, false},
		"exclude brace":   {func(s *Site) { s.PageCache.Exclude = []string{"/a}"} }, false},
		"exclude dollar":  {func(s *Site) { s.PageCache.Exclude = []string{"/$host"} }, false},
		"exclude newline": {func(s *Site) { s.PageCache.Exclude = []string{"/a\n"} }, false},
	} {
		s := site
		c := good
		s.PageCache = &c
		tc.mod(&s)

		err := Desired{Sites: []Site{s}}.Validate()
		if (err == nil) != tc.ok {
			t.Errorf("%s: err = %v", name, err)
		}
	}
}

func TestSafeQuoted(t *testing.T) {
	for s, want := range map[string]bool{
		`~^/a\.b`:      true,
		`~(^|;[ ]*)x_`: true,
		`a"b`:          false,
		`a\nb`:         false,
		`a\tb`:         false,
		`a\\b`:         false,
		`a\`:           false,
		"a\x01b":       false,
	} {
		if got := safeQuoted(s); got != want {
			t.Errorf("safeQuoted(%q) = %v, want %v", s, got, want)
		}
	}
}
