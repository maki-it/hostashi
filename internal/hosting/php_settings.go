// SPDX-License-Identifier: AGPL-3.0-or-later

package hosting

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/maki-it/hostashi/internal/agentapi"
	"github.com/maki-it/hostashi/internal/store"
)

// PHPSettings returns the PHP ini settings of a site (ADR-0049); unset
// fields mean the defaults.
func (s *Service) PHPSettings(ctx context.Context, name string) (store.SitePHPSettings, error) {
	site, err := s.Get(ctx, name)
	if err != nil {
		return store.SitePHPSettings{}, err
	}

	return s.st.SitePHPSettings(ctx, site.ID)
}

// SetPHPSettings replaces the PHP ini settings of an active PHP site,
// applies the web state (pool and nginx) and restores the old settings if
// that fails. A rejected value is an ErrInvalid that also matches
// *agentapi.PHPSettingError.
func (s *Service) SetPHPSettings(ctx context.Context, actor int64, name string, ps agentapi.PHPSettings) (store.SitePHPSettings, error) {
	if err := ps.Validate(); err != nil {
		return store.SitePHPSettings{}, fmt.Errorf("%w: %w", ErrInvalid, err)
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	site, err := s.Get(ctx, name)
	if err != nil {
		return store.SitePHPSettings{}, err
	}

	if site.State != store.SiteActive {
		return store.SitePHPSettings{}, ErrState
	}

	if site.PHP == "" {
		return store.SitePHPSettings{}, fmt.Errorf("%w: the site has no PHP", ErrInvalid)
	}

	old, err := s.st.SetSitePHPSettings(ctx, store.SitePHPSettings{SiteID: site.ID, Settings: ps, UpdatedBy: actor}, s.now())
	if err != nil {
		return store.SitePHPSettings{}, err
	}

	if err := s.applyWeb(ctx); err != nil {
		if _, rerr := s.st.SetSitePHPSettings(context.WithoutCancel(ctx), old, old.UpdatedAt); rerr != nil {
			s.log.ErrorContext(ctx, "restore php settings after failed change", "site", name, "err", rerr)
		}

		return store.SitePHPSettings{}, err
	}

	// The values are limits and switches, nothing secret.
	b, _ := json.Marshal(ps)
	s.audit(ctx, actor, "site.php_settings.set", name, map[string]string{"settings": string(b)})

	return s.st.SitePHPSettings(ctx, site.ID)
}

// webPHPSettings are the settings sent with web.apply; nil for defaults.
func webPHPSettings(all map[int64]store.SitePHPSettings, site store.Site) *agentapi.PHPSettings {
	ps, ok := all[site.ID]
	if !ok {
		return nil
	}

	v := ps.Settings

	return &v
}
