// SPDX-License-Identifier: AGPL-3.0-or-later

package serve

import (
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/maki-it/hostashi/internal/statuspage"
)

const previewDigest = "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"

func previewServer(t *testing.T) (*previews, *http.ServeMux, *time.Time) {
	t.Helper()

	dir := t.TempDir()
	page := filepath.Join(dir, "p7."+previewDigest)

	if err := os.MkdirAll(filepath.Join(page, "img"), 0o755); err != nil {
		t.Fatal(err)
	}

	if err := os.WriteFile(filepath.Join(page, "index.html"), []byte("<h1>Back soon</h1>"), 0o644); err != nil {
		t.Fatal(err)
	}

	if err := os.WriteFile(filepath.Join(page, "img", "logo.png"), []byte("PNG"), 0o644); err != nil {
		t.Fatal(err)
	}

	now := time.Date(2026, 11, 24, 10, 0, 0, 0, time.UTC)
	pv := newPreviews(dir)
	pv.now = func() time.Time { return now }

	mux := http.NewServeMux()
	mux.HandleFunc("GET "+previewPrefix+"{token}/{path...}", func(w http.ResponseWriter, r *http.Request) {
		servePreview(r.Context(), w, r, pv, slog.New(slog.DiscardHandler))
	})

	return pv, mux, &now
}

func get(mux http.Handler, path string) *httptest.ResponseRecorder {
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, path, nil))

	return rec
}

func TestPreviewServesThePageSandboxed(t *testing.T) {
	pv, mux, _ := previewServer(t)

	tok, err := pv.issue(preview{id: "p7", digest: previewDigest})
	if err != nil {
		t.Fatal(err)
	}

	rec := get(mux, previewPrefix+tok+"/")
	if rec.Code != http.StatusOK || rec.Body.String() != "<h1>Back soon</h1>" {
		t.Fatalf("index: %d %q", rec.Code, rec.Body)
	}

	csp := rec.Header().Get("Content-Security-Policy")
	if !strings.HasPrefix(csp, "sandbox allow-scripts;") || strings.Contains(csp, "allow-same-origin") || !strings.Contains(csp, "connect-src 'none'") {
		t.Fatalf("CSP %q", csp)
	}

	if ct := rec.Header().Get("Content-Type"); ct != "text/html; charset=utf-8" {
		t.Fatalf("type %q", ct)
	}

	if rec := get(mux, previewPrefix+tok+"/img/logo.png"); rec.Code != http.StatusOK || rec.Header().Get("Content-Type") != "image/png" {
		t.Fatalf("image: %d %q", rec.Code, rec.Header().Get("Content-Type"))
	}

	for _, p := range []string{"img/missing.png", "img/", "index.php", "img/logo.exe"} {
		if rec := get(mux, previewPrefix+tok+"/"+p); rec.Code != http.StatusNotFound {
			t.Errorf("%s: %d", p, rec.Code)
		}
	}

	// The mux cleans ".." away with a redirect out of the token's path.
	if rec := get(mux, previewPrefix+tok+"/../secret.html"); rec.Code == http.StatusOK {
		t.Errorf("..: %d %q", rec.Code, rec.Body)
	}
}

// TestPreviewDomainPlaceholder: the preview shows an example domain for
// %%DOMAIN%% in HTML, like nginx shows the visited one, and leaves other
// files as they are.
func TestPreviewDomainPlaceholder(t *testing.T) {
	pv, mux, _ := previewServer(t)
	page := filepath.Join(pv.dir, "p7."+previewDigest)

	if err := os.WriteFile(filepath.Join(page, "index.html"), []byte("<p>%%DOMAIN%%</p><p>%%DOMAIN%%</p>"), 0o644); err != nil {
		t.Fatal(err)
	}

	if err := os.WriteFile(filepath.Join(page, "a.js"), []byte("'%%DOMAIN%%'"), 0o644); err != nil {
		t.Fatal(err)
	}

	tok, err := pv.issue(preview{id: "p7", digest: previewDigest})
	if err != nil {
		t.Fatal(err)
	}

	if rec := get(mux, previewPrefix+tok+"/"); rec.Code != http.StatusOK || rec.Body.String() != "<p>example.com</p><p>example.com</p>" {
		t.Errorf("index: %d %q", rec.Code, rec.Body)
	}

	if rec := get(mux, previewPrefix+tok+"/a.js"); rec.Code != http.StatusOK || rec.Body.String() != "'%%DOMAIN%%'" {
		t.Errorf("script: %d %q", rec.Code, rec.Body)
	}
}

func TestPreviewBuiltinPage(t *testing.T) {
	pv, mux, _ := previewServer(t)

	tok, err := pv.issue(preview{id: statuspage.BuiltinMaintenance, builtin: true})
	if err != nil {
		t.Fatal(err)
	}

	rec := get(mux, previewPrefix+tok+"/")
	if rec.Code != http.StatusOK || !strings.Contains(strings.ToLower(rec.Body.String()), "<html") {
		t.Fatalf("builtin: %d %.80q", rec.Code, rec.Body)
	}
}

func TestPreviewTokens(t *testing.T) {
	pv, mux, now := previewServer(t)

	tok, err := pv.issue(preview{id: "p7", digest: previewDigest})
	if err != nil {
		t.Fatal(err)
	}

	if len(tok) < 40 {
		t.Fatalf("short token %q", tok)
	}

	if rec := get(mux, previewPrefix+"not-a-token/"); rec.Code != http.StatusNotFound {
		t.Fatalf("unknown token: %d", rec.Code)
	}

	*now = now.Add(previewTTL)

	if rec := get(mux, previewPrefix+tok+"/"); rec.Code != http.StatusNotFound {
		t.Fatalf("expired token: %d", rec.Code)
	}

	// The store stays bounded.
	for range previewMax + 10 {
		if _, err := pv.issue(preview{id: "p7", digest: previewDigest}); err != nil {
			t.Fatal(err)
		}
	}

	if len(pv.m) > previewMax {
		t.Fatalf("%d tokens kept", len(pv.m))
	}

	pv.rand = func([]byte) (int, error) { return 0, errors.New("no entropy") }
	if _, err := pv.issue(preview{id: "p7", digest: previewDigest}); err == nil {
		t.Fatal("token without randomness")
	}
}
