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

// programAppJSON is the app of an app site, a program of its own
// (ADR-0054 addendum 2). On input the port and stopped are ignored.
type programAppJSON struct {
	// Workdir is the working directory below the home, "htdocs" when left
	// out.
	Workdir string `json:"workdir"`
	// Program is the executable file below the home, e.g. "bin/server";
	// "" while it is not set (the app stays stopped). It must be a regular
	// file of the site user with an execute bit to start.
	Program string `json:"program"`
	// Args are the program's arguments, at most 16 of at most 256 bytes
	// each, without NUL or line breaks. They reach the program as they are,
	// without a shell.
	Args []string `json:"args"`
	// Port is the loopback port nginx passes requests to; the program
	// finds it in PORT and should listen on HOST (127.0.0.1).
	Port int `json:"port"`
	// Stopped: stopped in the panel, it stays stopped until started.
	Stopped bool `json:"stopped"`
}

func (a programAppJSON) app() store.SiteApp {
	out := store.SiteApp{Workdir: a.Workdir, Start: a.Program, Args: a.Args}
	if a.Program != "" {
		out.StartKind = agentapi.AppStartBinary
	}

	return out
}

func toProgramAppJSON(s store.Site) *programAppJSON {
	if s.Type != store.SiteTypeApp || s.App == nil {
		return nil
	}

	a := s.App

	args := a.Args
	if args == nil {
		args = []string{}
	}

	return &programAppJSON{Workdir: a.Workdir, Program: a.Start, Args: args, Port: a.Port, Stopped: a.Stopped}
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
	// Problem says why the program of an app site that should run is not
	// started, "" otherwise.
	Problem string `json:"problem"`
}

func toAppStatusJSON(s agentapi.AppStatus) appStatusJSON {
	return appStatusJSON{State: s.State, SubState: s.SubState, Restarts: s.Restarts, UsedBytes: s.UsedBytes, Since: s.Since, Problem: s.Problem}
}

type siteAppJSON struct {
	Site   siteJSON      `json:"site"`
	Status appStatusJSON `json:"status"`
}

// registerApps adds the routes of the apps of Node.js and app sites
// (ADR-0054).
// appEnvNamesJSON lists the names of an app's environment variables.
type appEnvNamesJSON struct {
	Names []string `json:"names"`
}

// appEnvVarJSON is one environment variable of an app (ADR-0054).
type appEnvVarJSON struct {
	// Name: A-Z, 0-9 and _, no digit first, at most 64 characters; HOST,
	// PORT, HOME, USER, LOGNAME and PATH are set by Hostashi.
	Name string `json:"name"`
	// Value: at most 4096 bytes on one line.
	Value string `json:"value"`
}

// appEnvJSON is all of an app's variables, at most 100 and 32 KiB as JSON.
type appEnvJSON struct {
	Variables []appEnvVarJSON `json:"variables"`
}

// appEnvSetJSON answers a change of the variables: their names and the
// app's state after the restart.
type appEnvSetJSON struct {
	Names  []string      `json:"names"`
	Status appStatusJSON `json:"status"`
}

func registerApps(mux *routeMux, a authn, svc *hosting.Service, log *slog.Logger) {
	registerAppEnv(mux, a, svc, log)

	mux.Op("PUT /api/v1/sites/{name}/app", openapi.Operation{
		ID: "setSiteApp", Tag: "sites", Summary: "Set the working directory, program and arguments of an app site",
		In: programAppJSON{}, Out: siteAppJSON{},
	}, a.allow("sites.app:write", func(w http.ResponseWriter, r *http.Request, p auth.Principal) {
		var in programAppJSON
		if !decodeJSON(w, r, &in) {
			return
		}

		ctx, cancel := context.WithTimeout(r.Context(), agentTimeout)
		defer cancel()

		s, err := svc.Get(ctx, r.PathValue("name"))
		if err == nil && s.Type != store.SiteTypeApp {
			err = hosting.ErrSiteType
		}

		var st agentapi.AppStatus
		if err == nil {
			s, st, err = svc.SetApp(ctx, p.User.ID, r.PathValue("name"), in.app())
		}

		if err != nil {
			siteFail(log, w, err, "set app")

			return
		}

		writeJSON(w, http.StatusOK, siteAppJSON{Site: toSiteJSON(svc, s), Status: toAppStatusJSON(st)})
	}))

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

		s, err := svc.Get(ctx, r.PathValue("name"))
		if err == nil && s.Type != store.SiteTypeNode {
			err = hosting.ErrSiteType
		}

		var st agentapi.AppStatus
		if err == nil {
			s, st, err = svc.SetApp(ctx, p.User.ID, r.PathValue("name"), in.app())
		}

		if err != nil {
			siteFail(log, w, err, "set app")

			return
		}

		writeJSON(w, http.StatusOK, siteAppJSON{Site: toSiteJSON(svc, s), Status: toAppStatusJSON(st)})
	}))

	mux.Op("GET /api/v1/sites/{name}/app", openapi.Operation{
		ID: "getSiteApp", Tag: "sites", Summary: "The state of the app of a Node.js or app site", Out: appStatusJSON{},
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
			agentapi.AppActionStart:   "Start the app of a Node.js or app site; it starts again after a reboot",
			agentapi.AppActionStop:    "Stop the app of a Node.js or app site; it stays stopped until started",
			agentapi.AppActionRestart: "Restart the app of a Node.js or app site",
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

// registerAppEnv adds the routes of an app's environment variables
// (ADR-0054). Reading the site shows the names; the values need the right
// to change the app, as anyone who can change it can read them from it.
func registerAppEnv(mux *routeMux, a authn, svc *hosting.Service, log *slog.Logger) {
	mux.Op("GET /api/v1/sites/{name}/app/env", openapi.Operation{
		ID: "getSiteAppEnvNames", Tag: "sites", Summary: "The names of the environment variables of a Node.js or app site, without values", Out: appEnvNamesJSON{},
	}, a.allow("sites:read", func(w http.ResponseWriter, r *http.Request, _ auth.Principal) {
		names, err := svc.AppEnvNames(r.Context(), r.PathValue("name"))
		if err != nil {
			siteFail(log, w, err, "app environment names")

			return
		}

		writeJSON(w, http.StatusOK, appEnvNamesJSON{Names: names})
	}))

	mux.Op("GET /api/v1/sites/{name}/app/env/values", openapi.Operation{
		ID: "getSiteAppEnv", Tag: "sites", Summary: "The environment variables of a Node.js or app site with their values", Out: appEnvJSON{},
	}, a.allow("sites.app.env:read", func(w http.ResponseWriter, r *http.Request, _ auth.Principal) {
		env, err := svc.AppEnv(r.Context(), r.PathValue("name"))
		if err != nil {
			siteFail(log, w, err, "app environment")

			return
		}

		out := appEnvJSON{Variables: make([]appEnvVarJSON, 0, len(env))}
		for _, v := range env {
			out.Variables = append(out.Variables, appEnvVarJSON{Name: v.Name, Value: v.Value})
		}

		w.Header().Set("Cache-Control", "no-store")
		writeJSON(w, http.StatusOK, out)
	}))

	mux.Op("PUT /api/v1/sites/{name}/app/env", openapi.Operation{
		ID: "setSiteAppEnv", Tag: "sites", Summary: "Replace the environment variables of a Node.js or app site; a running app restarts",
		In: appEnvJSON{}, Out: appEnvSetJSON{},
	}, a.allow("sites.app:write", func(w http.ResponseWriter, r *http.Request, p auth.Principal) {
		var in appEnvJSON
		if !decodeJSONLimit(w, r, &in, 256<<10) {
			return
		}

		env := make([]agentapi.AppEnvVar, 0, len(in.Variables))
		for _, v := range in.Variables {
			env = append(env, agentapi.AppEnvVar{Name: v.Name, Value: v.Value})
		}

		ctx, cancel := context.WithTimeout(r.Context(), agentTimeout)
		defer cancel()

		st, err := svc.SetAppEnv(ctx, p.User.ID, r.PathValue("name"), env)
		if err != nil {
			siteFail(log, w, err, "set app environment")

			return
		}

		names, err := svc.AppEnvNames(ctx, r.PathValue("name"))
		if err != nil {
			siteFail(log, w, err, "app environment names")

			return
		}

		writeJSON(w, http.StatusOK, appEnvSetJSON{Names: names, Status: toAppStatusJSON(st)})
	}))
}
