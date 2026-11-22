// SPDX-License-Identifier: AGPL-3.0-or-later

package hosting

import (
	"context"
	"fmt"

	"github.com/maki-it/hostashi/internal/agentapi"
	"github.com/maki-it/hostashi/internal/store"
)

// Headers returns the security headers of a site (ADR-0056).
func (s *Service) Headers(ctx context.Context, name string) (store.SiteHeaders, error) {
	site, err := s.Get(ctx, name)
	if err != nil {
		return store.SiteHeaders{}, err
	}

	return s.st.SiteHeaders(ctx, site.ID)
}

// checkHeaders validates headers against the site's snippets. A rejected
// value or a conflict with a snippet is an ErrInvalid that also matches
// agentapi.ErrHeaderRejected or agentapi.ErrHeaderConflict.
func checkHeaders(h agentapi.WebHeaders, sn store.SiteSnippets) error {
	if err := h.Validate(); err != nil {
		return fmt.Errorf("%w: %w", ErrInvalid, err)
	}

	if err := h.Conflict(sn.Server, sn.Root); err != nil {
		return fmt.Errorf("%w: %w", ErrInvalid, err)
	}

	return nil
}

// SetHeaders replaces the security headers of an active site, applies the
// web state and restores the old headers if that fails.
func (s *Service) SetHeaders(ctx context.Context, actor int64, name string, h agentapi.WebHeaders) (store.SiteHeaders, error) {
	if err := h.Validate(); err != nil {
		return store.SiteHeaders{}, fmt.Errorf("%w: %w", ErrInvalid, err)
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	site, err := s.Get(ctx, name)
	if err != nil {
		return store.SiteHeaders{}, err
	}

	if site.State != store.SiteActive {
		return store.SiteHeaders{}, ErrState
	}

	sn, err := s.st.SiteSnippets(ctx, site.ID)
	if err != nil {
		return store.SiteHeaders{}, err
	}

	if err := checkHeaders(h, sn); err != nil {
		return store.SiteHeaders{}, err
	}

	old, err := s.st.SetSiteHeaders(ctx, store.SiteHeaders{SiteID: site.ID, Headers: h, UpdatedBy: actor}, s.now())
	if err != nil {
		return store.SiteHeaders{}, err
	}

	if err := s.applyWeb(ctx); err != nil {
		if _, rerr := s.st.SetSiteHeaders(context.WithoutCancel(ctx), old, old.UpdatedAt); rerr != nil {
			s.log.ErrorContext(ctx, "restore security headers after failed change", "site", name, "err", rerr)
		}

		return store.SiteHeaders{}, err
	}

	// Header values are public: every visitor gets them.
	s.auditChange(ctx, actor, "site.headers.set", name, nil, store.FieldsOf(old.Headers), store.FieldsOf(h))

	return s.st.SiteHeaders(ctx, site.ID)
}

// webHeaders are the headers sent with web.apply; nil for none.
func webHeaders(all map[int64]store.SiteHeaders, site store.Site) *agentapi.WebHeaders {
	h, ok := all[site.ID]
	if !ok || h.Headers.IsZero() {
		return nil
	}

	v := h.Headers

	return &v
}
