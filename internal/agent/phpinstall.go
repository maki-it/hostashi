// SPDX-License-Identifier: AGPL-3.0-or-later

package agent

import (
	"context"
	"errors"

	"github.com/maki-it/hostashi/internal/agentapi"
	"github.com/maki-it/hostashi/internal/agentrpc"
	"github.com/maki-it/hostashi/internal/phpinstall"
)

var errNoPHPInstall = &agentrpc.Error{Code: agentrpc.CodePrecondition, Message: "installing PHP versions is not available on this server"}

// registerPHPInstall serves php.install and php.install.status (ADR-0036).
func registerPHPInstall(s *agentrpc.Server, m *phpinstall.Manager) {
	agentrpc.Handle(s, agentapi.PHPInstall, func(ctx context.Context, _ agentrpc.Caller, req agentapi.PHPInstallRequest) (agentapi.PHPInstallStatus, error) {
		if m == nil {
			return agentapi.PHPInstallStatus{}, errNoPHPInstall
		}

		st, err := m.Start(ctx, req.Version)
		switch {
		case errors.Is(err, phpinstall.ErrBusy), errors.Is(err, phpinstall.ErrInstalled):
			return st, &agentrpc.Error{Code: agentrpc.CodePrecondition, Message: err.Error()}
		}

		return st, err
	})

	agentrpc.Handle(s, agentapi.PHPInstallStatusOp, func(ctx context.Context, _ agentrpc.Caller, _ agentapi.PHPInstallStatusRequest) (agentapi.PHPInstallStatus, error) {
		if m == nil {
			return agentapi.PHPInstallStatus{}, errNoPHPInstall
		}

		return m.Status(ctx)
	})
}
