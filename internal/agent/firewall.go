// SPDX-License-Identifier: AGPL-3.0-or-later

package agent

import (
	"context"
	"errors"

	"github.com/maki-it/hostashi/internal/agentapi"
	"github.com/maki-it/hostashi/internal/agentrpc"
	"github.com/maki-it/hostashi/internal/firewall"
)

var errNoFirewall = &agentrpc.Error{Code: agentrpc.CodePrecondition, Message: "the firewall is not available on this server"}

// registerFirewall serves firewall.* (ADR-0034).
func registerFirewall(s *agentrpc.Server, m *firewall.Manager) {
	agentrpc.Handle(s, agentapi.FirewallStatusOp, func(context.Context, agentrpc.Caller, agentapi.FirewallStatusRequest) (agentapi.FirewallStatus, error) {
		if m == nil {
			return agentapi.FirewallStatus{}, errNoFirewall
		}

		st, err := m.Status()

		return st, mapFirewallError(err)
	})

	agentrpc.Handle(s, agentapi.FirewallApply, func(ctx context.Context, _ agentrpc.Caller, req agentapi.FirewallApplyRequest) (agentapi.FirewallStatus, error) {
		if m == nil {
			return agentapi.FirewallStatus{}, errNoFirewall
		}

		st, err := m.Apply(ctx, req.State)

		return st, mapFirewallError(err)
	})

	agentrpc.Handle(s, agentapi.FirewallConfirm, func(ctx context.Context, _ agentrpc.Caller, req agentapi.FirewallConfirmRequest) (agentapi.FirewallStatus, error) {
		if m == nil {
			return agentapi.FirewallStatus{}, errNoFirewall
		}

		st, err := m.Confirm(ctx, req.ID)

		return st, mapFirewallError(err)
	})
}

func mapFirewallError(err error) error {
	switch {
	case err == nil:
		return nil
	case errors.Is(err, firewall.ErrDisabled), errors.Is(err, firewall.ErrPending):
		return &agentrpc.Error{Code: agentrpc.CodePrecondition, Message: err.Error()}
	case errors.Is(err, firewall.ErrNoChange):
		return &agentrpc.Error{Code: agentrpc.CodeNotFound, Message: err.Error()}
	}

	return err
}
