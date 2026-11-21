// SPDX-License-Identifier: AGPL-3.0-or-later

package hosting

import (
	"context"
	"errors"
	"testing"

	"github.com/maki-it/hostashi/internal/agentapi"
	"github.com/maki-it/hostashi/internal/store"
)

func TestCheckCacheSize(t *testing.T) {
	const gib = int64(1) << 30

	for _, c := range []struct {
		mb    int
		limit int64
		ok    bool
	}{
		{64, 0, true},
		{1024, 0, true},
		{256, 512 << 20, true},  // exactly half
		{257, 512 << 20, false}, // more than half of the site's memory
		{512, gib, true},
		{0, 0, false},
		{31, 0, false},
		{1025, 0, false},
	} {
		err := checkCacheSize(c.mb, c.limit)
		if (err == nil) != c.ok || (err != nil && !errors.Is(err, ErrInvalid)) {
			t.Errorf("checkCacheSize(%d, %d) = %v", c.mb, c.limit, err)
		}
	}
}

type fakeCacheAgent struct{ applied map[string]int }

func (f *fakeCacheAgent) CacheApply(_ context.Context, site string, mb int) (agentapi.CacheStatus, error) {
	f.applied[site] = mb

	return agentapi.CacheStatus{Site: site, MemoryMB: mb, State: "active"}, nil
}

func (f *fakeCacheAgent) CacheFlush(_ context.Context, site string) (agentapi.CacheStatus, error) {
	return agentapi.CacheStatus{Site: site}, nil
}

func (f *fakeCacheAgent) CacheStatus(_ context.Context, site string) (agentapi.CacheStatus, error) {
	return agentapi.CacheStatus{Site: site, MemoryMB: f.applied[site]}, nil
}

func TestCheckPresetCaches(t *testing.T) {
	half := store.Limits{MemoryBytes: 512 << 20}

	for _, c := range []struct {
		mb int64
		l  store.Limits
		ok bool
	}{
		{0, half, true},
		{256, half, true},
		{257, half, false},
		{1024, store.Limits{}, true},
		{16, store.Limits{}, false},
	} {
		if err := CheckPresetCaches(store.PresetCaches{ObjectCacheMB: c.mb}, c.l); (err == nil) != c.ok {
			t.Errorf("CheckPresetCaches(%d, %d) = %v", c.mb, c.l.MemoryBytes, err)
		}
	}
}

// The preset's object cache is a starting value (ADR-0045): a new site and
// a site switched to the preset get it; a later change of the preset only
// resizes caches that are on, and never turns one on or off.
func TestPresetObjectCache(t *testing.T) {
	s, _, _ := newService(t)
	ctx := t.Context()

	s.EnableLimits(newFakeLimits())

	ag := &fakeCacheAgent{applied: map[string]int{}}
	s.EnableObjectCache(ag)

	small, err := s.LimitPreset(ctx, "small")
	if err != nil {
		t.Fatal(err)
	}

	if _, err := s.UpdateLimitPreset(ctx, 1, "small", "small", small.Limits, store.PresetCaches{ObjectCacheMB: 300}, false); !errors.Is(err, ErrInvalid) {
		t.Errorf("more than half of the preset's memory: %v", err)
	}

	if _, err := s.UpdateLimitPreset(ctx, 1, "small", "small", small.Limits, store.PresetCaches{ObjectCacheMB: 96}, false); err != nil {
		t.Fatal(err)
	}

	// A new PHP site gets the cache, a static one does not.
	if _, err := s.Create(ctx, 1, CreateRequest{Name: "blog", PHP: "8.4", Domains: []string{"blog.example.com"}}); err != nil {
		t.Fatal(err)
	}

	if _, err := s.Create(ctx, 1, CreateRequest{Name: "static", Domains: []string{"static.example.com"}}); err != nil {
		t.Fatal(err)
	}

	if v, err := s.ObjectCache(ctx, "blog"); err != nil || !v.Enabled || v.MemoryMB != 96 || ag.applied["blog"] != 96 {
		t.Fatalf("new site: %+v %v (agent %v)", v, err, ag.applied)
	}

	if _, ok := ag.applied["static"]; ok {
		t.Error("a site without PHP got a cache")
	}

	// Off on the site stays off when the preset's size changes.
	if _, err := s.Create(ctx, 1, CreateRequest{Name: "shop", PHP: "8.4", Domains: []string{"shop.example.com"}}); err != nil {
		t.Fatal(err)
	}

	if _, err := s.SetObjectCache(ctx, 1, "shop", false, 0); err != nil {
		t.Fatal(err)
	}

	if _, err := s.UpdateLimitPreset(ctx, 1, "small", "small", small.Limits, store.PresetCaches{ObjectCacheMB: 128}, false); err != nil {
		t.Fatal(err)
	}

	if v, _ := s.ObjectCache(ctx, "blog"); !v.Enabled || v.MemoryMB != 128 || ag.applied["blog"] != 128 {
		t.Errorf("following site after a size change: %+v (agent %v)", v, ag.applied)
	}

	if v, _ := s.ObjectCache(ctx, "shop"); v.Enabled || ag.applied["shop"] != 0 {
		t.Errorf("a cache that is off was turned on: %+v (agent %v)", v, ag.applied)
	}

	// Off in the preset leaves the sites' caches as they are.
	if _, err := s.UpdateLimitPreset(ctx, 1, "small", "small", small.Limits, store.PresetCaches{}, false); err != nil {
		t.Fatal(err)
	}

	if v, _ := s.ObjectCache(ctx, "blog"); !v.Enabled || v.MemoryMB != 128 {
		t.Errorf("preset off turned a site's cache off: %+v", v)
	}

	// Switching to a preset with a cache turns it on.
	medium, err := s.LimitPreset(ctx, "medium")
	if err != nil {
		t.Fatal(err)
	}

	if _, err := s.UpdateLimitPreset(ctx, 1, "medium", "medium", medium.Limits, store.PresetCaches{ObjectCacheMB: 64}, false); err != nil {
		t.Fatal(err)
	}

	if _, err := s.SetSiteLimits(ctx, 1, "shop", "medium", nil); err != nil {
		t.Fatal(err)
	}

	if v, _ := s.ObjectCache(ctx, "shop"); !v.Enabled || v.MemoryMB != 64 || ag.applied["shop"] != 64 {
		t.Errorf("switch to a preset with a cache: %+v (agent %v)", v, ag.applied)
	}
}
