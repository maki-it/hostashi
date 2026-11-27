// SPDX-License-Identifier: AGPL-3.0-or-later

package serve

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"log/slog"
	"net/http"
	"path"
	"strings"
	"sync"
	"time"

	"github.com/maki-it/hostashi/internal/agentapi"
	"github.com/maki-it/hostashi/internal/auth"
	"github.com/maki-it/hostashi/internal/hosting"
	"github.com/maki-it/hostashi/internal/openapi"
	"github.com/maki-it/hostashi/internal/pages"
	"github.com/maki-it/hostashi/internal/statuspage"
)

// Page previews (ADR-0027 addendum). An admin or manager asks for a
// preview link; the link carries a random token that is valid for
// previewTTL and opens exactly one version of one page. The page is served
// with a sandbox CSP, so it runs in an opaque origin: it cannot read the
// panel's cookies, call the API or script the panel's windows. Session
// cookies are SameSite=Strict, so the sandboxed document cannot use them
// either; that is why the token, not the session, authorizes the files.
const (
	previewTTL     = 15 * time.Minute
	previewMax     = 256 // live tokens; the oldest goes first
	previewPrefix  = "/api/page-preview/"
	previewCSP     = "sandbox allow-scripts; default-src 'none'; script-src 'self' 'unsafe-inline' https:; style-src 'self' 'unsafe-inline' https:; img-src 'self' data: https:; font-src 'self' data: https:; media-src 'self' data: https:; connect-src 'none'; form-action 'none'; base-uri 'none'; frame-ancestors 'none'"
	previewTokenSz = 32
)

type preview struct {
	id, digest string
	builtin    bool
	expires    time.Time
}

// previews holds the live preview tokens in memory; a restart ends them.
type previews struct {
	mu   sync.Mutex
	m    map[string]preview
	now  func() time.Time
	dir  string // pages.DefaultDir outside tests
	rand func([]byte) (int, error)
}

func newPreviews(dir string) *previews {
	if dir == "" {
		dir = pages.DefaultDir
	}

	return &previews{m: map[string]preview{}, now: time.Now, dir: dir, rand: rand.Read}
}

func (p *previews) issue(pv preview) (string, error) {
	b := make([]byte, previewTokenSz)
	if _, err := p.rand(b); err != nil {
		return "", err
	}

	tok := base64.RawURLEncoding.EncodeToString(b)

	p.mu.Lock()
	defer p.mu.Unlock()

	now := p.now()
	for k, v := range p.m {
		if !now.Before(v.expires) {
			delete(p.m, k)
		}
	}

	for len(p.m) >= previewMax {
		var oldest string
		for k, v := range p.m {
			if oldest == "" || v.expires.Before(p.m[oldest].expires) {
				oldest = k
			}
		}

		delete(p.m, oldest)
	}

	pv.expires = now.Add(previewTTL)
	p.m[tok] = pv

	return tok, nil
}

func (p *previews) lookup(tok string) (preview, bool) {
	p.mu.Lock()
	defer p.mu.Unlock()

	pv, ok := p.m[tok]
	if !ok || !p.now().Before(pv.expires) {
		return preview{}, false
	}

	return pv, true
}

// file returns the content of rel in the previewed page.
func (p *previews) file(pv preview, rel string) ([]byte, error) {
	if pv.builtin {
		files, _, ok := statuspage.Builtin(pv.id)
		if !ok {
			return nil, pages.ErrNoFile
		}

		b, ok := files[rel]
		if !ok {
			return nil, pages.ErrNoFile
		}

		return b, nil
	}

	return pages.ReadFile(p.dir, pv.id, pv.digest, rel)
}

// previewLinkJSON is the answer of POST .../preview.
type previewLinkJSON struct {
	// URL is a path on the panel; open it in a new tab. It ends with "/".
	URL     string    `json:"url"`
	Expires time.Time `json:"expires"`
}

func registerPagePreview(mux *routeMux, a authn, svc *hosting.Service, pv *previews, log *slog.Logger) {
	mux.Op("POST /api/v1/status-pages/{id}/preview", openapi.Operation{
		ID: "previewStatusPage", Tag: "pages", Summary: "Get a short-lived link that shows a maintenance or parking page", Out: previewLinkJSON{},
	}, a.allow("pages:read", func(w http.ResponseWriter, r *http.Request, _ auth.Principal) {
		pg, err := svc.Page(r.Context(), r.PathValue("id"))
		if err != nil {
			pageFail(log, w, err, "preview page")

			return
		}

		tok, err := pv.issue(preview{id: pg.ID, digest: pg.Digest, builtin: pg.Builtin})
		if err != nil {
			log.ErrorContext(r.Context(), "preview token", "err", err)
			writeError(w, http.StatusInternalServerError, "internal", "internal error")

			return
		}

		got, _ := pv.lookup(tok)
		writeJSON(w, http.StatusOK, previewLinkJSON{URL: previewPrefix + tok + "/", Expires: got.expires.UTC()})
	}))

	// Public: the token in the path is the credential, valid for minutes
	// and for one page version only. It shows nothing that visitors of a
	// site with that page would not see. It is for browsers only, so it
	// sits outside /api/v1 and the OpenAPI document.
	mux.HandleFunc("GET "+previewPrefix+"{token}/{path...}", func(w http.ResponseWriter, r *http.Request) {
		servePreview(r.Context(), w, r, pv, log)
	})
}

func servePreview(ctx context.Context, w http.ResponseWriter, r *http.Request, pv *previews, log *slog.Logger) {
	p, ok := pv.lookup(r.PathValue("token"))
	if !ok {
		writeError(w, http.StatusNotFound, "not_found", "this preview link has expired; open the preview again")

		return
	}

	rel := r.PathValue("path")
	if rel == "" || strings.HasSuffix(rel, "/") {
		rel += agentapi.PageIndex
	}

	ext := strings.ToLower(strings.TrimPrefix(path.Ext(rel), "."))
	ctype, ok := agentapi.PageTypes[ext]

	var b []byte

	var err error
	if ok {
		b, err = pv.file(p, rel)
	}

	if !ok || errors.Is(err, pages.ErrNoFile) {
		writeError(w, http.StatusNotFound, "not_found", "the page has no such file")

		return
	}

	if err != nil {
		log.ErrorContext(ctx, "preview file", "err", err)
		writeError(w, http.StatusInternalServerError, "internal", "internal error")

		return
	}

	h := w.Header()
	h.Set("Content-Security-Policy", previewCSP)
	h.Set("Content-Type", ctype)
	h.Set("Cache-Control", "no-store")
	h.Set("Referrer-Policy", "no-referrer")
	h.Set("Cross-Origin-Resource-Policy", "same-origin")
	w.WriteHeader(http.StatusOK)

	if ext == "html" {
		b = bytes.ReplaceAll(b, []byte(agentapi.PageDomainPlaceholder), []byte(agentapi.PagePreviewDomain))
	}

	if r.Method != http.MethodHead {
		_, _ = w.Write(b)
	}
}
