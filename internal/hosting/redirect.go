// SPDX-License-Identifier: AGPL-3.0-or-later

package hosting

import (
	"context"
	"strconv"

	"github.com/maki-it/hostashi/internal/store"
)

// SetRedirect changes the target of an active redirect site (ADR-0054).
// If the web server does not accept it, the previous target is stored
// again.
func (s *Service) SetRedirect(ctx context.Context, actor int64, name string, r store.SiteRedirect) (store.Site, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	site, err := s.Get(ctx, name)
	if err != nil {
		return store.Site{}, err
	}

	if site.State != store.SiteActive {
		return store.Site{}, ErrState
	}

	if site.Type != store.SiteTypeRedirect {
		return store.Site{}, ErrSiteType
	}

	if r, err = checkRedirect(r, site.AllDomains()); err != nil {
		return store.Site{}, err
	}

	if err := s.checkRedirectLoop(ctx, name, r, site.AllDomains()); err != nil {
		return store.Site{}, err
	}

	old, err := s.st.SetSiteRedirect(ctx, name, r, s.now())
	if err != nil {
		return store.Site{}, err
	}

	if err := s.applyWeb(ctx); err != nil {
		if _, rerr := s.st.SetSiteRedirect(context.WithoutCancel(ctx), name, old, s.now()); rerr != nil {
			s.log.ErrorContext(ctx, "restore redirect after failed change", "site", name, "err", rerr)
		}

		return store.Site{}, err
	}

	s.auditChange(ctx, actor, "site.redirect", name, nil, redirectAudit(old), redirectAudit(r))

	return s.Get(ctx, name)
}

func redirectAudit(r store.SiteRedirect) map[string]string {
	return map[string]string{"target": r.Target, "status": strconv.Itoa(r.Status), "keepPath": strconv.FormatBool(r.KeepPath)}
}
