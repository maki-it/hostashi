// SPDX-License-Identifier: AGPL-3.0-or-later

package agent

import (
	"context"
	"errors"

	"github.com/maki-it/hostashi/internal/agentapi"
	"github.com/maki-it/hostashi/internal/agentrpc"
	"github.com/maki-it/hostashi/internal/logrotate"
	"github.com/maki-it/hostashi/internal/sitelogs"
)

var errNoLogs = &agentrpc.Error{Code: agentrpc.CodePrecondition, Message: "site logs are not available on this server"}

// registerLogs serves site.logs.read and logs.retention.set (ADR-0053).
func (h *siteOps) registerLogs(s *agentrpc.Server) {
	agentrpc.Handle(s, agentapi.SiteLogsRead, func(ctx context.Context, _ agentrpc.Caller, req agentapi.SiteLogsReadRequest) (agentapi.SiteLogsReadResponse, error) {
		if h.logs == nil {
			return agentapi.SiteLogsReadResponse{}, errNoLogs
		}

		// A redirect site has nginx logs but no account (ADR-0054).
		if _, ok := h.m.Home(req.Site); !ok && !h.webOnly(req.Site) {
			return agentapi.SiteLogsReadResponse{}, &agentrpc.Error{Code: agentrpc.CodeNotFound, Message: "unknown site " + req.Site}
		}

		resp, err := h.logs.Read(req)
		if errors.Is(err, sitelogs.ErrUnsafe) {
			h.log.WarnContext(ctx, "site log is not a root-owned regular file", "site", req.Site, "kind", req.Kind, "err", err)

			return resp, &agentrpc.Error{Code: agentrpc.CodePrecondition, Message: "the log file is not safe to read"}
		}

		return resp, err
	})

	agentrpc.Handle(s, agentapi.LogsRetentionSet, func(_ context.Context, _ agentrpc.Caller, req agentapi.LogsRetentionSetRequest) (agentapi.LogsRetentionSetResponse, error) {
		if h.logrotate == "" {
			return agentapi.LogsRetentionSetResponse{}, errNoLogs
		}

		c := LogRotateConfig()
		c.Days = req.Days

		return agentapi.LogsRetentionSetResponse{}, logrotate.Write(h.logrotate, c)
	})
}
