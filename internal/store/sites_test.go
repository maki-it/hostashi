// SPDX-License-Identifier: AGPL-3.0-or-later

package store

import (
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"
)

func TestSiteStoreRoundTrip(t *testing.T) {
	s := open(t, dbPath(t))
	ctx := t.Context()
	now := time.Unix(2_000_000, 0)

	site, err := s.CreateSite(ctx, "blog", "default", "8.4", []string{"blog.example.com", "www.blog.example.com"}, now)
	if err != nil || site.ID == 0 || site.State != SiteCreating {
		t.Fatalf("%+v %v", site, err)
	}

	if _, err := s.CreateSite(ctx, "blog", "default", "", []string{"other.example.com"}, now); !errors.Is(err, ErrSiteExists) {
		t.Fatalf("duplicate name: %v", err)
	}

	if _, err := s.CreateSite(ctx, "shop", "default", "", []string{"www.blog.example.com"}, now); !errors.Is(err, ErrDomainTaken) {
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
	if err != nil || got.State != SiteActive || got.PHP != "8.4" || fmt.Sprint(got.Domains) != "[www.blog.example.com blog.example.org]" {
		t.Fatalf("got %+v %v", got, err)
	}

	if oldPHP, err := s.SetSitePHP(ctx, "blog", "8.3", now); err != nil || oldPHP != "8.4" {
		t.Fatalf("set php: %q %v", oldPHP, err)
	}

	if _, err := s.SetSitePHP(ctx, "blog", "8.4; x", now); err == nil {
		t.Fatal("bad php version stored")
	}

	if _, err := s.SetSitePHP(ctx, "nope", "8.4", now); !errors.Is(err, ErrNotFound) {
		t.Fatalf("unknown site: %v", err)
	}

	if _, err := s.CreateSite(ctx, "shop", "default", "", []string{"shop.example.com"}, now); err != nil {
		t.Fatal(err)
	}

	if _, err := s.ReplaceSiteDomains(ctx, "shop", []string{"blog.example.org"}, now); !errors.Is(err, ErrDomainTaken) {
		t.Fatalf("steal domain: %v", err)
	}

	all, err := s.Sites(ctx)
	if err != nil || len(all) != 2 || all[0].Name != "blog" || all[0].PHP != "8.3" || all[1].PHP != "" || fmt.Sprint(all[1].Domains) != "[shop.example.com]" {
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

func TestSiteTLSColumns(t *testing.T) {
	s := open(t, dbPath(t))
	ctx := t.Context()
	now := time.Unix(2_000_000, 0)

	if _, err := s.CreateSite(ctx, "blog", "default", "", []string{"blog.example.com"}, now); err != nil {
		t.Fatal(err)
	}

	if old, err := s.SetSiteTLS(ctx, "blog", true, now); err != nil || old {
		t.Fatalf("set tls: %v %v", old, err)
	}

	if err := s.SetSiteTLSError(ctx, "blog", "DNS problem: NXDOMAIN", now.Add(time.Minute)); err != nil {
		t.Fatal(err)
	}

	got, err := s.SiteByName(ctx, "blog")
	if err != nil || !got.TLS || got.TLSError != "DNS problem: NXDOMAIN" || !got.TLSFailedAt.Equal(now.Add(time.Minute)) || !got.TLSNotAfter.IsZero() {
		t.Fatalf("%+v %v", got, err)
	}

	notAfter := now.Add(90 * 24 * time.Hour)
	if err := s.SetSiteCert(ctx, "blog", []string{"blog.example.com", "www.blog.example.com"}, notAfter, now); err != nil {
		t.Fatal(err)
	}

	all, err := s.Sites(ctx)
	if err != nil || len(all) != 1 || fmt.Sprint(all[0].TLSDomains) != "[blog.example.com www.blog.example.com]" ||
		!all[0].TLSNotAfter.Equal(notAfter) || all[0].TLSError != "" || !all[0].TLSFailedAt.IsZero() {
		t.Fatalf("%+v %v", all, err)
	}

	if err := s.SetSiteTLSError(ctx, "blog", strings.Repeat("ä", 800), now); err != nil {
		t.Fatalf("long error: %v", err)
	}

	if old, err := s.SetSiteTLS(ctx, "blog", false, now); err != nil || !old {
		t.Fatalf("tls off: %v %v", old, err)
	}

	if err := s.SetSiteCert(ctx, "nope", nil, now, now); !errors.Is(err, ErrNotFound) {
		t.Fatalf("unknown: %v", err)
	}
}
