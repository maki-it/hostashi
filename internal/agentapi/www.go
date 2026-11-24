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
