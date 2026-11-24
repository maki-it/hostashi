// SPDX-License-Identifier: AGPL-3.0-or-later

package agent

import (
	"context"

	"github.com/maki-it/hostashi/internal/agentapi"
	"github.com/maki-it/hostashi/internal/agentrpc"
)

// registerPermissions serves site.permissions.reset (ADR-0067). It does not
// hold h.mu: the walk can take a while, and the manager checks the site and
// its account itself.
func (h *siteOps) registerPermissions(s *agentrpc.Server) {
	agentrpc.Handle(s, agentapi.SitePermissionsReset, func(ctx context.Context, _ agentrpc.Caller, req agentapi.SitePermissionsResetRequest) (agentapi.SitePermissionsResetResponse, error) {
		res, err := h.m.ResetPermissions(ctx, req.Site)
		if err != nil {
			return agentapi.SitePermissionsResetResponse{}, mapSiteError(err)
		}

		return res, nil
	})
}
