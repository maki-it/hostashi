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

// nodeAppJSON is the app of a Node.js site (ADR-0054). On input the port
// and stopped are ignored: the panel hands out the port, start and stop
// set stopped.
type nodeAppJSON struct {
	// Version is the Node.js LTS line, e.g. "24".
	Version string `json:"version"`
	// Workdir is the working directory below the home, "htdocs" when left
	// out.
	Workdir string `json:"workdir"`
	// StartKind is "script" (node <file>), "npm" (npm run <script>) or ""
	// (not set yet, the app stays stopped).
	StartKind string `json:"startKind"`
	Start     string `json:"start"`
	// Port is the loopback port nginx passes requests to.
	Port int `json:"port"`
	// Stopped: stopped in the panel, it stays stopped until started.
	Stopped bool `json:"stopped"`
}

func (a nodeAppJSON) app() store.SiteApp {
	return store.SiteApp{Node: a.Version, Workdir: a.Workdir, StartKind: a.StartKind, Start: a.Start}
}

func toNodeAppJSON(s store.Site) *nodeAppJSON {
	if s.Type != store.SiteTypeNode || s.App == nil {
		return nil
	}

	a := s.App

	return &nodeAppJSON{Version: a.Node, Workdir: a.Workdir, StartKind: a.StartKind, Start: a.Start, Port: a.Port, Stopped: a.Stopped}
}

// appStatusJSON is what runs of a site's app.
type appStatusJSON struct {
	// State is systemd's ActiveState (active, inactive, failed,
	// activating …), "" when the app is not set up.
	State     string `json:"state"`
	SubState  string `json:"subState"`
	Restarts  int    `json:"restarts"`
	UsedBytes uint64 `json:"usedBytes"`
	Since     string `json:"since"`
}

func toAppStatusJSON(s agentapi.AppStatus) appStatusJSON {
	return appStatusJSON{State: s.State, SubState: s.SubState, Restarts: s.Restarts, UsedBytes: s.UsedBytes, Since: s.Since}
}

type siteAppJSON struct {
	Site   siteJSON      `json:"site"`
	Status appStatusJSON `json:"status"`
}

// registerApps adds the routes of Node.js apps (ADR-0054).
func registerApps(mux *routeMux, a authn, svc *hosting.Service, log *slog.Logger) {
	mux.Op("PUT /api/v1/sites/{name}/node", openapi.Operation{
		ID: "setSiteNode", Tag: "sites", Summary: "Set the Node.js version, working directory and start command of a Node.js site",
		In: nodeAppJSON{}, Out: siteAppJSON{},
	}, a.allow("sites.app:write", func(w http.ResponseWriter, r *http.Request, p auth.Principal) {
		var in nodeAppJSON
		if !decodeJSON(w, r, &in) {
			return
		}

		ctx, cancel := context.WithTimeout(r.Context(), agentTimeout)
		defer cancel()

		s, st, err := svc.SetApp(ctx, p.User.ID, r.PathValue("name"), in.app())
		if err != nil {
			siteFail(log, w, err, "set app")

			return
		}

		writeJSON(w, http.StatusOK, siteAppJSON{Site: toSiteJSON(svc, s), Status: toAppStatusJSON(st)})
	}))

	mux.Op("GET /api/v1/sites/{name}/app", openapi.Operation{
		ID: "getSiteApp", Tag: "sites", Summary: "The state of a Node.js site's app", Out: appStatusJSON{},
	}, a.allow("sites:read", func(w http.ResponseWriter, r *http.Request, _ auth.Principal) {
		ctx, cancel := context.WithTimeout(r.Context(), agentTimeout)
		defer cancel()

		st, err := svc.AppStatus(ctx, r.PathValue("name"))
		if err != nil {
			siteFail(log, w, err, "app status")

			return
		}

		w.Header().Set("Cache-Control", "no-store")
		writeJSON(w, http.StatusOK, toAppStatusJSON(st))
	}))

	for _, action := range []string{agentapi.AppActionStart, agentapi.AppActionStop, agentapi.AppActionRestart} {
		summary := map[string]string{
			agentapi.AppActionStart:   "Start a Node.js site's app; it starts again after a reboot",
			agentapi.AppActionStop:    "Stop a Node.js site's app; it stays stopped until started",
			agentapi.AppActionRestart: "Restart a Node.js site's app",
		}[action]

		mux.Op("POST /api/v1/sites/{name}/app/"+action, openapi.Operation{
			ID: action + "SiteApp", Tag: "sites", Summary: summary, Out: appStatusJSON{},
		}, a.allow("sites.app:write", func(w http.ResponseWriter, r *http.Request, p auth.Principal) {
			ctx, cancel := context.WithTimeout(r.Context(), agentTimeout)
			defer cancel()

			st, err := svc.AppAction(ctx, p.User.ID, r.PathValue("name"), action)
			if err != nil {
				siteFail(log, w, err, action+" app")

				return
			}

			writeJSON(w, http.StatusOK, toAppStatusJSON(st))
		}))
	}
}
