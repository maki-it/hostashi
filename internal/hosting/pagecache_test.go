// SPDX-License-Identifier: AGPL-3.0-or-later

package hosting

import (
	"errors"
	"slices"
	"testing"
	"time"

	"github.com/maki-it/hostashi/internal/agentapi"
	"github.com/maki-it/hostashi/internal/store"
)

func (f *fakeAgent) siteWeb(user string) (agentapi.WebSite, bool) {
	f.mu.Lock()
	defer f.mu.Unlock()

	for _, s := range f.web.Sites {
		if s.User == user {
			return s, true
		}
	}

	return agentapi.WebSite{}, false
}

func TestPageCache(t *testing.T) {
	s, ag, _ := newService(t)
	ctx := t.Context()

	if _, err := s.Create(ctx, 1, CreateRequest{Name: "blog", PHP: "8.4", Domains: []string{"blog.example.com"}}); err != nil {
		t.Fatal(err)
	}

	if _, err := s.Create(ctx, 1, CreateRequest{Name: "static", Domains: []string{"static.example.com"}}); err != nil {
		t.Fatal(err)
	}

	c, err := s.PageCache(ctx, "blog")
	if err != nil || c.Enabled || c.Preset != agentapi.PageCacheWordPress || c.Minutes != 10 || c.SizeMB != 256 {
		t.Fatalf("default: %+v %v", c, err)
	}

	on := PageCacheSettings{Enabled: true, Preset: agentapi.PageCacheWooCommerce, Minutes: 30, Exclude: []string{"/warenkorb*"}}

	c, err = s.SetPageCache(ctx, 1, "blog", on)
	if err != nil || !c.Enabled || c.Preset != agentapi.PageCacheWooCommerce || c.SizeMB != 256 {
		t.Fatalf("on: %+v %v", c, err)
	}

	ws, _ := ag.siteWeb("blog")
	if ws.PageCache == nil || ws.PageCache.Preset != agentapi.PageCacheWooCommerce || ws.PageCache.Minutes != 30 ||
		ws.PageCache.SizeMB != 256 || !slices.Equal(ws.PageCache.Exclude, []string{"/warenkorb*"}) {
		t.Fatalf("sent %+v", ws.PageCache)
	}

	// The size is a separate call and keeps the other settings.
	if c, err = s.SetPageCacheSize(ctx, 1, "blog", 1024); err != nil || c.SizeMB != 1024 || c.Preset != agentapi.PageCacheWooCommerce {
		t.Fatalf("size: %+v %v", c, err)
	}

	if ws, _ := ag.siteWeb("blog"); ws.PageCache == nil || ws.PageCache.SizeMB != 1024 {
		t.Fatalf("size not sent: %+v", ws.PageCache)
	}

	for name, in := range map[string]PageCacheSettings{
		"preset":  {Enabled: true, Preset: "varnish", Minutes: 10},
		"minutes": {Enabled: true, Preset: agentapi.PageCachePHP, Minutes: 15},
		"exclude": {Enabled: true, Preset: agentapi.PageCachePHP, Minutes: 10, Exclude: []string{"/a b"}},
		// Also off: the settings are stored and must be valid.
		"off with bad preset": {Preset: "x", Minutes: 10},
	} {
		if _, err := s.SetPageCache(ctx, 1, "blog", in); !errors.Is(err, ErrInvalid) {
			t.Errorf("%s: %v", name, err)
		}
	}

	if _, err := s.SetPageCacheSize(ctx, 1, "blog", 100); !errors.Is(err, ErrInvalid) {
		t.Errorf("bad size: %v", err)
	}

	// Without PHP there is nothing to cache.
	if _, err := s.SetPageCache(ctx, 1, "static", on); !errors.Is(err, ErrInvalid) {
		t.Fatalf("static: %v", err)
	}

	if _, err := s.SetPageCache(ctx, 1, "nosuch", on); !errors.Is(err, ErrNotFound) {
		t.Fatalf("unknown: %v", err)
	}

	// PHP off: the cache stays switched on but is not sent.
	if _, err := s.SetPHP(ctx, 1, "blog", ""); err != nil {
		t.Fatal(err)
	}

	if ws, _ := ag.siteWeb("blog"); ws.PageCache != nil {
		t.Fatalf("cache sent without PHP: %+v", ws.PageCache)
	}

	if _, err := s.SetPHP(ctx, 1, "blog", "8.4"); err != nil {
		t.Fatal(err)
	}

	// Disabled: no PHP, so no cache either.
	if _, err := s.SetDisabled(ctx, 1, "blog", true, ""); err != nil {
		t.Fatal(err)
	}

	if ws, _ := ag.siteWeb("blog"); ws.PageCache != nil {
		t.Fatalf("cache sent for a disabled site: %+v", ws.PageCache)
	}

	if _, err := s.SetDisabled(ctx, 1, "blog", false, ""); err != nil {
		t.Fatal(err)
	}

	// Maintenance: still sent, the agent keeps the directory.
	if _, err := s.SetMaintenance(ctx, 1, "blog", Maintenance{Enabled: true}); err != nil {
		t.Fatal(err)
	}

	if ws, _ := ag.siteWeb("blog"); ws.PageCache == nil || ws.Maintenance == nil {
		t.Fatalf("maintenance: %+v", ws)
	}

	// A failed apply restores the previous settings.
	ag.webErr = errDown

	if _, err := s.SetPageCache(ctx, 1, "blog", PageCacheSettings{Preset: agentapi.PageCachePHP, Minutes: 1}); err == nil {
		t.Fatal("change with the agent down succeeded")
	}

	ag.webErr = nil

	if c, _ := s.PageCache(ctx, "blog"); !c.Enabled || c.Preset != agentapi.PageCacheWooCommerce {
		t.Fatalf("not restored: %+v", c)
	}

	// Switched off: not sent any more.
	if _, err := s.SetPageCache(ctx, 1, "blog", PageCacheSettings{Preset: agentapi.PageCacheWooCommerce, Minutes: 30}); err != nil {
		t.Fatal(err)
	}

	if ws, _ := ag.siteWeb("blog"); ws.PageCache != nil {
		t.Fatalf("off but sent: %+v", ws.PageCache)
	}
}

func TestPurgePageCache(t *testing.T) {
	s, ag, _ := newService(t)
	ctx := t.Context()
	now := time.Unix(1_000_000, 0)
	s.now = func() time.Time { return now }

	for _, n := range []string{"blog", "shop"} {
		if _, err := s.Create(ctx, 1, CreateRequest{Name: n, PHP: "8.4", Domains: []string{n + ".example.com"}}); err != nil {
			t.Fatal(err)
		}
	}

	// Off: nothing to purge.
	if err := s.PurgePageCache(ctx, 1, "blog"); !errors.Is(err, ErrInvalid) {
		t.Fatalf("off: %v", err)
	}

	for _, n := range []string{"blog", "shop"} {
		if _, err := s.SetPageCache(ctx, 1, n, PageCacheSettings{Enabled: true, Preset: agentapi.PageCacheWordPress, Minutes: 10}); err != nil {
			t.Fatal(err)
		}
	}

	if err := s.PurgePageCache(ctx, 1, "blog"); err != nil {
		t.Fatal(err)
	}

	// A second purge right away waits; another site does not.
	var re *RetryError
	if err := s.PurgePageCache(ctx, 1, "blog"); !errors.As(err, &re) || !errors.Is(err, ErrRateLimited) || re.After != PurgeInterval {
		t.Fatalf("second purge: %v", err)
	}

	if err := s.PurgePageCache(ctx, 1, "shop"); err != nil {
		t.Fatal(err)
	}

	now = now.Add(PurgeInterval - time.Second)

	if err := s.PurgePageCache(ctx, 1, "blog"); !errors.As(err, &re) || re.After != time.Second {
		t.Fatalf("almost: %v", err)
	}

	now = now.Add(time.Second)

	if err := s.PurgePageCache(ctx, 1, "blog"); err != nil {
		t.Fatal(err)
	}

	if !slices.Equal(ag.purges, []string{"blog", "shop", "blog"}) {
		t.Fatalf("purges %v", ag.purges)
	}

	if err := s.PurgePageCache(ctx, 1, "nosuch"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("unknown: %v", err)
	}
}

// The preset's page cache is a starting value (ADR-0046), as the object
// cache: a new site and a site switched to the preset get it; a later change
// of the preset only resizes caches that are on.
func TestPresetPageCache(t *testing.T) {
	s, _, _ := newService(t)
	ctx := t.Context()

	s.EnableLimits(newFakeLimits())

	small, err := s.LimitPreset(ctx, "small")
	if err != nil {
		t.Fatal(err)
	}

	if _, err := s.UpdateLimitPreset(ctx, 1, "small", "small", small.Limits, store.PresetCaches{PageCacheMB: 100}, false); !errors.Is(err, ErrInvalid) {
		t.Errorf("size outside the choices: %v", err)
	}

	if _, err := s.UpdateLimitPreset(ctx, 1, "small", "small", small.Limits, store.PresetCaches{PageCacheMB: 128}, false); err != nil {
		t.Fatal(err)
	}

	for _, n := range []string{"blog", "shop"} {
		if _, err := s.Create(ctx, 1, CreateRequest{Name: n, PHP: "8.4", Domains: []string{n + ".example.com"}}); err != nil {
			t.Fatal(err)
		}
	}

	if _, err := s.Create(ctx, 1, CreateRequest{Name: "static", Domains: []string{"static.example.com"}}); err != nil {
		t.Fatal(err)
	}

	// A new PHP site gets the cache with the strict preset; a static one not.
	if c, err := s.PageCache(ctx, "blog"); err != nil || !c.Enabled || c.SizeMB != 128 || c.Preset != agentapi.PageCachePHP {
		t.Fatalf("new site: %+v %v", c, err)
	}

	if c, _ := s.PageCache(ctx, "static"); c.Enabled {
		t.Error("a site without PHP got a page cache")
	}

	// Off on the site stays off when the preset's size changes.
	if _, err := s.SetPageCache(ctx, 1, "shop", PageCacheSettings{Preset: agentapi.PageCachePHP, Minutes: 10}); err != nil {
		t.Fatal(err)
	}

	if _, err := s.UpdateLimitPreset(ctx, 1, "small", "small", small.Limits, store.PresetCaches{PageCacheMB: 512}, false); err != nil {
		t.Fatal(err)
	}

	if c, _ := s.PageCache(ctx, "blog"); !c.Enabled || c.SizeMB != 512 {
		t.Errorf("following site after a size change: %+v", c)
	}

	if c, _ := s.PageCache(ctx, "shop"); c.Enabled {
		t.Errorf("a cache that is off was turned on: %+v", c)
	}

	// Off in the preset leaves the sites' caches as they are.
	if _, err := s.UpdateLimitPreset(ctx, 1, "small", "small", small.Limits, store.PresetCaches{}, false); err != nil {
		t.Fatal(err)
	}

	if c, _ := s.PageCache(ctx, "blog"); !c.Enabled || c.SizeMB != 512 {
		t.Errorf("preset off turned a site's cache off: %+v", c)
	}

	// Switching to a preset with a cache turns it on and keeps the site's
	// own preset choice.
	if _, err := s.SetPageCache(ctx, 1, "shop", PageCacheSettings{Preset: agentapi.PageCacheWooCommerce, Minutes: 30}); err != nil {
		t.Fatal(err)
	}

	medium, err := s.LimitPreset(ctx, "medium")
	if err != nil {
		t.Fatal(err)
	}

	if _, err := s.UpdateLimitPreset(ctx, 1, "medium", "medium", medium.Limits, store.PresetCaches{PageCacheMB: 1024}, false); err != nil {
		t.Fatal(err)
	}

	if _, err := s.SetSiteLimits(ctx, 1, "shop", "medium", nil); err != nil {
		t.Fatal(err)
	}

	if c, _ := s.PageCache(ctx, "shop"); !c.Enabled || c.SizeMB != 1024 || c.Preset != agentapi.PageCacheWooCommerce || c.Minutes != 30 {
		t.Errorf("switch to a preset with a cache: %+v", c)
	}
}
