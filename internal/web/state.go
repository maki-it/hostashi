// SPDX-License-Identifier: AGPL-3.0-or-later

// Package web turns the desired web server state into nginx configuration
// "generations": render, validate with nginx -t, switch atomically, reload,
// prove that the new generation is live, and roll back otherwise
// (docs/04-architecture.md 4.3, spike S2, ADR-0005).
package web

import (
	"errors"
	"fmt"
	"path"
	"regexp"

	"github.com/maki-it/hostashi/internal/agentapi"
)

// Site is the desired state of one virtual host. The agent fills Root from
// its registry; every field ends up in a config file and is validated
// positively.
type Site struct {
	User    string   `json:"user"`
	Domains []string `json:"domains"`
	Root    string   `json:"root"` // absolute document root
	// PHPSocket is the site's PHP-FPM socket; empty serves static files only.
	PHPSocket string `json:"phpSocket,omitempty"`
	// Cert and Key are the site's certificate chain and key files; empty
	// serves the site over HTTP only. With them, HTTP redirects to HTTPS.
	Cert string `json:"cert,omitempty"`
	Key  string `json:"key,omitempty"`
	// HSTS adds Strict-Transport-Security to the HTTPS responses (one year,
	// without includeSubDomains or preload). Ignored without a certificate.
	HSTS bool `json:"hsts,omitempty"`
	// Disabled answers every request with 503 and never serves Root.
	// ACME challenges, the health check and the HTTPS redirect stay.
	Disabled bool `json:"disabled,omitempty"`
	// Maintenance serves a stored page with 503 to every address outside
	// its allowlist (ADR-0027). Disabled wins over it.
	Maintenance *Maintenance `json:"maintenance,omitempty"`
}

// Maintenance is the maintenance mode of a site. Dir is the committed page
// directory (package pages); nginx serves it under agentapi.PagePathPrefix.
type Maintenance struct {
	Page     string   `json:"page"`
	Digest   string   `json:"digest"`
	Dir      string   `json:"dir"`
	AllowIPs []string `json:"allowIps,omitempty"`
}

// Desired is the complete web server state.
type Desired struct {
	Sites []Site `json:"sites"`
	// Panel is the panel's own address (ADR-0023); nil serves none.
	Panel *Panel `json:"panel,omitempty"`
	// Parked are domains without a site that show a page (ADR-0027).
	Parked []Parked `json:"parked,omitempty"`
}

// Parked is a parked domain: its page with status 200 on every path. Name
// is agentapi.ParkName of it and also its config and certificate name.
type Parked struct {
	Name   string `json:"name"`
	Domain string `json:"domain"`
	Page   string `json:"page"`
	Digest string `json:"digest"`
	Dir    string `json:"dir"`
	Cert   string `json:"cert,omitempty"`
	Key    string `json:"key,omitempty"`
}

// Panel is served on the driver's panel port and proxied to its upstream.
// Without a certificate, port 80 answers only ACME challenges for Domain.
type Panel struct {
	Domain string `json:"domain"`
	Cert   string `json:"cert,omitempty"`
	Key    string `json:"key,omitempty"`
}

// rootRe keeps nginx syntax out of the root path: no spaces, quotes,
// semicolons, braces or variables.
var rootRe = regexp.MustCompile(`^/[A-Za-z0-9_./-]+$`)

// Validate checks the desired state. Nothing is written for invalid input.
func (d Desired) Validate() error {
	req := agentapi.WebApplyRequest{Sites: make([]agentapi.WebSite, 0, len(d.Sites))}
	for _, s := range d.Sites {
		req.Sites = append(req.Sites, agentapi.WebSite{User: s.User, Domains: s.Domains})

		if m := s.Maintenance; m != nil {
			if err := (agentapi.WebMaintenance{Page: m.Page, Digest: m.Digest, AllowIPs: m.AllowIPs}).Validate(); err != nil {
				return fmt.Errorf("site %q: maintenance: %w", s.User, err)
			}

			if !rootRe.MatchString(m.Dir) || path.Clean(m.Dir) != m.Dir {
				return fmt.Errorf("site %q: invalid page directory %q", s.User, m.Dir)
			}
		}

		if !rootRe.MatchString(s.Root) || path.Clean(s.Root) != s.Root {
			return fmt.Errorf("site %q: invalid root %q", s.User, s.Root)
		}

		for name, p := range map[string]string{"PHP socket": s.PHPSocket, "certificate": s.Cert, "key": s.Key} {
			if p != "" && (!rootRe.MatchString(p) || path.Clean(p) != p) {
				return fmt.Errorf("site %q: invalid %s path %q", s.User, name, p)
			}
		}

		if (s.Cert == "") != (s.Key == "") {
			return fmt.Errorf("site %q: certificate and key go together", s.User)
		}
	}

	for _, p := range d.Parked {
		req.Parked = append(req.Parked, agentapi.WebParked{Name: p.Name, Domain: p.Domain, Page: p.Page, Digest: p.Digest})

		for name, f := range map[string]string{"page directory": p.Dir, "certificate": p.Cert, "key": p.Key} {
			if (f != "" || name == "page directory") && (!rootRe.MatchString(f) || path.Clean(f) != f) {
				return fmt.Errorf("parked %s: invalid %s path %q", p.Domain, name, f)
			}
		}

		if (p.Cert == "") != (p.Key == "") {
			return fmt.Errorf("parked %s: certificate and key go together", p.Domain)
		}
	}

	if p := d.Panel; p != nil {
		req.Panel = &agentapi.WebPanel{Domain: p.Domain}

		for name, f := range map[string]string{"certificate": p.Cert, "key": p.Key} {
			if f != "" && (!rootRe.MatchString(f) || path.Clean(f) != f) {
				return fmt.Errorf("panel: invalid %s path %q", name, f)
			}
		}

		if (p.Cert == "") != (p.Key == "") {
			return errors.New("panel: certificate and key go together")
		}
	}

	return req.Validate()
}

// Users returns the site users in d.
func (d Desired) Users() map[string]bool {
	out := make(map[string]bool, len(d.Sites))
	for _, s := range d.Sites {
		out[s.User] = true
	}

	return out
}

// Errors of the reconciler.
var (
	ErrInvalidDesired = errors.New("web: invalid desired state")
	// ErrValidation means nginx rejected the new config; nothing was changed.
	ErrValidation = errors.New("web: nginx rejected the generated config")
	// ErrApplyFailed means the new generation did not go live and the
	// previous one was restored.
	ErrApplyFailed = errors.New("web: apply failed, previous generation restored")
	// ErrRollbackFailed means the previous generation could not be restored
	// either; Recover at the next start tries again.
	ErrRollbackFailed = errors.New("web: apply failed and rollback failed")
	// ErrBusy means another process holds the reconciler lock.
	ErrBusy = errors.New("web: another reconcile run holds the lock")
)
