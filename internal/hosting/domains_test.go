// SPDX-License-Identifier: AGPL-3.0-or-later

package hosting

import (
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/maki-it/hostashi/internal/store"
)

// httpsSite creates site name with domain on HTTPS from Let's Encrypt.
func httpsSite(t *testing.T, s *Service, name, domain string) *fakeIssuer {
	t.Helper()

	ctx := t.Context()

	if s.now().After(time.Unix(0, 0).Add(2000 * 24 * time.Hour)) {
		now := time.Unix(0, 0).Add(1000 * 24 * time.Hour)
		s.now = func() time.Time { return now }
	}

	if _, err := s.Create(ctx, 1, CreateRequest{Name: name, Domains: []string{domain}}); err != nil {
		t.Fatal(err)
	}

	iss := &fakeIssuer{validDays: 1090}
	s.EnableTLS(iss)

	if _, err := s.SetTLS(ctx, 1, name, true); err != nil {
		t.Fatal(err)
	}

	s.RenewDue(ctx)

	if site, _ := s.Get(ctx, name); !site.TLSLive {
		t.Fatalf("%s not on HTTPS: %+v", name, site)
	}

	return iss
}

func webSite(ag *fakeAgent, user string) string {
	ag.mu.Lock()
	defer ag.mu.Unlock()

	for _, s := range ag.web.Sites {
		if s.User == user {
			return fmt.Sprintf("%v redirect=%v tls=%v", s.Domains, s.RedirectDomains, s.TLS)
		}
	}

	return "none"
}

func TestChangeDomainsCertificateFirst(t *testing.T) {
	s, ag, _ := newService(t)
	ctx := t.Context()
	now := time.Unix(0, 0).Add(1000 * 24 * time.Hour)
	s.now = func() time.Time { return now }

	iss := httpsSite(t, s, "blog", "old.example.com")

	// DNS of the new name is not ready: the change waits, the site stays
	// as it was.
	iss.err = errors.New("acme: NXDOMAIN new.example.com")

	site, c, err := s.ChangeDomains(ctx, 1, "blog", DomainsRequest{Domains: []string{"New.Example.com"}, RedirectDomains: []string{"old.example.com"}})
	if err != nil || c == nil || fmt.Sprint(c.Domains, c.Redirect) != "[new.example.com] [old.example.com]" || fmt.Sprint(site.Domains) != "[old.example.com]" {
		t.Fatalf("change: %+v %+v %v", site, c, err)
	}

	s.RenewDue(ctx)

	if got := webSite(ag, "blog"); got != "[old.example.com] redirect=[] tls=true" {
		t.Fatalf("site changed before its certificate: %s", got)
	}

	c, _ = s.DomainChange(ctx, "blog")
	if c == nil || !strings.Contains(c.Error, "NXDOMAIN") || !c.FailedAt.Equal(now) {
		t.Fatalf("failure not recorded: %+v", c)
	}

	// No new attempt within RetryAfter.
	calls := iss.calls

	s.RenewDue(ctx)

	if iss.calls != calls {
		t.Fatalf("retried within RetryAfter: %d calls", iss.calls-calls)
	}

	// Sending it again tries at once.
	iss.err = nil

	if _, _, err := s.ChangeDomains(ctx, 1, "blog", DomainsRequest{Domains: []string{"new.example.com"}, RedirectDomains: []string{"old.example.com"}}); err != nil {
		t.Fatal(err)
	}

	s.RenewDue(ctx)

	if got := webSite(ag, "blog"); got != "[new.example.com old.example.com] redirect=[old.example.com] tls=true" {
		t.Fatalf("after the certificate: %s", got)
	}

	site, _ = s.Get(ctx, "blog")
	if fmt.Sprint(site.Domains, site.RedirectDomains, site.TLSDomains) != "[new.example.com] [old.example.com] [new.example.com old.example.com]" {
		t.Fatalf("stored: %+v", site)
	}

	if c, _ := s.DomainChange(ctx, "blog"); c != nil {
		t.Fatalf("pending left: %+v", c)
	}

	// Dropping the redirect domain is covered by the certificate: at once.
	site, c, err = s.ChangeDomains(ctx, 1, "blog", DomainsRequest{Domains: []string{"new.example.com"}})
	if err != nil || c != nil || len(site.RedirectDomains) != 0 || webSite(ag, "blog") != "[new.example.com] redirect=[] tls=true" {
		t.Fatalf("drop redirect: %+v %+v %v %s", site, c, err, webSite(ag, "blog"))
	}
}

func TestChangeDomainsNowAndCancel(t *testing.T) {
	s, ag, _ := newService(t)
	ctx := t.Context()

	httpsSite(t, s, "blog", "old.example.com")

	if _, c, err := s.ChangeDomains(ctx, 1, "blog", DomainsRequest{Domains: []string{"new.example.com"}}); err != nil || c == nil {
		t.Fatalf("pending: %+v %v", c, err)
	}

	if err := s.CancelDomainChange(ctx, 1, "blog"); err != nil {
		t.Fatal(err)
	}

	if err := s.CancelDomainChange(ctx, 1, "blog"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("cancel twice: %v", err)
	}

	if _, c, err := s.ChangeDomains(ctx, 1, "blog", DomainsRequest{Domains: []string{"new.example.com"}}); err != nil || c == nil {
		t.Fatalf("pending: %+v %v", c, err)
	}

	// Now: today's behavior, HTTP until the certificate; the pending
	// change is gone.
	site, c, err := s.ChangeDomains(ctx, 1, "blog", DomainsRequest{Domains: []string{"new.example.com"}, Now: true})
	if err != nil || c != nil || fmt.Sprint(site.Domains) != "[new.example.com]" || webSite(ag, "blog") != "[new.example.com] redirect=[] tls=false" {
		t.Fatalf("now: %+v %+v %v %s", site, c, err, webSite(ag, "blog"))
	}

	if c, _ := s.DomainChange(ctx, "blog"); c != nil {
		t.Fatalf("pending left: %+v", c)
	}
}

func TestChangeDomainsChecks(t *testing.T) {
	s, _, _ := newService(t)
	ctx := t.Context()

	httpsSite(t, s, "blog", "blog.example.com")

	if _, err := s.Create(ctx, 1, CreateRequest{Name: "shop", Domains: []string{"shop.example.com"}}); err != nil {
		t.Fatal(err)
	}

	if _, err := s.Create(ctx, 1, CreateRequest{Name: "go", Domains: []string{"go.example.com"}, Redirect: &store.SiteRedirect{Target: "https://example.org", Status: 301}}); err != nil {
		t.Fatal(err)
	}

	for name, req := range map[string]DomainsRequest{
		"no domains":          {},
		"served and redirect": {Domains: []string{"a.example.com"}, RedirectDomains: []string{"a.example.com"}},
		"bad redirect":        {Domains: []string{"a.example.com"}, RedirectDomains: []string{"not a domain"}},
		"too many":            {Domains: []string{"a.example.com"}, RedirectDomains: manyDomains(50)},
	} {
		if _, _, err := s.ChangeDomains(ctx, 1, "shop", req); !errors.Is(err, ErrInvalid) {
			t.Errorf("%s: %v", name, err)
		}
	}

	if _, _, err := s.ChangeDomains(ctx, 1, "go", DomainsRequest{Domains: []string{"go.example.com"}, RedirectDomains: []string{"old.example.com"}}); !errors.Is(err, ErrInvalid) {
		t.Errorf("redirect site with redirect domains: %v", err)
	}

	if _, _, err := s.ChangeDomains(ctx, 1, "shop", DomainsRequest{Domains: []string{"shop.example.com"}, RedirectDomains: []string{"blog.example.com"}}); !errors.Is(err, ErrDomainTaken) {
		t.Errorf("redirect domain of another site: %v", err)
	}

	// A pending name is reserved for blog.
	if _, c, err := s.ChangeDomains(ctx, 1, "blog", DomainsRequest{Domains: []string{"blog.example.org"}}); err != nil || c == nil {
		t.Fatalf("pending: %+v %v", c, err)
	}

	if _, _, err := s.ChangeDomains(ctx, 1, "shop", DomainsRequest{Domains: []string{"blog.example.org"}}); !errors.Is(err, ErrDomainTaken) {
		t.Errorf("pending name of another site: %v", err)
	}

	if _, err := s.Create(ctx, 1, CreateRequest{Name: "new", Domains: []string{"blog.example.org"}}); !errors.Is(err, ErrDomainTaken) {
		t.Errorf("new site with a pending name: %v", err)
	}
}

func manyDomains(n int) []string {
	out := make([]string, n)
	for i := range out {
		out[i] = fmt.Sprintf("d%d.example.com", i)
	}

	return out
}

// A site that is turned to HTTP while its change waits changes at once on
// the next run.
func TestChangeDomainsPendingWithoutHTTPS(t *testing.T) {
	s, ag, _ := newService(t)
	ctx := t.Context()

	iss := httpsSite(t, s, "blog", "old.example.com")
	iss.err = errors.New("down")

	if _, c, err := s.ChangeDomains(ctx, 1, "blog", DomainsRequest{Domains: []string{"new.example.com"}}); err != nil || c == nil {
		t.Fatalf("pending: %+v %v", c, err)
	}

	if _, err := s.SetTLS(ctx, 1, "blog", false); err != nil {
		t.Fatal(err)
	}

	s.RenewDue(ctx)

	if got := webSite(ag, "blog"); got != "[new.example.com] redirect=[] tls=false" {
		t.Fatalf("after HTTPS off: %s", got)
	}

	if c, _ := s.DomainChange(ctx, "blog"); c != nil {
		t.Fatalf("pending left: %+v", c)
	}
}

// A change replaced while its certificate is ordered is not finished; the
// newer one is.
func TestChangeDomainsReplacedDuringOrder(t *testing.T) {
	s, ag, _ := newService(t)
	ctx := t.Context()

	iss := httpsSite(t, s, "blog", "old.example.com")

	if _, _, err := s.ChangeDomains(ctx, 1, "blog", DomainsRequest{Domains: []string{"a.example.com"}}); err != nil {
		t.Fatal(err)
	}

	once := false
	iss.before = func() {
		if once {
			return
		}

		once = true

		go func() {
			if _, _, err := s.ChangeDomains(ctx, 1, "blog", DomainsRequest{Domains: []string{"b.example.com"}}); err != nil {
				t.Error(err)
			}
		}()

		// Let the replacement through the service lock before the order
		// returns.
		for range 100 {
			if c, _ := s.DomainChange(ctx, "blog"); c != nil && c.Domains[0] == "b.example.com" {
				return
			}

			time.Sleep(10 * time.Millisecond)
		}
	}

	s.RenewDue(ctx)

	if got := webSite(ag, "blog"); got != "[old.example.com] redirect=[] tls=true" {
		t.Fatalf("replaced change went live: %s", got)
	}

	s.RenewDue(ctx)

	if got := webSite(ag, "blog"); got != "[b.example.com] redirect=[] tls=true" {
		t.Fatalf("newer change: %s", got)
	}
}
