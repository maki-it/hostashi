// SPDX-License-Identifier: AGPL-3.0-or-later

package agent

import (
	"context"
	"fmt"

	"github.com/maki-it/hostashi/internal/agentapi"
	"github.com/maki-it/hostashi/internal/agentrpc"
	"github.com/maki-it/hostashi/internal/web"
)

// accessFor sets the site's IP rule and password protection (ADR-0066)
// after checking that no bypass address is one of this server's: every
// site user can connect from those.
func (h *siteOps) accessFor(site *web.Site, a *agentapi.WebAccess) error {
	if a == nil || a.IsZero() {
		return nil
	}

	if a.Auth != nil {
		if h.auth == nil {
			return &agentrpc.Error{Code: agentrpc.CodePrecondition, Message: "site " + site.User + ": password protection needs the web server group " + WebUser}
		}

		if len(a.Auth.Bypass) > 0 {
			localAddrs := h.localAddrs
			if localAddrs == nil {
				localAddrs = systemLocalAddrs
			}

			local, err := localAddrs()
			if err != nil {
				return fmt.Errorf("addresses of this server: %w", err)
			}

			if err := a.CheckOwn(local); err != nil {
				return &agentrpc.Error{Code: agentrpc.CodeInvalidArgument, Message: "site " + site.User + ": " + err.Error()}
			}
		}
	}

	site.Access = *a

	return nil
}

// writeAuthFiles puts the password files of d's sites in place before
// nginx is reloaded; the files are named by their content, so the live
// generation keeps reading its own.
func (h *siteOps) writeAuthFiles(d web.Desired) error {
	if h.auth == nil {
		return nil
	}

	return h.auth.Write(d.Sites)
}

// pruneAuthFiles removes the password files no live site uses. The web
// change is already live, so a failure is only logged.
func (h *siteOps) pruneAuthFiles(ctx context.Context, d web.Desired) {
	if h.auth == nil {
		return
	}

	if err := h.auth.Prune(d.Sites); err != nil {
		h.log.ErrorContext(ctx, "remove unused password files", "err", err)
	}
}
