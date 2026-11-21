// SPDX-License-Identifier: AGPL-3.0-or-later

package hosting

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strconv"
	"time"

	"github.com/maki-it/hostashi/internal/agentapi"
	"github.com/maki-it/hostashi/internal/store"
)

// PageCachePurge calls pagecache.purge.
func (a RPCAgent) PageCachePurge(ctx context.Context, user string) error {
	_, err := agentapi.PageCachePurge.Call(ctx, a.C, agentapi.PageCachePurgeRequest{User: user})

	return err
}

// PurgeInterval is the shortest time between two purges of one site's page
// cache through the panel or the API (ADR-0046), so a save loop in a CMS
// cannot keep the cache empty or the agent busy.
const PurgeInterval = 10 * time.Second

// ErrRateLimited means the action was done for this site a moment ago.
var ErrRateLimited = errors.New("hosting: too many requests, try again shortly")

// RetryError is ErrRateLimited with the time to wait.
type RetryError struct{ After time.Duration }

func (e *RetryError) Error() string { return ErrRateLimited.Error() }

// Unwrap makes errors.Is(err, ErrRateLimited) work.
func (e *RetryError) Unwrap() error { return ErrRateLimited }

// PageCacheSettings are what a user with access to the site may change.
// The size is separate (SetPageCacheSize): it counts against the disk limit.
type PageCacheSettings struct {
	Enabled bool
	Preset  string
	Minutes int
	Exclude []string
}

// PageCache returns the page cache settings of a site.
func (s *Service) PageCache(ctx context.Context, name string) (store.PageCache, error) {
	site, err := s.Get(ctx, name)
	if err != nil {
		return store.PageCache{}, err
	}

	return s.st.SitePageCache(ctx, site.ID)
}

// SetPageCache changes the page cache of an active site and applies it. A
// cache needs PHP.
func (s *Service) SetPageCache(ctx context.Context, actor int64, name string, in PageCacheSettings) (store.PageCache, error) {
	if in.Exclude == nil {
		in.Exclude = []string{}
	}

	return s.changePageCache(ctx, actor, name, func(site store.Site, c *store.PageCache) (map[string]string, error) {
		check := agentapi.WebPageCache{Preset: in.Preset, Minutes: in.Minutes, SizeMB: c.SizeMB, Exclude: in.Exclude}
		if err := check.Validate(); err != nil {
			return nil, fmt.Errorf("%w: %w", ErrInvalid, err)
		}

		if in.Enabled && site.PHP == "" {
			return nil, fmt.Errorf("%w: the page cache needs PHP", ErrInvalid)
		}

		c.Enabled, c.Preset, c.Minutes, c.Exclude = in.Enabled, in.Preset, in.Minutes, slices.Clone(in.Exclude)

		return map[string]string{
			"enabled": strconv.FormatBool(in.Enabled), "preset": in.Preset, "minutes": strconv.Itoa(in.Minutes),
			"excluded": strconv.Itoa(len(in.Exclude)),
		}, nil
	})
}

// SetPageCacheSize changes the most disk space the page cache of a site may
// use.
func (s *Service) SetPageCacheSize(ctx context.Context, actor int64, name string, sizeMB int) (store.PageCache, error) {
	return s.changePageCache(ctx, actor, name, func(_ store.Site, c *store.PageCache) (map[string]string, error) {
		if !slices.Contains(agentapi.PageCacheSizesMB, sizeMB) {
			return nil, fmt.Errorf("%w: size must be one of %v MB", ErrInvalid, agentapi.PageCacheSizesMB)
		}

		c.SizeMB = sizeMB

		return map[string]string{"sizeMB": strconv.Itoa(sizeMB)}, nil
	})
}

func (s *Service) changePageCache(ctx context.Context, actor int64, name string,
	change func(store.Site, *store.PageCache) (map[string]string, error),
) (store.PageCache, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	site, err := s.Get(ctx, name)
	if err != nil {
		return store.PageCache{}, err
	}

	if site.State != store.SiteActive {
		return store.PageCache{}, ErrState
	}

	c, err := s.st.SitePageCache(ctx, site.ID)
	if err != nil {
		return store.PageCache{}, err
	}

	detail, err := change(site, &c)
	if err != nil {
		return store.PageCache{}, err
	}

	old, err := s.st.SetSitePageCache(ctx, c, s.now())
	if err != nil {
		return store.PageCache{}, err
	}

	if err := s.applyWeb(ctx); err != nil {
		if _, rerr := s.st.SetSitePageCache(context.WithoutCancel(ctx), old, s.now()); rerr != nil {
			s.log.ErrorContext(ctx, "restore page cache after failed change", "site", name, "err", rerr)
		}

		return store.PageCache{}, err
	}

	s.audit(ctx, actor, "site.pagecache.set", name, detail)

	return s.st.SitePageCache(ctx, site.ID)
}

// PurgePageCache empties the page cache of a site, at most once per
// PurgeInterval per site.
func (s *Service) PurgePageCache(ctx context.Context, actor int64, name string) error {
	site, err := s.Get(ctx, name)
	if err != nil {
		return err
	}

	c, err := s.st.SitePageCache(ctx, site.ID)
	if err != nil {
		return err
	}

	if !c.Enabled {
		return fmt.Errorf("%w: the page cache of this site is off", ErrInvalid)
	}

	if err := s.purgeSlot(name); err != nil {
		return err
	}

	if err := s.ag.PageCachePurge(ctx, name); err != nil {
		return err
	}

	s.audit(ctx, actor, "site.pagecache.purge", name, nil)

	return nil
}

// purgeSlot takes the site's purge slot or says how long to wait.
func (s *Service) purgeSlot(name string) error {
	s.purgeMu.Lock()
	defer s.purgeMu.Unlock()

	now := s.now()

	if last, ok := s.purged[name]; ok {
		if wait := last.Add(PurgeInterval).Sub(now); wait > 0 {
			return &RetryError{After: wait}
		}
	}

	if s.purged == nil {
		s.purged = map[string]time.Time{}
	}

	// Old entries go, so the map stays as small as the number of sites
	// purged in the last interval.
	for n, t := range s.purged {
		if now.Sub(t) >= PurgeInterval {
			delete(s.purged, n)
		}
	}

	s.purged[name] = now

	return nil
}

// webPageCache is the page cache that applyWeb sends for a site: only when
// it is on and the site runs PHP.
func webPageCache(caches map[int64]store.PageCache, site store.Site, php string) *agentapi.WebPageCache {
	c, ok := caches[site.ID]
	if !ok || !c.Enabled || php == "" {
		return nil
	}

	return &agentapi.WebPageCache{Preset: c.Preset, Minutes: c.Minutes, SizeMB: c.SizeMB, Exclude: c.Exclude}
}
