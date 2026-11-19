// SPDX-License-Identifier: AGPL-3.0-or-later

package serve

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"mime"
	"net/http"
	"net/netip"
	"time"

	"github.com/maki-it/hostashi/internal/agentapi"
	"github.com/maki-it/hostashi/internal/agentrpc"
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

// importFail maps the errors of creating and importing a site. Site
// service and agent errors keep the mapping of the sites API.
func importFail(log *slog.Logger, w http.ResponseWriter, err error, what string) {
	var pe *cpmigrate.PlanError

	switch {
	case errors.As(err, &pe):
		code := "skipped"
		if errors.Is(err, cpmigrate.ErrNeedsReview) {
			code = "needs_review"
		}

		writeJSON(w, http.StatusConflict, apiError{apiErrorBody{
			Code: code, Message: pe.Error(), RequestID: w.Header().Get(RequestIDHeader), Reasons: pe.Reasons,
		}})
	case errors.Is(err, cpmigrate.ErrNoSite):
		writeError(w, http.StatusNotFound, "no_site", "the report has no importable site of this name")
	case errors.Is(err, cpmigrate.ErrBadRequest):
		writeError(w, http.StatusUnprocessableEntity, "invalid_argument", err.Error())
	case errors.Is(err, cpmigrate.ErrNotDone):
		writeError(w, http.StatusConflict, "not_done", "the import has not finished copying")
	case errors.Is(err, cpmigrate.ErrNoImporter):
		writeError(w, http.StatusNotImplemented, "unavailable", "importing is not available on this panel")
	case errors.Is(err, cpmigrate.ErrNotFound), errors.Is(err, cpmigrate.ErrInvalid):
		migrationFail(log, w, err, what)
	default:
		siteFail(log, w, err, what)
	}
}

// cutoverFail maps the errors of the DNS cut-over.
func cutoverFail(log *slog.Logger, w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, cpmigrate.ErrBadRequest):
		writeError(w, http.StatusUnprocessableEntity, "invalid_argument", err.Error())
	case errors.Is(err, cpmigrate.ErrNoCutover):
		writeError(w, http.StatusNotFound, "not_found", "the site has no cut-over")
	case errors.Is(err, cpmigrate.ErrSiteState):
		writeError(w, http.StatusConflict, "site_state", "the site does not exist or is not active")
	case errors.Is(err, cpmigrate.ErrCutoverRunning):
		writeError(w, http.StatusConflict, "cutover_running", "a cut-over of this site is still waiting")
	default:
		log.Error("cloudpanel cut-over", "err", err)
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

	// Creating a site takes its name, domains and PHP version from the
	// stored report; the body can only confirm a review or pick another
	// PHP version. No certificate is requested before the DNS switch.
	mux.HandleFunc("POST "+base+"/{id}/sites/{name}", admin("migrations:write", func(w http.ResponseWriter, r *http.Request, p auth.Principal) {
		var in struct {
			Reviewed bool   `json:"reviewed"`
			PHP      string `json:"php"`
		}
		if !decodeJSON(w, r, &in) {
			return
		}

		ctx, cancel := context.WithTimeout(r.Context(), agentTimeout)
		defer cancel()

		site, err := svc.CreateSite(ctx, p.User.ID, r.PathValue("id"), r.PathValue("name"), cpmigrate.CreateOptions{Reviewed: in.Reviewed, PHP: in.PHP})
		if err != nil {
			importFail(log, w, err, "create site from cloudpanel report")

			return
		}

		writeJSON(w, http.StatusCreated, site)
	}))

	// The import copies from the old server what the report names for the
	// site; the body only lists the databases. Starting it again is the
	// final sync.
	mux.HandleFunc("POST "+base+"/{id}/sites/{name}/import", admin("migrations:write", func(w http.ResponseWriter, r *http.Request, p auth.Principal) {
		var in struct {
			Databases []agentapi.CloudPanelDBMap `json:"databases"`
		}
		if !decodeJSONLimit(w, r, &in, 64<<10) {
			return
		}

		ctx, cancel := context.WithTimeout(r.Context(), agentTimeout)
		defer cancel()

		imp, err := svc.StartImport(ctx, p.User.ID, r.PathValue("id"), r.PathValue("name"), in.Databases)
		if err != nil {
			importFail(log, w, err, "start cloudpanel import")

			return
		}

		writeJSON(w, http.StatusAccepted, imp)
	}))

	// Activating swaps the imported files in as the web root; the previous
	// one stays in the home as htdocs-before-import-<time>.
	mux.HandleFunc("POST /api/v1/migrations/cloudpanel/sites/{name}/activate", admin("migrations:write", func(w http.ResponseWriter, r *http.Request, p auth.Principal) {
		ctx, cancel := context.WithTimeout(r.Context(), agentTimeout)
		defer cancel()

		res, err := svc.Activate(ctx, p.User.ID, r.PathValue("name"))
		if err != nil {
			importFail(log, w, err, "activate cloudpanel import")

			return
		}

		writeJSON(w, http.StatusOK, res)
	}))

	// ?site=NAME limits the list to one site. The agent keeps the last 50
	// imports; each entry leaves out the skipped paths.
	mux.HandleFunc("GET /api/v1/migrations/cloudpanel/imports", admin("migrations:read", func(w http.ResponseWriter, r *http.Request, _ auth.Principal) {
		ctx, cancel := context.WithTimeout(r.Context(), agentTimeout)
		defer cancel()

		list, err := svc.Imports(ctx, r.URL.Query().Get("site"))
		if err != nil {
			importFail(log, w, err, "list cloudpanel imports")

			return
		}

		writeJSON(w, http.StatusOK, map[string][]agentapi.CloudPanelImport{"imports": list})
	}))

	mux.HandleFunc("GET /api/v1/migrations/cloudpanel/imports/{id}", admin("migrations:read", func(w http.ResponseWriter, r *http.Request, _ auth.Principal) {
		ctx, cancel := context.WithTimeout(r.Context(), agentTimeout)
		defer cancel()

		imp, err := svc.Import(ctx, r.PathValue("id"))
		if err != nil {
			var ae *agentrpc.Error
			if errors.As(err, &ae) && ae.Code == agentrpc.CodeNotFound || errors.Is(err, cpmigrate.ErrNotFound) {
				writeError(w, http.StatusNotFound, "not_found", "no such import")

				return
			}

			importFail(log, w, err, "read cloudpanel import")

			return
		}

		writeJSON(w, http.StatusOK, imp)
	}))

	// The dumps of a finished import are loaded on their own (ADR-0030);
	// this starts the loads again, for example after a panel restart. A
	// dump whose load started already is not loaded twice.
	mux.HandleFunc("POST /api/v1/migrations/cloudpanel/imports/{id}/load", admin("migrations:write", func(w http.ResponseWriter, r *http.Request, p auth.Principal) {
		ctx, cancel := context.WithTimeout(r.Context(), agentTimeout)
		defer cancel()

		loads, err := svc.LoadDumps(ctx, p.User.ID, r.PathValue("id"))
		if err != nil {
			var ae *agentrpc.Error
			if errors.As(err, &ae) && ae.Code == agentrpc.CodeNotFound || errors.Is(err, cpmigrate.ErrNotFound) {
				writeError(w, http.StatusNotFound, "not_found", "no such import")

				return
			}

			importFail(log, w, err, "load cloudpanel dumps")

			return
		}

		writeJSON(w, http.StatusAccepted, map[string][]cpmigrate.DumpLoad{"loads": loads})
	}))

	mux.HandleFunc("GET /api/v1/migrations/cloudpanel/imports/{id}/loads", admin("migrations:read", func(w http.ResponseWriter, r *http.Request, _ auth.Principal) {
		ctx, cancel := context.WithTimeout(r.Context(), agentTimeout)
		defer cancel()

		loads, err := svc.Loads(ctx, r.PathValue("id"))
		if err != nil {
			importFail(log, w, err, "read cloudpanel dump loads")

			return
		}

		writeJSON(w, http.StatusOK, map[string][]cpmigrate.DumpLoad{"loads": loads})
	}))

	// The DNS cut-over (ADR-0031): POST starts watching the site's domains,
	// HTTPS is turned on once they all point to this server; DELETE stops
	// a waiting one.
	cutover := func(scope string, run func(ctx context.Context, actor int64, site string) (cpmigrate.Cutover, error), status int) http.HandlerFunc {
		return admin(scope, func(w http.ResponseWriter, r *http.Request, p auth.Principal) {
			ctx, cancel := context.WithTimeout(r.Context(), agentTimeout)
			defer cancel()

			c, err := run(ctx, p.User.ID, r.PathValue("name"))
			if err != nil {
				cutoverFail(log, w, err)

				return
			}

			writeJSON(w, status, c)
		})
	}

	const cutoverPath = "/api/v1/migrations/cloudpanel/sites/{name}/cutover"

	mux.HandleFunc("POST "+cutoverPath, cutover("migrations:write", svc.StartCutover, http.StatusAccepted))
	mux.HandleFunc("GET "+cutoverPath, cutover("migrations:read", func(ctx context.Context, _ int64, site string) (cpmigrate.Cutover, error) {
		return svc.Cutover(ctx, site)
	}, http.StatusOK))
	mux.HandleFunc("DELETE "+cutoverPath, cutover("migrations:write", svc.CancelCutover, http.StatusOK))

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

// siteCreator creates a migrated site through the site service: no
// certificate yet, DNS still points to the old server.
func siteCreator(sites *hosting.Service, log *slog.Logger) cpmigrate.SiteCreator {
	if log == nil {
		log = slog.New(slog.DiscardHandler)
	}

	return func(ctx context.Context, actor int64, s cpmigrate.NewSite) (int, error) {
		req := hosting.CreateRequest{Name: s.Name, PHP: s.PHP, Domains: s.Domains}
		if u := s.Usage; u != nil && u.Bytes >= 0 && u.Inodes >= 0 {
			// Disk limits of at least the old use plus 50 % (ADR-0015), so
			// the import does not stop at the quota.
			req.Usage = &hosting.Usage{Bytes: uint64(u.Bytes), Inodes: uint64(u.Inodes)}
		}

		if _, err := sites.Create(ctx, actor, req); err != nil {
			return 0, err
		}

		n := 0

		for _, j := range s.Cron {
			_, err := sites.CreateCronJob(ctx, actor, s.Name, hosting.CronInput{Schedule: j.Schedule, Command: j.Command, Paused: true})
			if err != nil {
				// The site stands; the plan's review tells the admin to
				// compare the jobs.
				log.WarnContext(ctx, "imported cron job not created", "site", s.Name, "line", j.Line, "err", err)

				continue
			}

			n++
		}

		return n, nil
	}
}

// RPCCloudPanelImports starts and reads imports in the agent.
type RPCCloudPanelImports struct{ C *agentrpc.Client }

// Start implements cpmigrate.Importer.
func (c RPCCloudPanelImports) Start(ctx context.Context, req agentapi.CloudPanelImportRequest) (agentapi.CloudPanelImport, error) {
	return agentapi.CloudPanelImportStart.Call(ctx, c.C, req)
}

// List implements cpmigrate.Importer.
func (c RPCCloudPanelImports) List(ctx context.Context, site string) ([]agentapi.CloudPanelImport, error) {
	l, err := agentapi.CloudPanelImportList.Call(ctx, c.C, agentapi.CloudPanelImportListRequest{Site: site})

	return l.Imports, err
}

// Activate implements cpmigrate.Importer.
func (c RPCCloudPanelImports) Activate(ctx context.Context, site string) (agentapi.CloudPanelActivated, error) {
	return agentapi.CloudPanelActivate.Call(ctx, c.C, agentapi.CloudPanelActivateRequest{Site: site})
}

// Get implements cpmigrate.Importer.
func (c RPCCloudPanelImports) Get(ctx context.Context, id string) (agentapi.CloudPanelImport, error) {
	return agentapi.CloudPanelImportGet.Call(ctx, c.C, agentapi.CloudPanelImportGetRequest{ID: id})
}

// serverAddrs asks the agent for the server's public addresses: the web
// process has no netlink socket (ADR-0031).
func serverAddrs(c *agentrpc.Client) cpmigrate.AddrsFunc {
	return func(ctx context.Context) ([]netip.Addr, error) {
		r, err := agentapi.ServerAddrs.Call(ctx, c, agentapi.ServerAddrsRequest{})
		if err != nil {
			return nil, err
		}

		out := make([]netip.Addr, 0, len(r.Addrs))

		for _, s := range r.Addrs {
			a, err := netip.ParseAddr(s)
			if err != nil {
				return nil, fmt.Errorf("server address %q: %w", s, err)
			}

			out = append(out, a)
		}

		return out, nil
	}
}
