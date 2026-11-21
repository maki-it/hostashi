// SPDX-License-Identifier: AGPL-3.0-or-later

package store

import (
	"testing"
	"time"

	"github.com/maki-it/hostashi/internal/agentapi"
)

func TestSitePHPSettings(t *testing.T) {
	s := open(t, dbPath(t))
	ctx := t.Context()
	now := time.Unix(2_000_000, 0)

	site, err := s.CreateSite(ctx, "blog", "default", "8.4", []string{"blog.example.com"}, now)
	if err != nil {
		t.Fatal(err)
	}

	if ps, err := s.SitePHPSettings(ctx, site.ID); err != nil || ps.Settings != (agentapi.PHPSettings{}) || ps.SiteID != site.ID || !ps.UpdatedAt.IsZero() {
		t.Fatalf("none: %+v %v", ps, err)
	}

	on := true

	old, err := s.SetSitePHPSettings(ctx, SitePHPSettings{SiteID: site.ID, Settings: agentapi.PHPSettings{MemoryLimit: 512 << 20, DisplayErrors: &on}}, now)
	if err != nil || old.Settings.MemoryLimit != 0 {
		t.Fatalf("set: %+v %v", old, err)
	}

	got, err := s.SitePHPSettings(ctx, site.ID)
	if err != nil || got.Settings.MemoryLimit != 512<<20 || got.Settings.DisplayErrors == nil || !*got.Settings.DisplayErrors || !got.UpdatedAt.Equal(now) {
		t.Fatalf("get: %+v %v", got, err)
	}

	all, err := s.AllSitePHPSettings(ctx)
	if err != nil || len(all) != 1 || all[site.ID].Settings.MemoryLimit != 512<<20 {
		t.Fatalf("all: %+v %v", all, err)
	}

	// All defaults remove the row.
	if _, err := s.SetSitePHPSettings(ctx, SitePHPSettings{SiteID: site.ID}, now); err != nil {
		t.Fatal(err)
	}

	if all, err := s.AllSitePHPSettings(ctx); err != nil || len(all) != 0 {
		t.Fatalf("after reset: %+v %v", all, err)
	}

	// The table takes JSON objects only.
	if _, err := s.w.ExecContext(ctx, `INSERT INTO site_php_settings (site_id, settings, updated_at) VALUES (?, 'not json', 0)`, site.ID); err == nil {
		t.Fatal("non-JSON accepted")
	}

	if _, err := s.w.ExecContext(ctx, `INSERT INTO site_php_settings (site_id, settings, updated_at) VALUES (?, '[1]', 0)`, site.ID); err == nil {
		t.Fatal("JSON array accepted")
	}

	// Deleting the site deletes its settings.
	if _, err := s.SetSitePHPSettings(ctx, SitePHPSettings{SiteID: site.ID, Settings: agentapi.PHPSettings{MaxInputVars: 5000}}, now); err != nil {
		t.Fatal(err)
	}

	if _, err := s.w.ExecContext(ctx, `DELETE FROM sites WHERE id = ?`, site.ID); err != nil {
		t.Fatal(err)
	}

	if all, err := s.AllSitePHPSettings(ctx); err != nil || len(all) != 0 {
		t.Fatalf("after site delete: %+v %v", all, err)
	}
}
