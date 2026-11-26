// SPDX-License-Identifier: AGPL-3.0-or-later

package agent

import (
	"context"
	"errors"

	"github.com/maki-it/hostashi/internal/agentapi"
	"github.com/maki-it/hostashi/internal/agentrpc"
	"github.com/maki-it/hostashi/internal/sites"
)

// registerHomeCopy serves site.home.copy and site.home.copy.status
// (ADR-0070). The copy runs in the background; the manager checks both
// sites and their accounts itself.
func (h *siteOps) registerHomeCopy(s *agentrpc.Server) {
	agentrpc.Handle(s, agentapi.SiteHomeCopy, func(ctx context.Context, _ agentrpc.Caller, req agentapi.SiteHomeCopyRequest) (agentapi.SiteHomeCopyJob, error) {
		job, err := h.m.StartCopy(ctx, req.Source, req.Target, req.Replace)
		if err != nil {
			return agentapi.SiteHomeCopyJob{}, mapCopyError(err)
		}

		h.log.InfoContext(ctx, "site home copy started", "job", job.ID, "source", req.Source, "target", req.Target, "replace", req.Replace)

		return job, nil
	})

	agentrpc.Handle(s, agentapi.SiteHomeCopyStatus, func(_ context.Context, _ agentrpc.Caller, req agentapi.SiteHomeCopyStatusRequest) (agentapi.SiteHomeCopyJob, error) {
		job, err := h.m.CopyJob(req.ID)
		if err != nil {
			return agentapi.SiteHomeCopyJob{}, mapCopyError(err)
		}

		return job, nil
	})
}

func mapCopyError(err error) error {
	switch {
	case errors.Is(err, sites.ErrCopyBusy):
		return &agentrpc.Error{Code: agentrpc.CodeConflict, Message: "a copy into or out of this site is running"}
	case errors.Is(err, sites.ErrCopyNotFound):
		return &agentrpc.Error{Code: agentrpc.CodeNotFound, Message: "unknown copy job"}
	default:
		return mapSiteError(err)
	}
}
