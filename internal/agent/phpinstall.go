// SPDX-License-Identifier: AGPL-3.0-or-later

package agent

import (
	"context"
	"errors"
	"slices"

	"github.com/maki-it/hostashi/internal/agentapi"
	"github.com/maki-it/hostashi/internal/agentrpc"
	"github.com/maki-it/hostashi/internal/php"
	"github.com/maki-it/hostashi/internal/phpinstall"
)

var errNoPHPInstall = &agentrpc.Error{Code: agentrpc.CodePrecondition, Message: "installing PHP versions is not available on this server"}

// registerPHPInstall serves php.install, php.remove and php.install.status
// (ADR-0036). pools tells which versions the sites run on.
func registerPHPInstall(s *agentrpc.Server, m *phpinstall.Manager, pools *php.Manager) {
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

	agentrpc.Handle(s, agentapi.PHPRemove, func(ctx context.Context, _ agentrpc.Caller, req agentapi.PHPRemoveRequest) (agentapi.PHPInstallStatus, error) {
		if m == nil {
			return agentapi.PHPInstallStatus{}, errNoPHPInstall
		}

		users := []string{}

		if pools != nil {
			cur, err := pools.Current()
			if err != nil {
				return agentapi.PHPInstallStatus{}, err
			}

			for user, p := range cur {
				if p.Version == req.Version {
					users = append(users, user)
				}
			}

			slices.Sort(users)
		}

		st, err := m.Remove(ctx, req.Version, users)
		switch {
		case errors.Is(err, phpinstall.ErrBusy), errors.Is(err, phpinstall.ErrNotInstalled), errors.Is(err, phpinstall.ErrInUse), errors.Is(err, phpinstall.ErrNoRemove):
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
