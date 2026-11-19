// SPDX-License-Identifier: AGPL-3.0-or-later

package serve

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"mime"
	"mime/multipart"
	"net/http"
	"time"

	"github.com/maki-it/hostashi/internal/auth"
	"github.com/maki-it/hostashi/internal/hosting"
	"github.com/maki-it/hostashi/internal/statuspage"
	"github.com/maki-it/hostashi/internal/store"
)

type pageFileJSON struct {
	Path string `json:"path"`
	Size int    `json:"size"`
}

type pageJSON struct {
	ID        string         `json:"id"`
	Name      string         `json:"name"`
	Kind      string         `json:"kind"`
	Builtin   bool           `json:"builtin"`
	SHA256    string         `json:"sha256"`
	Files     []pageFileJSON `json:"files"`
	Sites     []string       `json:"sites"`  // sites that name the page
	Parked    []string       `json:"parked"` // parked domains that show it
	CreatedAt *time.Time     `json:"createdAt"`
	UpdatedAt *time.Time     `json:"updatedAt"`
}

func toPageJSON(p hosting.Page) pageJSON {
	files := make([]pageFileJSON, 0, len(p.Files))
	for _, f := range p.Files {
		files = append(files, pageFileJSON{Path: f.Path, Size: f.Size})
	}

	sites := p.Sites
	if sites == nil {
		sites = []string{}
	}

	parked := p.Parked
	if parked == nil {
		parked = []string{}
	}

	return pageJSON{
		Parked: parked,
		ID:     p.ID, Name: p.Name, Kind: p.Kind, Builtin: p.Builtin, SHA256: p.Digest, Files: files, Sites: sites,
		CreatedAt: timeOrNil(p.CreatedAt), UpdatedAt: timeOrNil(p.UpdatedAt),
	}
}

type maintenanceJSON struct {
	Enabled  bool     `json:"enabled"`
	Page     string   `json:"page"`
	AllowIPs []string `json:"allowIps"`
}

func toMaintenanceJSON(s store.Site) maintenanceJSON {
	allow := s.MaintenanceAllow
	if allow == nil {
		allow = []string{}
	}

	return maintenanceJSON{Enabled: s.Maintenance, Page: hosting.MaintenancePageID(s), AllowIPs: allow}
}

func pageFail(log *slog.Logger, w http.ResponseWriter, err error, what string) {
	switch {
	case errors.Is(err, hosting.ErrPageNotFound):
		writeError(w, http.StatusNotFound, "page_not_found", "no such page")
	case errors.Is(err, hosting.ErrPageBuiltin):
		writeError(w, http.StatusConflict, "page_builtin", "built-in pages cannot be changed or deleted")
	case errors.Is(err, hosting.ErrPageInUse):
		writeError(w, http.StatusConflict, "page_in_use", "a site or parked domain uses this page, choose another page there first")
	case errors.Is(err, hosting.ErrParkedNotFound):
		writeError(w, http.StatusNotFound, "parked_not_found", "the domain is not parked")
	default:
		siteFail(log, w, err, what)
	}
}

// maxPageForm is the largest multipart body: the file plus the small fields.
const maxPageForm = statuspage.MaxUploadBytes + 64<<10

// pageForm reads a multipart/form-data body with the fields name, kind and
// file. The file stays in memory; nothing is written to disk.
func pageForm(w http.ResponseWriter, r *http.Request) (name, kind string, upload *hosting.PageUpload, ok bool) {
	mt, _, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if err != nil || mt != "multipart/form-data" {
		writeError(w, http.StatusUnsupportedMediaType, "unsupported_media_type", "send multipart/form-data with the fields name, kind and file")

		return "", "", nil, false
	}

	r.Body = http.MaxBytesReader(w, r.Body, maxPageForm)

	mr, err := r.MultipartReader()
	if err != nil {
		writeError(w, http.StatusBadRequest, "bad_request", "invalid multipart body")

		return "", "", nil, false
	}

	for {
		part, err := mr.NextPart()
		if errors.Is(err, io.EOF) {
			return name, kind, upload, true
		}

		if err != nil {
			writeError(w, http.StatusRequestEntityTooLarge, "too_large", "the upload is larger than 2 MiB or not valid multipart")

			return "", "", nil, false
		}

		data, err := readPart(part)
		if err != nil {
			writeError(w, http.StatusRequestEntityTooLarge, "too_large", "the upload is larger than 2 MiB")

			return "", "", nil, false
		}

		switch part.FormName() {
		case "name":
			name = string(data)
		case "kind":
			kind = string(data)
		case "file":
			upload = &hosting.PageUpload{Filename: part.FileName(), Data: data}
		}
	}
}

func readPart(p *multipart.Part) ([]byte, error) {
	defer p.Close()

	b, err := io.ReadAll(io.LimitReader(p, statuspage.MaxUploadBytes+1))
	if err != nil {
		return nil, err
	}

	if len(b) > statuspage.MaxUploadBytes {
		return nil, errors.New("part too large")
	}

	return b, nil
}

// registerPages adds the routes of the maintenance and parking pages and
// of a site's maintenance (ADR-0027).
func registerPages(mux *http.ServeMux, a authn, svc *hosting.Service, log *slog.Logger) {
	admin := func(scope string, h func(http.ResponseWriter, *http.Request, auth.Principal)) http.HandlerFunc {
		return a.scoped(scope, func(w http.ResponseWriter, r *http.Request, p auth.Principal) {
			if p.User.Role != "admin" {
				writeError(w, http.StatusForbidden, "forbidden", "only administrators can manage pages")

				return
			}

			h(w, r, p)
		})
	}

	mux.HandleFunc("GET /api/v1/status-pages", admin("pages:read", func(w http.ResponseWriter, r *http.Request, _ auth.Principal) {
		pages, err := svc.Pages(r.Context())
		if err != nil {
			pageFail(log, w, err, "list pages")

			return
		}

		out := make([]pageJSON, 0, len(pages))
		for _, p := range pages {
			out = append(out, toPageJSON(p))
		}

		writeJSON(w, http.StatusOK, map[string][]pageJSON{"pages": out})
	}))

	mux.HandleFunc("GET /api/v1/status-pages/{id}", admin("pages:read", func(w http.ResponseWriter, r *http.Request, _ auth.Principal) {
		p, err := svc.Page(r.Context(), r.PathValue("id"))
		if err != nil {
			pageFail(log, w, err, "get page")

			return
		}

		writeJSON(w, http.StatusOK, toPageJSON(p))
	}))

	mux.HandleFunc("POST /api/v1/status-pages", admin("pages:write", func(w http.ResponseWriter, r *http.Request, p auth.Principal) {
		name, kind, upload, ok := pageForm(w, r)
		if !ok {
			return
		}

		if upload == nil {
			writeError(w, http.StatusUnprocessableEntity, "invalid_argument", `"file" is required: an .html or .zip file`)

			return
		}

		page, err := svc.CreatePage(r.Context(), p.User.ID, name, kind, *upload)
		if err != nil {
			pageFail(log, w, err, "create page")

			return
		}

		writeJSON(w, http.StatusCreated, toPageJSON(page))
	}))

	// Renames a page (field name) and/or replaces its files (field file).
	mux.HandleFunc("PUT /api/v1/status-pages/{id}", admin("pages:write", func(w http.ResponseWriter, r *http.Request, p auth.Principal) {
		name, kind, upload, ok := pageForm(w, r)
		if !ok {
			return
		}

		if kind != "" {
			writeError(w, http.StatusUnprocessableEntity, "invalid_argument", "the kind of a page cannot be changed")

			return
		}

		if name == "" && upload == nil {
			writeError(w, http.StatusUnprocessableEntity, "invalid_argument", `send "name", "file" or both`)

			return
		}

		ctx, cancel := context.WithTimeout(r.Context(), agentTimeout)
		defer cancel()

		page, err := svc.UpdatePage(ctx, p.User.ID, r.PathValue("id"), name, upload)
		if err != nil {
			pageFail(log, w, err, "update page")

			return
		}

		writeJSON(w, http.StatusOK, toPageJSON(page))
	}))

	mux.HandleFunc("DELETE /api/v1/status-pages/{id}", admin("pages:write", func(w http.ResponseWriter, r *http.Request, p auth.Principal) {
		ctx, cancel := context.WithTimeout(r.Context(), agentTimeout)
		defer cancel()

		if err := svc.DeletePage(ctx, p.User.ID, r.PathValue("id")); err != nil {
			pageFail(log, w, err, "delete page")

			return
		}

		w.WriteHeader(http.StatusNoContent)
	}))

	mux.HandleFunc("PUT /api/v1/sites/{name}/maintenance", admin("sites.maintenance:write", func(w http.ResponseWriter, r *http.Request, p auth.Principal) {
		var in struct {
			Enabled  *bool    `json:"enabled"`
			Page     string   `json:"page"`
			AllowIPs []string `json:"allowIps"`
		}
		if !decodeJSON(w, r, &in) {
			return
		}

		if in.Enabled == nil {
			writeError(w, http.StatusUnprocessableEntity, "invalid_argument", `"enabled" is required`)

			return
		}

		ctx, cancel := context.WithTimeout(r.Context(), agentTimeout)
		defer cancel()

		s, err := svc.SetMaintenance(ctx, p.User.ID, r.PathValue("name"), hosting.Maintenance{Enabled: *in.Enabled, Page: in.Page, AllowIPs: in.AllowIPs})
		if err != nil {
			pageFail(log, w, err, "set maintenance")

			return
		}

		writeJSON(w, http.StatusOK, toSiteJSON(svc, s))
	}))
}

type parkedJSON struct {
	Domain    string    `json:"domain"`
	Page      string    `json:"page"`
	TLS       parkTLS   `json:"tls"`
	CreatedAt time.Time `json:"createdAt"`
	UpdatedAt time.Time `json:"updatedAt"`
}

type parkTLS struct {
	Enabled  bool       `json:"enabled"`
	Active   bool       `json:"active"`
	NotAfter *time.Time `json:"notAfter"`
	Error    string     `json:"error,omitempty"`
	FailedAt *time.Time `json:"failedAt,omitempty"`
}

func toParkedJSON(svc *hosting.Service, p store.Parked) parkedJSON {
	return parkedJSON{
		Domain: p.Domain, Page: hosting.ParkedPageID(p), CreatedAt: p.CreatedAt, UpdatedAt: p.UpdatedAt,
		TLS: parkTLS{
			Enabled: p.TLS, Active: svc.ParkedTLSActive(p), NotAfter: timeOrNil(p.TLSNotAfter),
			Error: p.TLSError, FailedAt: timeOrNil(p.TLSFailedAt),
		},
	}
}

// registerParked adds the routes of parked domains (ADR-0027).
func registerParked(mux *http.ServeMux, a authn, svc *hosting.Service, log *slog.Logger) {
	admin := func(scope string, h func(http.ResponseWriter, *http.Request, auth.Principal)) http.HandlerFunc {
		return a.scoped(scope, func(w http.ResponseWriter, r *http.Request, p auth.Principal) {
			if p.User.Role != "admin" {
				writeError(w, http.StatusForbidden, "forbidden", "only administrators can park domains")

				return
			}

			h(w, r, p)
		})
	}

	mux.HandleFunc("GET /api/v1/parked-domains", admin("sites:read", func(w http.ResponseWriter, r *http.Request, _ auth.Principal) {
		list, err := svc.ParkedDomains(r.Context())
		if err != nil {
			pageFail(log, w, err, "list parked domains")

			return
		}

		out := make([]parkedJSON, 0, len(list))
		for _, p := range list {
			out = append(out, toParkedJSON(svc, p))
		}

		writeJSON(w, http.StatusOK, map[string][]parkedJSON{"parkedDomains": out})
	}))

	mux.HandleFunc("GET /api/v1/parked-domains/{domain}", admin("sites:read", func(w http.ResponseWriter, r *http.Request, _ auth.Principal) {
		p, err := svc.ParkedDomain(r.Context(), r.PathValue("domain"))
		if err != nil {
			pageFail(log, w, err, "get parked domain")

			return
		}

		writeJSON(w, http.StatusOK, toParkedJSON(svc, p))
	}))

	// Parks a domain or changes its page and HTTPS ("tls" defaults to on).
	mux.HandleFunc("PUT /api/v1/parked-domains/{domain}", admin("sites.maintenance:write", func(w http.ResponseWriter, r *http.Request, p auth.Principal) {
		var in struct {
			Page string `json:"page"`
			TLS  *bool  `json:"tls"`
		}
		if !decodeJSON(w, r, &in) {
			return
		}

		tls := in.TLS == nil || *in.TLS

		ctx, cancel := context.WithTimeout(r.Context(), agentTimeout)
		defer cancel()

		parked, err := svc.SetParked(ctx, p.User.ID, r.PathValue("domain"), in.Page, tls)
		if err != nil {
			pageFail(log, w, err, "park domain")

			return
		}

		writeJSON(w, http.StatusOK, toParkedJSON(svc, parked))
	}))

	mux.HandleFunc("DELETE /api/v1/parked-domains/{domain}", admin("sites.maintenance:write", func(w http.ResponseWriter, r *http.Request, p auth.Principal) {
		ctx, cancel := context.WithTimeout(r.Context(), agentTimeout)
		defer cancel()

		if err := svc.DeleteParked(ctx, p.User.ID, r.PathValue("domain")); err != nil {
			pageFail(log, w, err, "unpark domain")

			return
		}

		w.WriteHeader(http.StatusNoContent)
	}))
}
