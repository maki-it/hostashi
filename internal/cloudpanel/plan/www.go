// SPDX-License-Identifier: AGPL-3.0-or-later

package plan

import (
	"slices"

	"github.com/maki-it/hostashi/internal/agentapi"
)

// WWWRedirect maps the redirect hosts of a CloudPanel site to the www
// redirect of the new site (ADR-0065). A mode is chosen only when every
// name it would redirect is one of the old redirect hosts and goes to the
// old primary domain, so no name that the old site served starts to
// redirect. rest are the redirect hosts the mode does not cover; they stay
// aliases of the site.
func WWWRedirect(domains, redirectFrom []string) (mode string, rest []string) {
	rest = redirectFrom
	if len(domains) == 0 || len(redirectFrom) == 0 {
		return agentapi.WWWRedirectOff, rest
	}

	all := append(slices.Clone(domains), redirectFrom...)

	for _, m := range []string{agentapi.WWWRedirectToNonWWW, agentapi.WWWRedirectToWWW} {
		_, redir := agentapi.SplitWWW(all, m)
		if len(redir) == 0 {
			continue
		}

		covered := true

		for _, r := range redir {
			if !slices.Contains(redirectFrom, r.From) || r.To != domains[0] {
				covered = false

				break
			}
		}

		if !covered {
			continue
		}

		rest = nil

		for _, h := range redirectFrom {
			if !slices.ContainsFunc(redir, func(r agentapi.WWWName) bool { return r.From == h }) {
				rest = append(rest, h)
			}
		}

		return m, rest
	}

	return agentapi.WWWRedirectOff, redirectFrom
}
