// SPDX-License-Identifier: AGPL-3.0-or-later

package agent

import (
	"context"

	"github.com/maki-it/hostashi/internal/agentapi"
	"github.com/maki-it/hostashi/internal/agentrpc"
)

var errNoSSH = &agentrpc.Error{Code: agentrpc.CodePrecondition, Message: "SSH access is not available on this server"}

// registerSSH serves ssh.apply. It holds h.mu like web.apply, so a site
// cannot be deleted while its key file is written.
func (h *siteOps) registerSSH(s *agentrpc.Server) {
	agentrpc.Handle(s, agentapi.SSHApply, func(ctx context.Context, _ agentrpc.Caller, req agentapi.SSHApplyRequest) (agentapi.SSHApplied, error) {
		if h.ssh == nil {
			return agentapi.SSHApplied{}, errNoSSH
		}

		h.mu.Lock()
		defer h.mu.Unlock()

		if !h.siteKnown(req.Site) {
			return agentapi.SSHApplied{}, &agentrpc.Error{Code: agentrpc.CodeNotFound, Message: "unknown site " + req.Site}
		}

		shell, err := h.ssh.Apply(ctx, req)

		return agentapi.SSHApplied{Shell: shell}, err
	})
}

func (h *siteOps) siteKnown(user string) bool {
	for _, s := range h.m.List() {
		if s.User == user {
			return true
		}
	}

	return false
}

// sshDisabled empties the key files of disabled sites and fills them again
// for enabled ones, after a web.apply went live.
func (h *siteOps) sshDisabled(ctx context.Context, req agentapi.WebApplyRequest) {
	if h.ssh == nil {
		return
	}

	for _, s := range req.Sites {
		if err := h.ssh.SetDisabled(ctx, s.User, s.Disabled); err != nil {
			h.log.ErrorContext(ctx, "ssh keys of site", "site", s.User, "disabled", s.Disabled, "err", err)
		}
	}
}
