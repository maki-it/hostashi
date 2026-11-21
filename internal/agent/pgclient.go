// SPDX-License-Identifier: AGPL-3.0-or-later

package agent

import (
	"context"
	"errors"

	"github.com/maki-it/hostashi/internal/agentapi"
	"github.com/maki-it/hostashi/internal/agentrpc"
	"github.com/maki-it/hostashi/internal/pgclient"
)

var errNoPGClient = &agentrpc.Error{Code: agentrpc.CodePrecondition, Message: "installing the PostgreSQL client tools is not available on this server"}

// registerPGClient serves pg.client.install and pg.client.status (ADR-0051).
func registerPGClient(s *agentrpc.Server, m *pgclient.Manager) {
	agentrpc.Handle(s, agentapi.PGClientInstall, func(ctx context.Context, _ agentrpc.Caller, req agentapi.PGClientInstallRequest) (agentapi.PGClientStatus, error) {
		if m == nil {
			return agentapi.PGClientStatus{}, errNoPGClient
		}

		st, err := m.Start(ctx, req.Major)
		if errors.Is(err, pgclient.ErrBusy) || errors.Is(err, pgclient.ErrInstalled) {
			return st, &agentrpc.Error{Code: agentrpc.CodePrecondition, Message: err.Error()}
		}

		return st, err
	})

	agentrpc.Handle(s, agentapi.PGClientStatusOp, func(ctx context.Context, _ agentrpc.Caller, _ agentapi.PGClientStatusRequest) (agentapi.PGClientStatus, error) {
		if m == nil {
			return agentapi.PGClientStatus{}, errNoPGClient
		}

		return m.Status(ctx)
	})
}
