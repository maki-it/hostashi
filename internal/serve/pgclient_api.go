// SPDX-License-Identifier: AGPL-3.0-or-later

package serve

import (
	"context"
	"errors"
	"log/slog"
	"net/http"

	"github.com/maki-it/hostashi/internal/openapi"

	"github.com/maki-it/hostashi/internal/agentapi"
	"github.com/maki-it/hostashi/internal/agentrpc"
	"github.com/maki-it/hostashi/internal/auth"
)

// PGClient is the agent's part of installing the PostgreSQL client tools
// from apt.postgresql.org (ADR-0051).
type PGClient interface {
	Install(ctx context.Context, major string) (agentapi.PGClientStatus, error)
	Status(ctx context.Context) (agentapi.PGClientStatus, error)
}

// RPCPGClient talks to the agent.
type RPCPGClient struct{ C *agentrpc.Client }

// Install implements PGClient.
func (p RPCPGClient) Install(ctx context.Context, major string) (agentapi.PGClientStatus, error) {
	return agentapi.PGClientInstall.Call(ctx, p.C, agentapi.PGClientInstallRequest{Major: major})
}

// Status implements PGClient.
func (p RPCPGClient) Status(ctx context.Context) (agentapi.PGClientStatus, error) {
	return agentapi.PGClientStatusOp.Call(ctx, p.C, agentapi.PGClientStatusRequest{})
}

type pgClientJSON struct {
	agentapi.PGClientStatus

	// Newest is the major Hostashi installs (agentapi.PGClientNewest).
	Newest string `json:"newest"`
}

func pgClientOut(s agentapi.PGClientStatus) pgClientJSON {
	if s.Log == nil {
		s.Log = []string{}
	}

	if s.Installed == nil {
		s.Installed = []string{}
	}

	return pgClientJSON{PGClientStatus: s, Newest: agentapi.PGClientNewest()}
}

// registerPGClient adds GET and POST /api/v1/db-servers/postgresql-client
// (ADR-0051). Both need db_servers:write: the client is installed for
// external PostgreSQL servers, which only admins add.
type pgClientInstallRequest struct {
	// Major is empty for the newest.
	Major string `json:"major"`
}

func registerPGClient(mux *routeMux, a authn, p PGClient, st Settings, log *slog.Logger) {
	fail := func(w http.ResponseWriter, err error, what string) {
		var ae *agentrpc.Error

		switch {
		case errors.As(err, &ae) && ae.Code == agentrpc.CodePrecondition:
			writeError(w, http.StatusConflict, "pg_client_install_refused", ae.Message)
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

	mux.Op("GET /api/v1/db-servers/postgresql-client", openapi.Operation{
		ID: "getPostgreSQLClient", Tag: "db-servers", Summary: "Installed PostgreSQL client versions", Out: pgClientJSON{},
	}, a.allow("db_servers:write", func(w http.ResponseWriter, r *http.Request, _ auth.Principal) {
		ctx, cancel := context.WithTimeout(r.Context(), agentTimeout)
		defer cancel()

		s, err := p.Status(ctx)
		if err != nil {
			fail(w, err, "postgresql client status")

			return
		}

		w.Header().Set("Cache-Control", "no-store")
		writeJSON(w, http.StatusOK, pgClientOut(s))
	}))

	mux.Op("POST /api/v1/db-servers/postgresql-client", openapi.Operation{
		ID: "installPostgreSQLClient", Tag: "db-servers", Summary: "Install a PostgreSQL client version", In: pgClientInstallRequest{}, Out: pgClientJSON{}, Status: http.StatusAccepted,
	}, a.allow("db_servers:write", func(w http.ResponseWriter, r *http.Request, pr auth.Principal) {
		var in pgClientInstallRequest

		if !decodeJSON(w, r, &in) {
			return
		}

		if err := (agentapi.PGClientInstallRequest{Major: in.Major}).Validate(); err != nil {
			writeError(w, http.StatusUnprocessableEntity, "invalid_argument", "major must be empty or one of the supported PostgreSQL client majors")

			return
		}

		ctx, cancel := context.WithTimeout(r.Context(), agentTimeout)
		defer cancel()

		s, err := p.Install(ctx, in.Major)
		if err != nil {
			fail(w, err, "postgresql client install")

			return
		}

		uid := pr.User.ID
		if err := st.Audit(r.Context(), &uid, "pg_client.install", "", map[string]string{"major": s.Major}); err != nil {
			log.ErrorContext(r.Context(), "audit log", "action", "pg_client.install", "err", err)
		}

		w.Header().Set("Cache-Control", "no-store")
		writeJSON(w, http.StatusAccepted, pgClientOut(s))
	}))
}
