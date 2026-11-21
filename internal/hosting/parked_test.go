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

// A site that does not come up gives its parked domains back, with their
// ids and certificates (ADR-0048).
func TestFailedSiteChangesParkAgain(t *testing.T) {
	s, ag, _ := newService(t)
	ctx := t.Context()

	s.now = func() time.Time { return time.Unix(0, 0).Add(1000 * 24 * time.Hour) }
	s.EnableTLS(&fakeIssuer{validDays: 1090})

	if _, err := s.SetParked(ctx, 1, "soon.example.net", "", true); err != nil {
		t.Fatal(err)
	}

	s.RenewDue(ctx)

	before, err := s.ParkedDomain(ctx, "soon.example.net")
	if err != nil || before.TLSNotAfter.IsZero() {
		t.Fatalf("%+v %v", before, err)
	}

	same := func(step string) {
		t.Helper()

		p, err := s.ParkedDomain(ctx, "soon.example.net")
		if err != nil || p.ID != before.ID || !p.TLSNotAfter.Equal(before.TLSNotAfter) || p.TLS != before.TLS {
			t.Fatalf("%s: %+v %v", step, p, err)
		}
	}

	// The agent cannot create the account.
	ag.createErr = errDown
	if _, err := s.Create(ctx, 1, CreateRequest{Name: "blog", Domains: []string{"soon.example.net", "www.soon.example.net"}}); !errors.Is(err, errDown) {
		t.Fatalf("create: %v", err)
	}

	same("account failed")

	// nginx refuses the new state.
	ag.createErr = nil
	ag.webErr = &agentrpc.Error{Code: agentrpc.CodePrecondition, Message: "nginx rejected the configuration"}

	if _, err := s.Create(ctx, 1, CreateRequest{Name: "blog", Domains: []string{"soon.example.net"}}); !isCode(err, agentrpc.CodePrecondition) {
		t.Fatalf("create: %v", err)
	}

	same("web failed")

	// A domain change of another site that takes the domain fails too.
	ag.webErr = nil

	if _, err := s.Create(ctx, 1, CreateRequest{Name: "shop", Domains: []string{"shop.example.net"}}); err != nil {
		t.Fatal(err)
	}

	ag.webErr = errDown

	if _, err := s.SetDomains(ctx, 1, "shop", []string{"shop.example.net", "soon.example.net"}); !errors.Is(err, errDown) {
		t.Fatalf("domains: %v", err)
	}

	same("domain change failed")

	// When the change goes through, the parking ends.
	ag.webErr = nil

	if _, err := s.SetDomains(ctx, 1, "shop", []string{"shop.example.net", "soon.example.net"}); err != nil {
		t.Fatal(err)
	}

	if _, err := s.ParkedDomain(ctx, "soon.example.net"); !errors.Is(err, ErrParkedNotFound) {
		t.Fatalf("still parked: %v", err)
	}
}

func TestDeleteAndPark(t *testing.T) {
	s, ag, st := newService(t)
	ctx := t.Context()

	if _, err := s.Create(ctx, 1, CreateRequest{Name: "blog", Domains: []string{"blog.example.com", "www.blog.example.com", "old.example.org"}}); err != nil {
		t.Fatal(err)
	}

	park := func(req ParkRequest) error { return s.DeleteAndPark(ctx, 1, "blog", req) }
	keep := ParkRequest{Domains: []string{"www.blog.example.com", "Blog.Example.com"}, Confirm: "blog"}

	for name, tc := range map[string]struct {
		req  ParkRequest
		want error
	}{
		"wrong name":        {ParkRequest{Domains: keep.Domains, Confirm: "Blog"}, ErrInvalid},
		"no domain":         {ParkRequest{Confirm: "blog"}, ErrInvalid},
		"foreign domain":    {ParkRequest{Domains: []string{"shop.example.com"}, Confirm: "blog"}, ErrInvalid},
		"twice":             {ParkRequest{Domains: []string{"blog.example.com", "BLOG.example.com"}, Confirm: "blog"}, ErrInvalid},
		"https, no issuer":  {ParkRequest{Domains: keep.Domains, Confirm: "blog", TLS: true}, ErrNoTLS},
		"unknown page":      {ParkRequest{Domains: keep.Domains, Confirm: "blog", Page: "page-99"}, ErrPageNotFound},
		"page id no number": {ParkRequest{Domains: keep.Domains, Confirm: "blog", Page: "x"}, ErrPageNotFound},
	} {
		if err := park(tc.req); !errors.Is(err, tc.want) {
			t.Errorf("%s: %v, want %v", name, err, tc.want)
		}
	}

	if err := s.DeleteAndPark(ctx, 1, "nope", ParkRequest{Domains: []string{"a.example"}, Confirm: "nope"}); !errors.Is(err, ErrNotFound) {
		t.Fatalf("unknown site: %v", err)
	}

	// nginx refuses: the site stays as it was, nothing is parked.
	ag.webErr = errDown

	if err := park(keep); !errors.Is(err, errDown) {
		t.Fatalf("web failed: %v", err)
	}

	if site, err := s.Get(ctx, "blog"); err != nil || site.State != "active" || !ag.accounts["blog"] {
		t.Fatalf("after failed web state: %+v %v", site, err)
	}

	if p, _ := s.ParkedDomains(ctx); len(p) != 0 {
		t.Fatalf("parked after failure: %+v", p)
	}

	// Removing the account fails: the domains stay parked, the site waits
	// in state deleting, and a plain delete finishes the job.
	ag.webErr = nil
	ag.deleteErr = errDown

	if err := park(keep); !errors.Is(err, errDown) {
		t.Fatalf("account delete failed: %v", err)
	}

	if site, err := s.Get(ctx, "blog"); err != nil || site.State != "deleting" {
		t.Fatalf("after failed account delete: %+v %v", site, err)
	}

	if len(ag.web.Sites) != 0 || len(ag.web.Parked) != 2 || ag.web.Parked[0].Domain != "blog.example.com" {
		t.Fatalf("web %+v", ag.web)
	}

	if err := park(keep); !errors.Is(err, ErrState) {
		t.Fatalf("twice: %v", err)
	}

	ag.deleteErr = nil

	if err := s.Delete(ctx, 1, "blog"); err != nil {
		t.Fatal(err)
	}

	if _, err := s.Get(ctx, "blog"); !errors.Is(err, ErrNotFound) || ag.accounts["blog"] {
		t.Fatalf("site stayed: %v", err)
	}

	parked, err := s.ParkedDomains(ctx)
	if err != nil || len(parked) != 2 || parked[0].Domain != "blog.example.com" || parked[1].Domain != "www.blog.example.com" || parked[0].TLS {
		t.Fatalf("parked %+v %v", parked, err)
	}

	// The domain left out is free for a new site.
	if _, err := s.Create(ctx, 1, CreateRequest{Name: "old", Domains: []string{"old.example.org"}}); err != nil {
		t.Fatal(err)
	}

	var n int
	if err := st.RawCount(ctx, `SELECT count(*) FROM audit_log WHERE action = ?`, "site.park", &n); err != nil || n != 1 {
		t.Fatalf("audit %d %v", n, err)
	}
}

func TestDeleteAndParkWithHTTPSAndPage(t *testing.T) {
	s, ag, _ := newService(t)
	ctx := t.Context()

	s.EnableTLS(&fakeIssuer{validDays: 90})

	if _, err := s.Create(ctx, 1, CreateRequest{Name: "blog", Domains: []string{"blog.example.com"}, TLS: true}); err != nil {
		t.Fatal(err)
	}

	page, err := s.CreatePage(ctx, 1, "Soon", statuspage.KindParking, PageUpload{Filename: "p.html", Data: []byte("x")})
	if err != nil {
		t.Fatal(err)
	}

	maint, err := s.CreatePage(ctx, 1, "Work", statuspage.KindMaintenance, PageUpload{Filename: "m.html", Data: []byte("x")})
	if err != nil {
		t.Fatal(err)
	}

	req := ParkRequest{Domains: []string{"blog.example.com"}, Confirm: "blog", TLS: true, Page: maint.ID}
	if err := s.DeleteAndPark(ctx, 1, "blog", req); !errors.Is(err, ErrInvalid) {
		t.Fatalf("maintenance page: %v", err)
	}

	req.Page = page.ID
	if err := s.DeleteAndPark(ctx, 1, "blog", req); err != nil {
		t.Fatal(err)
	}

	p, err := s.ParkedDomain(ctx, "blog.example.com")
	if err != nil || !p.TLS || pageAgentID(p.Page) != page.ID {
		t.Fatalf("%+v %v", p, err)
	}

	if len(ag.web.Parked) != 1 || ag.web.Parked[0].Page != page.ID {
		t.Fatalf("web %+v", ag.web.Parked)
	}
}
