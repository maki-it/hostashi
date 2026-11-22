// SPDX-License-Identifier: AGPL-3.0-or-later

package serve

import (
	"context"
	"errors"
	"log/slog"
	"net/http"

	"github.com/maki-it/hostashi/internal/agentapi"
	"github.com/maki-it/hostashi/internal/agentrpc"
	"github.com/maki-it/hostashi/internal/auth"
	"github.com/maki-it/hostashi/internal/openapi"
)

// PythonInstaller is the agent's part of installing Python (ADR-0061).
type PythonInstaller interface {
	Install(ctx context.Context, line string) (agentapi.PythonStatus, error)
	Status(ctx context.Context) (agentapi.PythonStatus, error)
	Remove(ctx context.Context, line string) (agentapi.PythonStatus, error)
}

// RPCPythonInstaller talks to the agent.
type RPCPythonInstaller struct{ C *agentrpc.Client }

// Install implements PythonInstaller.
func (p RPCPythonInstaller) Install(ctx context.Context, line string) (agentapi.PythonStatus, error) {
	return agentapi.PythonInstall.Call(ctx, p.C, agentapi.PythonInstallRequest{Line: line})
}

// Status implements PythonInstaller.
func (p RPCPythonInstaller) Status(ctx context.Context) (agentapi.PythonStatus, error) {
	return agentapi.PythonStatusOp.Call(ctx, p.C, agentapi.PythonStatusRequest{})
}

// Remove implements PythonInstaller.
func (p RPCPythonInstaller) Remove(ctx context.Context, line string) (agentapi.PythonStatus, error) {
	return agentapi.PythonRemove.Call(ctx, p.C, agentapi.PythonRemoveRequest{Line: line})
}

type pythonReleaseJSON struct {
	// Line is the Python line, for example "3.13".
	Line    string `json:"line"`
	Version string `json:"version"`
	// End is the line's end of life (YYYY-MM-DD).
	End string `json:"end"`
}

type pythonJSON struct {
	agentapi.PythonStatus

	// Releases are the lines Hostashi installs, with the pinned release.
	Releases []pythonReleaseJSON `json:"releases"`
}

type pythonLineJSON struct {
	// Line is the Python line, for example "3.13".
	Line string `json:"line"`
}

func pythonOut(s agentapi.PythonStatus) pythonJSON {
	out := pythonJSON{PythonStatus: s, Releases: make([]pythonReleaseJSON, 0, len(agentapi.PythonReleases))}
	if out.Installed == nil {
		out.Installed = []agentapi.PythonInstalled{}
	}

	if out.Log == nil {
		out.Log = []string{}
	}

	for _, r := range agentapi.PythonReleases {
		out.Releases = append(out.Releases, pythonReleaseJSON{Line: r.Line, Version: r.Version, End: r.End})
	}

	return out
}

const pythonLineError = "line must be one of the Python lines Hostashi installs"

// registerPython adds GET /api/v1/python, POST /api/v1/python/install and
// POST /api/v1/python/remove.
func registerPython(mux *routeMux, a authn, p PythonInstaller, st Settings, log *slog.Logger) {
	fail := func(w http.ResponseWriter, err error, what string) {
		var ae *agentrpc.Error

		switch {
		case errors.As(err, &ae) && ae.Code == agentrpc.CodePrecondition:
			writeError(w, http.StatusConflict, "python_install_refused", ae.Message)
		case errors.As(err, &ae) && ae.Code == agentrpc.CodeInvalidArgument:
			writeError(w, http.StatusUnprocessableEntity, "invalid_argument", ae.Message)
		case errors.As(err, &ae):
			log.Warn(what+": agent refused", "code", ae.Code, "message", ae.Message)
			writeError(w, http.StatusBadGateway, "agent_error", "the agent refused the request")
		case errors.Is(err, context.DeadlineExceeded):
			writeError(w, http.StatusGatewayTimeout, "agent_timeout", "the agent did not answer in time")
		default:
			log.Error(what+" failed", "err", err)
			writeError(w, http.StatusServiceUnavailable, "agent_unavailable", "the agent could not be reached, try again")
		}
	}

	mux.Op("GET /api/v1/python", openapi.Operation{
		ID: "getPython", Tag: "python", Summary: "The Python lines Hostashi installs, what is installed and the last installation", Out: pythonJSON{},
	}, a.allow("python:read", func(w http.ResponseWriter, r *http.Request, _ auth.Principal) {
		ctx, cancel := context.WithTimeout(r.Context(), agentTimeout)
		defer cancel()

		s, err := p.Status(ctx)
		if err != nil {
			fail(w, err, "python status")

			return
		}

		w.Header().Set("Cache-Control", "no-store")
		writeJSON(w, http.StatusOK, pythonOut(s))
	}))

	mux.Op("POST /api/v1/python/install", openapi.Operation{
		ID: "installPython", Tag: "python", Summary: "Start the installation of the pinned CPython build of a Python line",
		In: pythonLineJSON{}, Out: pythonJSON{}, Status: http.StatusAccepted,
	}, a.allow("python:install", func(w http.ResponseWriter, r *http.Request, pr auth.Principal) {
		var in pythonLineJSON

		if !decodeJSON(w, r, &in) {
			return
		}

		rel, err := agentapi.PythonReleaseOf(in.Line)
		if err != nil {
			writeError(w, http.StatusUnprocessableEntity, "invalid_argument", pythonLineError)

			return
		}

		ctx, cancel := context.WithTimeout(r.Context(), agentTimeout)
		defer cancel()

		s, err := p.Install(ctx, in.Line)
		if err != nil {
			fail(w, err, "python install")

			return
		}

		uid := pr.User.ID
		if err := st.Audit(r.Context(), &uid, "python.install", "", map[string]string{"line": rel.Line, "version": rel.Version}); err != nil {
			log.ErrorContext(r.Context(), "audit log", "action", "python.install", "err", err)
		}

		w.Header().Set("Cache-Control", "no-store")
		writeJSON(w, http.StatusAccepted, pythonOut(s))
	}))

	mux.Op("POST /api/v1/python/remove", openapi.Operation{
		ID: "removePython", Tag: "python", Summary: "Remove an installed Python line that no site runs on",
		In: pythonLineJSON{}, Out: pythonJSON{},
	}, a.allow("python:remove", func(w http.ResponseWriter, r *http.Request, pr auth.Principal) {
		var in pythonLineJSON

		if !decodeJSON(w, r, &in) {
			return
		}

		if err := agentapi.ValidatePythonLine(in.Line); err != nil {
			writeError(w, http.StatusUnprocessableEntity, "invalid_argument", pythonLineError)

			return
		}

		ctx, cancel := context.WithTimeout(r.Context(), agentTimeout)
		defer cancel()

		s, err := p.Remove(ctx, in.Line)
		if err != nil {
			fail(w, err, "python remove")

			return
		}

		uid := pr.User.ID
		if err := st.Audit(r.Context(), &uid, "python.remove", "", map[string]string{"line": in.Line}); err != nil {
			log.ErrorContext(r.Context(), "audit log", "action", "python.remove", "err", err)
		}

		w.Header().Set("Cache-Control", "no-store")
		writeJSON(w, http.StatusOK, pythonOut(s))
	}))
}
