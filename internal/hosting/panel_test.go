// SPDX-License-Identifier: AGPL-3.0-or-later

package hosting

import (
	"errors"
	"strings"
	"testing"
	"time"
)

func TestPanelCertificate(t *testing.T) {
	s, ag, _ := newService(t)
	ctx := t.Context()

	day := func(d int) time.Time { return time.Unix(0, 0).Add(time.Duration(d) * 24 * time.Hour) }
	now := day(1000)
	s.now = func() time.Time { return now }

	if err := s.EnablePanel("panel example"); err == nil {
		t.Fatal("invalid panel domain accepted")
	}

	if err := s.EnablePanel("panel.example.com"); err != nil {
		t.Fatal(err)
	}

	// Without an issuer, nginx serves the panel name for ACME only.
	if err := s.Sync(ctx); err != nil {
		t.Fatal(err)
	}

	if p := ag.web.Panel; p == nil || p.Domain != "panel.example.com" || p.TLS {
		t.Fatalf("panel before the certificate: %+v", p)
	}

	iss := &fakeIssuer{validDays: 1090}
	s.EnableTLS(iss)
	s.RenewDue(ctx)

	p, err := s.PanelTLS(ctx)
	if err != nil || !p.Live || !p.NotAfter.Equal(day(1090)) || ag.web.Panel == nil || !ag.web.Panel.TLS || iss.calls != 1 {
		t.Fatalf("after issue: %+v %v web %+v calls %d", p, err, ag.web.Panel, iss.calls)
	}

	// Fresh: nothing to do. A site change keeps the panel.
	s.RenewDue(ctx)

	if _, err := s.Create(ctx, 1, CreateRequest{Name: "blog", Domains: []string{"blog.example.com"}}); err != nil {
		t.Fatal(err)
	}

	if iss.calls != 1 || ag.web.Panel == nil || !ag.web.Panel.TLS {
		t.Fatalf("after site create: calls %d web %+v", iss.calls, ag.web.Panel)
	}

	// A site cannot take the panel's name.
	if _, err := s.Create(ctx, 1, CreateRequest{Name: "shop", Domains: []string{"panel.example.com"}}); err == nil {
		t.Fatal("site took the panel domain")
	}

	// A failed renewal is recorded and retried after RetryAfter.
	now = day(1070)
	iss.err = errors.New("acme: NXDOMAIN")

	s.RenewDue(ctx)
	s.RenewDue(ctx)

	p, _ = s.PanelTLS(ctx)
	if !strings.Contains(p.Error, "NXDOMAIN") || !p.FailedAt.Equal(now) || !p.Live || iss.calls != 2 {
		t.Fatalf("after failure: %+v calls %d", p, iss.calls)
	}

	now = now.Add(RetryAfter + time.Minute)
	iss.err, iss.validDays = nil, 1160

	s.RenewDue(ctx)

	if p, _ = s.PanelTLS(ctx); p.Error != "" || !p.NotAfter.Equal(day(1160)) {
		t.Fatalf("after retry: %+v", p)
	}

	// Expired: back to ACME only.
	now = day(1200)
	iss.err = errors.New("down")

	s.RenewDue(ctx)

	if err := s.Sync(ctx); err != nil {
		t.Fatal(err)
	}

	if p, _ = s.PanelTLS(ctx); p.Live || ag.web.Panel.TLS {
		t.Fatalf("expired: %+v web %+v", p, ag.web.Panel)
	}

	// A new panel name starts over.
	if err := s.EnablePanel("admin.example.com"); err != nil {
		t.Fatal(err)
	}

	if p, _ = s.PanelTLS(ctx); p.Domain != "admin.example.com" || !p.NotAfter.IsZero() || len(p.Domains) != 0 {
		t.Fatalf("new name: %+v", p)
	}
}

// HTTP-01 for the panel needs its port 80 server before the CA calls.
func TestPanelServedBeforeChallenge(t *testing.T) {
	s, ag, _ := newService(t)

	if err := s.EnablePanel("panel.example.com"); err != nil {
		t.Fatal(err)
	}

	iss := &fakeIssuer{validDays: 1090}
	iss.before = func() {
		if ag.web.Panel == nil || ag.web.Panel.Domain != "panel.example.com" {
			t.Errorf("challenge before nginx serves the panel: %+v", ag.web.Panel)
		}
	}

	s.now = func() time.Time { return time.Unix(0, 0).Add(1000 * 24 * time.Hour) }
	s.EnableTLS(iss)
	s.RenewDue(t.Context())

	if p, err := s.PanelTLS(t.Context()); err != nil || !p.Live || iss.calls != 1 {
		t.Fatalf("%+v %v calls %d", p, err, iss.calls)
	}
}

func TestPanelDomainReserved(t *testing.T) {
	s, ag, _ := newService(t)
	ctx := t.Context()

	if s.PanelDomain() != "" {
		t.Fatalf("panel domain before EnablePanel: %q", s.PanelDomain())
	}

	if err := s.EnablePanel("cp.example.com"); err != nil {
		t.Fatal(err)
	}

	if s.PanelDomain() != "cp.example.com" {
		t.Fatalf("panel domain %q", s.PanelDomain())
	}

	// Site domains are normalized first, so case does not get around it.
	if _, err := s.Create(ctx, 1, CreateRequest{Name: "blog", Domains: []string{"blog.example.com", "CP.example.com"}}); !errors.Is(err, ErrDomainReserved) {
		t.Fatalf("create with the panel's name: %v", err)
	}

	if _, ok := ag.accounts["blog"]; ok {
		t.Fatal("a refused site got an account")
	}

	if _, err := s.Create(ctx, 1, CreateRequest{Name: "blog", Domains: []string{"blog.example.com"}}); err != nil {
		t.Fatal(err)
	}

	if _, err := s.SetDomains(ctx, 1, "blog", []string{"blog.example.com", "cp.example.com"}); !errors.Is(err, ErrDomainReserved) {
		t.Fatalf("set domains with the panel's name: %v", err)
	}

	site, err := s.Get(ctx, "blog")
	if err != nil || len(site.Domains) != 1 {
		t.Fatalf("domains changed: %+v %v", site, err)
	}

	if _, err := s.SetParked(ctx, 1, "cp.example.com", "", false); !errors.Is(err, ErrDomainReserved) {
		t.Fatalf("park the panel's name: %v", err)
	}

	// Other names of the same zone are fine.
	if _, err := s.SetParked(ctx, 1, "www.cp.example.com", "", false); err != nil {
		t.Fatal(err)
	}
}
