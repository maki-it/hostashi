// SPDX-License-Identifier: AGPL-3.0-or-later

package serve

import (
	"errors"
	"log/slog"
	"net/http"
	"strconv"
	"time"

	"github.com/maki-it/hostashi/internal/auth"
	"github.com/maki-it/hostashi/internal/hosting"
	"github.com/maki-it/hostashi/internal/openapi"
	"github.com/maki-it/hostashi/internal/store"
)

// deployJSON is one deploy run or rollback of a site (ADR-0062).
type deployJSON struct {
	ID int64 `json:"id"`
	// Release is the release directory below .deploy/releases.
	Release string `json:"release"`
	// Kind is "deploy" (a new release) or "rollback" (back to a kept one).
	Kind string `json:"kind"`
	// Source is "panel", "api" or "webhook".
	Source string `json:"source"`
	// Commit, Author and Message are the deployed commit's, once fetched.
	Commit  string `json:"commit"`
	Author  string `json:"author"`
	Message string `json:"message"`
	// State is "queued", "running", "succeeded" or "failed".
	State string `json:"state"`
	// Error says why a run failed, or what went wrong after it went live.
	Error string `json:"error"`
	// CreatedAt, StartedAt and FinishedAt are RFC 3339; the last two are
	// empty until then.
	CreatedAt  string `json:"createdAt"`
	StartedAt  string `json:"startedAt"`
	FinishedAt string `json:"finishedAt"`
}

// deployLogJSON is the log of a deploy run; a rollback has none.
type deployLogJSON struct {
	Log string `json:"log"`
	// Truncated: the log is longer, this is its end.
	Truncated bool `json:"truncated"`
}

func rfc3339(t time.Time) string {
	if t.IsZero() {
		return ""
	}

	return t.UTC().Format(time.RFC3339)
}

func toDeployJSON(d store.SiteDeploy) deployJSON {
	return deployJSON{
		ID: d.ID, Release: d.Release, Kind: d.Kind, Source: d.Source,
		Commit: d.Commit, Author: d.Author, Message: d.Message, State: d.State, Error: d.Error,
		CreatedAt: rfc3339(d.Created), StartedAt: rfc3339(d.Started), FinishedAt: rfc3339(d.Finished),
	}
}

// deploySource is where a request came from: a token or the browser.
func deploySource(p auth.Principal) string {
	if p.TokenID != 0 {
		return store.DeploySourceAPI
	}

	return store.DeploySourcePanel
}

// deployFail answers the errors of the deploy routes.
func deployFail(log *slog.Logger, w http.ResponseWriter, err error, what string) {
	switch {
	case errors.Is(err, hosting.ErrNoDeploys):
		writeError(w, http.StatusConflict, "deploy_unavailable", "Git deploys are not available on this panel")
	case errors.Is(err, hosting.ErrDeployBusy):
		writeError(w, http.StatusConflict, "deploy_running", "a deploy of this site is running")
	case errors.Is(err, hosting.ErrHostKeyUnconfirmed):
		writeError(w, http.StatusConflict, "host_key_unconfirmed", "confirm the host key of the repository first")
	case errors.Is(err, hosting.ErrNotDeployable):
		writeError(w, http.StatusConflict, "not_deployable", "only a successful deploy can be rolled back to")
	case errors.Is(err, hosting.ErrDeployNotFound):
		writeError(w, http.StatusNotFound, "deploy_not_found", "no such deploy")
	default:
		gitFail(log, w, err, what)
	}
}

func deployID(w http.ResponseWriter, r *http.Request) (int64, bool) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil || id < 1 {
		writeError(w, http.StatusNotFound, "deploy_not_found", "no such deploy")

		return 0, false
	}

	return id, true
}

// registerDeploys adds the routes of a site's deploy runs (ADR-0062).
// Starting a deploy or a rollback needs sites.deploy, which every role
// that works on the site has: it runs only the build commands that an
// admin or manager stored.
func registerDeploys(mux *routeMux, a authn, svc *hosting.Service, log *slog.Logger) {
	mux.Op("GET /api/v1/sites/{name}/deploys", openapi.Operation{
		ID: "listSiteDeploys", Tag: "git", Summary: "The newest deploy runs and rollbacks of a site, newest first", Out: []deployJSON{},
	}, a.allow("sites:read", func(w http.ResponseWriter, r *http.Request, _ auth.Principal) {
		list, err := svc.Deploys(r.Context(), r.PathValue("name"), store.MaxDeployHistory)
		if err != nil {
			deployFail(log, w, err, "list deploys")

			return
		}

		out := make([]deployJSON, 0, len(list))
		for _, d := range list {
			out = append(out, toDeployJSON(d))
		}

		writeJSON(w, http.StatusOK, out)
	}))

	mux.Op("POST /api/v1/sites/{name}/deploys", openapi.Operation{
		ID: "startSiteDeploy", Tag: "git", Summary: "Deploy the head of the site's branch: fetch, build, then go live", Out: deployJSON{},
		Status: http.StatusAccepted,
	}, a.allow("sites.deploy", func(w http.ResponseWriter, r *http.Request, p auth.Principal) {
		d, err := svc.Deploy(r.Context(), p.User.ID, r.PathValue("name"), deploySource(p))
		if err != nil {
			deployFail(log, w, err, "start deploy")

			return
		}

		writeJSON(w, http.StatusAccepted, toDeployJSON(d))
	}))

	mux.Op("POST /api/v1/sites/{name}/deploys/{id}/rollback", openapi.Operation{
		ID: "rollbackSiteDeploy", Tag: "git", Summary: "Make the release of an earlier successful deploy live again, without a build", Out: deployJSON{},
	}, a.allow("sites.deploy", func(w http.ResponseWriter, r *http.Request, p auth.Principal) {
		id, ok := deployID(w, r)
		if !ok {
			return
		}

		d, err := svc.Rollback(r.Context(), p.User.ID, r.PathValue("name"), id, deploySource(p))
		if err != nil {
			deployFail(log, w, err, "rollback")

			return
		}

		writeJSON(w, http.StatusOK, toDeployJSON(d))
	}))

	mux.Op("GET /api/v1/sites/{name}/deploys/{id}/log", openapi.Operation{
		ID: "getSiteDeployLog", Tag: "git", Summary: "The output of a deploy run's fetch and build commands", Out: deployLogJSON{},
	}, a.allow("sites.deploy", func(w http.ResponseWriter, r *http.Request, _ auth.Principal) {
		id, ok := deployID(w, r)
		if !ok {
			return
		}

		l, err := svc.DeployLog(r.Context(), r.PathValue("name"), id)
		if err != nil {
			deployFail(log, w, err, "deploy log")

			return
		}

		w.Header().Set("Cache-Control", "no-store")
		writeJSON(w, http.StatusOK, deployLogJSON{Log: l.Log, Truncated: l.Truncated})
	}))
}
