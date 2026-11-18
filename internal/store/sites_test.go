// SPDX-License-Identifier: AGPL-3.0-or-later

package store

import (
	"errors"
	"fmt"
	"testing"
	"time"
)

func TestSiteStoreRoundTrip(t *testing.T) {
	s := open(t, dbPath(t))
	ctx := t.Context()
	now := time.Unix(2_000_000, 0)

	site, err := s.CreateSite(ctx, "blog", "default", []string{"blog.example.com", "www.blog.example.com"}, now)
	if err != nil || site.ID == 0 || site.State != SiteCreating {
		t.Fatalf("%+v %v", site, err)
	}

	if _, err := s.CreateSite(ctx, "blog", "default", []string{"other.example.com"}, now); !errors.Is(err, ErrSiteExists) {
		t.Fatalf("duplicate name: %v", err)
	}

	if _, err := s.CreateSite(ctx, "shop", "default", []string{"www.blog.example.com"}, now); !errors.Is(err, ErrDomainTaken) {
		t.Fatalf("taken domain: %v", err)
	}

	// The failed create left nothing behind.
	if _, err := s.SiteByName(ctx, "shop"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("shop: %v", err)
	}

	if err := s.SetSiteState(ctx, "blog", SiteActive, now.Add(time.Minute)); err != nil {
		t.Fatal(err)
	}

	old, err := s.ReplaceSiteDomains(ctx, "blog", []string{"www.blog.example.com", "blog.example.org"}, now)
	if err != nil || fmt.Sprint(old) != "[blog.example.com www.blog.example.com]" {
		t.Fatalf("replace: %v %v", old, err)
	}

	got, err := s.SiteByName(ctx, "blog")
	if err != nil || got.State != SiteActive || fmt.Sprint(got.Domains) != "[www.blog.example.com blog.example.org]" {
		t.Fatalf("got %+v %v", got, err)
	}

	if _, err := s.CreateSite(ctx, "shop", "default", []string{"shop.example.com"}, now); err != nil {
		t.Fatal(err)
	}

	if _, err := s.ReplaceSiteDomains(ctx, "shop", []string{"blog.example.org"}, now); !errors.Is(err, ErrDomainTaken) {
		t.Fatalf("steal domain: %v", err)
	}

	all, err := s.Sites(ctx)
	if err != nil || len(all) != 2 || all[0].Name != "blog" || fmt.Sprint(all[1].Domains) != "[shop.example.com]" {
		t.Fatalf("sites %+v %v", all, err)
	}

	if err := s.DeleteSite(ctx, "blog"); err != nil {
		t.Fatal(err)
	}

	// The domains went with the site and are free again.
	if _, err := s.ReplaceSiteDomains(ctx, "shop", []string{"blog.example.org"}, now); err != nil {
		t.Fatal(err)
	}

	if err := s.SetSiteState(ctx, "blog", SiteActive, now); !errors.Is(err, ErrNotFound) {
		t.Fatalf("state of deleted site: %v", err)
	}
}
