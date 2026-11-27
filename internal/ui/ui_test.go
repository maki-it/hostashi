// SPDX-License-Identifier: AGPL-3.0-or-later

package ui

import (
	"compress/gzip"
	"io"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"testing"
	"testing/fstest"
)

var bigJS = strings.Repeat("console.log('hostashi');\n", 200)

func testFS() fstest.MapFS {
	return fstest.MapFS{
		"index.html":         {Data: []byte("<!doctype html><title>Hostashi</title>")},
		"assets/app-abc.js":  {Data: []byte(bigJS)},
		"assets/tiny-abc.js": {Data: []byte("x()")},
		"assets/logo.png":    {Data: []byte("\x89PNG\r\n\x1a\n" + strings.Repeat("\x00", 2000))},
		".gitkeep":           {Data: nil},
	}
}

func get(t *testing.T, h http.Handler, method, target string, hdr map[string]string) *httptest.ResponseRecorder {
	t.Helper()

	req := httptest.NewRequestWithContext(t.Context(), method, target, nil)
	for k, v := range hdr {
		req.Header.Set(k, v)
	}

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	return rec
}

func TestIndexAndSPAFallback(t *testing.T) {
	h := newHandler(testFS())

	for _, p := range []string{"/", "/login", "/sites/blog/settings", "/index.html", "/a/../b", "/%2e%2e/etc/passwd"} {
		rec := get(t, h, http.MethodGet, p, nil)
		if rec.Code != http.StatusOK {
			t.Fatalf("%s: status %d", p, rec.Code)
		}

		if !strings.Contains(rec.Body.String(), "<title>Hostashi</title>") {
			t.Fatalf("%s: body %q", p, rec.Body.String())
		}

		if got := rec.Header().Get("Cache-Control"); got != "no-store" {
			t.Fatalf("%s: Cache-Control %q", p, got)
		}

		if got := rec.Header().Get("Content-Security-Policy"); !strings.Contains(got, "script-src 'self'") || strings.Contains(got, "unsafe") {
			t.Fatalf("%s: CSP %q", p, got)
		}

		if got := rec.Header().Get("Content-Type"); !strings.HasPrefix(got, "text/html") {
			t.Fatalf("%s: Content-Type %q", p, got)
		}
	}
}

func TestAssets(t *testing.T) {
	h := newHandler(testFS())

	rec := get(t, h, http.MethodGet, "/assets/app-abc.js", nil)
	if rec.Code != http.StatusOK || rec.Body.String() != bigJS {
		t.Fatalf("status %d, %d bytes", rec.Code, rec.Body.Len())
	}

	if got := rec.Header().Get("Cache-Control"); got != "public, max-age=31536000, immutable" {
		t.Fatalf("Cache-Control %q", got)
	}

	if got := rec.Header().Get("Content-Type"); !strings.Contains(got, "javascript") {
		t.Fatalf("Content-Type %q", got)
	}

	if rec.Header().Get("Content-Encoding") != "" {
		t.Fatal("gzip without Accept-Encoding")
	}

	if rec.Header().Get("Vary") != "Accept-Encoding" {
		t.Fatalf("Vary %q", rec.Header().Get("Vary"))
	}
}

func TestGzip(t *testing.T) {
	h := newHandler(testFS())

	rec := get(t, h, http.MethodGet, "/assets/app-abc.js", map[string]string{"Accept-Encoding": "br, gzip;q=0.8"})
	if rec.Header().Get("Content-Encoding") != "gzip" {
		t.Fatalf("Content-Encoding %q", rec.Header().Get("Content-Encoding"))
	}

	if rec.Body.Len() >= len(bigJS) {
		t.Fatalf("not smaller: %d", rec.Body.Len())
	}

	zr, err := gzip.NewReader(rec.Body)
	if err != nil {
		t.Fatal(err)
	}

	plain, err := io.ReadAll(zr)
	if err != nil || string(plain) != bigJS {
		t.Fatalf("decompressed mismatch: %v", err)
	}

	// Refused, too small, or not text: served as is.
	for _, c := range []struct{ path, ae string }{
		{"/assets/app-abc.js", "gzip;q=0"},
		{"/assets/app-abc.js", "identity"},
		{"/assets/tiny-abc.js", "gzip"},
		{"/assets/logo.png", "gzip"},
	} {
		rec := get(t, h, http.MethodGet, c.path, map[string]string{"Accept-Encoding": c.ae})
		if rec.Header().Get("Content-Encoding") != "" {
			t.Fatalf("%s with %q: compressed", c.path, c.ae)
		}
	}
}

func TestNotFound(t *testing.T) {
	h := newHandler(testFS())

	for _, p := range []string{"/missing.ico", "/assets/missing.js", "/.gitkeep"} {
		if rec := get(t, h, http.MethodGet, p, nil); rec.Code != http.StatusNotFound {
			t.Fatalf("%s: status %d", p, rec.Code)
		}
	}
}

func TestMethods(t *testing.T) {
	h := newHandler(testFS())

	if rec := get(t, h, http.MethodPost, "/login", nil); rec.Code != http.StatusMethodNotAllowed {
		t.Fatalf("POST: status %d", rec.Code)
	}

	rec := get(t, h, http.MethodHead, "/", nil)
	if rec.Code != http.StatusOK || rec.Body.Len() != 0 {
		t.Fatalf("HEAD: status %d, %d bytes", rec.Code, rec.Body.Len())
	}
}

func TestNotBuilt(t *testing.T) {
	h := newHandler(fstest.MapFS{".gitkeep": {Data: nil}})

	rec := get(t, h, http.MethodGet, "/", nil)
	if rec.Code != http.StatusServiceUnavailable || !strings.Contains(rec.Body.String(), "npm run build") {
		t.Fatalf("status %d, body %q", rec.Code, rec.Body.String())
	}
}

// TestEmbedded checks the real embedded directory: it must at least
// compile and answer, whether or not the frontend was built.
func TestEmbedded(t *testing.T) {
	rec := get(t, Handler(), http.MethodGet, "/", nil)
	if rec.Code != http.StatusOK && rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("status %d", rec.Code)
	}

	// A built frontend has the nonce tag the editor needs (ADR-0040).
	if rec.Code == http.StatusOK && !strings.Contains(rec.Header().Get("Content-Security-Policy"), "'nonce-") {
		t.Fatalf("built index.html without the style nonce: %q", rec.Header().Get("Content-Security-Policy"))
	}
}

func TestAcceptsGzip(t *testing.T) {
	cases := map[string]bool{
		"":                  false,
		"gzip":              true,
		"GZIP":              true,
		"deflate, gzip":     true,
		"gzip;q=0":          false,
		"gzip; q=0.000":     false,
		"gzip;q=0.5":        true,
		"x-gzip":            false,
		"br;q=1, identity":  false,
		"*":                 false,
		"gzip;q=0, gzip":    false,
		"compress,gzip;q=1": true,
	}

	for in, want := range cases {
		if got := acceptsGzip([]string{in}); got != want {
			t.Errorf("%q: got %v, want %v", in, got, want)
		}
	}
}

func TestStyleNonce(t *testing.T) {
	fsys := testFS()
	fsys["index.html"] = &fstest.MapFile{Data: []byte("<!doctype html><head>" + nonceMeta + "<title>Hostashi</title>")}
	h := newHandler(fsys)

	seen := map[string]bool{}

	for range 3 {
		rec := get(t, h, http.MethodGet, "/sites/blog/files", map[string]string{"Accept-Encoding": "gzip"})

		m := regexp.MustCompile(`<meta name="hostashi-style-nonce" content="([A-Za-z0-9_-]{24})" />`).FindStringSubmatch(rec.Body.String())
		if m == nil {
			t.Fatalf("no nonce in %q", rec.Body.String())
		}

		got := rec.Header().Get("Content-Security-Policy")
		if !strings.Contains(got, "style-src 'self' 'nonce-"+m[1]+"';") || !strings.Contains(got, "script-src 'self';") || strings.Contains(got, "unsafe") {
			t.Fatalf("CSP %q for nonce %s", got, m[1])
		}

		if rec.Header().Get("Content-Encoding") != "" || rec.Header().Get("Cache-Control") != "no-store" {
			t.Fatalf("headers %v", rec.Header())
		}

		if seen[m[1]] {
			t.Fatal("nonce repeated")
		}

		seen[m[1]] = true
	}

	// Assets keep the policy without a nonce.
	if got := get(t, h, http.MethodGet, "/assets/app-abc.js", nil).Header().Get("Content-Security-Policy"); got != csp {
		t.Fatalf("asset CSP %q", got)
	}
}
