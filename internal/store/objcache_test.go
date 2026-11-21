// SPDX-License-Identifier: AGPL-3.0-or-later

package store

import (
	"testing"
	"time"
)

func TestObjectCache(t *testing.T) {
	s := open(t, dbPath(t))
	ctx := t.Context()
	now := time.Unix(2_000_000, 0)

	site, err := s.CreateSite(ctx, "blog", "default", "", []string{"blog.example.com"}, now)
	if err != nil {
		t.Fatal(err)
	}

	if c, err := s.ObjectCache(ctx, site.ID); err != nil || c.Enabled || c.MemoryMB != 0 || c.SiteID != site.ID {
		t.Fatalf("none: %+v %v", c, err)
	}

	if err := s.SetObjectCache(ctx, site.ID, true, 64, 0, now); err != nil {
		t.Fatal(err)
	}

	if err := s.SetObjectCache(ctx, site.ID, false, 128, 0, now.Add(time.Minute)); err != nil {
		t.Fatal(err)
	}

	c, err := s.ObjectCache(ctx, site.ID)
	if err != nil || c.Enabled || c.MemoryMB != 128 || !c.UpdatedAt.Equal(now.Add(time.Minute)) {
		t.Fatalf("get: %+v %v", c, err)
	}

	// The schema refuses sizes outside ADR-0045's range.
	for _, mb := range []int{0, 16, 2048} {
		if err := s.SetObjectCache(ctx, site.ID, true, mb, 0, now); err == nil {
			t.Errorf("size %d stored", mb)
		}
	}

	if err := s.DeleteSite(ctx, site.Name); err != nil {
		t.Fatal(err)
	}

	if c, _ := s.ObjectCache(ctx, site.ID); c.MemoryMB != 0 {
		t.Fatalf("cache row left after site delete: %+v", c)
	}
}
