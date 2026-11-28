// SPDX-License-Identifier: AGPL-3.0-or-later

package hosting

import (
	"context"
	"fmt"
	"slices"
	"sort"
	"strings"

	"github.com/maki-it/hostashi/internal/agentapi"
	"github.com/maki-it/hostashi/internal/store"
)

// Wildcard domains on redirect and reverse proxy sites (ADR-0075).

// wildcardType reports whether sites of a type can have a wildcard domain.
func wildcardType(typ string) bool { return noFiles(typ) }

// wildcardsOf returns the wildcard patterns among domains, in order.
func wildcardsOf(domains []string) []string {
	var out []string

	for _, d := range domains {
		if agentapi.IsWildcard(d) {
			out = append(out, d)
		}
	}

	return out
}

// errWildcardHTTP01 refuses Let's Encrypt over HTTP-01 for wildcards: a
// CA checks them only over DNS-01.
func errWildcardHTTP01(w []string) error {
	return fmt.Errorf("%w: Let's Encrypt checks %s only over DNS; add a DNS account and choose DNS, or upload a certificate that covers it",
		ErrInvalid, strings.Join(w, ", "))
}

// checkWildcardTLS refuses a TLS setting that would order a certificate
// for a wildcard over HTTP-01: HTTPS on, Let's Encrypt and HTTP.
func checkWildcardTLS(tls bool, source, challengeType string, domains []string) error {
	if !tls || source == store.TLSCustom || challengeType == store.ChallengeDNS {
		return nil
	}

	if w := wildcardsOf(domains); len(w) > 0 {
		return errWildcardHTTP01(w)
	}

	return nil
}

// nginxMatches reports whether nginx's name pattern matches host: equal,
// or a leading wildcard with host at any depth below it.
func nginxMatches(pattern, host string) bool {
	if rest, ok := strings.CutPrefix(pattern, agentapi.WildcardPrefix); ok {
		return strings.HasSuffix(host, "."+rest)
	}

	return pattern == host
}

// hostOwners are the names nginx serves, apart from the site being
// checked: exact names and wildcards of other sites, parked domains and
// the panel address.
type hostOwners struct {
	exact     map[string]bool
	wildcards []string
}

func (s *Service) otherOwners(ctx context.Context, name string) (hostOwners, error) {
	o := hostOwners{exact: map[string]bool{}}

	sites, err := s.st.Sites(ctx)
	if err != nil {
		return o, err
	}

	for _, site := range sites {
		if site.Name == name {
			continue
		}

		for _, d := range site.AllDomains() {
			if agentapi.IsWildcard(d) {
				o.wildcards = append(o.wildcards, d)
			} else {
				o.exact[d] = true
			}
		}
	}

	parked, err := s.st.ParkedDomains(ctx)
	if err != nil {
		return o, err
	}

	for _, p := range parked {
		o.exact[p.Domain] = true
	}

	if s.panelDomain != "" {
		o.exact[s.panelDomain] = true
	}

	return o, nil
}

// servesHost reports whether a site with domains would get a request for
// host, in nginx's order: an exact name first, then the longest leading
// wildcard.
func (o hostOwners) servesHost(domains []string, host string) bool {
	if slices.Contains(domains, host) {
		return true
	}

	if o.exact[host] {
		return false
	}

	best, mine := 0, false

	for _, w := range wildcardsOf(domains) {
		if nginxMatches(w, host) && len(w) > best {
			best, mine = len(w), true
		}
	}

	for _, w := range o.wildcards {
		if nginxMatches(w, host) && len(w) > best {
			best, mine = len(w), false
		}
	}

	return mine
}

// checkRedirectLoop refuses a redirect whose target this site would
// answer itself through one of its wildcards. A target that an exact
// name of another site, a parked domain, the panel or a longer wildcard
// serves is fine (Kim, 2026-11-28, ADR-0075 addendum 1).
func (s *Service) checkRedirectLoop(ctx context.Context, name string, r store.SiteRedirect, domains []string) error {
	if len(wildcardsOf(domains)) == 0 {
		return nil
	}

	u, err := agentapi.ParseRedirectTarget(r.Target, domains)
	if err != nil {
		return fmt.Errorf("%w: %w", ErrInvalid, err)
	}

	o, err := s.otherOwners(ctx, name)
	if err != nil {
		return err
	}

	if host := strings.ToLower(u.Hostname()); o.servesHost(domains, host) {
		return fmt.Errorf("%w: redirect target: %s is served by this site through its wildcard domain and would redirect to itself", ErrInvalid, host)
	}

	return nil
}

// ServedElsewhere returns the names below the wildcards of site that
// other sites, parked domains or the panel serve instead, sorted. nginx
// sends them to their own block.
func (s *Service) ServedElsewhere(ctx context.Context, site store.Site) ([]string, error) {
	w := wildcardsOf(site.AllDomains())
	if len(w) == 0 {
		return []string{}, nil
	}

	o, err := s.otherOwners(ctx, site.Name)
	if err != nil {
		return nil, err
	}

	out := []string{}

	for host := range o.exact {
		for _, p := range w {
			if nginxMatches(p, host) {
				out = append(out, host)

				break
			}
		}
	}

	for _, other := range o.wildcards {
		for _, p := range w {
			if len(other) > len(p) && nginxMatches(p, strings.TrimPrefix(other, "*.")) {
				out = append(out, other)

				break
			}
		}
	}

	sort.Strings(out)

	return out, nil
}

// WildcardsWithoutDNS returns the wildcard domains of site that no DNS
// account serves, in order: Let's Encrypt cannot issue them (ADR-0075).
// Without DNS accounts in the panel it returns them all.
func (s *Service) WildcardsWithoutDNS(ctx context.Context, site store.Site) ([]string, error) {
	w := wildcardsOf(site.AllDomains())
	if len(w) == 0 || s.dns == nil {
		return w, nil
	}

	accounts, err := s.st.DNSAccounts(ctx)
	if err != nil {
		return nil, err
	}

	var out []string

	for _, d := range w {
		if _, ok := accountFor(accounts, s.challengeNameOf(ctx, d)); !ok {
			out = append(out, d)
		}
	}

	return out, nil
}
