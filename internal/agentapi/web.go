// SPDX-License-Identifier: AGPL-3.0-or-later

package agentapi

import (
	"errors"
	"fmt"
	"net/netip"
	"regexp"
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
	// PHPWorkers is the most PHP worker processes of the site's pool
	// (ADR-0015), 0 for the default (DefaultPHPWorkers).
	PHPWorkers int `json:"phpWorkers,omitempty"`
	// PHPPrograms lets the site's PHP start programs (exec, system, ...);
	// without it the pool disables those functions (ADR-0033).
	PHPPrograms bool `json:"phpPrograms,omitempty"`
	// PHPSettings are the site's PHP ini settings (ADR-0049); nil uses
	// the defaults. They need a PHP version.
	PHPSettings *PHPSettings `json:"phpSettings,omitempty"`
	// TLS serves the site over HTTPS with its live certificate (tls.install)
	// and redirects HTTP to HTTPS. The certificate must name every domain.
	TLS bool `json:"tls,omitempty"`
	// HSTS adds Strict-Transport-Security to HTTPS responses. Without TLS
	// it has no effect.
	HSTS bool `json:"hsts,omitempty"`
	// Disabled answers every request of the site with 503 instead of
	// serving its files. ACME challenges and HTTPS stay; PHP must be empty.
	Disabled bool `json:"disabled,omitempty"`
	// DisabledPage is the committed page a disabled site shows with its
	// 503 (ADR-0047); nil shows a fixed text. Only with Disabled.
	DisabledPage *WebPageRef `json:"disabledPage,omitempty"`
	// Maintenance answers requests from addresses outside its allowlist
	// with its page and status 503 (ADR-0027). Disabled wins over it.
	Maintenance *WebMaintenance `json:"maintenance,omitempty"`
	// Snippets are the site's custom nginx snippets (ADR-0029). The agent
	// checks them against the allowlist itself; they are not rendered while
	// the site is disabled or in maintenance.
	Snippets *WebSnippets `json:"snippets,omitempty"`
	// PageCache caches the answers of the site's PHP (ADR-0046); nil
	// caches nothing. It needs PHP and is not used while the site is in
	// maintenance.
	PageCache *WebPageCache `json:"pageCache,omitempty"`
	// WebDir is the document root below the site's htdocs (ADR-0054
	// addendum 1), for example "public" or "dist"; empty serves htdocs.
	WebDir string `json:"webDir,omitempty"`
	// SPA answers unknown paths with /index.html instead of 404, for
	// single-page apps. Static sites only.
	SPA bool `json:"spa,omitempty"`
	// AssetCache sends Cache-Control with AssetCacheSeconds on files with
	// one of AssetCacheExts (lowercase, without the dot); HTML stays fresh
	// unless listed. Static sites only. Empty values use the defaults.
	AssetCache        bool     `json:"assetCache,omitempty"`
	AssetCacheSeconds int      `json:"assetCacheSeconds,omitempty"`
	AssetCacheExts    []string `json:"assetCacheExts,omitempty"`
}

// Bounds and defaults of the asset cache (ADR-0054 addendum 1).
const (
	MinAssetCacheSeconds     = 60
	MaxAssetCacheSeconds     = 365 * 24 * 3600
	DefaultAssetCacheSeconds = 30 * 24 * 3600
	MaxAssetCacheExts        = 40
)

// DefaultAssetCacheExts are the file types cached by default: stylesheets,
// scripts, fonts and images.
var DefaultAssetCacheExts = []string{"css", "js", "mjs", "woff", "woff2", "ttf", "otf", "eot", "svg", "png", "jpg", "jpeg", "gif", "webp", "avif", "ico"}

var assetExtRe = regexp.MustCompile(`^[a-z0-9]{1,10}$`)

// ValidateAssetCache checks the duration (0: default) and the extensions
// (empty: default): at most MaxAssetCacheExts, each 1 to 10 lowercase
// letters or digits, no duplicates.
func ValidateAssetCache(seconds int, exts []string) error {
	if seconds != 0 && (seconds < MinAssetCacheSeconds || seconds > MaxAssetCacheSeconds) {
		return fmt.Errorf("asset cache time must be between %d seconds and %d days", MinAssetCacheSeconds, MaxAssetCacheSeconds/86400)
	}

	if len(exts) > MaxAssetCacheExts {
		return fmt.Errorf("at most %d file types can be cached", MaxAssetCacheExts)
	}

	seen := map[string]bool{}

	for _, e := range exts {
		if !assetExtRe.MatchString(e) {
			return fmt.Errorf("file type %q: use 1 to 10 lowercase letters or digits, without the dot", e)
		}

		if seen[e] {
			return fmt.Errorf("file type %q is listed twice", e)
		}

		seen[e] = true
	}

	return nil
}

// MaxWebDir bounds the document root folder: length and number of levels.
const (
	MaxWebDir       = 255
	MaxWebDirLevels = 4
)

var webDirSegment = regexp.MustCompile(`^[A-Za-z0-9._-]{1,64}$`)

// ValidateWebDir checks a document root folder below htdocs: relative,
// at most MaxWebDirLevels segments of letters, digits, dot, underscore and
// dash, no "." or "..", no empty segment. Empty is valid (htdocs itself).
func ValidateWebDir(dir string) error {
	if dir == "" {
		return nil
	}

	if len(dir) > MaxWebDir {
		return fmt.Errorf("document root folder is longer than %d bytes", MaxWebDir)
	}

	segs := strings.Split(dir, "/")
	if len(segs) > MaxWebDirLevels {
		return fmt.Errorf("document root folder has more than %d levels", MaxWebDirLevels)
	}

	for _, seg := range segs {
		if seg == "." || seg == ".." || !webDirSegment.MatchString(seg) {
			return fmt.Errorf("document root folder %q: use folder names of letters, digits, '.', '_' and '-', separated by '/'", dir)
		}
	}

	return nil
}

// PHP workers of a site's pool (pm.max_children, ADR-0015).
const (
	DefaultPHPWorkers = 5
	MaxPHPWorkers     = 500
)

// MaxSnippetBytes bounds one custom snippet (ADR-0029).
const MaxSnippetBytes = 64 << 10

// WebSnippets are the custom snippets of a site, one per slot.
type WebSnippets struct {
	// Server goes into the server block that serves the site.
	Server string `json:"server,omitempty"`
	// Root goes into the site's "location /".
	Root string `json:"root,omitempty"`
}

// WebPageRef names a committed page version.
type WebPageRef struct {
	Page   string `json:"page"`
	Digest string `json:"digest"`
}

// Validate checks the page id and digest.
func (r WebPageRef) Validate() error {
	if err := ValidatePageID(r.Page); err != nil {
		return err
	}

	return ValidatePageDigest(r.Digest)
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
	// DBTool also serves the web database tool (ADR-0038) on its own port
	// with the panel's certificate; only together with TLS.
	DBTool bool `json:"dbTool,omitempty"`
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

		if s.DisabledPage != nil {
			if !s.Disabled {
				return fmt.Errorf("site %q: a disabled page needs a disabled site", s.User)
			}

			if err := s.DisabledPage.Validate(); err != nil {
				return fmt.Errorf("site %q: disabled page: %w", s.User, err)
			}
		}

		if s.Disabled && s.PHP != "" {
			return fmt.Errorf("site %q: a disabled site runs no PHP", s.User)
		}

		if sn := s.Snippets; sn != nil && (len(sn.Server) > MaxSnippetBytes || len(sn.Root) > MaxSnippetBytes) {
			return fmt.Errorf("site %q: a snippet is larger than %d bytes", s.User, MaxSnippetBytes)
		}

		if s.PHP != "" {
			if err := ValidatePHPVersion(s.PHP); err != nil {
				return fmt.Errorf("site %q: %w", s.User, err)
			}
		}

		if err := ValidateWebDir(s.WebDir); err != nil {
			return fmt.Errorf("site %q: %w", s.User, err)
		}

		if err := ValidateAssetCache(s.AssetCacheSeconds, s.AssetCacheExts); err != nil {
			return fmt.Errorf("site %q: %w", s.User, err)
		}

		if (s.SPA || s.AssetCache) && s.PHP != "" {
			return fmt.Errorf("site %q: single-page app and asset caching are for static sites", s.User)
		}

		if s.PHPWorkers < 0 || s.PHPWorkers > MaxPHPWorkers {
			return fmt.Errorf("site %q: php workers must be 0 (default) or between 1 and %d", s.User, MaxPHPWorkers)
		}

		if s.PHPPrograms && s.PHP == "" {
			return fmt.Errorf("site %q: php programs need a PHP version", s.User)
		}

		if ps := s.PHPSettings; ps != nil {
			if s.PHP == "" {
				return fmt.Errorf("site %q: php settings need a PHP version", s.User)
			}

			if err := ps.Validate(); err != nil {
				return fmt.Errorf("site %q: %w", s.User, err)
			}
		}

		if c := s.PageCache; c != nil {
			if s.PHP == "" {
				return fmt.Errorf("site %q: the page cache needs a PHP version", s.User)
			}

			if err := c.Validate(); err != nil {
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
