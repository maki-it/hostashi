// SPDX-License-Identifier: AGPL-3.0-or-later

// Package ui serves the browser frontend (web/, React + Vite) from files
// embedded in the binary. The build writes them to dist/; a checkout without
// a frontend build still compiles and serves a short notice instead.
package ui

import (
	"bytes"
	"compress/gzip"
	"crypto/rand"
	"embed"
	"encoding/base64"
	"io/fs"
	"mime"
	"net/http"
	"path"
	"strings"
	"sync"
	"time"
)

//go:embed all:dist
var embedded embed.FS

// CSP for the frontend. Scripts and styles only from the panel itself, no
// inline code, no eval. The API keeps its own stricter policy.
const csp = "default-src 'self'; script-src 'self'; style-src 'self'; img-src 'self' data:; " +
	"connect-src 'self'; font-src 'self'; object-src 'none'; base-uri 'none'; form-action 'self'; frame-ancestors 'none'"

// The code editor of the file manager (CodeMirror, ADR-0040) injects its
// styles as <style> elements. index.html gets a fresh nonce per response
// in the policy and in a meta tag, which the editor puts on those
// elements; scripts stay 'self' only.
const (
	nonceMeta  = `<meta name="hostashi-style-nonce" content="" />`
	nonceBytes = 18
)

// indexCSP is the policy of index.html with the style nonce.
func indexCSP(nonce string) string {
	return strings.Replace(csp, "style-src 'self'", "style-src 'self' 'nonce-"+nonce+"'", 1)
}

// newNonce returns a random nonce in base64 (URL alphabet, no padding: no
// quote or "/" can end up in the policy or the meta tag).
func newNonce() string {
	b := make([]byte, nonceBytes)
	_, _ = rand.Read(b) // crypto/rand.Read never fails (Go 1.24+)

	return base64.RawURLEncoding.EncodeToString(b)
}

const notBuilt = `<!doctype html>
<meta charset="utf-8">
<title>Hostashi</title>
<p>The web interface is not part of this build. Run <code>npm ci &amp;&amp; npm run build</code> in <code>web/</code> and rebuild the binary.</p>
`

type file struct {
	data []byte
	gz   []byte // nil when compression does not pay off
	ctyp string
}

// Handler serves the frontend. Files under assets/ have content hashes in
// their names and are cached for a year. Every other path that does not look
// like a file gets index.html, so client-side routes work on reload. The
// caller keeps /api away from it.
func Handler() http.Handler {
	sub, err := fs.Sub(embedded, "dist")
	if err != nil {
		panic(err) // the embed pattern guarantees dist exists
	}

	return newHandler(sub)
}

func newHandler(root fs.FS) http.Handler {
	var (
		once  sync.Once
		files map[string]file
	)

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		once.Do(func() { files = load(root) })

		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			w.Header().Set("Allow", "GET, HEAD")
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)

			return
		}

		h := w.Header()
		h.Set("Content-Security-Policy", csp)
		h.Set("Cross-Origin-Opener-Policy", "same-origin")

		name := strings.TrimPrefix(path.Clean("/"+r.URL.Path), "/")
		if f, ok := files[name]; ok && name != "index.html" {
			if strings.HasPrefix(name, "assets/") {
				h.Set("Cache-Control", "public, max-age=31536000, immutable")
			}

			serve(w, r, name, f)

			return
		}

		// A missing file with an extension is a real 404, not a page.
		if path.Ext(name) != "" && name != "index.html" {
			http.NotFound(w, r)

			return
		}

		index, ok := files["index.html"]
		if !ok {
			h.Set("Content-Type", "text/html; charset=utf-8")
			w.WriteHeader(http.StatusServiceUnavailable)
			_, _ = w.Write([]byte(notBuilt))

			return
		}

		h.Set("Cache-Control", "no-store")

		// With a nonce meta tag, every response gets its own nonce; the
		// page is small, so it is sent uncompressed.
		if bytes.Contains(index.data, []byte(nonceMeta)) {
			nonce := newNonce()
			h.Set("Content-Security-Policy", indexCSP(nonce))

			page := bytes.Replace(index.data, []byte(nonceMeta), []byte(`<meta name="hostashi-style-nonce" content="`+nonce+`" />`), 1)
			serve(w, r, "index.html", file{data: page, ctyp: index.ctyp})

			return
		}

		serve(w, r, "index.html", index)
	})
}

func serve(w http.ResponseWriter, r *http.Request, name string, f file) {
	h := w.Header()
	h.Set("Content-Type", f.ctyp)

	data := f.data
	if f.gz != nil {
		h.Add("Vary", "Accept-Encoding")

		if acceptsGzip(r.Header.Values("Accept-Encoding")) {
			h.Set("Content-Encoding", "gzip")

			data = f.gz
		}
	}

	http.ServeContent(w, r, name, time.Time{}, bytes.NewReader(data))
}

// load reads all files once and compresses the text ones. The frontend is a
// few hundred KB, so keeping it in memory is cheaper than reading the
// embedded FS on every request.
func load(root fs.FS) map[string]file {
	files := map[string]file{}

	_ = fs.WalkDir(root, ".", func(name string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() || strings.HasPrefix(path.Base(name), ".") {
			return nil //nolint:nilerr // unreadable entries are skipped, the rest is served
		}

		data, err := fs.ReadFile(root, name)
		if err != nil {
			return nil //nolint:nilerr // see above
		}

		ctyp := mime.TypeByExtension(path.Ext(name))
		if ctyp == "" {
			ctyp = http.DetectContentType(data)
		}

		f := file{data: data, ctyp: ctyp}
		if compressible(ctyp) && len(data) >= 1024 {
			if gz := gzipBytes(data); len(gz) < len(data)*9/10 {
				f.gz = gz
			}
		}

		files[name] = f

		return nil
	})

	return files
}

func compressible(ctyp string) bool {
	mt, _, _ := mime.ParseMediaType(ctyp)

	switch {
	case strings.HasPrefix(mt, "text/"):
		return true
	case mt == "application/javascript", mt == "application/json", mt == "image/svg+xml":
		return true
	default:
		return false
	}
}

func gzipBytes(data []byte) []byte {
	var buf bytes.Buffer

	zw, err := gzip.NewWriterLevel(&buf, gzip.BestCompression)
	if err != nil {
		return nil
	}

	if _, err := zw.Write(data); err != nil {
		return nil
	}

	if err := zw.Close(); err != nil {
		return nil
	}

	return buf.Bytes()
}

// acceptsGzip reports whether an Accept-Encoding header allows gzip.
// "gzip;q=0" is a refusal.
func acceptsGzip(values []string) bool {
	for _, v := range values {
		for part := range strings.SplitSeq(v, ",") {
			coding, params, _ := strings.Cut(strings.TrimSpace(part), ";")
			if !strings.EqualFold(strings.TrimSpace(coding), "gzip") {
				continue
			}

			q := strings.ReplaceAll(strings.ToLower(params), " ", "")

			return q != "q=0" && q != "q=0.0" && q != "q=0.00" && q != "q=0.000"
		}
	}

	return false
}
