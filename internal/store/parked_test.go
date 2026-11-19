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
