// SPDX-License-Identifier: AGPL-3.0-or-later

package agent

import (
	"context"
	"errors"

	"github.com/maki-it/hostashi/internal/agentapi"
	"github.com/maki-it/hostashi/internal/agentrpc"
	"github.com/maki-it/hostashi/internal/appunit"
	"github.com/maki-it/hostashi/internal/nodeinstall"
)

var errNoNodeInstall = &agentrpc.Error{Code: agentrpc.CodePrecondition, Message: "installing Node.js is not available on this server"}

// registerNodeInstall serves node.install, node.status and node.remove
// (ADR-0054). apps tells which lines are in use; without it no line is.
func registerNodeInstall(s *agentrpc.Server, m *nodeinstall.Manager, apps *appunit.Manager) {
	agentrpc.Handle(s, agentapi.NodeInstall, func(ctx context.Context, _ agentrpc.Caller, req agentapi.NodeInstallRequest) (agentapi.NodeStatus, error) {
		if m == nil {
			return agentapi.NodeStatus{}, errNoNodeInstall
		}

		st, err := m.Start(ctx, req.Major)
		if errors.Is(err, nodeinstall.ErrBusy) || errors.Is(err, nodeinstall.ErrInstalled) {
			return st, &agentrpc.Error{Code: agentrpc.CodePrecondition, Message: err.Error()}
		}

		return st, err
	})

	agentrpc.Handle(s, agentapi.NodeStatusOp, func(ctx context.Context, _ agentrpc.Caller, _ agentapi.NodeStatusRequest) (agentapi.NodeStatus, error) {
		if m == nil {
			return agentapi.NodeStatus{}, errNoNodeInstall
		}

		return m.Status(ctx)
	})

	agentrpc.Handle(s, agentapi.NodeRemove, func(ctx context.Context, _ agentrpc.Caller, req agentapi.NodeRemoveRequest) (agentapi.NodeStatus, error) {
		if m == nil {
			return agentapi.NodeStatus{}, errNoNodeInstall
		}

		var users []string

		if apps != nil {
			u, err := apps.Users(req.Major)
			if err != nil {
				return agentapi.NodeStatus{}, err
			}

			users = u
		}

		st, err := m.Remove(ctx, req.Major, users)
		if errors.Is(err, nodeinstall.ErrBusy) || errors.Is(err, nodeinstall.ErrNotInstalled) || errors.Is(err, nodeinstall.ErrInUse) {
			return st, &agentrpc.Error{Code: agentrpc.CodePrecondition, Message: err.Error()}
		}

		return st, err
	})
}
