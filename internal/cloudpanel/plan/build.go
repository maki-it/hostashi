// SPDX-License-Identifier: AGPL-3.0-or-later

package plan

import (
	"fmt"
	"slices"
	"strings"

	"github.com/maki-it/hostashi/internal/agentapi"
	"github.com/maki-it/hostashi/internal/cloudpanel/scan"
	"github.com/maki-it/hostashi/internal/cron"
)

// Target is what already exists on this server.
type Target struct {
	Sites   []string // site names (= Linux users)
	Domains []string // every domain of every site
	// Panel is the panel's own host name, reserved against sites
	// (ADR-0042); empty for none.
	Panel string
	// PHP lists the PHP versions installed here (agent php.versions).
	PHP []string
	// Node lists the Node.js lines installed here, for example "24"
	// (ADR-0054).
	Node []string
	// Python lists the Python lines installed here, for example "3.13"
	// (ADR-0061).
	Python []string
}

// Action of one site in the plan.
type Action string

const (
	// ActionImport: the site can be created and filled as it is.
	ActionImport Action = "import"
	// ActionReview: the site can be imported after a person has looked at
	// the reasons (custom snippets, cron, PHP version and so on).
	ActionReview Action = "review"
	// ActionSkip: the site cannot be imported; the reasons say why.
	ActionSkip Action = "skip"
)

// TLSAction says how the site gets its certificate on this server.
type TLSAction string

const (
	TLSNone    TLSAction = "none"    // the old site has no HTTPS
	TLSReissue TLSAction = "reissue" // a new certificate after the DNS switch
	TLSCopy    TLSAction = "copy"    // a purchased certificate: copy cert and key
)

// Reason explains why a site is not a plain import.
type Reason struct {
	Code string `json:"code"`
	Msg  string `json:"msg"`
}

// Site is the plan for one site of the report.
type Site struct {
	Name         string   `json:"name"` // new site name, the old site user
	Type         string   `json:"type"`
	Domains      []string `json:"domains"`
	RedirectFrom []string `json:"redirectFrom"`
	// WWWRedirect is the www redirect of the new site that takes over
	// RedirectFrom's www host (ADR-0065), "" for none.
	WWWRedirect string `json:"wwwRedirect,omitempty"`
	// RedirectDomains are the RedirectFrom hosts the www redirect does
	// not cover; they become redirect domains of the new site (ADR-0068).
	RedirectDomains []string `json:"redirectDomains,omitempty"`
	DocRoot         string   `json:"docRoot"` // on the old server
	PHP             string   `json:"php"`
	// Proxy is the target of a reverse proxy site, the old proxy_pass
	// (ADR-0054); such a site gets no Linux account and no files.
	Proxy string `json:"proxy,omitempty"`
	// Node is the Node.js line of a Node.js site (ADR-0054).
	Node string `json:"node,omitempty"`
	// Python is the Python line of a Python site (ADR-0061).
	Python string    `json:"python,omitempty"`
	TLS    TLSAction `json:"tls"`
	Custom int       `json:"customSnippets"`
	Cron   int       `json:"cronJobs"`
	// CronImport of them are created paused with the site (ADR-0028).
	CronImport int `json:"cronImport"`
	// SSHKeys is how many of the site user's keys are carried over, as
	// SFTP only (ADR-0032); SSHImport how many the site got.
	SSHKeys   int `json:"sshKeys"`
	SSHImport int `json:"sshImport"`
	// ObjectCache: the old site uses an object cache, so the new one gets
	// its own (ADR-0045). ObjectCacheOn tells whether it was turned on.
	ObjectCache   bool `json:"objectCache"`
	ObjectCacheOn bool `json:"objectCacheOn"`
	// HomeBytes and HomeInodes are what the home uses on the old server,
	// zero if the scan did not measure it. The new site's disk limits start
	// from them (ADR-0015: use plus 50 %).
	HomeBytes  int64 `json:"homeBytes,omitempty"`
	HomeInodes int64 `json:"homeInodes,omitempty"`
	// PageCache is the page cache preset proposed for a site that had a
	// cache (likely Varnish) in front of it on the old server, "" for none
	// (ADR-0046).
	PageCache string `json:"pageCache,omitempty"`
	// PHPSettings is how many PHP settings of the old pool the site takes
	// over (ADR-0049); PHPSettingsOn tells whether they were set.
	PHPSettings   int      `json:"phpSettings"`
	PHPSettingsOn bool     `json:"phpSettingsOn"`
	Action        Action   `json:"action"`
	Reasons       []Reason `json:"reasons"`
	// Source is the vhost file on the old server, to find the site in the report.
	Source string `json:"source"`
}

// Summary counts the plan's sites by action.
type Summary struct {
	Sites  int `json:"sites"`
	Import int `json:"import"`
	Review int `json:"review"`
	Skip   int `json:"skip"`
}

// Plan is the migration plan for one report.
type Plan struct {
	Sites   []Site  `json:"sites"`
	Summary Summary `json:"summary"`
}

// Scanned site types the plan handles apart from static and PHP ones.
const (
	typeProxy  = "proxy"
	typeNode   = "nodejs"
	typePython = "python"
)

// supportedTypes are the site types Hostashi can run today. WordPress is a
// PHP site with an application profile (ADR-0017 Part 4); nodejs becomes a
// Node.js site, python a Python site (ADR-0061) and proxy a reverse proxy
// (ADR-0054).
var supportedTypes = []string{"static", "php", "wordpress", typeNode, typePython, typeProxy}

// Build makes the plan. It never changes rep.
func Build(rep *scan.Report, t Target) Plan {
	users := map[string]int{}
	domains := map[string]int{}

	for _, s := range rep.Sites {
		if s.Status == scan.StatusBlocked {
			continue
		}

		users[s.User]++

		for _, d := range append(slices.Clone(s.Domains), s.RedirectFrom...) {
			domains[d]++
		}
	}

	p := Plan{Sites: make([]Site, 0, len(rep.Sites))}

	for _, s := range rep.Sites {
		ps := siteOf(s, t, users, domains)
		p.Sites = append(p.Sites, ps)

		p.Summary.Sites++

		switch ps.Action {
		case ActionImport:
			p.Summary.Import++
		case ActionReview:
			p.Summary.Review++
		case ActionSkip:
			p.Summary.Skip++
		}
	}

	return p
}

func siteOf(s scan.Site, t Target, users, domains map[string]int) Site {
	ps := Site{
		Name: s.User, Type: s.Type, Domains: nonNil(s.Domains), RedirectFrom: nonNil(s.RedirectFrom),
		DocRoot: s.DocRoot, Custom: len(s.Custom), Cron: len(s.Cron), CronImport: len(CronJobs(s)),
		SSHKeys: len(SSHKeys(s)), ObjectCache: s.ObjectCache,
		Reasons: []Reason{}, Source: first(s.Sources),
	}
	if s.Usage != nil {
		ps.HomeBytes, ps.HomeInodes = s.Usage.Bytes, s.Usage.Inodes
	}

	if s.Pool != nil && s.Type != typeProxy && s.Type != typeNode && s.Type != typePython {
		ps.PHP = s.Pool.Version
	}

	// A reverse proxy site has no Linux account: nothing of the old home,
	// its cron jobs or its SSH keys comes with it.
	if s.Type == typeProxy {
		ps.CronImport, ps.SSHKeys, ps.ObjectCache, ps.HomeBytes, ps.HomeInodes = 0, 0, false, 0, 0
	}

	skip := func(code, msg string) { ps.Reasons = append(ps.Reasons, Reason{code, msg}); ps.Action = ActionSkip }
	review := func(code, msg string) {
		ps.Reasons = append(ps.Reasons, Reason{code, msg})
		if ps.Action == ActionImport {
			ps.Action = ActionReview
		}
	}

	ps.Action = ActionImport

	if s.Status == scan.StatusBlocked {
		for _, f := range s.Findings {
			if f.Severity == scan.SevBlocker {
				skip(f.Code, f.Msg)
			}
		}

		if ps.Action != ActionSkip {
			skip("scan_blocked", "the scan blocked this site")
		}

		ps.TLS = TLSNone

		return ps
	}

	if !slices.Contains(supportedTypes, s.Type) {
		skip("type_unsupported", fmt.Sprintf("site type %q is not supported by Hostashi yet", s.Type))
	}

	if users[s.User] > 1 {
		skip("user_twice", "two sites of the report use the user "+s.User)
	}

	if slices.Contains(t.Sites, s.User) {
		skip("site_exists", "a site named "+s.User+" exists on this server")
	}

	for _, d := range append(slices.Clone(s.Domains), s.RedirectFrom...) {
		if domains[d] > 1 {
			skip("domain_twice", "two sites of the report use the domain "+d)
		}

		if slices.Contains(t.Domains, d) {
			skip("domain_taken", "the domain "+d+" belongs to a site on this server")
		}

		if t.Panel != "" && d == t.Panel {
			skip("domain_reserved", "the domain "+d+" is the address of this panel")
		}
	}

	switch {
	case ps.PHP == "":
	case !slices.Contains(agentapi.PHPVersions, ps.PHP):
		skip("php_unsupported", "PHP "+ps.PHP+" is not supported by Hostashi")
	case !slices.Contains(t.PHP, ps.PHP):
		review("php_missing", "PHP "+ps.PHP+" is not installed here; install it or choose another version")
	case slices.Contains(agentapi.LegacyPHPVersions, ps.PHP):
		review("php_legacy", "PHP "+ps.PHP+" gets no or only security fixes upstream")
	}

	switch s.TLS.Kind {
	case "letsencrypt", "selfsigned", "missing":
		ps.TLS = TLSReissue
	case "custom":
		ps.TLS = TLSCopy

		review("tls_copy", "a purchased certificate: its key is copied from the old server")
	default:
		ps.TLS = TLSNone
	}

	switch s.Type {
	case typeProxy:
		proxyPlan(&ps, s, skip, review)
	case typeNode:
		nodePlan(&ps, s, t, review)
	case typePython:
		pythonPlan(&ps, s, t, review)
	}

	switch {
	case ps.Custom == 0:
	case s.Type == typeProxy:
		review("custom_snippets_proxy", fmt.Sprintf("%d nginx directives are not carried over: reverse proxy sites take no snippets yet", ps.Custom))
	default:
		review("custom_snippets", fmt.Sprintf("%d nginx directives need a one-time review (ADR-0017 Part 2)", ps.Custom))
	}

	if s.Type == typeProxy && ps.Cron > 0 {
		review("cron_no_account", fmt.Sprintf("%d cron jobs stay behind: a reverse proxy site has no Linux account to run them", ps.Cron))
	}

	if ps.CronImport > 0 {
		review("cron", fmt.Sprintf("%d cron jobs are created paused; start them after the DNS switch", ps.CronImport))
	}

	if n := ps.Cron - ps.CronImport; n > 0 && s.Type != typeProxy {
		review("cron_not_carried", fmt.Sprintf("%d cron jobs are not carried over (schedule or command not accepted, or more than %d); set them up by hand", n, cron.MaxJobsPerSite))
	}

	if ps.SSHKeys > 0 {
		review("ssh_keys", fmt.Sprintf("%d SSH keys of the site user are carried over as SFTP only; give a key shell access in the site's settings if it needs one", ps.SSHKeys))
	}

	if ps.ObjectCache {
		review("object_cache", "the site gets its own object cache on a Unix socket; after the import point wp-config.php at /run/hostashi-valkey/"+ps.Name+"/valkey.sock (WP_REDIS_SCHEME unix, WP_REDIS_PATH), the old 127.0.0.1:6379 is not there")
	}

	if ps.PHP != "" {
		pi := PHPSettingsOf(s)
		ps.PHPSettings = countSet(pi.Settings)

		if len(pi.Changed) > 0 {
			review("php_settings_changed", "PHP settings out of Hostashi's bounds are changed: "+strings.Join(pi.Changed, "; "))
		}

		if len(pi.NotTaken) > 0 {
			review("php_settings_not_taken", "PHP settings not taken over: "+strings.Join(pi.NotTaken, ", "))
		}

		if msg := bodyDiffers(s.ClientMaxBody, pi.Settings); msg != "" {
			review("body_size", msg)
		}
	}

	// The www host becomes the site's www redirect (ADR-0065); other
	// redirect hosts become redirect domains (ADR-0068).
	www, rest := WWWRedirect(ps.Domains, ps.RedirectFrom)
	ps.WWWRedirect, ps.RedirectDomains = www, rest

	for _, f := range s.Findings {
		if f.Severity != scan.SevReview || reviewCodesCovered[f.Code] {
			continue
		}

		if f.Code == "vhost_cache_tier" && ps.PHP != "" {
			ps.PageCache = cachePreset(s.Type)
			review("page_cache", "the old server had a cache (likely Varnish) in front of the site: the page cache is switched on with the "+
				ps.PageCache+" preset. The Varnish rules are not carried over; remove a Varnish purge plugin from the app after the move.")

			continue
		}

		review(f.Code, f.Msg)
	}

	return ps
}

// cachePreset is the page cache preset for a site type (ADR-0046).
func cachePreset(siteType string) string {
	if siteType == "wordpress" {
		return agentapi.PageCacheWordPress
	}

	return agentapi.PageCachePHP
}

// reviewCodesCovered are scan findings the plan already explains itself.
// app_process is the start command of a Node.js or Python site.
var reviewCodesCovered = map[string]bool{"custom_directive": true, "app_process": true}

func nonNil(s []string) []string {
	if s == nil {
		return []string{}
	}

	return slices.Clone(s)
}

func first(s []string) string {
	if len(s) == 0 {
		return ""
	}

	return s[0]
}
