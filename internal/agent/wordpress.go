// SPDX-License-Identifier: AGPL-3.0-or-later

package agent

import (
	"context"
	"errors"

	"github.com/maki-it/hostashi/internal/agentapi"
	"github.com/maki-it/hostashi/internal/agentrpc"
	"github.com/maki-it/hostashi/internal/sites"
	"github.com/maki-it/hostashi/internal/wpunit"
)

var errNoWordPress = &agentrpc.Error{Code: agentrpc.CodePrecondition, Message: "the WordPress quick install is not available on this server"}

// wpSite looks up the account of a site for its quick install.
func wpSite(m *sites.Manager) func(string) (wpunit.Site, error) {
	deploy := deploySite(m)

	return func(user string) (wpunit.Site, error) {
		s, err := deploy(user)

		return wpunit.Site{Home: s.Home, UID: s.UID, GID: s.GID, Slice: s.Slice}, err
	}
}

// registerWordPress serves site.wordpress.* (ADR-0069, the clone
// configuration of ADR-0070). Start holds h.mu
// like site.deploy.start, so a site cannot be deleted meanwhile; the run
// goes on in its unit.
func (h *siteOps) registerWordPress(s *agentrpc.Server) {
	agentrpc.Handle(s, agentapi.WPInstallStart, func(ctx context.Context, _ agentrpc.Caller, req agentapi.WPInstallRequest) (agentapi.WPInstallStatus, error) {
		if h.wp == nil {
			return agentapi.WPInstallStatus{}, errNoWordPress
		}

		h.mu.Lock()
		defer h.mu.Unlock()

		st, err := h.wp.Start(ctx, req)

		return st, mapWPError(err)
	})

	agentrpc.Handle(s, agentapi.WPCloneStart, func(ctx context.Context, _ agentrpc.Caller, req agentapi.WPCloneRequest) (agentapi.WPInstallStatus, error) {
		if h.wp == nil {
			return agentapi.WPInstallStatus{}, errNoWordPress
		}

		h.mu.Lock()
		defer h.mu.Unlock()

		st, err := h.wp.StartClone(ctx, req)
		if errors.Is(err, wpunit.ErrBusy) {
			// The panel tries again later.
			return st, &agentrpc.Error{Code: agentrpc.CodeConflict, Message: err.Error()}
		}

		return st, mapWPError(err)
	})

	agentrpc.Handle(s, agentapi.DotEnvCloneStart, func(ctx context.Context, _ agentrpc.Caller, req agentapi.DotEnvCloneRequest) (agentapi.WPInstallStatus, error) {
		if h.wp == nil {
			return agentapi.WPInstallStatus{}, errNoWordPress
		}

		h.mu.Lock()
		defer h.mu.Unlock()

		st, err := h.wp.StartDotEnv(ctx, req)
		if errors.Is(err, wpunit.ErrBusy) {
			return st, &agentrpc.Error{Code: agentrpc.CodeConflict, Message: err.Error()}
		}

		return st, mapWPError(err)
	})

	agentrpc.Handle(s, agentapi.WPInstallStatOp, func(ctx context.Context, _ agentrpc.Caller, req agentapi.WPStatusRequest) (agentapi.WPInstallStatus, error) {
		if h.wp == nil {
			return agentapi.WPInstallStatus{}, errNoWordPress
		}

		st, err := h.wp.Status(ctx, req)

		return st, mapWPError(err)
	})
}

func mapWPError(err error) error {
	switch {
	case err == nil:
		return nil
	case errors.Is(err, sites.ErrNotFound):
		return &agentrpc.Error{Code: agentrpc.CodeNotFound, Message: "unknown site"}
	case errors.Is(err, wpunit.ErrBusy), errors.Is(err, wpunit.ErrNotEmpty):
		return &agentrpc.Error{Code: agentrpc.CodePrecondition, Message: err.Error()}
	}

	return err
}
