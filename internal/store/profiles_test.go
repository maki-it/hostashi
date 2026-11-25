// SPDX-License-Identifier: AGPL-3.0-or-later

package store

import (
	"testing"
	"time"

	"github.com/maki-it/hostashi/internal/profile"
)

func TestSiteProfiles(t *testing.T) {
	s := open(t, dbPath(t))
	ctx := t.Context()
	now := time.Unix(2_000_000, 0)

	site, err := s.CreateSite(ctx, "blog", "default", "8.4", []string{"blog.example.com"}, now)
	if err != nil {
		t.Fatal(err)
	}

	if p, err := s.SiteProfile(ctx, site.ID); err != nil || p.Profile != "" || !p.Overrides.IsZero() {
		t.Fatalf("none: %+v %v", p, err)
	}

	off := false
	dirs := []string{}
	want := SiteProfile{SiteID: site.ID, Profile: profile.WordPress, Overrides: profile.Overrides{ConfigFiles: &off, DenyFiles: &dirs}}

	if _, err := s.SetSiteProfile(ctx, want, now); err != nil {
		t.Fatal(err)
	}

	got, err := s.SiteProfile(ctx, site.ID)
	if err != nil || got.Profile != profile.WordPress || got.Overrides.ConfigFiles == nil || *got.Overrides.ConfigFiles ||
		got.Overrides.DenyFiles == nil || len(*got.Overrides.DenyFiles) != 0 || got.Overrides.NoPHPDirs != nil || !got.UpdatedAt.Equal(now) {
		t.Fatalf("get: %+v %v", got, err)
	}

	if all, err := s.AllSiteProfiles(ctx); err != nil || len(all) != 1 || all[site.ID].Profile != profile.WordPress {
		t.Fatalf("all: %+v %v", all, err)
	}

	if _, err := s.SetSiteProfile(ctx, SiteProfile{SiteID: site.ID, Overrides: want.Overrides}, now); err == nil {
		t.Fatal("overrides without a profile accepted")
	}

	for _, bad := range []string{`'[1]'`, `'x'`} {
		if _, err := s.w.ExecContext(ctx, `UPDATE site_profiles SET overrides = `+bad+` WHERE site_id = ?`, site.ID); err == nil {
			t.Fatalf("overrides %s accepted", bad)
		}
	}

	if _, err := s.w.ExecContext(ctx, `UPDATE site_profiles SET profile = '' WHERE site_id = ?`, site.ID); err == nil {
		t.Fatal("empty profile accepted")
	}

	// Back to the default removes the row.
	if _, err := s.SetSiteProfile(ctx, SiteProfile{SiteID: site.ID}, now); err != nil {
		t.Fatal(err)
	}

	if all, err := s.AllSiteProfiles(ctx); err != nil || len(all) != 0 {
		t.Fatalf("after reset: %+v %v", all, err)
	}

	// Deleting the site deletes its profile.
	if _, err := s.SetSiteProfile(ctx, want, now); err != nil {
		t.Fatal(err)
	}

	if _, err := s.w.ExecContext(ctx, `DELETE FROM sites WHERE id = ?`, site.ID); err != nil {
		t.Fatal(err)
	}

	if all, err := s.AllSiteProfiles(ctx); err != nil || len(all) != 0 {
		t.Fatalf("after site delete: %+v %v", all, err)
	}
}
