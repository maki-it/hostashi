// SPDX-License-Identifier: AGPL-3.0-or-later

// Package profile holds the application profiles of PHP sites (ADR-0069):
// named sets of typed defaults such as the document root folder and the
// hardening rules of ADR-0017, never nginx text. A site stores its profile
// name and the values it changed; everything else follows the profile, so
// a later change of a built-in profile reaches the sites that kept its
// value.
//
// Every built-in profile is written from the application's own
// documentation (ADR-0001). Folder names and the lowest PHP versions are
// assumptions until each profile's spec has checked them [A].
package profile

import (
	"slices"
	"strconv"
	"strings"

	"github.com/maki-it/hostashi/internal/agentapi"
)

// Names of the built-in profiles.
const (
	// Static is the profile of every static site; it is not offered for
	// PHP sites and needs no choice.
	Static      = "static"
	GenericPHP  = "php"
	WordPress   = "wordpress"
	WooCommerce = "woocommerce"
	Laravel     = "laravel"
	Symfony     = "symfony"
	Shopware    = "shopware6"
	TYPO3       = "typo3"
	Drupal      = "drupal"
)

// Profile is one application profile.
type Profile struct {
	Name  string `json:"name"`
	Title string `json:"title"`
	// Type is the site type the profile is for: store.SiteTypePHP or, for
	// Static only, store.SiteTypeStatic.
	Type string `json:"type"`
	// MinPHP is the lowest PHP version the application supports, empty for
	// any.
	MinPHP string `json:"minPhp,omitempty"`
	// WebDir is the document root folder below htdocs, empty for htdocs.
	WebDir string `json:"webDir,omitempty"`
	// Hardening are the default hardening rules (ADR-0017 Part 4).
	Hardening agentapi.WebHardening `json:"hardening"`
	// PageCache is the page cache preset offered when the cache is turned
	// on (ADR-0046), empty for none.
	PageCache string `json:"pageCache,omitempty"`
	// Cron are suggested cron jobs, shown to the person and never created
	// by Hostashi.
	Cron []Cron `json:"cron,omitempty"`
	// Checklist is short text for the person after the site is created.
	Checklist []string `json:"checklist,omitempty"`
	// Builtin marks a profile that ships with Hostashi.
	Builtin bool `json:"builtin"`
}

// Cron is a suggested cron job: schedule and command, run as the site user
// from htdocs.
type Cron struct {
	Schedule string `json:"schedule"`
	Command  string `json:"command"`
	Note     string `json:"note,omitempty"`
}

// generic are the rules every PHP profile starts from.
var generic = agentapi.WebHardening{ConfigFiles: true}

func hardening(h agentapi.WebHardening) agentapi.WebHardening {
	h.ConfigFiles = true

	return h
}

var wordPressRules = hardening(agentapi.WebHardening{
	NoPHPDirs:  []string{"wp-content/uploads"},
	DenyFiles:  []string{"xmlrpc.php"},
	LimitFiles: []string{"wp-login.php"},
})

var builtins = []Profile{
	{
		Name: Static, Title: "Static site", Type: "static", Hardening: generic,
	},
	{
		Name: GenericPHP, Title: "Generic PHP", Type: "php", Hardening: generic, PageCache: agentapi.PageCachePHP,
	},
	{
		Name: WordPress, Title: "WordPress", Type: "php", MinPHP: "7.4", Hardening: wordPressRules, PageCache: agentapi.PageCacheWordPress,
		Checklist: []string{
			"xmlrpc.php is blocked; allow it in the hardening settings if an app or plugin needs it.",
			"wp-login.php is rate limited per address.",
		},
	},
	{
		Name: WooCommerce, Title: "WooCommerce", Type: "php", MinPHP: "7.4", Hardening: wordPressRules, PageCache: agentapi.PageCacheWooCommerce,
		Checklist: []string{
			"xmlrpc.php is blocked; allow it in the hardening settings if an app or plugin needs it.",
			"wp-login.php is rate limited per address.",
		},
	},
	{
		Name: Laravel, Title: "Laravel", Type: "php", MinPHP: "8.2", WebDir: "public", Hardening: generic, PageCache: agentapi.PageCachePHP,
		Cron:      []Cron{{Schedule: "* * * * *", Command: "php artisan schedule:run", Note: "Laravel's task scheduler."}},
		Checklist: []string{"Deploy the project into htdocs; the site serves its public folder."},
	},
	{
		Name: Symfony, Title: "Symfony", Type: "php", MinPHP: "8.2", WebDir: "public", Hardening: generic, PageCache: agentapi.PageCachePHP,
		Checklist: []string{"Deploy the project into htdocs; the site serves its public folder."},
	},
	{
		Name: Shopware, Title: "Shopware 6", Type: "php", MinPHP: "8.2", WebDir: "public", Hardening: generic, PageCache: agentapi.PageCachePHP,
		Checklist: []string{"Shopware uses its own HTTP cache; leave the page cache off unless you know you need it."},
	},
	{
		Name: TYPO3, Title: "TYPO3", Type: "php", MinPHP: "8.2", WebDir: "public",
		Hardening: hardening(agentapi.WebHardening{NoPHPDirs: []string{"fileadmin", "typo3temp"}}), PageCache: agentapi.PageCachePHP,
		Checklist: []string{"For a composer installation; the site serves its public folder."},
	},
	{
		Name: Drupal, Title: "Drupal", Type: "php", MinPHP: "8.3", WebDir: "web",
		Hardening: hardening(agentapi.WebHardening{NoPHPDirs: []string{"sites/default/files"}}), PageCache: agentapi.PageCachePHP,
		Checklist: []string{"For a composer installation; the site serves its web folder."},
	},
}

// Builtins returns copies of the built-in profiles, Static first.
func Builtins() []Profile {
	out := make([]Profile, len(builtins))
	for i, p := range builtins {
		out[i] = p.clone()
		out[i].Builtin = true
	}

	return out
}

// Builtin returns the built-in profile with that name.
func Builtin(name string) (Profile, bool) {
	for _, p := range builtins {
		if p.Name == name {
			c := p.clone()
			c.Builtin = true

			return c, true
		}
	}

	return Profile{}, false
}

// Default is the profile of a site that has none stored: Static for a
// static site, GenericPHP for a PHP site. Other site types have no
// profile.
func Default(siteType string) (string, bool) {
	switch siteType {
	case "static":
		return Static, true
	case "php":
		return GenericPHP, true
	}

	return "", false
}

func (p Profile) clone() Profile {
	h := p.Hardening
	h.NoPHPDirs = slices.Clone(h.NoPHPDirs)
	h.DenyDirs = slices.Clone(h.DenyDirs)
	h.DenyFiles = slices.Clone(h.DenyFiles)
	h.LimitFiles = slices.Clone(h.LimitFiles)
	p.Hardening = h
	p.Cron = slices.Clone(p.Cron)
	p.Checklist = slices.Clone(p.Checklist)

	return p
}

// Overrides are the hardening values a site changed against its profile;
// nil keeps the profile's value.
type Overrides struct {
	ConfigFiles *bool     `json:"configFiles,omitempty"`
	NoPHPDirs   *[]string `json:"noPhpDirs,omitempty"`
	DenyDirs    *[]string `json:"denyDirs,omitempty"`
	DenyFiles   *[]string `json:"denyFiles,omitempty"`
	LimitFiles  *[]string `json:"limitFiles,omitempty"`
}

// IsZero reports whether the site follows its profile in everything.
func (o Overrides) IsZero() bool {
	return o.ConfigFiles == nil && o.NoPHPDirs == nil && o.DenyDirs == nil && o.DenyFiles == nil && o.LimitFiles == nil
}

// Diff returns the overrides that turn base into want: only the values
// that differ, so a later change of the profile reaches the others.
func Diff(base, want agentapi.WebHardening) Overrides {
	var o Overrides

	if base.ConfigFiles != want.ConfigFiles {
		v := want.ConfigFiles
		o.ConfigFiles = &v
	}

	list := func(b, w []string) *[]string {
		if slices.Equal(b, w) {
			return nil
		}

		c := slices.Clone(w)
		if c == nil {
			c = []string{}
		}

		return &c
	}

	o.NoPHPDirs = list(base.NoPHPDirs, want.NoPHPDirs)
	o.DenyDirs = list(base.DenyDirs, want.DenyDirs)
	o.DenyFiles = list(base.DenyFiles, want.DenyFiles)
	o.LimitFiles = list(base.LimitFiles, want.LimitFiles)

	return o
}

// Effective returns the hardening of a site: the profile's rules with the
// site's overrides. Without PHP, the rules that need PHP are dropped, so a
// PHP site that is disabled or turned static stays valid.
func Effective(p Profile, o Overrides, php bool) agentapi.WebHardening {
	h := p.clone().Hardening

	if o.ConfigFiles != nil {
		h.ConfigFiles = *o.ConfigFiles
	}

	for _, f := range []struct {
		dst *[]string
		src *[]string
	}{{&h.NoPHPDirs, o.NoPHPDirs}, {&h.DenyDirs, o.DenyDirs}, {&h.DenyFiles, o.DenyFiles}, {&h.LimitFiles, o.LimitFiles}} {
		if f.src != nil {
			*f.dst = slices.Clone(*f.src)
		}
	}

	if !php {
		h.NoPHPDirs, h.LimitFiles = nil, nil
	}

	for _, l := range []*[]string{&h.NoPHPDirs, &h.DenyDirs, &h.DenyFiles, &h.LimitFiles} {
		if len(*l) == 0 {
			*l = nil
		}
	}

	return h
}

// PHPAtLeast reports whether PHP version v ("8.4") is lowest or newer. An
// unreadable version is never enough.
func PHPAtLeast(v, lowest string) bool {
	vm, vn, ok1 := majorMinor(v)
	mm, mn, ok2 := majorMinor(lowest)

	return ok1 && ok2 && (vm > mm || vm == mm && vn >= mn)
}

func majorMinor(v string) (int, int, bool) {
	maj, minor, ok := strings.Cut(v, ".")
	if !ok {
		return 0, 0, false
	}

	a, err1 := strconv.Atoi(maj)
	b, err2 := strconv.Atoi(minor)

	return a, b, err1 == nil && err2 == nil && a >= 0 && b >= 0
}
