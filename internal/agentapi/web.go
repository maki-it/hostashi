// SPDX-License-Identifier: AGPL-3.0-or-later

package agentapi

import (
	"errors"
	"fmt"
	"strings"

	"github.com/maki-it/hostashi/internal/agentrpc"
)

// MaxDomainsPerSite bounds the server_name list of one site.
const MaxDomainsPerSite = 50

// ValidateDomain accepts a lower-case host name of at least two labels made
// of letters, digits and inner hyphens (internationalized names in their
// xn-- form). No trailing dot, no wildcard, no IP address.
func ValidateDomain(d string) error {
	if len(d) < 3 || len(d) > 253 {
		return fmt.Errorf("invalid domain %q: length", d)
	}

	labels := strings.Split(d, ".")
	if len(labels) < 2 {
		return fmt.Errorf("invalid domain %q: needs at least two labels", d)
	}

	for _, l := range labels {
		if !validLabel(l) {
			return fmt.Errorf("invalid domain %q", d)
		}
	}

	// The last label of a host name is never all digits (rules out IPv4).
	if strings.Trim(labels[len(labels)-1], "0123456789") == "" {
		return fmt.Errorf("invalid domain %q: numeric top-level label", d)
	}

	return nil
}

func validLabel(l string) bool {
	if l == "" || len(l) > 63 || l[0] == '-' || l[len(l)-1] == '-' {
		return false
	}

	for i := range len(l) {
		c := l[i]
		if (c < 'a' || c > 'z') && (c < '0' || c > '9') && c != '-' {
			return false
		}
	}

	return true
}

// WebSite is the web server state of one registered site. The agent takes
// the web root from its own registry, never from the caller.
type WebSite struct {
	User    string   `json:"user"`
	Domains []string `json:"domains"` // the first one is the primary domain
	// PHP is the PHP version of the site's FPM pool, empty for a site
	// without PHP (static files only).
	PHP string `json:"php,omitempty"`
	// TLS serves the site over HTTPS with its live certificate (tls.install)
	// and redirects HTTP to HTTPS. The certificate must name every domain.
	TLS bool `json:"tls,omitempty"`
	// HSTS adds Strict-Transport-Security to HTTPS responses. Without TLS
	// it has no effect.
	HSTS bool `json:"hsts,omitempty"`
}

// WebPanel is the panel's own address (ADR-0023). The agent serves it on
// its configured panel port and proxies to the panel's socket; the caller
// names neither.
type WebPanel struct {
	Domain string `json:"domain"`
	// TLS serves the panel with the live PanelCert certificate. Without it
	// port 80 answers only ACME challenges for the domain.
	TLS bool `json:"tls,omitempty"`
}

// WebApplyRequest is the complete desired web server state. Sites that are
// not listed get no virtual host.
type WebApplyRequest struct {
	Sites []WebSite `json:"sites"`
	Panel *WebPanel `json:"panel,omitempty"`
}

// Validate checks names and that no user or domain appears twice.
func (r WebApplyRequest) Validate() error {
	if len(r.Sites) > MaxSites {
		return fmt.Errorf("too many sites (max %d)", MaxSites)
	}

	users := make(map[string]bool, len(r.Sites))
	domains := map[string]bool{}

	for _, s := range r.Sites {
		if err := ValidateSiteUser(s.User); err != nil {
			return err
		}

		if users[s.User] {
			return fmt.Errorf("site %q listed twice", s.User)
		}

		users[s.User] = true

		if s.PHP != "" {
			if err := ValidatePHPVersion(s.PHP); err != nil {
				return fmt.Errorf("site %q: %w", s.User, err)
			}
		}

		if len(s.Domains) == 0 || len(s.Domains) > MaxDomainsPerSite {
			return fmt.Errorf("site %q: between 1 and %d domains required", s.User, MaxDomainsPerSite)
		}

		for _, d := range s.Domains {
			if err := ValidateDomain(d); err != nil {
				return err
			}

			if domains[d] {
				return errors.New("domain " + d + " is used twice")
			}

			domains[d] = true
		}
	}

	if r.Panel != nil {
		if err := ValidateDomain(r.Panel.Domain); err != nil {
			return fmt.Errorf("panel: %w", err)
		}

		if domains[r.Panel.Domain] {
			return errors.New("domain " + r.Panel.Domain + " is the panel's address")
		}
	}

	return nil
}

// WebApplyResponse names the live generation after the call.
type WebApplyResponse struct {
	Generation int  `json:"generation"`
	Changed    bool `json:"changed"` // false: the state was already live
}

// WebApply makes the web server state live. First the PHP-FPM pool of every
// site with PHP is started or changed, then the virtual hosts are rendered,
// validated with nginx -t, switched atomically, reloaded and verified, and
// last the pools of sites that lost PHP are stopped. On any failure the
// previous generation stays or is restored.
var WebApply = agentrpc.Op[WebApplyRequest, WebApplyResponse]{Name: "web.apply"}
