// SPDX-License-Identifier: AGPL-3.0-or-later

package store

import (
	"testing"
	"time"
)

func TestSiteSnippets(t *testing.T) {
	s := open(t, dbPath(t))
	ctx := t.Context()
	now := time.Unix(2_000_000, 0)

	site, err := s.CreateSite(ctx, "blog", "default", "", []string{"blog.example.com"}, now)
	if err != nil {
		t.Fatal(err)
	}

	if sn, err := s.SiteSnippets(ctx, site.ID); err != nil || !sn.Empty() || sn.SiteID != site.ID {
		t.Fatalf("none: %+v %v", sn, err)
	}

	old, err := s.SetSiteSnippets(ctx, SiteSnippets{SiteID: site.ID, Server: "add_header X a;"}, now)
	if err != nil || !old.Empty() {
		t.Fatalf("set: %+v %v", old, err)
	}

	old, err = s.SetSiteSnippets(ctx, SiteSnippets{SiteID: site.ID, Root: "expires 1h;"}, now.Add(time.Minute))
	if err != nil || old.Server != "add_header X a;" {
		t.Fatalf("replace: %+v %v", old, err)
	}

	got, err := s.SiteSnippets(ctx, site.ID)
	if err != nil || got.Server != "" || got.Root != "expires 1h;" || !got.UpdatedAt.Equal(now.Add(time.Minute)) || got.UpdatedBy != 0 {
		t.Fatalf("get: %+v %v", got, err)
	}

	all, err := s.AllSiteSnippets(ctx)
	if err != nil || len(all) != 1 || all[site.ID].Root != "expires 1h;" {
		t.Fatalf("all: %+v %v", all, err)
	}

	if _, err := s.SetSiteSnippets(ctx, SiteSnippets{SiteID: site.ID}, now); err != nil {
		t.Fatal(err)
	}

	if all, _ := s.AllSiteSnippets(ctx); len(all) != 0 {
		t.Fatalf("empty snippets kept a row: %+v", all)
	}

	// Deleting the site removes its snippets.
	if _, err := s.SetSiteSnippets(ctx, SiteSnippets{SiteID: site.ID, Server: "x"}, now); err != nil {
		t.Fatal(err)
	}

	if err := s.DeleteSite(ctx, site.Name); err != nil {
		t.Fatal(err)
	}

	if all, _ := s.AllSiteSnippets(ctx); len(all) != 0 {
		t.Fatalf("snippets outlived the site: %+v", all)
	}
}
