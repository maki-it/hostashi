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

// NodeInstaller is the agent's part of installing Node.js (ADR-0054).
type NodeInstaller interface {
	Install(ctx context.Context, major string) (agentapi.NodeStatus, error)
	Status(ctx context.Context) (agentapi.NodeStatus, error)
	Remove(ctx context.Context, major string) (agentapi.NodeStatus, error)
}

// RPCNodeInstaller talks to the agent.
type RPCNodeInstaller struct{ C *agentrpc.Client }

// Install implements NodeInstaller.
func (p RPCNodeInstaller) Install(ctx context.Context, major string) (agentapi.NodeStatus, error) {
	return agentapi.NodeInstall.Call(ctx, p.C, agentapi.NodeInstallRequest{Major: major})
}

// Status implements NodeInstaller.
func (p RPCNodeInstaller) Status(ctx context.Context) (agentapi.NodeStatus, error) {
	return agentapi.NodeStatusOp.Call(ctx, p.C, agentapi.NodeStatusRequest{})
}

// Remove implements NodeInstaller.
func (p RPCNodeInstaller) Remove(ctx context.Context, major string) (agentapi.NodeStatus, error) {
	return agentapi.NodeRemove.Call(ctx, p.C, agentapi.NodeRemoveRequest{Major: major})
}

type nodeReleaseJSON struct {
	Major    string `json:"major"`
	Version  string `json:"version"`
	Codename string `json:"codename"`
	// End is the line's end of life (YYYY-MM-DD).
	End string `json:"end"`
}

type nodeJSON struct {
	agentapi.NodeStatus

	// Releases are the lines Hostashi installs, with the pinned release.
	Releases []nodeReleaseJSON `json:"releases"`
}

type nodeInstallJSON struct {
	// Major is the LTS line, for example "24".
	Major string `json:"major"`
}

func nodeOut(s agentapi.NodeStatus) nodeJSON {
	out := nodeJSON{NodeStatus: s, Releases: make([]nodeReleaseJSON, 0, len(agentapi.NodeReleases))}
	if out.Installed == nil {
		out.Installed = []agentapi.NodeInstalled{}
	}

	if out.Log == nil {
		out.Log = []string{}
	}

	for _, r := range agentapi.NodeReleases {
		out.Releases = append(out.Releases, nodeReleaseJSON{Major: r.Major, Version: r.Version, Codename: r.Codename, End: r.End})
	}

	return out
}

// registerNode adds GET /api/v1/node, POST /api/v1/node/install and
// POST /api/v1/node/remove.
func registerNode(mux *routeMux, a authn, p NodeInstaller, st Settings, log *slog.Logger) {
	fail := func(w http.ResponseWriter, err error, what string) {
		var ae *agentrpc.Error

		switch {
		case errors.As(err, &ae) && ae.Code == agentrpc.CodePrecondition:
			writeError(w, http.StatusConflict, "node_install_refused", ae.Message)
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

	mux.Op("GET /api/v1/node", openapi.Operation{
		ID: "getNode", Tag: "node", Summary: "The Node.js LTS lines Hostashi installs, what is installed and the last installation", Out: nodeJSON{},
	}, a.allow("node:read", func(w http.ResponseWriter, r *http.Request, _ auth.Principal) {
		ctx, cancel := context.WithTimeout(r.Context(), agentTimeout)
		defer cancel()

		s, err := p.Status(ctx)
		if err != nil {
			fail(w, err, "node status")

			return
		}

		w.Header().Set("Cache-Control", "no-store")
		writeJSON(w, http.StatusOK, nodeOut(s))
	}))

	mux.Op("POST /api/v1/node/install", openapi.Operation{
		ID: "installNode", Tag: "node", Summary: "Start the installation of the pinned build of a Node.js LTS line",
		In: nodeInstallJSON{}, Out: nodeJSON{}, Status: http.StatusAccepted,
	}, a.allow("node:install", func(w http.ResponseWriter, r *http.Request, pr auth.Principal) {
		var in nodeInstallJSON

		if !decodeJSON(w, r, &in) {
			return
		}

		rel, err := agentapi.NodeReleaseOf(in.Major)
		if err != nil {
			writeError(w, http.StatusUnprocessableEntity, "invalid_argument", "major must be one of the Node.js LTS lines Hostashi installs")

			return
		}

		ctx, cancel := context.WithTimeout(r.Context(), agentTimeout)
		defer cancel()

		s, err := p.Install(ctx, in.Major)
		if err != nil {
			fail(w, err, "node install")

			return
		}

		uid := pr.User.ID
		if err := st.Audit(r.Context(), &uid, "node.install", "", map[string]string{"major": rel.Major, "version": rel.Version}); err != nil {
			log.ErrorContext(r.Context(), "audit log", "action", "node.install", "err", err)
		}

		w.Header().Set("Cache-Control", "no-store")
		writeJSON(w, http.StatusAccepted, nodeOut(s))
	}))
	mux.Op("POST /api/v1/node/remove", openapi.Operation{
		ID: "removeNode", Tag: "node", Summary: "Remove an installed Node.js line that no site's app runs on",
		In: nodeInstallJSON{}, Out: nodeJSON{},
	}, a.allow("node:remove", func(w http.ResponseWriter, r *http.Request, pr auth.Principal) {
		var in nodeInstallJSON

		if !decodeJSON(w, r, &in) {
			return
		}

		if err := agentapi.ValidateNodeMajor(in.Major); err != nil {
			writeError(w, http.StatusUnprocessableEntity, "invalid_argument", "major must be one of the Node.js LTS lines Hostashi installs")

			return
		}

		ctx, cancel := context.WithTimeout(r.Context(), agentTimeout)
		defer cancel()

		s, err := p.Remove(ctx, in.Major)
		if err != nil {
			fail(w, err, "node remove")

			return
		}

		uid := pr.User.ID
		if err := st.Audit(r.Context(), &uid, "node.remove", "", map[string]string{"major": in.Major}); err != nil {
			log.ErrorContext(r.Context(), "audit log", "action", "node.remove", "err", err)
		}

		w.Header().Set("Cache-Control", "no-store")
		writeJSON(w, http.StatusOK, nodeOut(s))
	}))
}
