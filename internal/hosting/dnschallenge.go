// SPDX-License-Identifier: AGPL-3.0-or-later

package hosting

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/go-acme/lego/v5/challenge"
	"github.com/go-acme/lego/v5/challenge/dns01"

	"github.com/maki-it/hostashi/internal/dnsprov"
	"github.com/maki-it/hostashi/internal/store"
)

// Errors of DNS-01 (ADR-0076).
var (
	// ErrNoDNSAccount: a name of a DNS-01 certificate falls into no zone
	// of a DNS account.
	ErrNoDNSAccount = errors.New("hosting: no DNS account serves this name")
	// ErrDNSAccountInUse: a site or the panel address gets its
	// certificate over the account.
	ErrDNSAccountInUse = errors.New("hosting: the DNS account is in use")
)

// DNSIssuer is an Issuer that also orders over DNS-01; package acme
// implements it.
type DNSIssuer interface {
	Issuer
	ObtainDNS(ctx context.Context, csrPEM []byte, dns challenge.Provider) ([]byte, error)
}

// dnsIssueTimeout bounds one DNS-01 certificate: the provider's API, the
// wait until the zone's name servers show the record, and the CA's check.
const dnsIssueTimeout = 15 * time.Minute

// lookupChallengeName returns the name whose TXT record answers the
// DNS-01 challenge for domain: _acme-challenge.<domain>, or where a CNAME
// there points. Lowercase, without the trailing dot. Service.challengeName
// is this function outside tests.
func lookupChallengeName(ctx context.Context, domain string) string {
	fqdn := dns01.GetChallengeInfo(ctx, domain, "").EffectiveFQDN

	return strings.ToLower(strings.TrimSuffix(fqdn, "."))
}

// accountFor returns the account whose longest zone covers name.
func accountFor(accounts []store.DNSAccount, name string) (store.DNSAccount, bool) {
	var (
		best     store.DNSAccount
		bestZone string
	)

	for _, a := range accounts {
		if z, ok := dnsprov.MatchZone(name, a.Zones); ok && len(z) > len(bestZone) {
			best, bestZone = a, z
		}
	}

	return best, bestZone != ""
}

// dnsAccountsFor returns the account of each domain, by domain. A domain
// that no account serves is an ErrNoDNSAccount naming its challenge name.
func (s *Service) dnsAccountsFor(ctx context.Context, domains []string) (map[string]store.DNSAccount, error) {
	if s.dns == nil {
		return nil, ErrDNSAccountsOff
	}

	accounts, err := s.st.DNSAccounts(ctx)
	if err != nil {
		return nil, err
	}

	out := make(map[string]store.DNSAccount, len(domains))

	var missing []string

	for _, d := range domains {
		name := s.challengeName(ctx, d)

		a, ok := accountFor(accounts, name)
		if !ok {
			missing = append(missing, name)

			continue
		}

		out[d] = a
	}

	if len(missing) > 0 {
		return nil, fmt.Errorf("%w: %s; add its zone to a DNS account or point it there with a CNAME", ErrNoDNSAccount, strings.Join(missing, ", "))
	}

	return out, nil
}

// dnsRouter is the DNS-01 provider of one order: each domain's record goes
// through the provider of its account. One lego provider per account
// lives from Present to CleanUp, as some keep the ids of their records.
type dnsRouter struct {
	byDomain map[string]store.DNSAccount
	open     func(store.DNSAccount) (map[string]string, error)
	build    func(provider string, creds map[string]string) (challenge.ProviderTimeout, error)

	mu    sync.Mutex
	provs map[int64]challenge.ProviderTimeout
	creds map[int64]map[string]string
}

func (r *dnsRouter) provider(domain string) (challenge.ProviderTimeout, map[string]string, error) {
	// lego hands in the domain without "*." for a wildcard name.
	a, ok := r.byDomain[domain]
	if !ok {
		a, ok = r.byDomain["*."+domain]
	}

	if !ok {
		return nil, nil, fmt.Errorf("%w: %s", ErrNoDNSAccount, domain)
	}

	r.mu.Lock()
	defer r.mu.Unlock()

	if p, ok := r.provs[a.ID]; ok {
		return p, r.creds[a.ID], nil
	}

	creds, err := r.open(a)
	if err != nil {
		return nil, nil, err
	}

	p, err := r.build(a.Provider, creds)
	if err != nil {
		return nil, nil, fmt.Errorf("DNS account %q: %s", a.Label, dnsprov.Redact(err.Error(), creds))
	}

	r.provs[a.ID], r.creds[a.ID] = p, creds

	return p, creds, nil
}

// Present implements challenge.Provider.
func (r *dnsRouter) Present(ctx context.Context, domain, token, keyAuth string) error {
	p, creds, err := r.provider(domain)
	if err != nil {
		return err
	}

	if err := p.Present(ctx, domain, token, keyAuth); err != nil {
		return errors.New(dnsprov.Redact(err.Error(), creds))
	}

	return nil
}

// CleanUp implements challenge.Provider.
func (r *dnsRouter) CleanUp(ctx context.Context, domain, token, keyAuth string) error {
	p, creds, err := r.provider(domain)
	if err != nil {
		return err
	}

	if err := p.CleanUp(ctx, domain, token, keyAuth); err != nil {
		return errors.New(dnsprov.Redact(err.Error(), creds))
	}

	return nil
}

// Timeout is the wait for the record at the zone's name servers; every
// provider of the allowlist uses the same (internal/dnsprov).
func (r *dnsRouter) Timeout() (timeout, interval time.Duration) {
	return dnsprov.PropagationTimeout, dnsprov.PollingInterval
}

// obtainChain asks the CA for a certificate for csr, over DNS-01 with the
// accounts of domains when challenge is store.ChallengeDNS.
func (s *Service) obtainChain(ctx context.Context, csr []byte, domains []string, challengeType string) ([]byte, error) {
	if challengeType != store.ChallengeDNS {
		return s.issuer.Obtain(ctx, csr)
	}

	di, ok := s.issuer.(DNSIssuer)
	if !ok {
		return nil, fmt.Errorf("%w: the certificate client cannot use DNS-01", ErrNoTLS)
	}

	byDomain, err := s.dnsAccountsFor(ctx, domains)
	if err != nil {
		return nil, err
	}

	r := &dnsRouter{
		byDomain: byDomain, open: s.dns.Open, build: s.dnsBuild,
		provs: map[int64]challenge.ProviderTimeout{}, creds: map[int64]map[string]string{},
	}

	return di.ObtainDNS(ctx, csr, r)
}

// dnsLosers returns the sites, and "the panel address", that get their
// certificate over DNS-01 and would have a domain without an account if
// the accounts were change(accounts).
func (s *Service) dnsLosers(ctx context.Context, change func([]store.DNSAccount) []store.DNSAccount) ([]string, error) {
	accounts, err := s.st.DNSAccounts(ctx)
	if err != nil {
		return nil, err
	}

	after := change(accounts)

	loses := func(domains []string) bool {
		for _, d := range domains {
			if _, ok := accountFor(after, s.challengeName(ctx, d)); !ok {
				return true
			}
		}

		return false
	}

	sites, err := s.st.Sites(ctx)
	if err != nil {
		return nil, err
	}

	var out []string

	for _, site := range sites {
		if site.TLSChallenge == store.ChallengeDNS && loses(site.AllDomains()) {
			out = append(out, site.Name)
		}
	}

	sort.Strings(out)

	if s.panelDomain != "" {
		p, err := s.PanelTLS(ctx)
		if err != nil {
			return nil, err
		}

		if p.challenge() == store.ChallengeDNS && loses([]string{s.panelDomain}) {
			out = append(out, "the panel address")
		}
	}

	return out, nil
}

// dnsInUse is the refusal of a change that leaves users without account.
func dnsInUse(users []string) error {
	return fmt.Errorf("%w: %s would have a domain without a DNS account; switch them to HTTP or give the zones to another account first",
		ErrDNSAccountInUse, strings.Join(users, ", "))
}

// checkDNSChallenge refuses DNS-01 for domains that no account serves.
func (s *Service) checkDNSChallenge(ctx context.Context, domains []string) error {
	if s.issuer == nil {
		return ErrNoTLS
	}

	if _, ok := s.issuer.(DNSIssuer); !ok {
		return fmt.Errorf("%w: the certificate client cannot use DNS-01", ErrNoTLS)
	}

	_, err := s.dnsAccountsFor(ctx, domains)

	return err
}

func validChallenge(c string) error {
	if c != store.ChallengeHTTP && c != store.ChallengeDNS {
		return fmt.Errorf("%w: challenge must be %q or %q", ErrInvalid, store.ChallengeHTTP, store.ChallengeDNS)
	}

	return nil
}

// SetTLSChallenge chooses how Let's Encrypt checks the domains of a site
// (ADR-0076). DNS needs a DNS account for every domain. A change asks the
// renewal loop for a new certificate when the site wants one.
func (s *Service) SetTLSChallenge(ctx context.Context, actor int64, name, challengeType string) (store.Site, error) {
	if err := validChallenge(challengeType); err != nil {
		return store.Site{}, err
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	site, err := s.Get(ctx, name)
	if err != nil {
		return store.Site{}, err
	}

	if challengeType == store.ChallengeDNS {
		if err := s.checkDNSChallenge(ctx, site.AllDomains()); err != nil {
			return store.Site{}, err
		}
	}

	old, err := s.st.SetSiteTLSChallenge(ctx, name, challengeType, s.now())
	if err != nil {
		return store.Site{}, err
	}

	if old != challengeType {
		s.auditChange(ctx, actor, "site.tls.challenge", name, nil,
			map[string]string{"challenge": old}, map[string]string{"challenge": challengeType})
		s.Kick()
	}

	return s.Get(ctx, name)
}

// SetPanelTLSChallenge is SetTLSChallenge for the panel address.
func (s *Service) SetPanelTLSChallenge(ctx context.Context, actor int64, challengeType string) (PanelTLS, error) {
	if err := validChallenge(challengeType); err != nil {
		return PanelTLS{}, err
	}

	if s.panelDomain == "" {
		return PanelTLS{}, ErrNoPanel
	}

	if challengeType == store.ChallengeDNS {
		if err := s.checkDNSChallenge(ctx, []string{s.panelDomain}); err != nil {
			return PanelTLS{}, err
		}
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	p, err := s.PanelTLS(ctx)
	if err != nil {
		return PanelTLS{}, err
	}

	old := p.challenge()
	if old == challengeType {
		return p, nil
	}

	p.Challenge, p.Error, p.FailedAt = challengeType, "", time.Time{}
	if err := s.savePanelTLS(ctx, p); err != nil {
		return PanelTLS{}, err
	}

	s.auditChange(ctx, actor, "panel.tls.challenge", s.panelDomain, nil,
		map[string]string{"challenge": old}, map[string]string{"challenge": challengeType})
	s.Kick()

	return p, nil
}
