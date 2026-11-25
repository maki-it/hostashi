// SPDX-License-Identifier: AGPL-3.0-or-later

package serve

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"strings"
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
		Choices: newProfilesJSON(st.Choices),
	}

	if !st.UpdatedAt.IsZero() {
		t := st.UpdatedAt
		out.UpdatedAt = &t
	}

	return out
}

// profileIn is an own profile (ADR-0069): the typed values of a built-in
// profile, never nginx text.
type profileIn struct {
	Name      string                `json:"name"`
	Title     string                `json:"title"`
	Type      string                `json:"type"`
	MinPHP    string                `json:"minPhp,omitempty"`
	WebDir    string                `json:"webDir,omitempty"`
	Hardening agentapi.WebHardening `json:"hardening"`
	PageCache string                `json:"pageCache,omitempty"`
	Cron      []profileCronIn       `json:"cron,omitempty"`
	Checklist []string              `json:"checklist,omitempty"`
}

// profileCronIn is a suggested cron job: shown to the person, never
// created by Hostashi.
type profileCronIn struct {
	Schedule string `json:"schedule"`
	Command  string `json:"command"`
	Note     string `json:"note,omitempty"`
}

func (in profileIn) profile() profile.Profile {
	p := profile.Profile{
		Name: in.Name, Title: in.Title, Type: in.Type, MinPHP: in.MinPHP, WebDir: in.WebDir,
		Hardening: in.Hardening, PageCache: in.PageCache, Checklist: in.Checklist,
	}

	for _, c := range in.Cron {
		p.Cron = append(p.Cron, profile.Cron(c))
	}

	return p
}

// profileImportIn is a profile in its YAML form, with "version: 1".
type profileImportIn struct {
	YAML string `json:"yaml"`
}

func profileFail(log *slog.Logger, w http.ResponseWriter, err error, what string) {
	switch {
	case errors.Is(err, hosting.ErrProfileNotFound):
		writeError(w, http.StatusNotFound, "not_found", "no such profile")
	case errors.Is(err, hosting.ErrProfileExists):
		writeError(w, http.StatusConflict, "profile_exists", "a profile with this name exists")
	case errors.Is(err, hosting.ErrProfileBuiltin):
		writeError(w, http.StatusConflict, "profile_builtin", "built-in profiles cannot be changed or deleted; copy one instead")
	case errors.Is(err, hosting.ErrProfileInUse):
		writeError(w, http.StatusConflict, "profile_in_use", strings.TrimPrefix(err.Error(), "hosting: "))
	default:
		siteFail(log, w, err, what)
	}
}

func registerProfiles(mux *routeMux, a authn, svc *hosting.Service, log *slog.Logger) {
	mux.Op("GET /api/v1/profiles", openapi.Operation{
		ID: "listProfiles", Tag: "sites", Summary: "List the built-in and own application profiles", Out: profilesJSON{},
	}, a.allow("profiles:read", func(w http.ResponseWriter, r *http.Request, _ auth.Principal) {
		ps, err := svc.ListProfiles(r.Context())
		if err != nil {
			profileFail(log, w, err, "list profiles")

			return
		}

		writeJSON(w, http.StatusOK, newProfilesJSON(ps))
	}))

	mux.Op("POST /api/v1/profiles", openapi.Operation{
		ID: "createProfile", Tag: "sites", Summary: "Create an own application profile", In: profileIn{}, Out: profile.Profile{}, Status: http.StatusCreated,
	}, a.allow("profiles:write", func(w http.ResponseWriter, r *http.Request, p auth.Principal) {
		var in profileIn
		if !decodeJSON(w, r, &in) {
			return
		}

		out, err := svc.CreateProfile(r.Context(), p.User.ID, in.profile())
		if err != nil {
			profileFail(log, w, err, "create profile")

			return
		}

		writeJSON(w, http.StatusCreated, out)
	}))

	mux.Op("POST /api/v1/profiles/import", openapi.Operation{
		ID: "importProfile", Tag: "sites", Summary: "Import an own application profile from YAML", In: profileImportIn{}, Out: profile.Profile{}, Status: http.StatusCreated,
	}, a.allow("profiles:write", func(w http.ResponseWriter, r *http.Request, p auth.Principal) {
		var in profileImportIn
		// JSON escaping can double the size of the YAML.
		if !decodeJSONLimit(w, r, &in, 2*profile.MaxYAMLBytes+1024) {
			return
		}

		out, err := svc.ImportProfile(r.Context(), p.User.ID, []byte(in.YAML))
		if err != nil {
			profileFail(log, w, err, "import profile")

			return
		}

		writeJSON(w, http.StatusCreated, out)
	}))

	// PUT replaces the values of an own profile; the sites that use it get
	// its new rules at once. The name cannot change.
	mux.Op("PUT /api/v1/profiles/{name}", openapi.Operation{
		ID: "updateProfile", Tag: "sites", Summary: "Change an own application profile", In: profileIn{}, Out: profile.Profile{},
	}, a.allow("profiles:write", func(w http.ResponseWriter, r *http.Request, p auth.Principal) {
		var in profileIn
		if !decodeJSON(w, r, &in) {
			return
		}

		ctx, cancel := context.WithTimeout(r.Context(), agentTimeout)
		defer cancel()

		out, err := svc.UpdateProfile(ctx, p.User.ID, r.PathValue("name"), in.profile())
		if err != nil {
			profileFail(log, w, err, "change profile")

			return
		}

		writeJSON(w, http.StatusOK, out)
	}))

	mux.Op("DELETE /api/v1/profiles/{name}", openapi.Operation{
		ID: "deleteProfile", Tag: "sites", Summary: "Delete an own application profile that no site uses",
	}, a.allow("profiles:write", func(w http.ResponseWriter, r *http.Request, p auth.Principal) {
		if err := svc.DeleteProfile(r.Context(), p.User.ID, r.PathValue("name")); err != nil {
			profileFail(log, w, err, "delete profile")

			return
		}

		w.WriteHeader(http.StatusNoContent)
	}))

	// The YAML form of a built-in or own profile, for a download and a
	// later import.
	mux.Op("GET /api/v1/profiles/{name}/yaml", openapi.Operation{
		ID: "exportProfile", Tag: "sites", Summary: "Export an application profile as YAML", OutContentType: "application/yaml",
	}, a.allow("profiles:read", func(w http.ResponseWriter, r *http.Request, _ auth.Principal) {
		pr, err := svc.Profile(r.Context(), r.PathValue("name"))
		if err != nil {
			profileFail(log, w, err, "export profile")

			return
		}

		out, err := profile.ExportYAML(pr)
		if err != nil {
			profileFail(log, w, err, "export profile")

			return
		}

		// Profile names are letters, digits and dashes only.
		w.Header().Set("Content-Type", "application/yaml; charset=utf-8")
		w.Header().Set("Content-Disposition", `attachment; filename="hostashi-profile-`+pr.Name+`.yaml"`)
		w.Header().Set("Cache-Control", "no-store")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write(out)
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
