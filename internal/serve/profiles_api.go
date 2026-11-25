// SPDX-License-Identifier: AGPL-3.0-or-later

package serve

import (
	"context"
	"log/slog"
	"net/http"
	"time"

	"github.com/maki-it/hostashi/internal/agentapi"
	"github.com/maki-it/hostashi/internal/auth"
	"github.com/maki-it/hostashi/internal/hosting"
	"github.com/maki-it/hostashi/internal/openapi"
	"github.com/maki-it/hostashi/internal/profile"
)

// profilesJSON lists the application profiles (ADR-0069) with the fixed
// values of the hardening rules.
type profilesJSON struct {
	Profiles []profile.Profile `json:"profiles"`
	// ConfigFileSuffixes are the endings the config file rule blocks.
	ConfigFileSuffixes []string `json:"configFileSuffixes"`
	// LimitPerMinute and LimitBurst are the rate limit of limitFiles.
	LimitPerMinute int `json:"limitPerMinute"`
	LimitBurst     int `json:"limitBurst"`
	MaxPaths       int `json:"maxPaths"`
}

func newProfilesJSON(ps []profile.Profile) profilesJSON {
	return profilesJSON{
		Profiles: ps, ConfigFileSuffixes: agentapi.ConfigFileSuffixes,
		LimitPerMinute: agentapi.LimitRate, LimitBurst: agentapi.LimitBurst, MaxPaths: agentapi.MaxHardeningPaths,
	}
}

// siteProfileJSON is a site's profile and its effective hardening.
type siteProfileJSON struct {
	Profile string `json:"profile"`
	// Hardening is what the site serves; Defaults are its profile's rules.
	Hardening agentapi.WebHardening `json:"hardening"`
	Defaults  agentapi.WebHardening `json:"defaults"`
	// Changed: Hardening differs from Defaults.
	Changed bool `json:"changed"`
	// Choices are the profiles a site of this type can choose.
	Choices   profilesJSON `json:"choices"`
	UpdatedAt *time.Time   `json:"updatedAt,omitempty"`
}

// siteProfileIn sets the profile; without hardening the site takes the
// profile's rules.
type siteProfileIn struct {
	Profile   string                 `json:"profile"`
	Hardening *agentapi.WebHardening `json:"hardening,omitempty"`
}

// siteProfilePreviewIn picks what to preview: an empty profile without
// hardening shows the site as it is, otherwise as siteProfileIn would set
// it.
type siteProfilePreviewIn struct {
	Profile   string                 `json:"profile,omitempty"`
	Hardening *agentapi.WebHardening `json:"hardening,omitempty"`
}

// siteProfilePreviewJSON is the site's rendered server block, read only,
// with placeholder paths and without password hashes.
type siteProfilePreviewJSON struct {
	Config string `json:"config"`
}

func toSiteProfileJSON(st hosting.SiteProfileState) siteProfileJSON {
	out := siteProfileJSON{
		Profile: st.Profile.Name, Hardening: st.Hardening, Defaults: st.Profile.Hardening, Changed: st.Changed,
		Choices: newProfilesJSON(hosting.Profiles(st.Profile.Type)),
	}

	if !st.UpdatedAt.IsZero() {
		t := st.UpdatedAt
		out.UpdatedAt = &t
	}

	return out
}

func registerProfiles(mux *routeMux, a authn, svc *hosting.Service, log *slog.Logger) {
	mux.Op("GET /api/v1/profiles", openapi.Operation{
		ID: "listProfiles", Tag: "sites", Summary: "List the application profiles", Out: profilesJSON{},
	}, a.allow("sites:read", func(w http.ResponseWriter, _ *http.Request, _ auth.Principal) {
		writeJSON(w, http.StatusOK, newProfilesJSON(profile.Builtins()))
	}))

	mux.Op("GET /api/v1/sites/{name}/profile", openapi.Operation{
		ID: "getSiteProfile", Tag: "sites", Summary: "Get the site's application profile and hardening", Out: siteProfileJSON{},
	}, a.allow("sites:read", func(w http.ResponseWriter, r *http.Request, _ auth.Principal) {
		st, err := svc.SiteProfile(r.Context(), r.PathValue("name"))
		if err != nil {
			siteFail(log, w, err, "read profile")

			return
		}

		writeJSON(w, http.StatusOK, toSiteProfileJSON(st))
	}))

	// The body replaces the profile and the hardening; only values that
	// differ from the profile are stored.
	mux.Op("PUT /api/v1/sites/{name}/profile", openapi.Operation{
		ID: "setSiteProfile", Tag: "sites", Summary: "Set the site's application profile and hardening", In: siteProfileIn{}, Out: siteProfileJSON{},
	}, a.allow("sites.profile:write", func(w http.ResponseWriter, r *http.Request, p auth.Principal) {
		var in siteProfileIn
		if !decodeJSON(w, r, &in) {
			return
		}

		ctx, cancel := context.WithTimeout(r.Context(), agentTimeout)
		defer cancel()

		st, err := svc.SetSiteProfile(ctx, p.User.ID, r.PathValue("name"), in.Profile, in.Hardening)
		if err != nil {
			siteFail(log, w, err, "set profile")

			return
		}

		writeJSON(w, http.StatusOK, toSiteProfileJSON(st))
	}))

	// Read only: renders with the agent's templates in the panel, stores
	// nothing and does not reach the agent. It holds nothing that the
	// site's other read routes do not show (ADR-0069).
	mux.Op("POST /api/v1/sites/{name}/profile/preview", openapi.Operation{
		ID: "previewSiteProfile", Tag: "sites", Summary: "Render the site's server block for a profile and hardening (read only)",
		In: siteProfilePreviewIn{}, Out: siteProfilePreviewJSON{},
	}, a.allow("sites:read", func(w http.ResponseWriter, r *http.Request, _ auth.Principal) {
		var in siteProfilePreviewIn
		if !decodeJSON(w, r, &in) {
			return
		}

		conf, err := svc.SiteProfilePreview(r.Context(), r.PathValue("name"), in.Profile, in.Hardening)
		if err != nil {
			siteFail(log, w, err, "preview profile")

			return
		}

		writeJSON(w, http.StatusOK, siteProfilePreviewJSON{Config: conf})
	}))
}
