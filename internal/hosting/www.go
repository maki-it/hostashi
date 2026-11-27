// SPDX-License-Identifier: AGPL-3.0-or-later

package hosting

import (
	"context"
	"fmt"
	"strings"

	"github.com/maki-it/hostashi/internal/agentapi"
	"github.com/maki-it/hostashi/internal/store"
)

// SetWWWRedirect sets the www redirect of an active site (ADR-0065). Off is
// always allowed; the other modes need a site that is not a redirect and
// has at least one pair of d and www.d among its domains, else ErrInvalid
// names the domain to add.
func (s *Service) SetWWWRedirect(ctx context.Context, actor int64, name, mode string) (store.Site, error) {
	if err := agentapi.ValidateWWWRedirect(mode); err != nil {
		return store.Site{}, fmt.Errorf("%w: %w", ErrInvalid, err)
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	site, err := s.Get(ctx, name)
	if err != nil {
		return store.Site{}, err
	}

	if site.State != store.SiteActive {
		return store.Site{}, ErrState
	}

	if mode != agentapi.WWWRedirectOff {
		if site.Type == store.SiteTypeRedirect {
			return store.Site{}, ErrSiteType
		}

		if len(agentapi.WWWPairs(site.Domains)) == 0 {
			return store.Site{}, fmt.Errorf("%w: %s", ErrInvalid, wwwMissingText(site.Domains))
		}
	}

	if site.WWWRedirect == mode {
		return site, nil
	}

	old, err := s.st.SetSiteWWWRedirect(ctx, name, mode, s.now())
	if err != nil {
		return store.Site{}, err
	}

	if err := s.applyWeb(ctx); err != nil {
		if _, rerr := s.st.SetSiteWWWRedirect(context.WithoutCancel(ctx), name, old, s.now()); rerr != nil {
			s.log.ErrorContext(ctx, "restore www redirect after failed change", "site", name, "err", rerr)
		}

		return store.Site{}, err
	}

	s.auditChange(ctx, actor, "site.www_redirect", name, nil,
		map[string]string{"wwwRedirect": wwwModeText(old)}, map[string]string{"wwwRedirect": wwwModeText(mode)})

	return s.Get(ctx, name)
}

// wwwMissingText tells what to add for a first pair.
func wwwMissingText(domains []string) string {
	missing := agentapi.WWWMissing(domains)
	if len(missing) == 0 {
		return "the site has no domain with a www partner"
	}

	return "add " + strings.Join(missing, " or ") + " as a domain first"
}

func wwwModeText(mode string) string {
	if mode == agentapi.WWWRedirectOff {
		return "off"
	}

	return mode
}
