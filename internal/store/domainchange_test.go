// SPDX-License-Identifier: AGPL-3.0-or-later

package store

import (
	"errors"
	"fmt"
	"testing"
	"time"
)

func TestRedirectDomains(t *testing.T) {
	s := open(t, dbPath(t))
	ctx := t.Context()
	now := time.Unix(2_000_000, 0)

	if _, err := s.CreateSite(ctx, "blog", "default", "", []string{"new.example.com"}, now); err != nil {
		t.Fatal(err)
	}

	set := DomainSet{Domains: []string{"new.example.com", "www.new.example.com"}, Redirect: []string{"old.example.com"}}

	old, err := s.ReplaceSiteDomainSet(ctx, "blog", set, now)
	if err != nil || fmt.Sprint(old.Domains, old.Redirect) != "[new.example.com] []" {
		t.Fatalf("replace: %+v %v", old, err)
	}

	got, err := s.SiteByName(ctx, "blog")
	if err != nil || fmt.Sprint(got.Domains, got.RedirectDomains) != "[new.example.com www.new.example.com] [old.example.com]" {
		t.Fatalf("site: %+v %v", got, err)
	}

	if fmt.Sprint(got.AllDomains()) != "[new.example.com www.new.example.com old.example.com]" {
		t.Fatalf("all: %v", got.AllDomains())
	}

	all, err := s.Sites(ctx)
	if err != nil || len(all) != 1 || fmt.Sprint(all[0].RedirectDomains) != "[old.example.com]" {
		t.Fatalf("sites: %+v %v", all, err)
	}

	// A redirect domain belongs to the site like a served one.
	if _, err := s.CreateSite(ctx, "shop", "default", "", []string{"old.example.com"}, now); !errors.Is(err, ErrDomainTaken) {
		t.Fatalf("take redirect domain: %v", err)
	}

	if _, _, err := s.PutParked(ctx, "old.example.com", 0, false, now); !errors.Is(err, ErrDomainTaken) {
		t.Fatalf("park redirect domain: %v", err)
	}

	// Replacing only the served domains keeps the redirect domains, except
	// one that is served now.
	if _, err := s.ReplaceSiteDomains(ctx, "blog", []string{"new.example.com"}, now); err != nil {
		t.Fatal(err)
	}

	if got, _ := s.SiteByName(ctx, "blog"); fmt.Sprint(got.RedirectDomains) != "[old.example.com]" {
		t.Fatalf("kept: %v", got.RedirectDomains)
	}

	if _, err := s.ReplaceSiteDomains(ctx, "blog", []string{"old.example.com"}, now); err != nil {
		t.Fatal(err)
	}

	if got, _ := s.SiteByName(ctx, "blog"); fmt.Sprint(got.Domains, got.RedirectDomains) != "[old.example.com] []" {
		t.Fatalf("served again: %v %v", got.Domains, got.RedirectDomains)
	}
}

func TestDomainChange(t *testing.T) {
	s := open(t, dbPath(t))
	ctx := t.Context()
	now := time.Unix(2_000_000, 0)

	for _, c := range []struct{ name, domain string }{{"blog", "old.example.com"}, {"shop", "shop.example.com"}} {
		if _, err := s.CreateSite(ctx, c.name, "default", "", []string{c.domain}, now); err != nil {
			t.Fatal(err)
		}
	}

	if _, err := s.DomainChangeOf(ctx, "blog"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("no change: %v", err)
	}

	if err := s.SetDomainChange(ctx, "nope", DomainSet{Domains: []string{"x.example.com"}}, 0, now); !errors.Is(err, ErrNotFound) {
		t.Fatalf("unknown site: %v", err)
	}

	if err := s.SetDomainChange(ctx, "blog", DomainSet{Domains: []string{"shop.example.com"}}, 0, now); !errors.Is(err, ErrDomainTaken) {
		t.Fatalf("name of another site: %v", err)
	}

	set := DomainSet{Domains: []string{"new.example.com"}, Redirect: []string{"old.example.com"}}
	if err := s.SetDomainChange(ctx, "blog", set, 0, now); err != nil {
		t.Fatal(err)
	}

	c, err := s.DomainChangeOf(ctx, "blog")
	if err != nil || fmt.Sprint(c.Domains, c.Redirect) != "[new.example.com] [old.example.com]" || !c.RequestedAt.Equal(now) || c.Error != "" {
		t.Fatalf("change: %+v %v", c, err)
	}

	// The pending names are reserved.
	if _, err := s.ReplaceSiteDomains(ctx, "shop", []string{"new.example.com"}, now); !errors.Is(err, ErrDomainPending) {
		t.Fatalf("take pending name: %v", err)
	}

	if err := s.SetDomainChange(ctx, "shop", DomainSet{Domains: []string{"new.example.com"}}, 0, now); !errors.Is(err, ErrDomainPending) {
		t.Fatalf("second change: %v", err)
	}

	if _, _, err := s.PutParked(ctx, "new.example.com", 0, false, now); !errors.Is(err, ErrDomainPending) {
		t.Fatalf("park pending name: %v", err)
	}

	if p, err := s.DomainPending(ctx, "new.example.com"); err != nil || !p {
		t.Fatalf("pending: %v %v", p, err)
	}

	if err := s.SetDomainChangeError(ctx, "blog", "dns", now.Add(time.Minute)); err != nil {
		t.Fatal(err)
	}

	list, err := s.DomainChanges(ctx)
	if err != nil || len(list) != 1 || list[0].Error != "dns" || !list[0].FailedAt.Equal(now.Add(time.Minute)) {
		t.Fatalf("list: %+v %v", list, err)
	}

	// Finishing a change that asks for something else fails.
	if _, err := s.FinishDomainChange(ctx, "blog", DomainSet{Domains: []string{"other.example.com"}}, nil, now, now); !errors.Is(err, ErrNotFound) {
		t.Fatalf("finish other set: %v", err)
	}

	notAfter := now.Add(90 * 24 * time.Hour)

	old, err := s.FinishDomainChange(ctx, "blog", set, set.All(), notAfter, now)
	if err != nil || fmt.Sprint(old.Domains) != "[old.example.com]" {
		t.Fatalf("finish: %+v %v", old, err)
	}

	got, err := s.SiteByName(ctx, "blog")
	if err != nil || fmt.Sprint(got.Domains, got.RedirectDomains, got.TLSDomains) != "[new.example.com] [old.example.com] [new.example.com old.example.com]" ||
		!got.TLSNotAfter.Equal(notAfter) || got.TLSCertSource != TLSLetsEncrypt {
		t.Fatalf("site: %+v %v", got, err)
	}

	if _, err := s.DomainChangeOf(ctx, "blog"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("change left: %v", err)
	}

	// Cancel, and a deleted site takes its change along.
	if err := s.SetDomainChange(ctx, "shop", DomainSet{Domains: []string{"shop.example.org"}}, 0, now); err != nil {
		t.Fatal(err)
	}

	if ok, err := s.DeleteDomainChange(ctx, "shop"); err != nil || !ok {
		t.Fatalf("cancel: %v %v", ok, err)
	}

	if ok, err := s.DeleteDomainChange(ctx, "shop"); err != nil || ok {
		t.Fatalf("cancel twice: %v %v", ok, err)
	}

	if err := s.SetDomainChange(ctx, "shop", DomainSet{Domains: []string{"shop.example.org"}}, 0, now); err != nil {
		t.Fatal(err)
	}

	if err := s.DeleteSite(ctx, "shop"); err != nil {
		t.Fatal(err)
	}

	if p, err := s.DomainPending(ctx, "shop.example.org"); err != nil || p {
		t.Fatalf("pending after delete: %v %v", p, err)
	}
}

func TestRedirectDomainNeverPrimary(t *testing.T) {
	s := open(t, dbPath(t))
	ctx := t.Context()
	now := time.Unix(2_000_000, 0)

	if _, err := s.CreateSite(ctx, "blog", "default", "", []string{"a.example.com"}, now); err != nil {
		t.Fatal(err)
	}

	if _, err := s.ReplaceSiteDomainSet(ctx, "blog", DomainSet{Redirect: []string{"a.example.com"}}, now); err == nil {
		t.Fatal("redirect domain stored at position 0")
	}

	if got, _ := s.SiteByName(ctx, "blog"); fmt.Sprint(got.Domains) != "[a.example.com]" {
		t.Fatalf("domains after refused change: %v", got.Domains)
	}
}
