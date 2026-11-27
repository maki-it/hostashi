// SPDX-License-Identifier: AGPL-3.0-or-later

package agent

import (
	"context"
	"errors"
	"log/slog"

	"github.com/maki-it/hostashi/internal/agentapi"
	"github.com/maki-it/hostashi/internal/agentrpc"
	"github.com/maki-it/hostashi/internal/f2bctl"
)

var errNoFail2ban = &agentrpc.Error{Code: agentrpc.CodePrecondition, Message: "fail2ban is not available on this server"}

// registerFail2ban serves fail2ban.* (ADR-0078).
func registerFail2ban(s *agentrpc.Server, c *f2bctl.Controller, log *slog.Logger) {
	agentrpc.Handle(s, agentapi.Fail2banStatusOp, func(ctx context.Context, _ agentrpc.Caller, _ agentapi.Fail2banStatusRequest) (agentapi.Fail2banStatus, error) {
		if c == nil {
			return agentapi.Fail2banStatus{}, errNoFail2ban
		}

		st, err := c.Status(ctx)

		return st, mapFail2banError(err)
	})

	agentrpc.Handle(s, agentapi.Fail2banUnban, func(ctx context.Context, _ agentrpc.Caller, req agentapi.Fail2banUnbanRequest) (agentapi.Fail2banUnbanResponse, error) {
		if c == nil {
			return agentapi.Fail2banUnbanResponse{}, errNoFail2ban
		}

		resp, err := c.Unban(ctx, req)
		if err == nil && log != nil {
			log.InfoContext(ctx, "fail2ban ban lifted", "address", req.Address, "jails", resp.Jails)
		}

		return resp, mapFail2banError(err)
	})
}

func mapFail2banError(err error) error {
	if errors.Is(err, f2bctl.ErrUnavailable) {
		return &agentrpc.Error{Code: agentrpc.CodePrecondition, Message: "fail2ban does not answer"}
	}

	return err
}
