// SPDX-License-Identifier: AGPL-3.0-or-later

package agent

import (
	"fmt"

	"github.com/maki-it/hostashi/internal/agentapi"
	"github.com/maki-it/hostashi/internal/agentrpc"
	"github.com/maki-it/hostashi/internal/web"
)

// realIPFor sets the site's real client address setting (ADR-0059) after
// checking that no trusted range holds an address of this server: every
// site user can connect to those with any header.
func (h *siteOps) realIPFor(site *web.Site, r *agentapi.WebRealIP) error {
	if r == nil || r.IsZero() {
		return nil
	}

	localAddrs := h.localAddrs
	if localAddrs == nil {
		localAddrs = systemLocalAddrs
	}

	local, err := localAddrs()
	if err != nil {
		return fmt.Errorf("addresses of this server: %w", err)
	}

	if err := r.CheckOwn(local); err != nil {
		return &agentrpc.Error{Code: agentrpc.CodeInvalidArgument, Message: "site " + site.User + ": " + err.Error()}
	}

	site.RealIP = *r

	return nil
}
