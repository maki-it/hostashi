// SPDX-License-Identifier: AGPL-3.0-or-later

package store

import (
	"errors"
	"testing"
	"time"
)

func TestLimitPresets(t *testing.T) {
	s := open(t, dbPath(t))
	ctx := t.Context()
	now := time.Unix(2_000_000, 0)

	all, err := s.LimitPresets(ctx)
	if err != nil || len(all) != 3 {
		t.Fatalf("seeded presets: %+v %v", all, err)
	}

	small, err := s.DefaultLimitPreset(ctx)
	if err != nil || small.Name != "small" || small.Limits.DiskBytes != 10<<30 || small.Limits.MemoryBytes != 512<<20 ||
		small.Limits.PHPWorkers != 5 || small.Limits.Tasks != 128 || small.Limits.DiskInodes != 500_000 || small.Limits.CPUPriority != "normal" {
		t.Fatalf("default preset %+v %v", small, err)
	}

	if m, _ := s.LimitPreset(ctx, "medium"); m.Limits.CPUPriority != "high" || m.Limits.DiskBytes != 25<<30 || m.Default {
		t.Errorf("medium %+v", m)
	}

	if l, _ := s.LimitPreset(ctx, "large"); l.Limits.MemoryBytes != 2<<30 || l.Limits.Tasks != 512 || l.Limits.PHPWorkers != 20 {
		t.Errorf("large %+v", l)
	}

	if _, err := s.LimitPreset(ctx, "huge"); !errors.Is(err, ErrNotFound) {
		t.Errorf("unknown preset: %v", err)
	}

	vals := Limits{DiskBytes: 5 << 30, DiskInodes: 100_000, MemoryBytes: 256 << 20, PHPWorkers: 3, Tasks: 64, CPUPriority: "low"}

	if _, err := s.CreateLimitPreset(ctx, "small", vals, PresetCaches{}, false, now); !errors.Is(err, ErrPresetExists) {
		t.Errorf("duplicate name: %v", err)
	}

	tiny, err := s.CreateLimitPreset(ctx, "tiny", vals, PresetCaches{}, true, now)
	if err != nil || !tiny.Default {
		t.Fatalf("create %+v %v", tiny, err)
	}

	if d, _ := s.DefaultLimitPreset(ctx); d.ID != tiny.ID {
		t.Errorf("default after create: %+v", d)
	}

	if _, err := s.DeleteLimitPreset(ctx, tiny.ID); !errors.Is(err, ErrPresetDefault) {
		t.Errorf("deleting the default: %v", err)
	}

	site, err := s.CreateSite(ctx, "blog", "default", "", []string{"blog.example.com"}, now)
	if err != nil {
		t.Fatal(err)
	}

	if _, ok, err := s.SiteLimits(ctx, site.ID); ok || err != nil {
		t.Fatalf("limits of a new site: %v %v", ok, err)
	}

	if err := s.SetSiteLimits(ctx, site.ID, tiny.ID, vals, now); err != nil {
		t.Fatal(err)
	}

	if err := s.MarkSiteLimits(ctx, site.ID, now.Unix(), ""); err != nil {
		t.Fatal(err)
	}

	before, ok, err := s.SiteLimits(ctx, site.ID)
	if err != nil || !ok || before.PresetID != tiny.ID || before.Limits != vals || before.AppliedAt.IsZero() {
		t.Fatalf("site limits %+v %v %v", before, ok, err)
	}

	bigger := vals
	bigger.MemoryBytes = 1 << 30

	old, err := s.UpdateLimitPreset(ctx, tiny.ID, "tiny", bigger, PresetCaches{ObjectCacheMB: 64}, false, now.Add(time.Minute))
	if err != nil || old.Limits != vals {
		t.Fatalf("update %+v %v", old, err)
	}

	if p, _ := s.LimitPresetByID(ctx, tiny.ID); !p.Default || p.Limits != bigger || p.Caches.ObjectCacheMB != 64 {
		t.Errorf("update with def=false must keep the default: %+v", p)
	}

	follower, _, _ := s.SiteLimits(ctx, site.ID)
	if follower.Limits != bigger || !follower.AppliedAt.IsZero() {
		t.Errorf("follower after preset update %+v", follower)
	}

	if ids, _ := s.PresetFollowers(ctx, tiny.ID); len(ids) != 1 || ids[0] != "blog" {
		t.Errorf("followers %v", ids)
	}

	if err := s.RestoreLimitPreset(ctx, old, now); err != nil {
		t.Fatal(err)
	}

	if f, _, _ := s.SiteLimits(ctx, site.ID); f.Limits != vals {
		t.Errorf("follower after restore %+v", f)
	}

	if p, _ := s.LimitPresetByID(ctx, tiny.ID); p.Caches.ObjectCacheMB != 0 {
		t.Errorf("object cache after restore %+v", p.Caches)
	}

	if _, err := s.UpdateLimitPreset(ctx, tiny.ID, "tiny", vals, PresetCaches{ObjectCacheMB: 8}, false, now); err == nil {
		t.Error("a cache below 32 MiB must be refused by the schema")
	}

	if _, err := s.UpdateLimitPreset(ctx, tiny.ID, "medium", vals, PresetCaches{}, false, now); !errors.Is(err, ErrPresetExists) {
		t.Errorf("rename onto another preset: %v", err)
	}

	// Making "small" the default again frees "tiny" for deletion; its
	// site keeps the values as custom ones.
	if _, err := s.UpdateLimitPreset(ctx, small.ID, "small", small.Limits, small.Caches, true, now); err != nil {
		t.Fatal(err)
	}

	if _, err := s.DeleteLimitPreset(ctx, tiny.ID); err != nil {
		t.Fatal(err)
	}

	if f, ok, _ := s.SiteLimits(ctx, site.ID); !ok || f.PresetID != 0 || f.Limits != vals {
		t.Errorf("site after its preset was deleted %+v", f)
	}

	if err := s.MarkSiteLimits(ctx, site.ID, 0, "boom"); err != nil {
		t.Fatal(err)
	}

	if f, _, _ := s.SiteLimits(ctx, site.ID); f.ApplyError != "boom" || !f.AppliedAt.IsZero() {
		t.Errorf("failed apply %+v", f)
	}

	before.PresetID = 0 // deleted meanwhile

	if err := s.RestoreSiteLimits(ctx, before, true); err != nil {
		t.Fatal(err)
	}

	if f, _, _ := s.SiteLimits(ctx, site.ID); f.ApplyError != "" || f.AppliedAt.IsZero() {
		t.Errorf("restored %+v", f)
	}

	if err := s.RestoreSiteLimits(ctx, SiteLimits{SiteID: site.ID}, false); err != nil {
		t.Fatal(err)
	}

	if _, ok, _ := s.SiteLimits(ctx, site.ID); ok {
		t.Error("restore to none kept the row")
	}
}
