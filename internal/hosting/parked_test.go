// SPDX-License-Identifier: AGPL-3.0-or-later

package hosting

import (
	"errors"
	"testing"
	"time"

	"github.com/maki-it/hostashi/internal/agentrpc"
	"github.com/maki-it/hostashi/internal/statuspage"
)

func TestParkedDomainWithHTTPS(t *testing.T) {
	s, ag, st := newService(t)
	ctx := t.Context()

	if _, err := s.SetParked(ctx, 1, "soon.example.net", "", true); !errors.Is(err, ErrNoTLS) {
		t.Fatalf("https without issuer: %v", err)
	}

	p, err := s.SetParked(ctx, 1, "Soon.Example.NET", "", false)
	if err != nil || p.Domain != "soon.example.net" || p.TLS {
		t.Fatalf("%+v %v", p, err)
	}

	if len(ag.web.Parked) != 1 || ag.web.Parked[0].Page != statuspage.BuiltinParking || ag.web.Parked[0].Name != "_park-1" || ag.web.Parked[0].TLS {
		t.Fatalf("web %+v", ag.web.Parked)
	}

	// The fake certificate counts its days from 1970.
	s.now = func() time.Time { return time.Unix(0, 0).Add(1000 * 24 * time.Hour) }
	s.EnableTLS(&fakeIssuer{validDays: 1090})

	if _, err := s.SetParked(ctx, 1, "soon.example.net", "", true); err != nil {
		t.Fatal(err)
	}

	s.RenewDue(ctx)

	p, err = s.ParkedDomain(ctx, "soon.example.net")
	if err != nil || !s.ParkedTLSActive(p) || !ag.web.Parked[0].TLS {
		t.Fatalf("after renew: %+v %v %+v", p, err, ag.web.Parked)
	}

	// A site can take the domain; that ends the parking.
	if _, err := s.Create(ctx, 1, CreateRequest{Name: "blog", Domains: []string{"soon.example.net"}}); err != nil {
		t.Fatal(err)
	}

	if _, err := s.ParkedDomain(ctx, "soon.example.net"); !errors.Is(err, ErrParkedNotFound) {
		t.Fatalf("still parked: %v", err)
	}

	if len(ag.web.Parked) != 0 {
		t.Fatalf("web still parks: %+v", ag.web.Parked)
	}

	// A site's domain cannot be parked.
	if _, err := s.SetParked(ctx, 1, "soon.example.net", "", false); !errors.Is(err, ErrDomainTaken) {
		t.Fatalf("site domain: %v", err)
	}

	var n int
	if err := st.RawCount(ctx, `SELECT count(*) FROM audit_log WHERE action = ?`, "parked.set", &n); err != nil || n != 2 {
		t.Fatalf("audit %d %v", n, err)
	}
}

func TestParkedDomainPagesAndFailures(t *testing.T) {
	s, ag, _ := newService(t)
	ctx := t.Context()

	maint, err := s.CreatePage(ctx, 1, "Maint", statuspage.KindMaintenance, PageUpload{Filename: "m.html", Data: []byte("m")})
	if err != nil {
		t.Fatal(err)
	}

	park, err := s.CreatePage(ctx, 1, "Coming", statuspage.KindParking, PageUpload{Filename: "p.html", Data: []byte("p")})
	if err != nil {
		t.Fatal(err)
	}

	if _, err := s.SetParked(ctx, 1, "a.example.net", maint.ID, false); !errors.Is(err, ErrInvalid) {
		t.Fatalf("maintenance page: %v", err)
	}

	for _, bad := range []string{"localhost", "a.example.net;", "1.2.3.4"} {
		if _, err := s.SetParked(ctx, 1, bad, "", false); !errors.Is(err, ErrInvalid) {
			t.Errorf("%q: %v", bad, err)
		}
	}

	if _, err := s.SetParked(ctx, 1, "a.example.net", park.ID, false); err != nil {
		t.Fatal(err)
	}

	if ag.web.Parked[0].Page != park.ID {
		t.Fatalf("web %+v", ag.web.Parked)
	}

	if err := s.DeletePage(ctx, 1, park.ID); !errors.Is(err, ErrPageInUse) {
		t.Fatalf("delete page in use: %v", err)
	}

	pages, err := s.Pages(ctx)
	if err != nil || len(pages[2].Parked) != 1 || pages[2].Parked[0] != "a.example.net" {
		t.Fatalf("%+v %v", pages, err)
	}

	// The agent refuses: nothing changes.
	ag.webErr = &agentrpc.Error{Code: agentrpc.CodeInternal, Message: "nginx down"}

	if _, err := s.SetParked(ctx, 1, "b.example.net", "", false); err == nil {
		t.Fatal("park succeeded although the agent refused")
	}

	if _, err := s.ParkedDomain(ctx, "b.example.net"); !errors.Is(err, ErrParkedNotFound) {
		t.Fatalf("failed park stored: %v", err)
	}

	if err := s.DeleteParked(ctx, 1, "a.example.net"); err == nil {
		t.Fatal("delete succeeded although the agent refused")
	}

	if _, err := s.ParkedDomain(ctx, "a.example.net"); err != nil {
		t.Fatalf("failed delete removed the domain: %v", err)
	}

	ag.webErr = nil

	if err := s.DeleteParked(ctx, 1, "a.example.net"); err != nil || len(ag.web.Parked) != 0 {
		t.Fatalf("%v %+v", err, ag.web.Parked)
	}

	if err := s.DeleteParked(ctx, 1, "a.example.net"); !errors.Is(err, ErrParkedNotFound) {
		t.Fatalf("delete twice: %v", err)
	}
}
