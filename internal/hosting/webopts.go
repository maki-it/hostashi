// SPDX-License-Identifier: AGPL-3.0-or-later

package hosting

import (
	"context"
	"fmt"
	"slices"
	"strconv"
	"strings"

	"github.com/maki-it/hostashi/internal/agentapi"
	"github.com/maki-it/hostashi/internal/store"
)

// SetWebOptions sets the document root folder and the static-site options
// of an active site (ADR-0054 addendum 1). SPA and asset caching need a
// static site; the folder works for static and PHP sites.
func (s *Service) SetWebOptions(ctx context.Context, actor int64, name string, o store.WebOptions) (store.Site, error) {
	if err := agentapi.ValidateWebDir(o.Dir); err != nil {
		return store.Site{}, fmt.Errorf("%w: %w", ErrInvalid, err)
	}

	if err := agentapi.ValidateAssetCache(o.AssetCacheSeconds, o.AssetCacheExts); err != nil {
		return store.Site{}, fmt.Errorf("%w: %w", ErrInvalid, err)
	}

	// The default list is stored as empty, so a later change of the
	// defaults reaches the site.
	if slices.Equal(o.AssetCacheExts, agentapi.DefaultAssetCacheExts) {
		o.AssetCacheExts = nil
	}

	if o.AssetCacheSeconds == agentapi.DefaultAssetCacheSeconds {
		o.AssetCacheSeconds = 0
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

	if (o.SPA || o.AssetCache) && site.Type != store.SiteTypeStatic {
		return store.Site{}, fmt.Errorf("%w: single-page app and asset caching are for static sites", ErrInvalid)
	}

	old, err := s.st.SetSiteWebOptions(ctx, name, o, s.now())
	if err != nil {
		return store.Site{}, err
	}

	if err := s.applyWeb(ctx); err != nil {
		if _, rerr := s.st.SetSiteWebOptions(context.WithoutCancel(ctx), name, old, s.now()); rerr != nil {
			s.log.ErrorContext(ctx, "restore web options after failed change", "site", name, "err", rerr)
		}

		return store.Site{}, err
	}

	view := func(w store.WebOptions) map[string]string {
		return map[string]string{
			"dir": w.Dir, "spa": strconv.FormatBool(w.SPA), "assetCache": strconv.FormatBool(w.AssetCache),
			"assetCacheSeconds": strconv.Itoa(w.AssetCacheSeconds), "assetCacheExts": strings.Join(w.AssetCacheExts, " "),
		}
	}

	s.auditChange(ctx, actor, "site.web", name, nil, view(old), view(o))

	return s.Get(ctx, name)
}
