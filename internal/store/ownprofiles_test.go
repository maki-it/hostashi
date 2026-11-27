// SPDX-License-Identifier: AGPL-3.0-or-later

package store

import (
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/maki-it/hostashi/internal/agentapi"
	"github.com/maki-it/hostashi/internal/profile"
)

func TestOwnProfiles(t *testing.T) {
	s := open(t, dbPath(t))
	ctx := t.Context()
	now := time.Unix(2_000_000, 0).UTC()

	if ps, err := s.OwnProfiles(ctx); err != nil || len(ps) != 0 {
		t.Fatalf("empty: %+v %v", ps, err)
	}

	want := profile.Profile{
		Name: "agency", Title: "Agency", Type: "php", MinPHP: "8.3", WebDir: "public",
		Hardening: agentapi.WebHardening{ConfigFiles: true, DenyFiles: []string{"xmlrpc.php"}},
		PageCache: agentapi.PageCachePHP, Checklist: []string{"Check it."}, Builtin: true,
	}

	got, err := s.CreateOwnProfile(ctx, want, 0, now)
	if err != nil {
		t.Fatal(err)
	}

	want.Builtin = false
	if !reflect.DeepEqual(got.Profile, want) || !got.CreatedAt.Equal(now) || got.CreatedBy != 0 {
		t.Fatalf("create: %+v", got)
	}

	if _, err := s.CreateOwnProfile(ctx, want, 0, now); !errors.Is(err, ErrProfileExists) {
		t.Fatalf("twice: %v", err)
	}

	if _, err := s.OwnProfile(ctx, "nope"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("missing: %v", err)
	}

	changed := want
	changed.Title, changed.WebDir = "Agency 2", "web"
	later := now.Add(time.Hour)

	old, err := s.UpdateOwnProfile(ctx, changed, 0, later)
	if err != nil || old.Profile.Title != "Agency" {
		t.Fatalf("update: %+v %v", old, err)
	}

	if p, err := s.OwnProfile(ctx, "agency"); err != nil || p.Profile.Title != "Agency 2" || !p.UpdatedAt.Equal(later) || !p.CreatedAt.Equal(now) {
		t.Fatalf("after update: %+v %v", p, err)
	}

	if err := s.RestoreOwnProfile(ctx, old); err != nil {
		t.Fatal(err)
	}

	if p, err := s.OwnProfile(ctx, "agency"); err != nil || p.Profile.Title != "Agency" || !p.UpdatedAt.Equal(now) {
		t.Fatalf("restore: %+v %v", p, err)
	}

	if _, err := s.UpdateOwnProfile(ctx, profile.Profile{Name: "nope"}, 0, now); !errors.Is(err, ErrNotFound) {
		t.Fatalf("update missing: %v", err)
	}

	// The table refuses what the panel never writes.
	for _, q := range []string{
		`INSERT INTO profiles (name, data, created_at, updated_at) VALUES ('', '{}', 0, 0)`,
		`INSERT INTO profiles (name, data, created_at, updated_at) VALUES ('` + strings.Repeat("a", 41) + `', '{}', 0, 0)`,
		`INSERT INTO profiles (name, data, created_at, updated_at) VALUES ('x', '[1]', 0, 0)`,
		`INSERT INTO profiles (name, data, created_at, updated_at) VALUES ('x', 'nope', 0, 0)`,
	} {
		if _, err := s.w.ExecContext(ctx, q); err == nil {
			t.Errorf("accepted: %s", q)
		}
	}

	// A site that uses the profile keeps it from being deleted.
	site, err := s.CreateSite(ctx, "blog", "default", "8.4", []string{"blog.example.com"}, now)
	if err != nil {
		t.Fatal(err)
	}

	if _, err := s.SetSiteProfile(ctx, SiteProfile{SiteID: site.ID, Profile: "agency"}, now); err != nil {
		t.Fatal(err)
	}

	if names, err := s.ProfileSites(ctx, "agency"); err != nil || !reflect.DeepEqual(names, []string{"blog"}) {
		t.Fatalf("sites: %v %v", names, err)
	}

	if _, err := s.DeleteOwnProfile(ctx, "agency"); !errors.Is(err, ErrProfileInUse) {
		t.Fatalf("delete in use: %v", err)
	}

	if _, err := s.SetSiteProfile(ctx, SiteProfile{SiteID: site.ID}, now); err != nil {
		t.Fatal(err)
	}

	if old, err := s.DeleteOwnProfile(ctx, "agency"); err != nil || old.Profile.Name != "agency" {
		t.Fatalf("delete: %+v %v", old, err)
	}

	if _, err := s.DeleteOwnProfile(ctx, "agency"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("delete twice: %v", err)
	}
}
