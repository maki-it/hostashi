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
	"github.com/maki-it/hostashi/internal/openapi"
)

// resetTimeout stays below nginx's proxy_read_timeout for the panel (60 s).
// A reset that runs out of time has done part of the work; running it again
// only looks at what is already right.
const resetTimeout = 50 * time.Second

// permissionsResetJSON is the result of a permissions reset (ADR-0067).
type permissionsResetJSON struct {
	Checked int64 `json:"checked"`
	Changed int64 `json:"changed"`
	Skipped int64 `json:"skipped"`
	// SkippedPaths lists up to 50 skipped entries, relative to the home.
	SkippedPaths []agentapi.PermissionsSkip `json:"skippedPaths"`
}

func registerPermissions(mux *routeMux, a authn, svc *hosting.Service, log *slog.Logger) {
	mux.Op("POST /api/v1/sites/{name}/permissions/reset", openapi.Operation{
		ID: "resetSitePermissions", Tag: "sites",
		Summary: "Give all files of the site back to its user and reset their modes",
		Out:     permissionsResetJSON{},
	}, a.allow("sites.permissions:reset", func(w http.ResponseWriter, r *http.Request, p auth.Principal) {
		ctx, cancel := context.WithTimeout(r.Context(), resetTimeout)
		defer cancel()

		res, err := svc.ResetPermissions(ctx, p.User.ID, r.PathValue("name"))

		switch {
		case errors.Is(err, hosting.ErrNoPermissionsReset):
			writeError(w, http.StatusConflict, "permissions_unavailable", "permission reset is not available on this panel")

			return
		case err != nil && ctx.Err() != nil:
			writeError(w, http.StatusGatewayTimeout, "reset_incomplete", "the reset took too long and stopped part way; run it again to finish")

			return
		case err != nil:
			siteFail(log, w, err, "reset permissions")

			return
		}

		out := permissionsResetJSON{Checked: res.Checked, Changed: res.Changed, Skipped: res.Skipped, SkippedPaths: res.SkippedPaths}
		if out.SkippedPaths == nil {
			out.SkippedPaths = []agentapi.PermissionsSkip{}
		}

		writeJSON(w, http.StatusOK, out)
	}))
}
