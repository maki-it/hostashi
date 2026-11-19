// SPDX-License-Identifier: AGPL-3.0-or-later

package serve

import (
	"context"
	"errors"
	"log/slog"
	"mime"
	"net/http"
	"time"

	"github.com/maki-it/hostashi/internal/auth"
	"github.com/maki-it/hostashi/internal/cloudpanel/plan"
	"github.com/maki-it/hostashi/internal/cpmigrate"
	"github.com/maki-it/hostashi/internal/hosting"
)

type cloudPanelReportJSON struct {
	ID        string     `json:"id"`
	Sites     int        `json:"sites"`
	CreatedAt time.Time  `json:"createdAt"`
	Plan      *plan.Plan `json:"plan,omitempty"`
}

func toReportJSON(r cpmigrate.Report) cloudPanelReportJSON {
	return cloudPanelReportJSON{ID: r.ID, Sites: r.Sites, CreatedAt: r.CreatedAt, Plan: r.Plan}
}

func migrationFail(log *slog.Logger, w http.ResponseWriter, err error, what string) {
	var mbe *http.MaxBytesError

	switch {
	case errors.As(err, &mbe):
		writeError(w, http.StatusRequestEntityTooLarge, "too_large", "the report is larger than the limit")
	case errors.Is(err, cpmigrate.ErrInvalid):
		writeError(w, http.StatusUnprocessableEntity, "invalid_report", err.Error())
	case errors.Is(err, cpmigrate.ErrNotFound):
		writeError(w, http.StatusNotFound, "not_found", "no such report")
	case errors.Is(err, cpmigrate.ErrTooMany):
		writeError(w, http.StatusConflict, "too_many_reports", err.Error())
	default:
		log.Error(what, "err", err)
		writeError(w, http.StatusInternalServerError, "internal", "internal error")
	}
}

// registerMigrations adds the CloudPanel import routes (ADR-0017, ADR-0026).
// The body of an upload is the JSON report of "hostashi cloudpanel scan
// -json -full". Responses carry the plan, never the report's snippets or
// cron commands.
func registerMigrations(mux *http.ServeMux, a authn, svc *cpmigrate.Service, log *slog.Logger) {
	admin := func(scope string, h func(http.ResponseWriter, *http.Request, auth.Principal)) http.HandlerFunc {
		return a.scoped(scope, func(w http.ResponseWriter, r *http.Request, p auth.Principal) {
			if p.User.Role != "admin" {
				writeError(w, http.StatusForbidden, "forbidden", "only administrators can import sites")

				return
			}

			h(w, r, p)
		})
	}

	const base = "/api/v1/migrations/cloudpanel/reports"

	mux.HandleFunc("POST "+base, admin("migrations:write", func(w http.ResponseWriter, r *http.Request, p auth.Principal) {
		if mt, _, err := mime.ParseMediaType(r.Header.Get("Content-Type")); err != nil || mt != "application/json" {
			writeError(w, http.StatusUnsupportedMediaType, "unsupported_media_type", "Content-Type must be application/json")

			return
		}

		rep, err := svc.Upload(r.Context(), p.User.ID, http.MaxBytesReader(w, r.Body, plan.MaxReportBytes))
		if err != nil {
			migrationFail(log, w, err, "upload cloudpanel report")

			return
		}

		writeJSON(w, http.StatusCreated, toReportJSON(rep))
	}))

	mux.HandleFunc("GET "+base, admin("migrations:read", func(w http.ResponseWriter, r *http.Request, _ auth.Principal) {
		list, err := svc.List(r.Context())
		if err != nil {
			migrationFail(log, w, err, "list cloudpanel reports")

			return
		}

		out := make([]cloudPanelReportJSON, 0, len(list))
		for _, rep := range list {
			out = append(out, toReportJSON(rep))
		}

		writeJSON(w, http.StatusOK, map[string][]cloudPanelReportJSON{"reports": out})
	}))

	mux.HandleFunc("GET "+base+"/{id}", admin("migrations:read", func(w http.ResponseWriter, r *http.Request, _ auth.Principal) {
		rep, err := svc.Get(r.Context(), r.PathValue("id"))
		if err != nil {
			migrationFail(log, w, err, "read cloudpanel report")

			return
		}

		writeJSON(w, http.StatusOK, toReportJSON(rep))
	}))

	mux.HandleFunc("DELETE "+base+"/{id}", admin("migrations:write", func(w http.ResponseWriter, r *http.Request, p auth.Principal) {
		if err := svc.Delete(r.Context(), p.User.ID, r.PathValue("id")); err != nil {
			migrationFail(log, w, err, "delete cloudpanel report")

			return
		}

		w.WriteHeader(http.StatusNoContent)
	}))
}

// migrationTarget describes this server for the plan: its sites, their
// domains and the PHP versions the agent reports as installed.
func migrationTarget(sites *hosting.Service) cpmigrate.TargetFunc {
	return func(ctx context.Context) (plan.Target, error) {
		list, err := sites.List(ctx)
		if err != nil {
			return plan.Target{}, err
		}

		var t plan.Target

		for _, s := range list {
			t.Sites = append(t.Sites, s.Name)
			t.Domains = append(t.Domains, s.Domains...)
		}

		if t.PHP, err = sites.PHPVersions(ctx); err != nil {
			return plan.Target{}, err
		}

		return t, nil
	}
}
