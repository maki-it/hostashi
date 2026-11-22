// SPDX-License-Identifier: AGPL-3.0-or-later

package serve

import (
	"context"
	"log/slog"
	"net/http"

	"github.com/maki-it/hostashi/internal/agentapi"
	"github.com/maki-it/hostashi/internal/auth"
	"github.com/maki-it/hostashi/internal/hosting"
	"github.com/maki-it/hostashi/internal/openapi"
	"github.com/maki-it/hostashi/internal/store"
)

// pythonAppJSON is the app of a Python site (ADR-0061). On input the port
// and stopped are ignored, as for Node.js.
type pythonAppJSON struct {
	// Version is the Python line, e.g. "3.13".
	Version string `json:"version"`
	// Workdir is the working directory below the home, "htdocs" when left
	// out.
	Workdir string `json:"workdir"`
	// StartKind is "wsgi" (gunicorn), "asgi" (uvicorn), "script" (a .py
	// file) or "" (not set yet, the app stays stopped).
	StartKind string `json:"startKind"`
	// Target is module.path:callable for wsgi and asgi (for Django
	// "project.wsgi:application"), the .py file below the working
	// directory for script.
	Target string `json:"target"`
	// Args are extra arguments, at most 16 of at most 256 bytes each; for
	// wsgi and asgi the address options (-b, --bind, --host, --port, --fd,
	// --uds) are refused, Hostashi sets 127.0.0.1 and the port.
	Args []string `json:"args"`
	// Port is the loopback port nginx passes requests to.
	Port int `json:"port"`
	// Stopped: stopped in the panel, it stays stopped until started.
	Stopped bool `json:"stopped"`
}

func (a pythonAppJSON) app() store.SiteApp {
	return store.SiteApp{Python: a.Version, Workdir: a.Workdir, StartKind: a.StartKind, Start: a.Target, Args: a.Args}
}

func toPythonAppJSON(s store.Site) *pythonAppJSON {
	if s.Type != store.SiteTypePython || s.App == nil {
		return nil
	}

	a := s.App

	args := a.Args
	if args == nil {
		args = []string{}
	}

	return &pythonAppJSON{Version: a.Python, Workdir: a.Workdir, StartKind: a.StartKind, Target: a.Start, Args: args, Port: a.Port, Stopped: a.Stopped}
}

// pythonSetRequest changes the app of a Python site. A new version
// rebuilds .venv, so its packages go: then confirm must be the site's
// name.
type pythonSetRequest struct {
	pythonAppJSON

	Confirm string `json:"confirm"`
}

// pythonVenvRequest recreates a Python site's .venv; confirm is the
// site's name.
type pythonVenvRequest struct {
	Confirm string `json:"confirm"`
}

const venvConfirmText = "this rebuilds .venv and its installed packages go: send confirm with the site's name"

// registerPythonSites adds the routes of the Python sites' own settings;
// start, stop, state and environment variables are the app routes.
func registerPythonSites(mux *routeMux, a authn, svc *hosting.Service, log *slog.Logger) {
	mux.Op("PUT /api/v1/sites/{name}/python", openapi.Operation{
		ID: "setSitePython", Tag: "sites", Summary: "Set the Python version, working directory, start form and arguments of a Python site; a new version rebuilds .venv",
		In: pythonSetRequest{}, Out: siteAppJSON{},
	}, a.allow("sites.app:write", func(w http.ResponseWriter, r *http.Request, p auth.Principal) {
		var in pythonSetRequest
		if !decodeJSON(w, r, &in) {
			return
		}

		name := r.PathValue("name")

		ctx, cancel := context.WithTimeout(r.Context(), agentTimeout)
		defer cancel()

		s, err := svc.Get(ctx, name)
		if err == nil && (s.Type != store.SiteTypePython || s.App == nil) {
			err = hosting.ErrSiteType
		}

		if err == nil && in.Version != s.App.Python && in.Confirm != name {
			writeError(w, http.StatusUnprocessableEntity, "confirm_required", venvConfirmText)

			return
		}

		var st agentapi.AppStatus
		if err == nil {
			s, st, err = svc.SetApp(ctx, p.User.ID, name, in.app())
		}

		if err != nil {
			siteFail(log, w, err, "set python app")

			return
		}

		writeJSON(w, http.StatusOK, siteAppJSON{Site: toSiteJSON(svc, s), Status: toAppStatusJSON(st)})
	}))

	mux.Op("POST /api/v1/sites/{name}/python/venv", openapi.Operation{
		ID: "recreateSitePythonVenv", Tag: "sites", Summary: "Recreate the .venv of a Python site; its installed packages go",
		In: pythonVenvRequest{}, Out: appStatusJSON{},
	}, a.allow("sites.app:write", func(w http.ResponseWriter, r *http.Request, p auth.Principal) {
		var in pythonVenvRequest
		if !decodeJSON(w, r, &in) {
			return
		}

		name := r.PathValue("name")
		if in.Confirm != name {
			writeError(w, http.StatusUnprocessableEntity, "confirm_required", venvConfirmText)

			return
		}

		ctx, cancel := context.WithTimeout(r.Context(), agentTimeout)
		defer cancel()

		st, err := svc.RecreateVenv(ctx, p.User.ID, name)
		if err != nil {
			siteFail(log, w, err, "recreate python venv")

			return
		}

		writeJSON(w, http.StatusOK, toAppStatusJSON(st))
	}))
}
