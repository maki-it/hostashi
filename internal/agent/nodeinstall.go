// SPDX-License-Identifier: AGPL-3.0-or-later

package agent

import (
	"context"
	"errors"

	"github.com/maki-it/hostashi/internal/agentapi"
	"github.com/maki-it/hostashi/internal/agentrpc"
	"github.com/maki-it/hostashi/internal/nodeinstall"
)

var errNoNodeInstall = &agentrpc.Error{Code: agentrpc.CodePrecondition, Message: "installing Node.js is not available on this server"}

// registerNodeInstall serves node.install and node.status (ADR-0054).
func registerNodeInstall(s *agentrpc.Server, m *nodeinstall.Manager) {
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
}
