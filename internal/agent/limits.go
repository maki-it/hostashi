// SPDX-License-Identifier: AGPL-3.0-or-later

package agent

import (
	"context"

	"github.com/maki-it/hostashi/internal/agentapi"
	"github.com/maki-it/hostashi/internal/agentrpc"
	"github.com/maki-it/hostashi/internal/siteslice"
)

var errNoSlices = &agentrpc.Error{Code: agentrpc.CodePrecondition, Message: "site limits are not available on this server"}

// registerLimits serves site.limits.* (ADR-0015).
func (h *siteOps) registerLimits(s *agentrpc.Server) {
	agentrpc.Handle(s, agentapi.SiteLimitsSet, func(ctx context.Context, _ agentrpc.Caller, req agentapi.SiteLimitsSetRequest) (agentapi.SiteLimits, error) {
		if h.slices == nil {
			return agentapi.SiteLimits{}, errNoSlices
		}

		h.mu.Lock()
		defer h.mu.Unlock()

		if _, _, err := h.site(req.User); err != nil {
			return agentapi.SiteLimits{}, err
		}

		l := siteslice.FromAPI(req.Limits)
		if err := h.slices.Set(ctx, req.User, l); err != nil {
			return agentapi.SiteLimits{}, err
		}

		return l.API(), nil
	})

	agentrpc.Handle(s, agentapi.SiteLimitsGet, func(_ context.Context, _ agentrpc.Caller, req agentapi.SiteLimitsGetRequest) (agentapi.SiteLimits, error) {
		if h.slices == nil {
			return agentapi.SiteLimits{}, errNoSlices
		}

		if _, _, err := h.site(req.User); err != nil {
			return agentapi.SiteLimits{}, err
		}

		l, _, err := h.slices.Get(req.User)
		if err != nil {
			return agentapi.SiteLimits{}, err
		}

		return l.API(), nil
	})
}
