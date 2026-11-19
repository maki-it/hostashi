// SPDX-License-Identifier: AGPL-3.0-or-later

package agentapi

import (
	"errors"
	"fmt"
	"net/netip"
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
	// Disabled answers every request of the site with 503 instead of
	// serving its files. ACME challenges and HTTPS stay; PHP must be empty.
	Disabled bool `json:"disabled,omitempty"`
	// Maintenance answers requests from addresses outside its allowlist
	// with its page and status 503 (ADR-0027). Disabled wins over it.
	Maintenance *WebMaintenance `json:"maintenance,omitempty"`
}

// WebMaintenance is the maintenance mode of a site: a committed page and
// the addresses that still see the site.
type WebMaintenance struct {
	Page     string   `json:"page"`
	Digest   string   `json:"digest"`
	AllowIPs []string `json:"allowIps,omitempty"` // canonical addresses or CIDRs
}

// Validate checks the page and that every entry is an IP address or CIDR in
// its canonical form, so it can be written into the nginx configuration.
func (m WebMaintenance) Validate() error {
	if err := ValidatePageID(m.Page); err != nil {
		return err
	}

	if err := ValidatePageDigest(m.Digest); err != nil {
		return err
	}

	if len(m.AllowIPs) > MaxAllowIPs {
		return fmt.Errorf("at most %d allowed addresses", MaxAllowIPs)
	}

	for _, ip := range m.AllowIPs {
		c, err := CanonicalIPOrPrefix(ip)
		if err != nil {
			return err
		}

		if c != ip {
			return fmt.Errorf("allowed address %q is not canonical (%s)", ip, c)
		}
	}

	return nil
}

// CanonicalIPOrPrefix parses an IP address or CIDR and returns its canonical
// form: addresses without zone, prefixes masked ("10.1.2.3/8" is refused,
// "10.0.0.0/8" is fine), IPv4-mapped IPv6 as IPv4.
func CanonicalIPOrPrefix(s string) (string, error) {
	if strings.Contains(s, "/") {
		p, err := netip.ParsePrefix(s)
		if err != nil {
			return "", fmt.Errorf("invalid address range %q", s)
		}

		if p.Masked() != p {
			return "", fmt.Errorf("address range %q has host bits set, use %s", s, p.Masked())
		}

		if p.Addr().Is4In6() {
			return "", fmt.Errorf("use the IPv4 form for %q", s)
		}

		return p.String(), nil
	}

	a, err := netip.ParseAddr(s)
	if err != nil || a.Zone() != "" {
		return "", fmt.Errorf("invalid address %q", s)
	}

	return a.Unmap().String(), nil
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
	// Parked are domains without a site that show a parking page.
	Parked []WebParked `json:"parked,omitempty"`
}

// MaxParked bounds the parked domains of a server.
const MaxParked = 500

// WebParked is a parked domain (ADR-0027): its page with status 200, over
// HTTPS with the certificate Name when TLS is set.
type WebParked struct {
	Name   string `json:"name"` // ParkName, also the certificate name
	Domain string `json:"domain"`
	Page   string `json:"page"`
	Digest string `json:"digest"`
	TLS    bool   `json:"tls,omitempty"`
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

		if s.Maintenance != nil {
			if err := s.Maintenance.Validate(); err != nil {
				return fmt.Errorf("site %q: maintenance: %w", s.User, err)
			}
		}

		if s.Disabled && s.PHP != "" {
			return fmt.Errorf("site %q: a disabled site runs no PHP", s.User)
		}

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

		domains[r.Panel.Domain] = true
	}

	if len(r.Parked) > MaxParked {
		return fmt.Errorf("too many parked domains (max %d)", MaxParked)
	}

	names := map[string]bool{}

	for _, p := range r.Parked {
		if err := ValidateParkName(p.Name); err != nil {
			return err
		}

		if names[p.Name] {
			return fmt.Errorf("parked name %q listed twice", p.Name)
		}

		names[p.Name] = true

		if err := ValidateDomain(p.Domain); err != nil {
			return fmt.Errorf("parked: %w", err)
		}

		if domains[p.Domain] {
			return errors.New("parked domain " + p.Domain + " is used elsewhere")
		}

		domains[p.Domain] = true

		if err := ValidatePageID(p.Page); err != nil {
			return fmt.Errorf("parked %s: %w", p.Domain, err)
		}

		if err := ValidatePageDigest(p.Digest); err != nil {
			return fmt.Errorf("parked %s: %w", p.Domain, err)
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
