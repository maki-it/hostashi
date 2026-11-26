// SPDX-License-Identifier: AGPL-3.0-or-later

package serve

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"time"

	"github.com/maki-it/hostashi/internal/agentapi"
	"github.com/maki-it/hostashi/internal/auth"
	"github.com/maki-it/hostashi/internal/authz"
	"github.com/maki-it/hostashi/internal/hosting"
	"github.com/maki-it/hostashi/internal/openapi"
)

// cloneIn starts a clone of the site (ADR-0070).
type cloneIn struct {
	// Name is the new site's name, Domain its first domain.
	Name   string `json:"name"`
	Domain string `json:"domain"`
	// Pool is the target storage pool; empty: the source's.
	Pool string `json:"pool,omitempty"`
	// Staging marks the clone as staging copy of the source: cron jobs
	// paused, password protection on, noindex, "refresh from source"
	// offered.
	Staging bool `json:"staging,omitempty"`
	// Maintenance puts the source in maintenance until its files and
	// databases are copied, so the copy sees one consistent state.
	Maintenance bool `json:"maintenance,omitempty"`
}

// cloneRefreshIn is the optional body of a refresh.
type cloneRefreshIn struct {
	// Maintenance puts the source in maintenance until its files and
	// databases are copied.
	Maintenance bool `json:"maintenance,omitempty"`
}

// cloneAccessJSON is the generated basic auth user of a clone with
// password protection. The password is shown only in this answer.
type cloneAccessJSON struct {
	User     string `json:"user"`
	Password string `json:"password"`
}

// cloneStartJSON answers a started clone.
type cloneStartJSON struct {
	Site siteJSON `json:"site"`
	// Access is set when the clone asks for a password: always for a
	// staging clone, otherwise when the source did.
	Access *cloneAccessJSON `json:"access,omitempty"`
}

// cloneDatabaseJSON is one database pair of a clone.
type cloneDatabaseJSON struct {
	Source string `json:"source"`
	Target string `json:"target"`
	Done   bool   `json:"done"`
}

// cloneHomeJSON is the progress of a running copy of the files.
type cloneHomeJSON struct {
	Dirs    int64 `json:"dirs"`
	Files   int64 `json:"files"`
	Links   int64 `json:"links"`
	Bytes   int64 `json:"bytes"`
	Skipped int64 `json:"skipped"`
	// SkippedPaths lists up to 50 entries left out, relative to the home.
	SkippedPaths []agentapi.PermissionsSkip `json:"skippedPaths"`
}

// cloneJSON is the clone state of a site.
type cloneJSON struct {
	// Source is the source site, empty once it was deleted.
	Source  string `json:"source"`
	Staging bool   `json:"staging"`
	// State is copying, done or failed.
	State     string `json:"state"`
	Refresh   bool   `json:"refresh"`
	Error     string `json:"error,omitempty"`
	FilesDone bool   `json:"filesDone"`
	// Config is the configuration step after the copy: "" not yet (or
	// none needed), running (WordPress), done.
	Config     string              `json:"config"`
	Files      *cloneHomeJSON      `json:"files,omitempty"`
	Databases  []cloneDatabaseJSON `json:"databases"`
	StartedAt  time.Time           `json:"startedAt"`
	FinishedAt *time.Time          `json:"finishedAt,omitempty"`
}

func toCloneJSON(v hosting.CloneView) cloneJSON {
	out := cloneJSON{
		Source: v.Source, Staging: v.Staging, State: v.State, Refresh: v.Refresh, Error: v.Error,
		FilesDone: v.HomeDone, Config: v.Config, Databases: []cloneDatabaseJSON{}, StartedAt: v.StartedAt,
	}

	if !v.FinishedAt.IsZero() {
		t := v.FinishedAt
		out.FinishedAt = &t
	}

	for _, d := range v.Databases {
		out.Databases = append(out.Databases, cloneDatabaseJSON{Source: d.Source, Target: d.Target, Done: d.Done})
	}

	if h := v.Home; h != nil {
		out.Files = &cloneHomeJSON{Dirs: h.Dirs, Files: h.Files, Links: h.Links, Bytes: h.Bytes, Skipped: h.Skipped, SkippedPaths: h.SkippedPaths}
		if out.Files.SkippedPaths == nil {
			out.Files.SkippedPaths = []agentapi.PermissionsSkip{}
		}
	}

	return out
}

func cloneFail(log *slog.Logger, w http.ResponseWriter, err error, what string) {
	switch {
	case errors.Is(err, hosting.ErrNoClone):
		writeError(w, http.StatusConflict, "clone_unavailable", "cloning is not available on this panel")
	case errors.Is(err, hosting.ErrNoSpace):
		writeError(w, http.StatusConflict, "no_space", err.Error())
	case errors.Is(err, hosting.ErrCloneBusy):
		writeError(w, http.StatusConflict, "clone_running", "a copy of this site is running")
	case errors.Is(err, hosting.ErrNotClone):
		writeError(w, http.StatusNotFound, "not_a_clone", "the site is no staging clone of an existing site")
	default:
		dbFail(log, w, err, what)
	}
}

func registerClones(mux *routeMux, a authn, svc *hosting.Service, log *slog.Logger) {
	// The clone gets new databases, so it also needs databases:write.
	mux.Op("POST /api/v1/sites/{name}/clone", openapi.Operation{
		ID: "cloneSite", Tag: "sites", Summary: "Create a new site as a copy of this one (files, local databases, settings)",
		In: cloneIn{}, Out: cloneStartJSON{}, Status: http.StatusAccepted,
	}, a.allow("sites:clone", func(w http.ResponseWriter, r *http.Request, p auth.Principal) {
		if !p.Allows(authz.ScopeOf("databases:write")) {
			writeError(w, http.StatusForbidden, "insufficient_scope", "the token lacks the scope "+authz.ScopeOf("databases:write"))

			return
		}

		var in cloneIn
		if !decodeJSON(w, r, &in) {
			return
		}

		ctx, cancel := context.WithTimeout(r.Context(), agentTimeout)
		defer cancel()

		s, err := svc.CloneSite(ctx, p.User.ID, r.PathValue("name"), hosting.CloneInput{Name: in.Name, Domain: in.Domain, Pool: in.Pool, Staging: in.Staging, Maintenance: in.Maintenance})
		if err != nil {
			cloneFail(log, w, err, "clone site")

			return
		}

		out := cloneStartJSON{Site: toSiteJSON(svc, s.Site)}
		if s.AccessUser != "" {
			out.Access = &cloneAccessJSON{User: s.AccessUser, Password: s.AccessPassword}
		}

		writeJSON(w, http.StatusAccepted, out)
	}))

	mux.Op("GET /api/v1/sites/{name}/clone", openapi.Operation{
		ID: "getSiteClone", Tag: "sites", Summary: "Where the site was copied from and how far the copy is", Out: cloneJSON{},
	}, a.allow("sites:read", func(w http.ResponseWriter, r *http.Request, _ auth.Principal) {
		ctx, cancel := context.WithTimeout(r.Context(), agentTimeout)
		defer cancel()

		v, err := svc.Clone(ctx, r.PathValue("name"))
		if err != nil {
			cloneFail(log, w, err, "read clone")

			return
		}

		writeJSON(w, http.StatusOK, toCloneJSON(v))
	}))

	// Replaces the clone's files and databases with the source's.
	mux.Op("POST /api/v1/sites/{name}/clone/refresh", openapi.Operation{
		ID: "refreshSiteClone", Tag: "sites", Summary: "Copy the source's files and databases into this staging clone again",
		In: cloneRefreshIn{}, Out: cloneJSON{}, Status: http.StatusAccepted,
	}, a.allow("sites.clone:refresh", func(w http.ResponseWriter, r *http.Request, p auth.Principal) {
		var in cloneRefreshIn
		if r.ContentLength != 0 && !decodeJSON(w, r, &in) {
			return
		}

		ctx, cancel := context.WithTimeout(r.Context(), agentTimeout)
		defer cancel()

		name := r.PathValue("name")

		if err := svc.RefreshClone(ctx, p.User.ID, name, in.Maintenance); err != nil {
			cloneFail(log, w, err, "refresh clone")

			return
		}

		v, err := svc.Clone(ctx, name)
		if err != nil {
			cloneFail(log, w, err, "read clone")

			return
		}

		writeJSON(w, http.StatusAccepted, toCloneJSON(v))
	}))
}
