// SPDX-License-Identifier: AGPL-3.0-or-later

package agent

import (
	"context"
	"errors"
	"strings"

	"github.com/maki-it/hostashi/internal/agentapi"
	"github.com/maki-it/hostashi/internal/agentrpc"
	"github.com/maki-it/hostashi/internal/cronjobs"
)

var errNoCron = &agentrpc.Error{Code: agentrpc.CodePrecondition, Message: "cron jobs are not available on this server"}

// siteSlice is the slice of a site's processes (ADR-0015):
// hostashi-site-<user>.slice with "-" escaped as systemd-escape does,
// because "-" in a slice name means nesting.
func siteSlice(user string) string {
	return "hostashi-site-" + strings.ReplaceAll(user, "-", `\x2d`) + ".slice"
}

// registerCron serves cron.*. cron.apply holds h.mu like web.apply, so a
// site cannot be deleted while its units are written.
func (h *siteOps) registerCron(s *agentrpc.Server) {
	agentrpc.Handle(s, agentapi.CronApply, func(ctx context.Context, _ agentrpc.Caller, req agentapi.CronApplyRequest) (agentapi.CronApplied, error) {
		if h.cron == nil {
			return agentapi.CronApplied{}, errNoCron
		}

		h.mu.Lock()
		defer h.mu.Unlock()

		site, ok := h.cronSite(req.Site)
		if !ok {
			return agentapi.CronApplied{}, &agentrpc.Error{Code: agentrpc.CodeNotFound, Message: "unknown site " + req.Site}
		}

		return agentapi.CronApplied{}, mapCronError(h.cron.Apply(ctx, site, req.Jobs))
	})

	agentrpc.Handle(s, agentapi.CronRun, func(ctx context.Context, _ agentrpc.Caller, req agentapi.CronRunRequest) (agentapi.CronRunResponse, error) {
		if h.cron == nil {
			return agentapi.CronRunResponse{}, errNoCron
		}

		return agentapi.CronRunResponse{}, mapCronError(h.cron.Run(ctx, req.Site, req.ID))
	})

	agentrpc.Handle(s, agentapi.CronStatus, func(ctx context.Context, _ agentrpc.Caller, req agentapi.CronStatusRequest) (agentapi.CronSiteStatus, error) {
		if h.cron == nil {
			return agentapi.CronSiteStatus{}, errNoCron
		}

		st, err := h.cron.Status(ctx, req.Site)

		return st, mapCronError(err)
	})
}

func (h *siteOps) cronSite(user string) (cronjobs.Site, bool) {
	home, ok := h.m.Home(user)
	if !ok {
		return cronjobs.Site{}, false
	}

	for _, s := range h.m.List() {
		if s.User == user {
			return cronjobs.Site{Name: user, Home: home, UID: s.UID, GID: s.GID, Slice: siteSlice(user)}, true
		}
	}

	return cronjobs.Site{}, false
}

// cronDisabled stops the timers of disabled sites and starts them again
// for enabled ones, after a web.apply went live.
func (h *siteOps) cronDisabled(ctx context.Context, req agentapi.WebApplyRequest) {
	if h.cron == nil {
		return
	}

	for _, s := range req.Sites {
		if err := h.cron.SetDisabled(ctx, s.User, s.Disabled); err != nil {
			h.log.ErrorContext(ctx, "cron timers of site", "site", s.User, "disabled", s.Disabled, "err", err)
		}
	}
}

func mapCronError(err error) error {
	switch {
	case err == nil:
		return nil
	case errors.Is(err, cronjobs.ErrUnknownJob):
		return &agentrpc.Error{Code: agentrpc.CodeNotFound, Message: "no such cron job"}
	case errors.Is(err, cronjobs.ErrDisabled):
		return &agentrpc.Error{Code: agentrpc.CodePrecondition, Message: "the site is disabled"}
	case errors.Is(err, cronjobs.ErrRootSite):
		return &agentrpc.Error{Code: agentrpc.CodePrecondition, Message: "refusing a site user with uid or gid 0"}
	default:
		return err
	}
}
