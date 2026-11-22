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
	"github.com/maki-it/hostashi/internal/hosting"
	"github.com/maki-it/hostashi/internal/store"
)

// headersJSON is a site's security headers (ADR-0056) and the choices for
// the form.
type headersJSON struct {
	Headers                 agentapi.WebHeaders `json:"headers"`
	Defaults                agentapi.WebHeaders `json:"defaults"`
	ReferrerPolicies        []string            `json:"referrerPolicies"`
	PermissionsPolicyStrict string              `json:"permissionsPolicyStrict"`
	MaxValueBytes           int                 `json:"maxValueBytes"`
	UpdatedAt               *time.Time          `json:"updatedAt,omitempty"`
}

func toHeadersJSON(h store.SiteHeaders) headersJSON {
	out := headersJSON{
		Headers: h.Headers, Defaults: agentapi.DefaultWebHeaders, ReferrerPolicies: agentapi.ReferrerPolicies,
		PermissionsPolicyStrict: agentapi.PermissionsPolicyStrict, MaxValueBytes: agentapi.MaxHeaderValueBytes,
	}

	if !h.UpdatedAt.IsZero() {
		t := h.UpdatedAt
		out.UpdatedAt = &t
	}

	return out
}

// headersFail answers a rejected value or a snippet conflict with its own
// code; everything else as for any site call.
func headersFail(log *slog.Logger, w http.ResponseWriter, err error, what string) {
	switch {
	case errors.Is(err, agentapi.ErrHeaderRejected):
		writeError(w, http.StatusUnprocessableEntity, "header_rejected", err.Error())
	case errors.Is(err, agentapi.ErrHeaderConflict):
		writeError(w, http.StatusUnprocessableEntity, "header_conflict", err.Error())
	default:
		siteFail(log, w, err, what)
	}
}

func registerHeaders(mux *routeMux, a authn, svc *hosting.Service, log *slog.Logger) {
	mux.HandleFunc("GET /api/v1/sites/{name}/headers", a.allow("sites:read", func(w http.ResponseWriter, r *http.Request, _ auth.Principal) {
		h, err := svc.Headers(r.Context(), r.PathValue("name"))
		if err != nil {
			siteFail(log, w, err, "read security headers")

			return
		}

		writeJSON(w, http.StatusOK, toHeadersJSON(h))
	}))

	// The body replaces all headers; a field left out is off.
	mux.HandleFunc("PUT /api/v1/sites/{name}/headers", a.allow("sites.headers:write", func(w http.ResponseWriter, r *http.Request, p auth.Principal) {
		var in agentapi.WebHeaders
		if !decodeJSON(w, r, &in) {
			return
		}

		ctx, cancel := context.WithTimeout(r.Context(), agentTimeout)
		defer cancel()

		h, err := svc.SetHeaders(ctx, p.User.ID, r.PathValue("name"), in)
		if err != nil {
			headersFail(log, w, err, "set security headers")

			return
		}

		writeJSON(w, http.StatusOK, toHeadersJSON(h))
	}))
}
