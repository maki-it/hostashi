// SPDX-License-Identifier: AGPL-3.0-or-later

package hosting

import (
	"github.com/maki-it/hostashi/internal/agentapi"
	"github.com/maki-it/hostashi/internal/profile"
	"github.com/maki-it/hostashi/internal/store"
)

// siteProfile returns the profile a site follows: its stored one, or the
// default of its type (ADR-0069). A stored name that no longer exists
// falls back to the default as well, so a site never loses its rules.
func siteProfile(all map[int64]store.SiteProfile, site store.Site) (profile.Profile, profile.Overrides, bool) {
	def, ok := profile.Default(site.Type)
	if !ok {
		return profile.Profile{}, profile.Overrides{}, false
	}

	if sp, ok := all[site.ID]; ok {
		if p, ok := profile.Builtin(sp.Profile); ok && p.Type == site.Type {
			return p, sp.Overrides, true
		}
	}

	p, _ := profile.Builtin(def)

	return p, profile.Overrides{}, true
}

// webHardening is the hardening the agent gets for a static or PHP site
// (ADR-0017 Part 4); php is the PHP version it sends, empty while the
// site is disabled.
func webHardening(all map[int64]store.SiteProfile, site store.Site, php string) *agentapi.WebHardening {
	p, o, ok := siteProfile(all, site)
	if !ok {
		return nil
	}

	h := profile.Effective(p, o, php != "")
	if h.IsZero() {
		return nil
	}

	return &h
}
