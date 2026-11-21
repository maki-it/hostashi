// SPDX-License-Identifier: AGPL-3.0-or-later

package store

import (
	"errors"
	"slices"
	"strings"
	"testing"
	"time"
)

func TestParkedDomains(t *testing.T) {
	s := open(t, dbPath(t))
	ctx := t.Context()
	now := time.Unix(2_000_000, 0)

	if _, err := s.CreateSite(ctx, "blog", "default", "", []string{"blog.example.com"}, now); err != nil {
		t.Fatal(err)
	}

	if _, _, err := s.PutParked(ctx, "blog.example.com", 0, true, now); !errors.Is(err, ErrDomainTaken) {
		t.Fatalf("site domain: %v", err)
	}

	if _, ok, err := s.PutParked(ctx, "soon.example.net", 0, true, now); err != nil || ok {
		t.Fatalf("park: %v %v", ok, err)
	}

	page, err := s.CreateStatusPage(ctx, "Coming", "parking", strings.Repeat("a", 64), map[string][]byte{"index.html": []byte("x")}, now)
	if err != nil {
		t.Fatal(err)
	}

	old, ok, err := s.PutParked(ctx, "soon.example.net", page, false, now)
	if err != nil || !ok || old.Page != 0 || !old.TLS {
		t.Fatalf("change: %+v %v %v", old, ok, err)
	}

	if _, _, err := s.PutParked(ctx, "other.example.net", page+10, true, now); !errors.Is(err, ErrNotFound) {
		t.Fatalf("unknown page: %v", err)
	}

	if err := s.DeleteStatusPage(ctx, page); !errors.Is(err, ErrPageInUse) {
		t.Fatalf("page in use: %v", err)
	}

	if p, _ := s.StatusPageByID(ctx, page); !slices.Equal(p.Parked, []string{"soon.example.net"}) {
		t.Fatalf("parked %v", p.Parked)
	}

	if err := s.RestoreParked(ctx, "soon.example.net", old, true, now); err != nil {
		t.Fatal(err)
	}

	p, err := s.ParkedDomain(ctx, "soon.example.net")
	if err != nil || p.Page != 0 || !p.TLS {
		t.Fatalf("restored %+v %v", p, err)
	}

	if err := s.SetParkedCert(ctx, p.ID, now.Add(90*24*time.Hour), now); err != nil {
		t.Fatal(err)
	}

	if err := s.SetParkedTLSLive(ctx, []int64{p.ID}); err != nil {
		t.Fatal(err)
	}

	if p, _ := s.ParkedDomain(ctx, "soon.example.net"); !p.TLSLive || p.TLSNotAfter.IsZero() {
		t.Fatalf("tls %+v", p)
	}

	// A site that takes the domain ends the parking.
	if _, err := s.ReplaceSiteDomains(ctx, "blog", []string{"blog.example.com", "soon.example.net"}, now); err != nil {
		t.Fatal(err)
	}

	if _, err := s.ParkedDomain(ctx, "soon.example.net"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("still parked: %v", err)
	}

	if err := s.DeleteParked(ctx, "soon.example.net"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("delete missing: %v", err)
	}

	all, err := s.ParkedDomains(ctx)
	if err != nil || len(all) != 0 {
		t.Fatalf("%v %v", all, err)
	}
}

func TestParkedCertificateSource(t *testing.T) {
	s := open(t, dbPath(t))
	ctx := t.Context()
	now := time.Unix(2_000_000, 0)

	if _, _, err := s.PutParked(ctx, "soon.example.net", 0, false, now); err != nil {
		t.Fatal(err)
	}

	p, _ := s.ParkedDomain(ctx, "soon.example.net")
	if p.TLSSource != TLSLetsEncrypt || p.TLSCertSource != "" || !p.TLSTrusted {
		t.Fatalf("defaults %+v", p)
	}

	if err := s.SetParkedCustomCert(ctx, p.ID, now.Add(time.Hour), false, now); err != nil {
		t.Fatal(err)
	}

	p, _ = s.ParkedDomain(ctx, "soon.example.net")
	if !p.TLS || p.TLSSource != TLSCustom || p.TLSCertSource != TLSCustom || p.TLSTrusted {
		t.Fatalf("custom %+v", p)
	}

	// Changing the page keeps the certificate source.
	if _, _, err := s.PutParked(ctx, "soon.example.net", 0, true, now); err != nil {
		t.Fatal(err)
	}

	if p, _ = s.ParkedDomain(ctx, "soon.example.net"); p.TLSSource != TLSCustom {
		t.Fatalf("after put %+v", p)
	}

	if err := s.SetParkedTLSSource(ctx, p.ID, TLSLetsEncrypt, now); err != nil {
		t.Fatal(err)
	}

	if err := s.SetParkedTLSSource(ctx, p.ID, "x", now); err == nil {
		t.Fatal("bad source accepted")
	}

	if err := s.RequestParkedRenew(ctx, p.ID, 10*time.Minute, now); err != nil {
		t.Fatal(err)
	}

	if err := s.RequestParkedRenew(ctx, p.ID, 10*time.Minute, now.Add(time.Minute)); !errors.Is(err, ErrTooSoon) {
		t.Fatalf("again: %v", err)
	}

	if err := s.RequestParkedRenew(ctx, 999, 10*time.Minute, now); !errors.Is(err, ErrNotFound) {
		t.Fatalf("unknown: %v", err)
	}

	if err := s.SetParkedCert(ctx, p.ID, now.Add(90*24*time.Hour), now); err != nil {
		t.Fatal(err)
	}

	p, _ = s.ParkedDomain(ctx, "soon.example.net")
	if p.TLSCertSource != TLSLetsEncrypt || p.TLSRenewForced || !p.TLSTrusted {
		t.Fatalf("after LE %+v", p)
	}

	if err := s.ClearParkedCert(ctx, p.ID, now); err != nil {
		t.Fatal(err)
	}

	if p, _ = s.ParkedDomain(ctx, "soon.example.net"); p.TLSCertSource != "" || !p.TLSNotAfter.IsZero() {
		t.Fatalf("cleared %+v", p)
	}

	if err := s.ClearParkedCert(ctx, 999, now); !errors.Is(err, ErrNotFound) {
		t.Fatalf("clear unknown: %v", err)
	}
}

func TestReinsertParked(t *testing.T) {
	s := open(t, dbPath(t))
	ctx := t.Context()
	now := time.Unix(2_000_000, 0)

	if _, _, err := s.PutParked(ctx, "soon.example.net", 0, true, now); err != nil {
		t.Fatal(err)
	}

	if err := s.SetParkedCert(ctx, 1, now.Add(90*24*time.Hour), now); err != nil {
		t.Fatal(err)
	}

	before, err := s.ParkedDomain(ctx, "soon.example.net")
	if err != nil {
		t.Fatal(err)
	}

	// A site takes the domain over, which ends the parking.
	if _, err := s.CreateSite(ctx, "blog", "default", "", []string{"soon.example.net"}, now); err != nil {
		t.Fatal(err)
	}

	if _, err := s.ParkedDomain(ctx, "soon.example.net"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("still parked: %v", err)
	}

	// While the site owns it, it cannot be parked again.
	if err := s.ReinsertParked(ctx, []Parked{before}); !errors.Is(err, ErrDomainTaken) {
		t.Fatalf("taken: %v", err)
	}

	if err := s.DeleteSite(ctx, "blog"); err != nil {
		t.Fatal(err)
	}

	if err := s.ReinsertParked(ctx, nil); err != nil {
		t.Fatal(err)
	}

	if err := s.ReinsertParked(ctx, []Parked{before}); err != nil {
		t.Fatal(err)
	}

	after, err := s.ParkedDomain(ctx, "soon.example.net")
	if err != nil || after != before {
		t.Fatalf("after %+v\nbefore %+v\n%v", after, before, err)
	}

	// Twice is a conflict, and nothing of the batch is stored.
	if err := s.ReinsertParked(ctx, []Parked{{ID: 7, Domain: "new.example.net", TLSSource: TLSLetsEncrypt, CreatedAt: now, UpdatedAt: now}, before}); err == nil {
		t.Fatal("duplicate accepted")
	}

	if _, err := s.ParkedDomain(ctx, "new.example.net"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("partial batch stored: %v", err)
	}
}

func TestParkSiteDomains(t *testing.T) {
	s := open(t, dbPath(t))
	ctx := t.Context()
	now := time.Unix(2_000_000, 0)

	if _, err := s.CreateSite(ctx, "blog", "default", "", []string{"blog.example.com", "www.blog.example.com"}, now); err != nil {
		t.Fatal(err)
	}

	if _, err := s.CreateSite(ctx, "shop", "default", "", []string{"shop.example.com"}, now); err != nil {
		t.Fatal(err)
	}

	if err := s.ParkSiteDomains(ctx, "nope", []string{"blog.example.com"}, 0, false, now); !errors.Is(err, ErrNotFound) {
		t.Fatalf("unknown site: %v", err)
	}

	for _, d := range []string{"shop.example.com", "free.example.com"} {
		if err := s.ParkSiteDomains(ctx, "blog", []string{"blog.example.com", d}, 0, false, now); !errors.Is(err, ErrDomainTaken) {
			t.Fatalf("%s: %v", d, err)
		}
	}

	if err := s.ParkSiteDomains(ctx, "blog", []string{"blog.example.com"}, 99, false, now); !errors.Is(err, ErrNotFound) {
		t.Fatalf("unknown page: %v", err)
	}

	// Refused calls left nothing behind.
	if p, _ := s.ParkedDomains(ctx); len(p) != 0 {
		t.Fatalf("parked %+v", p)
	}

	if err := s.ParkSiteDomains(ctx, "blog", []string{"blog.example.com"}, 0, true, now); err != nil {
		t.Fatal(err)
	}

	site, err := s.SiteByName(ctx, "blog")
	if err != nil || site.State != SiteDeleting {
		t.Fatalf("%+v %v", site, err)
	}

	if p, err := s.ParkedDomain(ctx, "blog.example.com"); err != nil || !p.TLS {
		t.Fatalf("%+v %v", p, err)
	}

	if err := s.UnparkSiteDomains(ctx, "blog", []string{"blog.example.com"}, now); err != nil {
		t.Fatal(err)
	}

	if site, _ := s.SiteByName(ctx, "blog"); site.State != SiteActive {
		t.Fatalf("state %s", site.State)
	}

	if _, err := s.ParkedDomain(ctx, "blog.example.com"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("still parked: %v", err)
	}

	// After the site row is gone, the parked domain stays.
	if err := s.ParkSiteDomains(ctx, "blog", []string{"www.blog.example.com"}, 0, false, now); err != nil {
		t.Fatal(err)
	}

	if err := s.DeleteSite(ctx, "blog"); err != nil {
		t.Fatal(err)
	}

	if _, err := s.ParkedDomain(ctx, "www.blog.example.com"); err != nil {
		t.Fatal(err)
	}
}
