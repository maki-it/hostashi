// SPDX-License-Identifier: AGPL-3.0-or-later

package agent

import (
	"context"
	"errors"

	"github.com/maki-it/hostashi/internal/agentapi"
	"github.com/maki-it/hostashi/internal/agentrpc"
	"github.com/maki-it/hostashi/internal/pythoninstall"
	"github.com/maki-it/hostashi/internal/rtinstall"
)

var errNoPythonInstall = &agentrpc.Error{Code: agentrpc.CodePrecondition, Message: "installing Python is not available on this server"}

// registerPythonInstall serves python.install, python.status and
// python.remove (ADR-0061). users tells which sites run on a line; until
// Python sites exist, nil means none does.
func registerPythonInstall(s *agentrpc.Server, m *pythoninstall.Manager, users func(line string) ([]string, error)) {
	refused := func(err error) bool {
		return errors.Is(err, rtinstall.ErrBusy) || errors.Is(err, rtinstall.ErrInstalled) ||
			errors.Is(err, rtinstall.ErrNotInstalled) || errors.Is(err, rtinstall.ErrInUse)
	}

	agentrpc.Handle(s, agentapi.PythonInstall, func(ctx context.Context, _ agentrpc.Caller, req agentapi.PythonInstallRequest) (agentapi.PythonStatus, error) {
		if m == nil {
			return agentapi.PythonStatus{}, errNoPythonInstall
		}

		st, err := m.Start(ctx, req.Line)
		if refused(err) {
			return pythoninstall.Status(st), &agentrpc.Error{Code: agentrpc.CodePrecondition, Message: err.Error()}
		}

		return pythoninstall.Status(st), err
	})

	agentrpc.Handle(s, agentapi.PythonStatusOp, func(ctx context.Context, _ agentrpc.Caller, _ agentapi.PythonStatusRequest) (agentapi.PythonStatus, error) {
		if m == nil {
			return agentapi.PythonStatus{}, errNoPythonInstall
		}

		st, err := m.Status(ctx)

		return pythoninstall.Status(st), err
	})

	agentrpc.Handle(s, agentapi.PythonRemove, func(ctx context.Context, _ agentrpc.Caller, req agentapi.PythonRemoveRequest) (agentapi.PythonStatus, error) {
		if m == nil {
			return agentapi.PythonStatus{}, errNoPythonInstall
		}

		var in []string

		if users != nil {
			u, err := users(req.Line)
			if err != nil {
				return agentapi.PythonStatus{}, err
			}

			in = u
		}

		st, err := m.Remove(ctx, req.Line, in)
		if refused(err) {
			return pythoninstall.Status(st), &agentrpc.Error{Code: agentrpc.CodePrecondition, Message: err.Error()}
		}

		return pythoninstall.Status(st), err
	})
}
