// SPDX-License-Identifier: AGPL-3.0-or-later

package agent

import (
	"context"
	"errors"
	"io/fs"
	"os"
	"strings"

	"github.com/maki-it/hostashi/internal/agentapi"
	"github.com/maki-it/hostashi/internal/agentrpc"
	"github.com/maki-it/hostashi/internal/pages"
	"github.com/maki-it/hostashi/internal/web"
)

// PagesDir holds the maintenance and parking pages (ADR-0027). It is not
// below DefaultStateDir: that one is 0700, and the nginx workers read the
// pages.
const PagesDir = "/var/lib/hostashi-pages"

var errNoPages = &agentrpc.Error{Code: agentrpc.CodePrecondition, Message: "pages are not available on this server"}

// registerPages serves page.*. The operations hold h.mu like web.apply, so
// a page cannot be deleted while a configuration that uses it goes live.
func (h *siteOps) registerPages(s *agentrpc.Server) {
	agentrpc.Handle(s, agentapi.PageBegin, func(_ context.Context, _ agentrpc.Caller, req agentapi.PageBeginRequest) (agentapi.PageResponse, error) {
		if h.pages == nil {
			return agentapi.PageResponse{}, errNoPages
		}

		return agentapi.PageResponse{}, mapPageError(h.pages.Begin(req.ID))
	})

	agentrpc.Handle(s, agentapi.PageWrite, func(_ context.Context, _ agentrpc.Caller, req agentapi.PageWriteRequest) (agentapi.PageResponse, error) {
		if h.pages == nil {
			return agentapi.PageResponse{}, errNoPages
		}

		return agentapi.PageResponse{}, mapPageError(h.pages.Write(req.ID, req.Path, req.Offset, req.Data))
	})

	agentrpc.Handle(s, agentapi.PageCommit, func(ctx context.Context, _ agentrpc.Caller, req agentapi.PageCommitRequest) (agentapi.PageResponse, error) {
		if h.pages == nil {
			return agentapi.PageResponse{}, errNoPages
		}

		h.mu.Lock()
		defer h.mu.Unlock()

		if err := h.pages.Commit(req.ID, req.Digest); err != nil {
			return agentapi.PageResponse{}, mapPageError(err)
		}

		h.prunePages(ctx)

		return agentapi.PageResponse{}, nil
	})

	agentrpc.Handle(s, agentapi.PageDelete, func(_ context.Context, _ agentrpc.Caller, req agentapi.PageDeleteRequest) (agentapi.PageResponse, error) {
		if h.pages == nil {
			return agentapi.PageResponse{}, errNoPages
		}

		h.mu.Lock()
		defer h.mu.Unlock()

		inUse, err := h.pagesInUse()
		if err != nil {
			return agentapi.PageResponse{}, err
		}

		for v := range inUse {
			if pageID(v) == req.ID {
				return agentapi.PageResponse{}, &agentrpc.Error{
					Code: agentrpc.CodePrecondition, Message: "page is in use, remove it from the sites with web.apply first",
				}
			}
		}

		return agentapi.PageResponse{}, mapPageError(h.pages.Delete(req.ID))
	})

	agentrpc.Handle(s, agentapi.PageList, func(_ context.Context, _ agentrpc.Caller, _ agentapi.PageListRequest) (agentapi.PageListResponse, error) {
		if h.pages == nil {
			return agentapi.PageListResponse{Pages: []agentapi.PageVersion{}}, nil
		}

		list, err := h.pages.List()
		if err != nil {
			return agentapi.PageListResponse{}, err
		}

		return agentapi.PageListResponse{Pages: list}, nil
	})
}

// pageID is the id part of "<id>.<digest>".
func pageID(v string) string {
	id, _, _ := strings.Cut(v, ".")

	return id
}

// pagesInUse returns the page versions ("<id>.<digest>") of the live web
// server configuration.
func (h *siteOps) pagesInUse() (map[string]bool, error) {
	if h.web == nil {
		return map[string]bool{}, nil
	}

	cur, _, err := h.web.Current()
	if err != nil {
		return nil, err
	}

	return usedPages(cur), nil
}

func usedPages(d web.Desired) map[string]bool {
	out := map[string]bool{}

	for _, p := range d.Parked {
		out[p.Page+"."+p.Digest] = true
	}

	for _, s := range d.Sites {
		if m := s.Maintenance; m != nil {
			out[m.Page+"."+m.Digest] = true
		}

		if p := s.DisabledPage; p != nil {
			out[p.Page+"."+p.Digest] = true
		}
	}

	return out
}

// prunePages drops old page versions that the live configuration does not
// use. A failure is only logged; the next commit or web.apply tries again.
func (h *siteOps) prunePages(ctx context.Context) {
	inUse, err := h.pagesInUse()
	if err == nil {
		err = h.pages.Prune(inUse)
	}

	if err != nil {
		h.log.ErrorContext(ctx, "prune old page versions", "err", err)
	}
}

// maintenanceFor maps the maintenance mode of a site to the web server
// state; the page version must have been committed before.
func (h *siteOps) maintenanceFor(m *agentapi.WebMaintenance) (*web.Maintenance, error) {
	if h.pages == nil {
		return nil, errNoPages
	}

	if !h.pages.Has(m.Page, m.Digest) {
		return nil, &agentrpc.Error{Code: agentrpc.CodeNotFound, Message: "page " + m.Page + " with this digest is not stored, send it with page.* first"}
	}

	return &web.Maintenance{Page: m.Page, Digest: m.Digest, Dir: h.pages.Path(m.Page, m.Digest), AllowIPs: m.AllowIPs}, nil
}

func mapPageError(err error) error {
	switch {
	case err == nil:
		return nil
	case errors.Is(err, pages.ErrNoUpload), errors.Is(err, pages.ErrNotFound), errors.Is(err, fs.ErrNotExist):
		return &agentrpc.Error{Code: agentrpc.CodePrecondition, Message: "no upload of this page in progress, start with page.begin"}
	case errors.Is(err, pages.ErrDigest):
		return &agentrpc.Error{Code: agentrpc.CodeInvalidArgument, Message: "the received files do not match the digest"}
	case errors.Is(err, pages.ErrTooLarge):
		return &agentrpc.Error{Code: agentrpc.CodeInvalidArgument, Message: "page too large or too many files"}
	case errors.Is(err, pages.ErrNoIndex):
		return &agentrpc.Error{Code: agentrpc.CodeInvalidArgument, Message: "the page has no " + agentapi.PageIndex}
	}

	return err
}

// parkedFor builds the web state of a parked domain; its page version must
// be stored, and with TLS its certificate must be valid for the domain.
func (h *siteOps) parkedFor(p agentapi.WebParked) (web.Parked, error) {
	m, err := h.maintenanceFor(&agentapi.WebMaintenance{Page: p.Page, Digest: p.Digest})
	if err != nil {
		return web.Parked{}, err
	}

	out := web.Parked{Name: p.Name, Domain: p.Domain, Page: m.Page, Digest: m.Digest, Dir: m.Dir}
	if !p.TLS {
		return out, nil
	}

	if h.certs == nil || len(h.web.Driver.ListenTLS) == 0 {
		return web.Parked{}, &agentrpc.Error{Code: agentrpc.CodePrecondition, Message: "HTTPS is not available on this server"}
	}

	ok, err := h.certs.Covers(p.Name, []string{p.Domain})
	if err != nil {
		return web.Parked{}, err
	}

	if !ok {
		return web.Parked{}, &agentrpc.Error{Code: agentrpc.CodePrecondition, Message: "parked domain " + p.Domain + " has no valid certificate"}
	}

	out.Cert, out.Key = h.certs.CertPath(p.Name), h.certs.KeyPath(p.Name)

	return out, nil
}

// pruneParkCerts removes the keys and certificates of parked domains that
// the live configuration no longer has. Failures are only logged.
func (h *siteOps) pruneParkCerts(ctx context.Context, d web.Desired) {
	if h.certs == nil {
		return
	}

	entries, err := os.ReadDir(h.certs.Dir)
	if err != nil {
		return
	}

	live := map[string]bool{}
	for _, p := range d.Parked {
		live[p.Name] = true
	}

	for _, e := range entries {
		if agentapi.ValidateParkName(e.Name()) != nil || live[e.Name()] {
			continue
		}

		if err := h.certs.Remove(e.Name()); err != nil {
			h.log.ErrorContext(ctx, "remove certificate of a parked domain", "name", e.Name(), "err", err)
		}
	}
}
