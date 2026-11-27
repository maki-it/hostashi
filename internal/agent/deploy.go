// SPDX-License-Identifier: AGPL-3.0-or-later

package agent

import (
	"context"
	"errors"

	"github.com/maki-it/hostashi/internal/agentapi"
	"github.com/maki-it/hostashi/internal/agentrpc"
	"github.com/maki-it/hostashi/internal/deployunit"
	"github.com/maki-it/hostashi/internal/sites"
	"github.com/maki-it/hostashi/internal/siteslice"
)

var errNoDeploy = &agentrpc.Error{Code: agentrpc.CodePrecondition, Message: "Git deploys are not available on this server"}

// deploySite looks up the account of a site for its deploys.
func deploySite(m *sites.Manager) func(string) (deployunit.Site, error) {
	return func(user string) (deployunit.Site, error) {
		for _, s := range m.List() {
			if s.User != user {
				continue
			}

			home, ok := m.Home(user)
			if !ok {
				break
			}

			return deployunit.Site{Home: home, UID: s.UID, GID: s.GID, Slice: siteslice.Name(user)}, nil
		}

		return deployunit.Site{}, sites.ErrNotFound
	}
}

// registerDeploy serves site.deploy.* (ADR-0062). Start and switch hold
// h.mu like site.app.apply, so a site cannot be deleted meanwhile; the
// run itself goes on in its unit.
func (h *siteOps) registerDeploy(s *agentrpc.Server) {
	agentrpc.Handle(s, agentapi.DeployStart, func(ctx context.Context, _ agentrpc.Caller, req agentapi.DeployStartRequest) (agentapi.DeployStatus, error) {
		if h.deploy == nil {
			return agentapi.DeployStatus{}, errNoDeploy
		}

		h.mu.Lock()
		defer h.mu.Unlock()

		st, err := h.deploy.Start(ctx, req)

		return st, mapDeployError(err)
	})

	agentrpc.Handle(s, agentapi.DeployStatusOp, func(ctx context.Context, _ agentrpc.Caller, req agentapi.DeployStatusRequest) (agentapi.DeployStatus, error) {
		if h.deploy == nil {
			return agentapi.DeployStatus{}, errNoDeploy
		}

		st, err := h.deploy.Status(ctx, req)

		return st, mapDeployError(err)
	})

	agentrpc.Handle(s, agentapi.DeploySwitch, func(ctx context.Context, _ agentrpc.Caller, req agentapi.DeploySwitchRequest) (agentapi.DeploySwitchResult, error) {
		if h.deploy == nil {
			return agentapi.DeploySwitchResult{}, errNoDeploy
		}

		h.mu.Lock()
		defer h.mu.Unlock()

		res, err := h.deploy.Switch(ctx, req)

		return res, mapDeployError(err)
	})

	agentrpc.Handle(s, agentapi.DeployPrune, func(ctx context.Context, _ agentrpc.Caller, req agentapi.DeployPruneRequest) (agentapi.DeployReleases, error) {
		if h.deploy == nil {
			return agentapi.DeployReleases{}, errNoDeploy
		}

		res, err := h.deploy.Prune(ctx, req)

		return res, mapDeployError(err)
	})

	h.registerDeployLog(s)
}

// registerDeployLog serves site.deploy.log.
func (h *siteOps) registerDeployLog(s *agentrpc.Server) {
	agentrpc.Handle(s, agentapi.DeployLogOp, func(ctx context.Context, _ agentrpc.Caller, req agentapi.DeployStatusRequest) (agentapi.DeployLog, error) {
		if h.deploy == nil {
			return agentapi.DeployLog{}, errNoDeploy
		}

		res, err := h.deploy.RunLog(ctx, req)

		return res, mapDeployError(err)
	})
}

func mapDeployError(err error) error {
	switch {
	case err == nil:
		return nil
	case errors.Is(err, sites.ErrNotFound):
		return &agentrpc.Error{Code: agentrpc.CodeNotFound, Message: "unknown site"}
	case errors.Is(err, deployunit.ErrBusy), errors.Is(err, deployunit.ErrRelease), errors.Is(err, deployunit.ErrDeployDir):
		return &agentrpc.Error{Code: agentrpc.CodePrecondition, Message: err.Error()}
	}

	return err
}
