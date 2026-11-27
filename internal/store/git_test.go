// SPDX-License-Identifier: AGPL-3.0-or-later

package store

import (
	"errors"
	"reflect"
	"testing"
	"time"
)

func TestSiteGit(t *testing.T) {
	s := open(t, dbPath(t))
	ctx := t.Context()
	now := time.Unix(2_000_000, 0).UTC()

	site, err := s.CreateSite(ctx, "blog", "default", "8.4", []string{"blog.example.com"}, now)
	if err != nil {
		t.Fatal(err)
	}

	if _, err := s.SiteGit(ctx, site.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("none: %v", err)
	}

	g := SiteGit{
		SiteID: site.ID, URL: "git@github.com:acme/blog.git", Branch: "main", DeployDir: "htdocs/blog.example.com",
		Keep: 5, BuildTimeout: 15 * time.Minute, Auth: "ssh",
		SSHKey: []byte{1, 2, 3}, SSHPublic: "ssh-ed25519 AAAA hostashi-blog",
	}

	if err := s.SetSiteGit(ctx, g, now); err != nil {
		t.Fatal(err)
	}

	got, err := s.SiteGit(ctx, site.ID)
	if err != nil {
		t.Fatal(err)
	}

	want := g
	want.Shared, want.Commands, want.UpdatedAt = []string{}, []string{}, now

	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %+v\nwant %+v", got, want)
	}

	// Replacing keeps one row and stores every field.
	g.Auth, g.SSHKey, g.SSHPublic = "token", nil, ""
	g.Token, g.TokenUser = []byte{9}, "x-access-token"
	g.Shared, g.Commands = []string{".env", "wp-content/uploads"}, []string{"composer install --no-dev"}
	g.Submodules, g.Keep, g.BuildTimeout = true, 3, time.Hour
	g.HostKey = "ssh-ed25519 AAAA"
	g.WebhookID, g.WebhookSecret = "0123456789abcdef0123456789abcdef", []byte{4}

	if err := s.SetSiteGit(ctx, g, now.Add(time.Minute)); err != nil {
		t.Fatal(err)
	}

	got, _ = s.SiteGit(ctx, site.ID)
	want = g
	want.UpdatedAt = now.Add(time.Minute)

	if !reflect.DeepEqual(got, want) {
		t.Fatalf("replaced: got %+v\nwant %+v", got, want)
	}

	if byHook, name, err := s.SiteGitByWebhook(ctx, g.WebhookID); err != nil || byHook.SiteID != site.ID || name != site.Name {
		t.Fatalf("by webhook %+v %q %v", byHook, name, err)
	}

	for _, id := range []string{"", "fedcba9876543210fedcba9876543210"} {
		if _, _, err := s.SiteGitByWebhook(ctx, id); !errors.Is(err, ErrNotFound) {
			t.Fatalf("webhook %q: %v", id, err)
		}
	}

	// The schema refuses what the panel never stores.
	for name, bad := range map[string]func(*SiteGit){
		"token without token": func(b *SiteGit) { b.Token = nil },
		"unknown auth":        func(b *SiteGit) { b.Auth = "password" },
		"keep 1":              func(b *SiteGit) { b.Keep = 1 },
		"timeout 0":           func(b *SiteGit) { b.BuildTimeout = 0 },
		"hook without secret": func(b *SiteGit) { b.WebhookSecret = nil },
		"short hook id":       func(b *SiteGit) { b.WebhookID = "abc" },
		"empty url":           func(b *SiteGit) { b.URL = "" },
	} {
		b := g
		bad(&b)

		if err := s.SetSiteGit(ctx, b, now); err == nil {
			t.Errorf("%s stored", name)
		}
	}

	if got, _ := s.SiteGit(ctx, site.ID); !reflect.DeepEqual(got, want) {
		t.Fatalf("after refused writes: %+v", got)
	}

	if err := s.DeleteSiteGit(ctx, site.ID); err != nil {
		t.Fatal(err)
	}

	if _, err := s.SiteGit(ctx, site.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("deleted: %v", err)
	}

	if err := s.DeleteSiteGit(ctx, site.ID); err != nil {
		t.Fatalf("delete twice: %v", err)
	}

	// Deleting the site deletes its setting.
	if err := s.SetSiteGit(ctx, g, now); err != nil {
		t.Fatal(err)
	}

	if _, err := s.w.ExecContext(ctx, `DELETE FROM sites WHERE id = ?`, site.ID); err != nil {
		t.Fatal(err)
	}

	var n int
	if err := s.db.QueryRowContext(ctx, `SELECT count(*) FROM site_git`).Scan(&n); err != nil || n != 0 {
		t.Fatalf("rows after site delete: %d %v", n, err)
	}
}
