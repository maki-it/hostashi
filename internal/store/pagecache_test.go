// SPDX-License-Identifier: AGPL-3.0-or-later

package store

import (
	"slices"
	"testing"
	"time"
)

func TestSitePageCache(t *testing.T) {
	s := open(t, dbPath(t))
	ctx := t.Context()
	now := time.Unix(2_000_000, 0)

	site, err := s.CreateSite(ctx, "blog", "default", "8.4", []string{"blog.example.com"}, now)
	if err != nil {
		t.Fatal(err)
	}

	c, err := s.SitePageCache(ctx, site.ID)
	if err != nil || c.Enabled || c.Preset != "wordpress" || c.Minutes != 10 || c.SizeMB != 256 || c.Exclude == nil {
		t.Fatalf("default: %+v %v", c, err)
	}

	on := PageCache{SiteID: site.ID, Enabled: true, Preset: "woocommerce", Minutes: 30, SizeMB: 512, Exclude: []string{"/shop*"}}

	old, err := s.SetSitePageCache(ctx, on, now)
	if err != nil || old.Enabled {
		t.Fatalf("set: %+v %v", old, err)
	}

	got, err := s.SitePageCache(ctx, site.ID)
	if err != nil || !got.Enabled || got.Preset != "woocommerce" || got.Minutes != 30 || got.SizeMB != 512 ||
		!slices.Equal(got.Exclude, []string{"/shop*"}) || !got.UpdatedAt.Equal(now) {
		t.Fatalf("get: %+v %v", got, err)
	}

	off := got
	off.Enabled, off.Exclude = false, nil

	if old, err := s.SetSitePageCache(ctx, off, now); err != nil || !old.Enabled {
		t.Fatalf("off: %+v %v", old, err)
	}

	all, err := s.AllPageCaches(ctx)
	if err != nil || len(all) != 1 || all[site.ID].Enabled || all[site.ID].Exclude == nil || len(all[site.ID].Exclude) != 0 {
		t.Fatalf("all: %+v %v", all, err)
	}

	// The database refuses values outside the fixed choices.
	for _, bad := range []PageCache{
		{SiteID: site.ID, Preset: "varnish", Minutes: 10, SizeMB: 256},
		{SiteID: site.ID, Preset: "php", Minutes: 7, SizeMB: 256},
		{SiteID: site.ID, Preset: "php", Minutes: 10, SizeMB: 100},
		{SiteID: site.ID + 99, Preset: "php", Minutes: 10, SizeMB: 256},
	} {
		if _, err := s.SetSitePageCache(ctx, bad, now); err == nil {
			t.Errorf("%+v accepted", bad)
		}
	}

	if err := s.DeleteSite(ctx, "blog"); err != nil {
		t.Fatal(err)
	}

	if all, err := s.AllPageCaches(ctx); err != nil || len(all) != 0 {
		t.Fatalf("after delete: %+v %v", all, err)
	}
}
