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
	"github.com/maki-it/hostashi/internal/nginxconf"
	"github.com/maki-it/hostashi/internal/snippet"
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
	// DisabledPage is the stored page a disabled site shows with its 503
	// (ADR-0047), without an allowlist; nil shows a fixed text.
	DisabledPage *PageRef `json:"disabledPage,omitempty"`
	// Maintenance serves a stored page with 503 to every address outside
	// its allowlist (ADR-0027). Disabled wins over it.
	Maintenance *Maintenance `json:"maintenance,omitempty"`
	// Home is the site's home directory; custom snippets may only point
	// below it. Required when Snippets is set.
	Home string `json:"home,omitempty"`
	// Snippets are the site's custom nginx snippets (ADR-0029). They are
	// checked against the allowlist in Validate and rendered from the
	// checked directives, and left out while the site is disabled or in
	// maintenance.
	Snippets Snippets `json:"snippets,omitzero"`
	// PageCache caches the answers of the site's PHP (ADR-0046). It needs
	// PHPSocket and is left out while the site is disabled or in
	// maintenance.
	PageCache *PageCache `json:"pageCache,omitempty"`
	// Headers are the site's security headers (ADR-0056), sent on every
	// answer of the site. A snippet must not set one of them as well.
	Headers agentapi.WebHeaders `json:"headers,omitzero"`
	// BodyMax is the site's client_max_body_size in bytes, a whole number
	// of MiB, and FastCGIReadTimeout its fastcgi_read_timeout in seconds;
	// both follow the site's PHP settings (ADR-0049) and need PHPSocket.
	// 0 keeps nginx's default (1 MiB, 60 s). A server snippet that sets
	// the same directive wins.
	BodyMax            uint64 `json:"bodyMax,omitempty"`
	FastCGIReadTimeout int    `json:"fastcgiReadTimeout,omitempty"`
	// SPA answers unknown paths with /index.html, AssetCache sends 30 days
	// of Cache-Control on assets (ADR-0054). Both only without PHPSocket.
	SPA        bool `json:"spa,omitempty"`
	AssetCache bool `json:"assetCache,omitempty"`
	// AssetCacheSeconds and AssetCacheExts refine AssetCache; zero values
	// use agentapi's defaults.
	AssetCacheSeconds int      `json:"assetCacheSeconds,omitempty"`
	AssetCacheExts    []string `json:"assetCacheExts,omitempty"`
}

// Bounds of BodyMax and FastCGIReadTimeout (ADR-0049).
const (
	MaxBodyMax            uint64 = 2 << 30
	MinFastCGIReadTimeout        = 60
	MaxFastCGIReadTimeout        = 3700
)

func (s Site) checkPHPLimits() error {
	if (s.BodyMax != 0 || s.FastCGIReadTimeout != 0) && s.PHPSocket == "" {
		return errors.New("body size and read timeout need PHP")
	}

	if s.BodyMax%(1<<20) != 0 || s.BodyMax > MaxBodyMax {
		return fmt.Errorf("body size %d: must be whole MiB up to %d", s.BodyMax, MaxBodyMax)
	}

	if t := s.FastCGIReadTimeout; t != 0 && (t < MinFastCGIReadTimeout || t > MaxFastCGIReadTimeout) {
		return fmt.Errorf("read timeout %d: must be between %d and %d seconds", t, MinFastCGIReadTimeout, MaxFastCGIReadTimeout)
	}

	return nil
}

// Snippets are the custom snippets of a site, one per slot.
type Snippets struct {
	// Server goes into the server block that serves the site.
	Server string `json:"server,omitempty"`
	// Root goes into the site's "location /".
	Root string `json:"root,omitempty"`
}

// IsZero reports whether no snippet is set.
func (s Snippets) IsZero() bool { return s.Server == "" && s.Root == "" }

// check runs the allowlist check (package snippet) for both slots.
func (s Site) checkSnippets() (server, root []nginxconf.Directive, err error) {
	if s.Snippets.IsZero() {
		return nil, nil, nil
	}

	ss := snippet.Site{Home: s.Home, PHPSocket: s.PHPSocket}

	server, err = snippet.Check(s.Snippets.Server, snippet.SlotServer, ss)
	if err != nil {
		return nil, nil, fmt.Errorf("server snippet: %w", err)
	}

	root, err = snippet.Check(s.Snippets.Root, snippet.SlotRoot, ss)
	if err != nil {
		return nil, nil, fmt.Errorf("location / snippet: %w", err)
	}

	return server, root, nil
}

// Maintenance is the maintenance mode of a site. Dir is the committed page
// directory (package pages); nginx serves it under agentapi.PagePathPrefix.
type Maintenance struct {
	Page     string   `json:"page"`
	Digest   string   `json:"digest"`
	Dir      string   `json:"dir"`
	AllowIPs []string `json:"allowIps,omitempty"`
}

// PageRef is a committed page version and its directory (package pages).
type PageRef struct {
	Page   string `json:"page"`
	Digest string `json:"digest"`
	Dir    string `json:"dir"`
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
	// DBTool serves the web database tool (ADR-0038) on the driver's tool
	// port, with the panel's certificate. Without one it is not served.
	DBTool bool `json:"dbTool,omitempty"`
	// Allow limits the panel's HTTPS server to these addresses and
	// prefixes (nginx allow, then deny all); empty allows everyone. The
	// agent sets it from the firewall's panel allowlist when the panel
	// shares port 443 with the sites (ADR-0042).
	Allow []string `json:"allow,omitempty"`
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

		if p := s.DisabledPage; p != nil {
			if !s.Disabled {
				return fmt.Errorf("site %q: a disabled page needs a disabled site", s.User)
			}

			if err := (agentapi.WebPageRef{Page: p.Page, Digest: p.Digest}).Validate(); err != nil {
				return fmt.Errorf("site %q: disabled page: %w", s.User, err)
			}

			if !rootRe.MatchString(p.Dir) || path.Clean(p.Dir) != p.Dir {
				return fmt.Errorf("site %q: invalid page directory %q", s.User, p.Dir)
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

		if _, _, err := s.checkSnippets(); err != nil {
			return fmt.Errorf("site %q: %w", s.User, err)
		}

		if err := s.checkHeaders(); err != nil {
			return fmt.Errorf("site %q: %w", s.User, err)
		}

		if err := s.checkPHPLimits(); err != nil {
			return fmt.Errorf("site %q: %w", s.User, err)
		}

		if err := agentapi.ValidateAssetCache(s.AssetCacheSeconds, s.AssetCacheExts); err != nil {
			return fmt.Errorf("site %q: %w", s.User, err)
		}

		if s.SPA && s.PHPSocket != "" {
			return fmt.Errorf("site %q: single-page app is for static sites", s.User)
		}

		if c := s.PageCache; c != nil {
			if s.PHPSocket == "" {
				return fmt.Errorf("site %q: the page cache needs PHP", s.User)
			}

			if err := c.validate(); err != nil {
				return fmt.Errorf("site %q: %w", s.User, err)
			}
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

		// Only the canonical form reaches nginx: no other syntax fits.
		for _, a := range p.Allow {
			pfx, err := agentapi.ParsePrefix(a)

			canonical := a == pfx.String() || (pfx.IsSingleIP() && a == pfx.Addr().String())
			if err != nil || pfx.Bits() == 0 || !canonical {
				return fmt.Errorf("panel: invalid allowed address %q", a)
			}
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
