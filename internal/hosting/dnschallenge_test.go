// SPDX-License-Identifier: AGPL-3.0-or-later

package hosting

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/go-acme/lego/v5/challenge"

	"github.com/maki-it/hostashi/internal/store"
)

// fakeDNSIssuer signs like fakeIssuer and, for DNS-01, presents and cleans
// up a record for every name of the request through the given provider.
type fakeDNSIssuer struct {
	fakeIssuer

	dnsCalls int
	dnsErr   error // returned by ObtainDNS after the records
}

func (i *fakeDNSIssuer) ObtainDNS(ctx context.Context, csr []byte, p challenge.Provider) ([]byte, error) {
	// "csr user domains", the domains joined by commas or one name.
	fields := strings.Fields(string(csr))
	names := strings.Split(strings.Trim(fields[2], "[]"), ",")

	for _, n := range names {
		if err := p.Present(ctx, n, "tok", "auth"); err != nil {
			return nil, err
		}
	}

	for _, n := range names {
		if err := p.CleanUp(ctx, n, "tok", "auth"); err != nil {
			return nil, err
		}
	}

	i.mu.Lock()
	i.dnsCalls++
	err := i.dnsErr
	i.mu.Unlock()

	if err != nil {
		return nil, err
	}

	return i.Obtain(ctx, csr)
}

// recordingDNS is the lego provider of every account: it records the
// calls with the account's token.
type recordingDNS struct {
	mu    sync.Mutex
	calls []string
	fail  error
}

func (r *recordingDNS) build(provider string, creds map[string]string) (challenge.ProviderTimeout, error) {
	return &recordingProvider{r: r, tag: provider + ":" + creds["apiToken"]}, nil
}

func (r *recordingDNS) list() []string {
	r.mu.Lock()
	defer r.mu.Unlock()

	out := append([]string(nil), r.calls...)
	sort.Strings(out)

	return out
}

type recordingProvider struct {
	r   *recordingDNS
	tag string
}

func (p *recordingProvider) Present(_ context.Context, domain, _, _ string) error {
	p.r.mu.Lock()
	defer p.r.mu.Unlock()

	if p.r.fail != nil {
		return fmt.Errorf("%s: %w", p.tag, p.r.fail)
	}

	p.r.calls = append(p.r.calls, "present "+domain+" "+p.tag)

	return nil
}

func (p *recordingProvider) CleanUp(_ context.Context, domain, _, _ string) error {
	p.r.mu.Lock()
	defer p.r.mu.Unlock()

	p.r.calls = append(p.r.calls, "cleanup "+domain+" "+p.tag)

	return nil
}

func (*recordingProvider) Timeout() (time.Duration, time.Duration) { return time.Minute, time.Second }

// dnsTLSService is a service with DNS accounts, a DNS-01 issuer and
// challenge names from cnames (domain -> target, else _acme-challenge.).
func dnsTLSService(t *testing.T, cnames map[string]string) (*Service, *fakeAgent, *fakeDNSIssuer, *recordingDNS) {
	t.Helper()

	s, ag, _ := newService(t)
	s.EnableDNSAccounts(newFakeDNS(t))

	rec := &recordingDNS{}
	s.dnsBuild = rec.build
	s.challengeName = func(_ context.Context, d string) string {
		if c, ok := cnames[d]; ok {
			return c
		}

		return "_acme-challenge." + d
	}

	iss := &fakeDNSIssuer{fakeIssuer: fakeIssuer{validDays: 1090}}
	s.EnableTLS(iss)

	return s, ag, iss, rec
}

func addAccount(t *testing.T, s *Service, label, provider, token string, zones ...string) DNSAccountView {
	t.Helper()

	v, err := s.AddDNSAccount(t.Context(), 1, NewDNSAccount{
		Label: label, Provider: provider, Zones: zones, Credentials: map[string]string{"apiToken": token},
	})
	if err != nil {
		t.Fatal(err)
	}

	return v
}

func TestDNSChallengeIssue(t *testing.T) {
	// www goes through a CNAME into a separate ACME zone.
	s, ag, iss, rec := dnsTLSService(t, map[string]string{"www.example.com": "www.example.com.acme.example.net"})
	ctx := t.Context()

	day := func(d int) time.Time { return time.Unix(0, 0).Add(time.Duration(d) * 24 * time.Hour) }
	now := day(1000)
	s.now = func() time.Time { return now }

	if _, err := s.Create(ctx, 1, CreateRequest{Name: "shop", Domains: []string{"example.com", "www.example.com"}}); err != nil {
		t.Fatal(err)
	}

	// No account yet: refused, naming the challenge names.
	if _, err := s.SetTLSChallenge(ctx, 1, "shop", store.ChallengeDNS); !errors.Is(err, ErrNoDNSAccount) ||
		!strings.Contains(err.Error(), "_acme-challenge.example.com") {
		t.Fatalf("without account: %v", err)
	}

	addAccount(t, s, "cf", "cloudflare", "cf-token", "example.com")

	// The CNAME target is in no zone yet.
	if _, err := s.SetTLSChallenge(ctx, 1, "shop", store.ChallengeDNS); !errors.Is(err, ErrNoDNSAccount) ||
		!strings.Contains(err.Error(), "www.example.com.acme.example.net") || strings.Contains(err.Error(), "_acme-challenge.example.com") {
		t.Fatalf("CNAME target uncovered: %v", err)
	}

	addAccount(t, s, "acme zone", "hetzner", "hz-token", "acme.example.net")

	site, err := s.SetTLSChallenge(ctx, 1, "shop", store.ChallengeDNS)
	if err != nil || site.TLSChallenge != store.ChallengeDNS {
		t.Fatalf("set dns: %+v %v", site, err)
	}

	if d := lastDiff(t, s.st, "site.tls.challenge"); !changed(d["challenge"], "http", "dns") {
		t.Fatalf("diff %v", d)
	}

	if _, err := s.SetTLS(ctx, 1, "shop", true); err != nil {
		t.Fatal(err)
	}

	s.RenewDue(ctx)

	if got := ag.live(); got != "[shop=[example.com www.example.com]+tls]" || iss.dnsCalls != 1 || iss.calls != 1 {
		t.Fatalf("live %s, dns calls %d, calls %d", got, iss.dnsCalls, iss.calls)
	}

	// Each name through its own account.
	want := []string{
		"cleanup example.com cloudflare:cf-token", "cleanup www.example.com hetzner:hz-token",
		"present example.com cloudflare:cf-token", "present www.example.com hetzner:hz-token",
	}
	if got := rec.list(); strings.Join(got, "|") != strings.Join(want, "|") {
		t.Fatalf("provider calls %v", got)
	}

	// A new domain without an account is refused at once.
	if _, err := s.SetDomains(ctx, 1, "shop", []string{"example.com", "shop.example.org"}); !errors.Is(err, ErrNoDNSAccount) {
		t.Fatalf("uncovered domain: %v", err)
	}

	// An account that the site needs cannot go, nor lose the zone.
	accounts, _ := s.DNSAccountList(ctx)

	var cf DNSAccountView

	for _, a := range accounts {
		if a.Label == "cf" {
			cf = a
		}
	}

	if err := s.DeleteDNSAccount(ctx, 1, cf.ID); !errors.Is(err, ErrDNSAccountInUse) || !strings.Contains(err.Error(), "shop") {
		t.Fatalf("delete in use: %v", err)
	}

	if _, err := s.UpdateDNSAccount(ctx, 1, cf.ID, NewDNSAccount{Label: "cf", Zones: []string{"example.org"}}); !errors.Is(err, ErrDNSAccountInUse) {
		t.Fatalf("drop zone in use: %v", err)
	}

	// A narrower account for the name takes over; then the old one may go.
	addAccount(t, s, "inner", "cloudflare", "inner-token", "_acme-challenge.example.com")

	if err := s.DeleteDNSAccount(ctx, 1, cf.ID); err != nil {
		t.Fatalf("delete after takeover: %v", err)
	}

	// A failed DNS order keeps the old certificate and records the error
	// without the token.
	now = day(1070)
	rec.fail = errors.New("401 for inner-token")

	s.RenewDue(ctx)

	site, _ = s.Get(ctx, "shop")
	if site.TLSError == "" || strings.Contains(site.TLSError, "inner-token") || ag.live() != "[shop=[example.com www.example.com]+tls]" {
		t.Fatalf("after failure: %q live %s", site.TLSError, ag.live())
	}

	// Back to HTTP: the issuer's HTTP-01 path, the error cleared.
	rec.fail = nil

	site, err = s.SetTLSChallenge(ctx, 1, "shop", store.ChallengeHTTP)
	if err != nil || site.TLSChallenge != store.ChallengeHTTP || site.TLSError != "" {
		t.Fatalf("back to http: %+v %v", site, err)
	}

	s.RenewDue(ctx)

	if iss.calls != 2 || iss.dnsCalls != 1 {
		t.Fatalf("http renewal: calls %d, dns calls %d", iss.calls, iss.dnsCalls)
	}

	if _, err := s.SetTLSChallenge(ctx, 1, "shop", "tls-alpn"); !errors.Is(err, ErrInvalid) {
		t.Fatalf("unknown type: %v", err)
	}
}

func TestDNSChallengeNeedsDNSIssuer(t *testing.T) {
	s, _, _ := newService(t)
	s.EnableDNSAccounts(newFakeDNS(t))
	s.challengeName = func(_ context.Context, d string) string { return "_acme-challenge." + d }
	ctx := t.Context()

	if _, err := s.Create(ctx, 1, CreateRequest{Name: "blog", Domains: []string{"blog.example.com"}}); err != nil {
		t.Fatal(err)
	}

	addAccount(t, s, "hz", "hetzner", "t", "example.com")

	if _, err := s.SetTLSChallenge(ctx, 1, "blog", store.ChallengeDNS); !errors.Is(err, ErrNoTLS) {
		t.Fatalf("without issuer: %v", err)
	}

	s.EnableTLS(&fakeIssuer{validDays: 1090})

	if _, err := s.SetTLSChallenge(ctx, 1, "blog", store.ChallengeDNS); !errors.Is(err, ErrNoTLS) {
		t.Fatalf("HTTP-only issuer: %v", err)
	}
}

func TestPanelDNSChallenge(t *testing.T) {
	s, ag, iss, rec := dnsTLSService(t, nil)
	ctx := t.Context()

	if _, err := s.SetPanelTLSChallenge(ctx, 1, store.ChallengeDNS); !errors.Is(err, ErrNoPanel) {
		t.Fatalf("no panel address: %v", err)
	}

	if err := s.EnablePanel("panel.example.com"); err != nil {
		t.Fatal(err)
	}

	if _, err := s.SetPanelTLSChallenge(ctx, 1, store.ChallengeDNS); !errors.Is(err, ErrNoDNSAccount) {
		t.Fatalf("without account: %v", err)
	}

	a := addAccount(t, s, "hz", "hetzner", "t", "example.com")

	p, err := s.SetPanelTLSChallenge(ctx, 1, store.ChallengeDNS)
	if err != nil || p.WantedChallenge() != store.ChallengeDNS {
		t.Fatalf("set: %+v %v", p, err)
	}

	// DNS-01 needs no web state for port 80 first.
	applies := ag.applies

	s.RenewDue(ctx)

	if iss.dnsCalls != 1 || len(rec.list()) != 2 {
		t.Fatalf("dns calls %d, provider %v", iss.dnsCalls, rec.list())
	}

	if ag.applies != applies+1 {
		t.Fatalf("web applies %d, want %d (only the switch to HTTPS)", ag.applies, applies+1)
	}

	if err := s.DeleteDNSAccount(ctx, 1, a.ID); !errors.Is(err, ErrDNSAccountInUse) || !strings.Contains(err.Error(), "panel") {
		t.Fatalf("delete in use: %v", err)
	}

	if p, err = s.SetPanelTLSChallenge(ctx, 1, store.ChallengeHTTP); err != nil || p.WantedChallenge() != store.ChallengeHTTP {
		t.Fatalf("back to http: %+v %v", p, err)
	}

	if err := s.DeleteDNSAccount(ctx, 1, a.ID); err != nil {
		t.Fatal(err)
	}
}

func TestAccountFor(t *testing.T) {
	accounts := []store.DNSAccount{
		{ID: 1, Zones: []string{"example.com"}},
		{ID: 2, Zones: []string{"dev.example.com", "example.net"}},
	}

	for name, want := range map[string]int64{
		"_acme-challenge.example.com":       1,
		"_acme-challenge.a.dev.example.com": 2,
		"_acme-challenge.example.net":       2,
		"_acme-challenge.example.org":       0,
	} {
		a, ok := accountFor(accounts, name)
		if a.ID != want || ok != (want != 0) {
			t.Errorf("%s: %d %v, want %d", name, a.ID, ok, want)
		}
	}
}
