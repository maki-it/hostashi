// SPDX-License-Identifier: AGPL-3.0-or-later

package agentapi

import (
	"fmt"
	"slices"
	"strings"
)

// www redirect modes of a site (ADR-0065). The empty string is "off".
const (
	WWWRedirectOff      = ""
	WWWRedirectToWWW    = "to-www"
	WWWRedirectToNonWWW = "to-non-www"
)

const wwwPrefix = "www."

// ValidateWWWRedirect checks a www redirect mode.
func ValidateWWWRedirect(mode string) error {
	switch mode {
	case WWWRedirectOff, WWWRedirectToWWW, WWWRedirectToNonWWW:
		return nil
	}

	return fmt.Errorf("invalid www redirect %q: want %q, %q or empty", mode, WWWRedirectToWWW, WWWRedirectToNonWWW)
}

// WWWName is one domain that redirects to its partner.
type WWWName struct {
	From string `json:"from"`
	To   string `json:"to"`
	// Domain: From is a redirect domain of the site (ADR-0068), not the
	// partner of a www pair.
	Domain bool `json:"-"`
}

// WWWPair is a domain d that does not start with "www." and the name
// "www."+d.
type WWWPair struct {
	Bare string `json:"bare"`
	WWW  string `json:"www"`
}

// WWWPairs returns the pairs among domains, in the order of the bare names.
// A domain d and "www."+d are a pair when both are listed and d does not
// start with "www." itself, so there are never chains.
func WWWPairs(domains []string) []WWWPair {
	var out []WWWPair

	for _, d := range domains {
		if strings.HasPrefix(d, wwwPrefix) {
			continue
		}

		if w := wwwPrefix + d; slices.Contains(domains, w) {
			out = append(out, WWWPair{Bare: d, WWW: w})
		}
	}

	return out
}

// WWWMissing returns the partner name each domain of the site would need
// for a pair: "www."+d for a bare d, d for "www."+d. Domains that are
// already in a pair, and names that cannot have a partner (the bare name
// would have one label, or www.www.*), are left out.
func WWWMissing(domains []string) []string {
	var out []string

	for _, d := range domains {
		var p string

		switch {
		case strings.HasPrefix(d, wwwPrefix):
			p = strings.TrimPrefix(d, wwwPrefix)
			if strings.HasPrefix(p, wwwPrefix) || ValidateDomain(p) != nil {
				continue
			}
		default:
			p = wwwPrefix + d
			if ValidateDomain(p) != nil {
				continue
			}
		}

		if !slices.Contains(domains, p) && !slices.Contains(out, p) {
			out = append(out, p)
		}
	}

	return out
}

// SplitWWW splits the domains of a site into the names it serves and the
// names that redirect to their partner under mode. Off, or a site without
// pairs, serves every domain. The order of domains is kept.
func SplitWWW(domains []string, mode string) (serve []string, redirect []WWWName) {
	pairs := WWWPairs(domains)
	if mode == WWWRedirectOff || len(pairs) == 0 {
		return domains, nil
	}

	to := make(map[string]string, len(pairs))

	for _, p := range pairs {
		if mode == WWWRedirectToWWW {
			to[p.Bare] = p.WWW
		} else {
			to[p.WWW] = p.Bare
		}
	}

	serve = make([]string, 0, len(domains)-len(pairs))

	for _, d := range domains {
		if t, ok := to[d]; ok {
			redirect = append(redirect, WWWName{From: d, To: t})
		} else {
			serve = append(serve, d)
		}
	}

	return serve, redirect
}

// SplitRedirects splits the names of a site into the names it serves and
// the names that redirect: the redirect domains (ADR-0068) go to the first
// name the site serves, then the www pairs of the remaining names follow
// mode (ADR-0065). domains are all names of the site, the primary one
// first; redirect is a subset without the primary domain.
func SplitRedirects(domains []string, mode string, redirect []string) (serve []string, out []WWWName) {
	if len(redirect) == 0 {
		return SplitWWW(domains, mode)
	}

	served := make([]string, 0, len(domains))

	for _, d := range domains {
		if !slices.Contains(redirect, d) {
			served = append(served, d)
		}
	}

	serve, out = SplitWWW(served, mode)
	if len(serve) == 0 {
		return serve, out
	}

	for _, d := range domains {
		if slices.Contains(redirect, d) {
			out = append(out, WWWName{From: d, To: serve[0], Domain: true})
		}
	}

	return serve, out
}

// ValidateRedirectDomains checks the redirect domains of a site against
// all its names: each one listed once, one of the names, never the
// primary domain.
func ValidateRedirectDomains(domains, redirect []string) error {
	seen := make(map[string]bool, len(redirect))

	for _, d := range redirect {
		switch {
		case len(domains) > 0 && d == domains[0]:
			return fmt.Errorf("the primary domain %s cannot redirect", d)
		case !slices.Contains(domains, d):
			return fmt.Errorf("redirect domain %s is not a domain of the site", d)
		case seen[d]:
			return fmt.Errorf("redirect domain %s listed twice", d)
		}

		seen[d] = true
	}

	return nil
}
