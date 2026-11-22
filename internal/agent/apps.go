// SPDX-License-Identifier: AGPL-3.0-or-later

package agent

import (
	"context"
	"errors"

	"github.com/maki-it/hostashi/internal/agentapi"
	"github.com/maki-it/hostashi/internal/agentrpc"
	"github.com/maki-it/hostashi/internal/appunit"
	"github.com/maki-it/hostashi/internal/sites"
	"github.com/maki-it/hostashi/internal/siteslice"
)

var errNoApps = &agentrpc.Error{Code: agentrpc.CodePrecondition, Message: "apps are not available on this server"}

// appSite looks up the account of a site for its app: home, uid and slice.
func appSite(m *sites.Manager) func(string) (appunit.Site, error) {
	return func(user string) (appunit.Site, error) {
		for _, s := range m.List() {
			if s.User != user {
				continue
			}

			home, ok := m.Home(user)
			if !ok {
				break
			}

			return appunit.Site{Home: home, UID: s.UID, Slice: siteslice.Name(user)}, nil
		}

		return appunit.Site{}, sites.ErrNotFound
	}
}

// registerApps serves site.app.* (ADR-0054). site.app.apply holds h.mu
// like web.apply, so a site cannot be deleted while its app is set up.
func (h *siteOps) registerApps(s *agentrpc.Server) {
	agentrpc.Handle(s, agentapi.AppApply, func(ctx context.Context, _ agentrpc.Caller, req agentapi.AppApplyRequest) (agentapi.AppStatus, error) {
		if h.apps == nil {
			return agentapi.AppStatus{}, errNoApps
		}

		h.mu.Lock()
		defer h.mu.Unlock()

		if _, _, err := h.site(req.App.Site); err != nil {
			return agentapi.AppStatus{}, &agentrpc.Error{Code: agentrpc.CodeNotFound, Message: "unknown site " + req.App.Site}
		}

		st, err := h.apps.Apply(ctx, req.App)

		return st, mapAppError(err)
	})

	agentrpc.Handle(s, agentapi.AppRemove, func(ctx context.Context, _ agentrpc.Caller, req agentapi.AppRemoveRequest) (agentapi.AppStatus, error) {
		if h.apps == nil {
			return agentapi.AppStatus{}, errNoApps
		}

		h.mu.Lock()
		defer h.mu.Unlock()

		return agentapi.AppStatus{Site: req.Site}, mapAppError(h.apps.Remove(ctx, req.Site))
	})

	agentrpc.Handle(s, agentapi.AppAction, func(ctx context.Context, _ agentrpc.Caller, req agentapi.AppActionRequest) (agentapi.AppStatus, error) {
		if h.apps == nil {
			return agentapi.AppStatus{}, errNoApps
		}

		st, err := h.apps.Action(ctx, req.Site, req.Action)

		return st, mapAppError(err)
	})

	agentrpc.Handle(s, agentapi.AppStatusOp, func(ctx context.Context, _ agentrpc.Caller, req agentapi.AppStatusRequest) (agentapi.AppStatus, error) {
		if h.apps == nil {
			return agentapi.AppStatus{}, errNoApps
		}

		st, err := h.apps.Status(ctx, req.Site)

		return st, mapAppError(err)
	})
}

func mapAppError(err error) error {
	switch {
	case err == nil:
		return nil
	case errors.Is(err, appunit.ErrNoApp):
		return &agentrpc.Error{Code: agentrpc.CodeNotFound, Message: "the site has no app"}
	case errors.Is(err, appunit.ErrNodeMissing), errors.Is(err, appunit.ErrStopped), errors.Is(err, appunit.ErrPortTaken):
		return &agentrpc.Error{Code: agentrpc.CodePrecondition, Message: err.Error()}
	}

	return err
}
