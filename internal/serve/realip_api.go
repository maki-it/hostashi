// SPDX-License-Identifier: AGPL-3.0-or-later

package serve

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"time"

	"github.com/maki-it/hostashi/internal/agentapi"
	"github.com/maki-it/hostashi/internal/auth"
	"github.com/maki-it/hostashi/internal/authz"
	"github.com/maki-it/hostashi/internal/hosting"
	"github.com/maki-it/hostashi/internal/openapi"
	"github.com/maki-it/hostashi/internal/store"
)

// realIPJSON is a site's real client address setting (ADR-0059) and the
// choices for the form.
type realIPJSON struct {
	RealIP agentapi.WebRealIP `json:"realIp"`
	// Headers are the headers a site may choose, the default first.
	Headers []string `json:"headers"`
	// Cloudflare is the shipped list and the day it last changed.
	Cloudflare     []string   `json:"cloudflare"`
	CloudflareDate string     `json:"cloudflareDate"`
	MaxRanges      int        `json:"maxRanges"`
	UpdatedAt      *time.Time `json:"updatedAt,omitempty"`
}

func toRealIPJSON(r store.SiteRealIP) realIPJSON {
	out := realIPJSON{
		RealIP: r.RealIP, Headers: agentapi.RealIPHeaders, Cloudflare: agentapi.CloudflareRanges,
		CloudflareDate: agentapi.CloudflareRangesDate, MaxRanges: agentapi.MaxRealIPRanges,
	}

	if !r.UpdatedAt.IsZero() {
		t := r.UpdatedAt
		out.UpdatedAt = &t
	}

	return out
}

func registerRealIP(mux *routeMux, a authn, svc *hosting.Service, log *slog.Logger) {
	mux.Op("GET /api/v1/sites/{name}/real-ip", openapi.Operation{
		ID: "getSiteRealIP", Tag: "sites", Summary: "Which proxies a site takes the client address from", Out: realIPJSON{},
	}, a.allow("sites:read", func(w http.ResponseWriter, r *http.Request, _ auth.Principal) {
		ri, err := svc.RealIP(r.Context(), r.PathValue("name"))
		if err != nil {
			siteFail(log, w, err, "read real IP setting")

			return
		}

		writeJSON(w, http.StatusOK, toRealIPJSON(ri))
	}))

	// The body replaces the setting; {} trusts nobody. Adding own ranges
	// needs an admin, because they also reach fail2ban's never-ban list.
	mux.Op("PUT /api/v1/sites/{name}/real-ip", openapi.Operation{
		ID: "setSiteRealIP", Tag: "sites", Summary: "Set which proxies a site takes the client address from", In: agentapi.WebRealIP{}, Out: realIPJSON{},
	}, a.allow("sites.realip:write", func(w http.ResponseWriter, r *http.Request, p auth.Principal) {
		var in agentapi.WebRealIP
		if !decodeJSON(w, r, &in) {
			return
		}

		addRanges := authz.Allowed(authz.Role(p.User.Role), "sites.realip.ranges:write")

		ctx, cancel := context.WithTimeout(r.Context(), agentTimeout)
		defer cancel()

		ri, err := svc.SetRealIP(ctx, p.User.ID, r.PathValue("name"), in, addRanges)

		switch {
		case err == nil:
			writeJSON(w, http.StatusOK, toRealIPJSON(ri))
		case errors.Is(err, hosting.ErrRealIPRanges):
			writeError(w, http.StatusForbidden, "forbidden", "only admins add own proxy ranges; the Cloudflare preset is open to everyone with access to the site")
		case errors.Is(err, agentapi.ErrRealIPRejected):
			writeError(w, http.StatusUnprocessableEntity, "real_ip_rejected", err.Error())
		default:
			siteFail(log, w, err, "set real IP setting")
		}
	}))
}
