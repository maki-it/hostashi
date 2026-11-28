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

func redirectTo(target string) *store.SiteRedirect {
	return &store.SiteRedirect{Target: target, Status: 301}
}

func TestWildcardDomainsPerType(t *testing.T) {
	s, ag, _ := newService(t)
	ctx := t.Context()

	site, err := s.Create(ctx, 1, CreateRequest{Name: "fwd", Domains: []string{"example.com", " *.Example.com "}, Redirect: redirectTo("https://example.org")})
	if err != nil || fmt.Sprint(site.Domains) != "[example.com *.example.com]" {
		t.Fatalf("create redirect: %+v %v", site, err)
	}

	if got := ag.live(); got != "[fwd=[example.com *.example.com]]" {
		t.Fatalf("live %s", got)
	}

	for name, req := range map[string]CreateRequest{
		"static":   {Name: "blog", Domains: []string{"blog.example.net", "*.blog.example.net"}},
		"php":      {Name: "blog", Domains: []string{"blog.example.net", "*.blog.example.net"}, PHP: "8.4"},
		"primary":  {Name: "blog", Domains: []string{"*.blog.example.net"}, Redirect: redirectTo("https://example.org")},
		"tld":      {Name: "blog", Domains: []string{"blog.example.net", "*.net"}, Redirect: redirectTo("https://example.org")},
		"inner":    {Name: "blog", Domains: []string{"blog.example.net", "a.*.example.net"}, Redirect: redirectTo("https://example.org")},
		"same one": {Name: "blog", Domains: []string{"blog.example.com", "*.example.com"}, Redirect: redirectTo("https://example.org")},
	} {
		if _, err := s.Create(ctx, 1, req); err == nil {
			t.Errorf("%s accepted", name)
		}
	}

	// A wildcard never redirects to the primary domain (ADR-0068).
	if _, _, err := s.ChangeDomains(ctx, 1, "fwd", DomainsRequest{Domains: []string{"example.com"}, RedirectDomains: []string{"*.example.com"}}); !errors.Is(err, ErrInvalid) {
		t.Fatalf("wildcard redirect domain: %v", err)
	}

	// Leaving the redirect type needs the wildcard gone first.
	if _, err := s.SetType(ctx, 1, "fwd", TypeRequest{Type: store.SiteTypeStatic}); !errors.Is(err, ErrInvalid) || !strings.Contains(err.Error(), "*.example.com") {
		t.Fatalf("type change: %v", err)
	}

	if _, err := s.SetDomains(ctx, 1, "fwd", []string{"example.com"}); err != nil {
		t.Fatal(err)
	}

	if _, err := s.SetType(ctx, 1, "fwd", TypeRequest{Type: store.SiteTypeStatic}); err != nil {
		t.Fatalf("type change without wildcard: %v", err)
	}

	// A static site cannot take one through a domain change either.
	if _, err := s.SetDomains(ctx, 1, "fwd", []string{"example.com", "*.example.com"}); !errors.Is(err, ErrInvalid) {
		t.Fatalf("wildcard on static: %v", err)
	}
}

func TestWildcardRedirectLoop(t *testing.T) {
	s, _, _ := newService(t)
	ctx := t.Context()

	// Nobody else serves www.example.com: the site would answer it itself.
	for _, target := range []string{"https://www.example.com", "https://a.b.example.com/x", "https://WWW.example.com"} {
		if _, err := s.Create(ctx, 1, CreateRequest{Name: "fwd", Domains: []string{"example.com", "*.example.com"}, Redirect: redirectTo(target)}); !errors.Is(err, ErrInvalid) {
			t.Errorf("%s: %v", target, err)
		}
	}

	// Another site serves www.example.com: nginx prefers its exact name.
	if _, err := s.Create(ctx, 1, CreateRequest{Name: "www", Domains: []string{"www.example.com"}}); err != nil {
		t.Fatal(err)
	}

	if _, err := s.Create(ctx, 1, CreateRequest{Name: "fwd", Domains: []string{"example.com", "*.example.com"}, Redirect: redirectTo("https://www.example.com")}); err != nil {
		t.Fatalf("target served elsewhere: %v", err)
	}

	// A longer wildcard of another site wins over the site's own.
	if _, err := s.Create(ctx, 1, CreateRequest{Name: "shop", Domains: []string{"shop.example.com", "*.shop.example.com"}, Redirect: redirectTo("https://example.org")}); err != nil {
		t.Fatal(err)
	}

	if _, err := s.SetRedirect(ctx, 1, "fwd", *redirectTo("https://a.shop.example.com")); err != nil {
		t.Fatalf("target below a longer wildcard: %v", err)
	}

	if _, err := s.SetRedirect(ctx, 1, "fwd", *redirectTo("https://nobody.example.com")); !errors.Is(err, ErrInvalid) {
		t.Fatalf("loop through set redirect: %v", err)
	}

	// Adding the wildcard by a domain change is checked as well.
	if _, err := s.Create(ctx, 1, CreateRequest{Name: "net", Domains: []string{"example.net"}, Redirect: redirectTo("https://www.example.net")}); err != nil {
		t.Fatal(err)
	}

	if _, err := s.SetDomains(ctx, 1, "net", []string{"example.net", "*.example.net"}); !errors.Is(err, ErrInvalid) {
		t.Fatalf("loop through domain change: %v", err)
	}

	site, _ := s.Get(ctx, "fwd")

	got, err := s.ServedElsewhere(ctx, site)
	if err != nil || fmt.Sprint(got) != "[*.shop.example.com shop.example.com www.example.com]" {
		t.Fatalf("served elsewhere %v %v", got, err)
	}

	www, _ := s.Get(ctx, "www")
	if got, _ := s.ServedElsewhere(ctx, www); len(got) != 0 {
		t.Fatalf("without wildcard %v", got)
	}
}

func TestWildcardNeedsDNS01(t *testing.T) {
	s, ag, iss, rec := dnsTLSService(t, nil)
	ctx := t.Context()
	s.now = func() time.Time { return time.Unix(0, 0).Add(1000 * 24 * time.Hour) }

	// HTTP-01 cannot check a wildcard: not at create, not later.
	if _, err := s.Create(ctx, 1, CreateRequest{Name: "fwd", Domains: []string{"example.com", "*.example.com"}, Redirect: redirectTo("https://example.org"), TLS: true}); !errors.Is(err, ErrInvalid) ||
		!strings.Contains(err.Error(), "DNS account") {
		t.Fatalf("create with tls: %v", err)
	}

	if _, err := s.Create(ctx, 1, CreateRequest{Name: "fwd", Domains: []string{"example.com", "*.example.com"}, Redirect: redirectTo("https://example.org")}); err != nil {
		t.Fatal(err)
	}

	if _, err := s.SetTLS(ctx, 1, "fwd", true); !errors.Is(err, ErrInvalid) {
		t.Fatalf("tls over http: %v", err)
	}

	// The wildcard uses the record of example.com.
	if _, err := s.SetTLSChallenge(ctx, 1, "fwd", store.ChallengeDNS); !errors.Is(err, ErrNoDNSAccount) ||
		!strings.Contains(err.Error(), "_acme-challenge.example.com") || strings.Contains(err.Error(), "*") {
		t.Fatalf("without account: %v", err)
	}

	addAccount(t, s, "cf", "cloudflare", "tok", "example.com")

	if _, err := s.SetTLSChallenge(ctx, 1, "fwd", store.ChallengeDNS); err != nil {
		t.Fatal(err)
	}

	if _, err := s.SetTLS(ctx, 1, "fwd", true); err != nil {
		t.Fatalf("tls over dns: %v", err)
	}

	s.RenewDue(ctx)

	if fs, _ := s.Get(ctx, "fwd"); fs.TLSError != "" {
		t.Fatalf("tls error %s", fs.TLSError)
	}

	if got := ag.live(); got != "[fwd=[example.com *.example.com]+tls]" || iss.dnsCalls != 1 {
		t.Fatalf("live %s, dns calls %d, provider %v", got, iss.dnsCalls, rec.list())
	}

	// Back to HTTP is refused while HTTPS is on.
	if _, err := s.SetTLSChallenge(ctx, 1, "fwd", store.ChallengeHTTP); !errors.Is(err, ErrInvalid) {
		t.Fatalf("back to http: %v", err)
	}

	// An HTTP site cannot add a wildcard while it wants Let's Encrypt.
	if _, err := s.Create(ctx, 1, CreateRequest{Name: "net", Domains: []string{"example.net"}, Redirect: redirectTo("https://example.org"), TLS: true}); err != nil {
		t.Fatal(err)
	}

	if _, err := s.SetDomains(ctx, 1, "net", []string{"example.net", "*.example.net"}); !errors.Is(err, ErrInvalid) {
		t.Fatalf("wildcard on http-01 site: %v", err)
	}

	// An own certificate is fine.
	if _, err := s.SetTLSSource(ctx, 1, "net", store.TLSCustom); err != nil {
		t.Fatal(err)
	}

	if _, err := s.SetDomains(ctx, 1, "net", []string{"example.net", "*.example.net"}); err != nil {
		t.Fatalf("wildcard with own certificate: %v", err)
	}

	// Back to Let's Encrypt over HTTP is refused.
	if _, err := s.SetTLSSource(ctx, 1, "net", store.TLSLetsEncrypt); !errors.Is(err, ErrInvalid) {
		t.Fatalf("source back to letsencrypt: %v", err)
	}
}

func TestWildcardsWithoutDNS(t *testing.T) {
	s, _, _, _ := dnsTLSService(t, map[string]string{"example.net": "example.net.acme.example.org"})
	ctx := t.Context()

	for _, c := range []CreateRequest{
		{Name: "fwd", Domains: []string{"example.com", "*.example.com", "*.shop.example.com"}, Redirect: redirectTo("https://example.org")},
		{Name: "net", Domains: []string{"example.net", "*.example.net"}, Redirect: redirectTo("https://example.org")},
		{Name: "blog", Domains: []string{"blog.example.com"}},
	} {
		if _, err := s.Create(ctx, 1, c); err != nil {
			t.Fatal(err)
		}
	}

	get := func(name string) []string {
		site, _ := s.Get(ctx, name)

		got, err := s.WildcardsWithoutDNS(ctx, site)
		if err != nil {
			t.Fatal(err)
		}

		return got
	}

	if got := get("fwd"); fmt.Sprint(got) != "[*.example.com *.shop.example.com]" {
		t.Fatalf("no accounts: %v", got)
	}

	addAccount(t, s, "cf", "cloudflare", "tok", "shop.example.com")

	if got := get("fwd"); fmt.Sprint(got) != "[*.example.com]" {
		t.Fatalf("one zone: %v", got)
	}

	// The CNAME target counts, not the domain's own zone.
	addAccount(t, s, "acme", "cloudflare", "tok2", "acme.example.org")

	if got := get("net"); len(got) != 0 {
		t.Fatalf("through CNAME: %v", got)
	}

	if got := get("blog"); len(got) != 0 {
		t.Fatalf("without wildcard: %v", got)
	}
}
