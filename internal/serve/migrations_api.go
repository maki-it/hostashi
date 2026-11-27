// SPDX-License-Identifier: AGPL-3.0-or-later

package serve

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"mime"
	"net/http"
	"net/netip"
	"slices"
	"time"

	"github.com/maki-it/hostashi/internal/agentapi"
	"github.com/maki-it/hostashi/internal/agentrpc"
	"github.com/maki-it/hostashi/internal/auth"
	"github.com/maki-it/hostashi/internal/authz"
	"github.com/maki-it/hostashi/internal/cloudpanel/plan"
	"github.com/maki-it/hostashi/internal/cpmigrate"
	"github.com/maki-it/hostashi/internal/dnsq"
	"github.com/maki-it/hostashi/internal/hosting"
	"github.com/maki-it/hostashi/internal/openapi"
	"github.com/maki-it/hostashi/internal/sshkeys"
	"github.com/maki-it/hostashi/internal/store"
)

type cloudPanelReportJSON struct {
	ID        string     `json:"id"`
	Sites     int        `json:"sites"`
	CreatedAt time.Time  `json:"createdAt"`
	Plan      *plan.Plan `json:"plan,omitempty"`
}

type reportListJSON struct {
	Reports []cloudPanelReportJSON `json:"reports"`
}

type importListJSON struct {
	Imports []agentapi.CloudPanelImport `json:"imports"`
}

type dumpLoadListJSON struct {
	Loads []cpmigrate.DumpLoad `json:"loads"`
}

type cloudPanelSiteRequest struct {
	Reviewed  bool   `json:"reviewed"`
	PHP       string `json:"php"`
	PageCache *bool  `json:"pageCache"`
	Pool      string `json:"pool"`
	Node      string `json:"node"`
	Python    string `json:"python"`
}

type cloudPanelImportRequest struct {
	Databases []agentapi.CloudPanelDBMap `json:"databases"`
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
	case errors.Is(err, cpmigrate.ErrPool):
		writeError(w, http.StatusConflict, "pool_unusable", err.Error())
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
func registerMigrations(mux *routeMux, a authn, svc *cpmigrate.Service, log *slog.Logger) {
	const base = "/api/v1/migrations/cloudpanel/reports"

	mux.Op("POST "+base, openapi.Operation{
		ID: "uploadCloudPanelReport", Tag: "migrations", Summary: "Upload the JSON report of hostashi cloudpanel scan -json -full", In: json.RawMessage{}, Out: cloudPanelReportJSON{}, Status: http.StatusCreated,
	}, a.allow("migrations:write", func(w http.ResponseWriter, r *http.Request, p auth.Principal) {
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

	mux.Op("GET "+base, openapi.Operation{
		ID: "listCloudPanelReports", Tag: "migrations", Summary: "List the uploaded CloudPanel reports", Out: reportListJSON{},
	}, a.allow("migrations:read", func(w http.ResponseWriter, r *http.Request, _ auth.Principal) {
		list, err := svc.List(r.Context())
		if err != nil {
			migrationFail(log, w, err, "list cloudpanel reports")

			return
		}

		out := make([]cloudPanelReportJSON, 0, len(list))
		for _, rep := range list {
			out = append(out, toReportJSON(rep))
		}

		writeJSON(w, http.StatusOK, reportListJSON{Reports: out})
	}))

	mux.Op("GET "+base+"/{id}", openapi.Operation{
		ID: "getCloudPanelReport", Tag: "migrations", Summary: "Get a report with its import plan", Out: cloudPanelReportJSON{},
	}, a.allow("migrations:read", func(w http.ResponseWriter, r *http.Request, _ auth.Principal) {
		rep, err := svc.Get(r.Context(), r.PathValue("id"))
		if err != nil {
			migrationFail(log, w, err, "read cloudpanel report")

			return
		}

		writeJSON(w, http.StatusOK, toReportJSON(rep))
	}))

	// Creating a site takes its name, domains and PHP version from the
	// stored report; the body can only confirm a review, pick another
	// PHP version or Node.js line, a pool, or leave the proposed page cache
	// off. No certificate is requested before the DNS switch.
	mux.Op("POST "+base+"/{id}/sites/{name}", openapi.Operation{
		ID: "createCloudPanelSite", Tag: "migrations", Summary: "Create a site from the report", In: cloudPanelSiteRequest{}, Out: plan.Site{}, Status: http.StatusCreated,
	}, a.allow("migrations:write", func(w http.ResponseWriter, r *http.Request, p auth.Principal) {
		var in cloudPanelSiteRequest
		if !decodeJSON(w, r, &in) {
			return
		}

		ctx, cancel := context.WithTimeout(r.Context(), agentTimeout)
		defer cancel()

		site, err := svc.CreateSite(ctx, p.User.ID, r.PathValue("id"), r.PathValue("name"), cpmigrate.CreateOptions{
			Reviewed: in.Reviewed, PHP: in.PHP, PageCache: in.PageCache, Pool: in.Pool, Node: in.Node, Python: in.Python,
		})
		if err != nil {
			importFail(log, w, err, "create site from cloudpanel report")

			return
		}

		writeJSON(w, http.StatusCreated, site)
	}))

	// The import copies from the old server what the report names for the
	// site; the body only lists the databases. Starting it again is the
	// final sync.
	mux.Op("POST "+base+"/{id}/sites/{name}/import", openapi.Operation{
		ID: "startCloudPanelImport", Tag: "migrations", Summary: "Copy the site's files and databases from the old server", In: cloudPanelImportRequest{}, Out: agentapi.CloudPanelImport{}, Status: http.StatusAccepted,
	}, a.allow("migrations:write", func(w http.ResponseWriter, r *http.Request, p auth.Principal) {
		var in cloudPanelImportRequest
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
	mux.Op("POST /api/v1/migrations/cloudpanel/sites/{name}/activate", openapi.Operation{
		ID: "activateCloudPanelImport", Tag: "migrations", Summary: "Swap the imported files in as the web root", Out: agentapi.CloudPanelActivated{},
	}, a.allow("migrations:write", func(w http.ResponseWriter, r *http.Request, p auth.Principal) {
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
	mux.Op("GET /api/v1/migrations/cloudpanel/imports", openapi.Operation{
		ID: "listCloudPanelImports", Tag: "migrations", Summary: "List the last imports", Out: importListJSON{}, Query: []openapi.Param{{Name: "site", Description: "only the imports of this site"}},
	}, a.allow("migrations:read", func(w http.ResponseWriter, r *http.Request, _ auth.Principal) {
		ctx, cancel := context.WithTimeout(r.Context(), agentTimeout)
		defer cancel()

		list, err := svc.Imports(ctx, r.URL.Query().Get("site"))
		if err != nil {
			importFail(log, w, err, "list cloudpanel imports")

			return
		}

		writeJSON(w, http.StatusOK, importListJSON{Imports: list})
	}))

	mux.Op("GET /api/v1/migrations/cloudpanel/imports/{id}", openapi.Operation{
		ID: "getCloudPanelImport", Tag: "migrations", Summary: "Get an import", Out: agentapi.CloudPanelImport{},
	}, a.allow("migrations:read", func(w http.ResponseWriter, r *http.Request, _ auth.Principal) {
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
	// this starts loads whose start failed again. A dump whose load started
	// already is not loaded twice, also after a panel restart.
	mux.Op("POST /api/v1/migrations/cloudpanel/imports/{id}/load", openapi.Operation{
		ID: "loadCloudPanelDumps", Tag: "migrations", Summary: "Start the dump loads of an import again", Out: dumpLoadListJSON{}, Status: http.StatusAccepted,
	}, a.allow("migrations:write", func(w http.ResponseWriter, r *http.Request, p auth.Principal) {
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

		writeJSON(w, http.StatusAccepted, dumpLoadListJSON{Loads: loads})
	}))

	mux.Op("GET /api/v1/migrations/cloudpanel/imports/{id}/loads", openapi.Operation{
		ID: "listCloudPanelDumpLoads", Tag: "migrations", Summary: "List the dump loads of an import", Out: dumpLoadListJSON{},
	}, a.allow("migrations:read", func(w http.ResponseWriter, r *http.Request, _ auth.Principal) {
		ctx, cancel := context.WithTimeout(r.Context(), agentTimeout)
		defer cancel()

		loads, err := svc.Loads(ctx, r.PathValue("id"))
		if err != nil {
			importFail(log, w, err, "read cloudpanel dump loads")

			return
		}

		writeJSON(w, http.StatusOK, dumpLoadListJSON{Loads: loads})
	}))

	// The DNS cut-over (ADR-0031): POST starts watching the site's domains,
	// HTTPS is turned on once they all point to this server; DELETE stops
	// a waiting one.
	cutover := func(scope authz.Action, run func(ctx context.Context, actor int64, site string) (cpmigrate.Cutover, error), status int) http.HandlerFunc {
		return a.allow(scope, func(w http.ResponseWriter, r *http.Request, p auth.Principal) {
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

	mux.Op("POST "+cutoverPath, openapi.Operation{
		ID: "startCloudPanelCutover", Tag: "migrations", Summary: "Wait for the DNS switch, then turn HTTPS on", Out: cpmigrate.Cutover{}, Status: http.StatusAccepted,
	}, cutover("migrations:write", svc.StartCutover, http.StatusAccepted))
	mux.Op("GET "+cutoverPath, openapi.Operation{
		ID: "getCloudPanelCutover", Tag: "migrations", Summary: "State of the DNS cut-over", Out: cpmigrate.Cutover{},
	}, cutover("migrations:read", func(ctx context.Context, _ int64, site string) (cpmigrate.Cutover, error) {
		return svc.Cutover(ctx, site)
	}, http.StatusOK))
	mux.Op("DELETE "+cutoverPath, openapi.Operation{
		ID: "cancelCloudPanelCutover", Tag: "migrations", Summary: "Stop a waiting cut-over", Out: cpmigrate.Cutover{},
	}, cutover("migrations:write", svc.CancelCutover, http.StatusOK))

	mux.Op("DELETE "+base+"/{id}", openapi.Operation{
		ID: "deleteCloudPanelReport", Tag: "migrations", Summary: "Delete a report",
	}, a.allow("migrations:write", func(w http.ResponseWriter, r *http.Request, p auth.Principal) {
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

		t := plan.Target{Panel: sites.PanelDomain()}

		for _, s := range list {
			t.Sites = append(t.Sites, s.Name)
			t.Domains = append(t.Domains, s.Domains...)
		}

		if t.PHP, err = sites.PHPVersions(ctx); err != nil {
			return plan.Target{}, err
		}

		if t.Node, err = sites.NodeLines(ctx); err != nil {
			return plan.Target{}, err
		}

		if t.Python, err = sites.PythonLines(ctx); err != nil {
			return plan.Target{}, err
		}

		return t, nil
	}
}

// setWWW takes over the old site's redirect hosts as redirect domains
// (ADR-0068) and its www redirect (ADR-0065). The site stands without
// them; the admin can set them on the site's page.
func setWWW(ctx context.Context, sites *hosting.Service, log *slog.Logger, actor int64, s cpmigrate.NewSite) {
	if len(s.RedirectDomains) > 0 {
		served := make([]string, 0, len(s.Domains))

		for _, d := range s.Domains {
			if !slices.Contains(s.RedirectDomains, d) {
				served = append(served, d)
			}
		}

		// The new site has no certificate yet, so this goes live at once.
		if _, _, err := sites.ChangeDomains(ctx, actor, s.Name, hosting.DomainsRequest{Domains: served, RedirectDomains: s.RedirectDomains}); err != nil {
			log.WarnContext(ctx, "imported redirect domains not set", "site", s.Name, "domains", s.RedirectDomains, "err", err)
		}
	}

	if s.WWWRedirect == "" {
		return
	}

	if _, err := sites.SetWWWRedirect(ctx, actor, s.Name, s.WWWRedirect); err != nil {
		log.WarnContext(ctx, "imported www redirect not set", "site", s.Name, "mode", s.WWWRedirect, "err", err)
	}
}

// siteCreator creates a migrated site through the site service: no
// certificate yet, DNS still points to the old server.
func siteCreator(sites *hosting.Service, log *slog.Logger) cpmigrate.SiteCreator {
	if log == nil {
		log = slog.New(slog.DiscardHandler)
	}

	return func(ctx context.Context, actor int64, s cpmigrate.NewSite) (cpmigrate.Created, error) {
		// The old site may call programs from PHP; it keeps that until the
		// admin turns it off in the site's settings (ADR-0033).
		req := hosting.CreateRequest{Name: s.Name, Pool: s.Pool, PHP: s.PHP, Domains: s.Domains, PHPPrograms: s.PHP != "", Profile: s.Profile}

		// A reverse proxy site has no account: nothing else comes with it.
		if s.Proxy != "" {
			if _, err := sites.Create(ctx, actor, hosting.CreateRequest{Name: s.Name, Domains: s.Domains, Proxy: &store.SiteProxy{Target: s.Proxy}}); err != nil {
				return cpmigrate.Created{}, err
			}

			setWWW(ctx, sites, log, actor, s)

			return cpmigrate.Created{}, nil
		}

		// A Node.js site's app stays stopped until its start command is set
		// (ADR-0054); the files land in htdocs, its working directory.
		if s.Node != "" {
			req.App = &store.SiteApp{Node: s.Node, Workdir: agentapi.DefaultAppWorkdir}
		}

		// The same for a Python site (ADR-0061); Hostashi makes its .venv.
		if s.Python != "" {
			req.App = &store.SiteApp{Python: s.Python, Workdir: agentapi.DefaultAppWorkdir}
		}

		if u := s.Usage; u != nil && u.Bytes >= 0 && u.Inodes >= 0 {
			// Disk limits of at least the old use plus 50 % (ADR-0015), so
			// the import does not stop at the quota.
			req.Usage = &hosting.Usage{Bytes: uint64(u.Bytes), Inodes: uint64(u.Inodes)}
		}

		if _, err := sites.Create(ctx, actor, req); err != nil {
			return cpmigrate.Created{}, err
		}

		setWWW(ctx, sites, log, actor, s)

		var n cpmigrate.Created

		for _, j := range s.Cron {
			_, err := sites.CreateCronJob(ctx, actor, s.Name, hosting.CronInput{Schedule: j.Schedule, Command: j.Command, Paused: true})
			if err != nil {
				// The site stands; the plan's review tells the admin to
				// compare the jobs.
				log.WarnContext(ctx, "imported cron job not created", "site", s.Name, "line", j.Line, "err", err)

				continue
			}

			n.Cron++
		}

		// SFTP only: shell access is given per key in the site's settings
		// (ADR-0032). Without SSH on this panel the keys stay behind.
		for _, k := range s.SSHKeys {
			_, err := sites.CreateSSHKey(ctx, actor, s.Name, hosting.SSHKeyInput{Label: k.Label, Access: sshkeys.SFTP, Public: k.Public})
			if errors.Is(err, hosting.ErrNoSSH) {
				log.WarnContext(ctx, "imported SSH keys not created: SSH is not available", "site", s.Name, "keys", len(s.SSHKeys))

				break
			}

			if err != nil {
				log.WarnContext(ctx, "imported SSH key not created", "site", s.Name, "label", k.Label, "err", err)

				continue
			}

			n.SSHKeys++
		}

		// A cache in front of the old site becomes the page cache
		// (ADR-0046). The default size counts against the site's disk.
		// The old pool's PHP settings (ADR-0049), clamped by the plan.
		if s.PHPSettings != nil {
			if _, err := sites.SetPHPSettings(ctx, actor, s.Name, *s.PHPSettings); err != nil {
				log.WarnContext(ctx, "imported PHP settings not set", "site", s.Name, "err", err)
			} else {
				n.PHPSettings = true
			}
		}

		if s.PageCache != "" {
			_, err := sites.SetPageCache(ctx, actor, s.Name, hosting.PageCacheSettings{
				Enabled: true, Preset: s.PageCache, Minutes: agentapi.DefaultPageCacheMinutes,
			})
			if err != nil {
				log.WarnContext(ctx, "imported page cache not switched on", "site", s.Name, "err", err)
			} else {
				n.PageCache = true
			}
		}

		// The site's own cache with the default size (ADR-0045). The site
		// stands without it; the plan tells the admin to check.
		if s.ObjectCache {
			_, err := sites.SetObjectCache(ctx, actor, s.Name, true, 0)
			switch {
			case errors.Is(err, hosting.ErrNoObjectCache):
				log.WarnContext(ctx, "imported object cache not created: not available", "site", s.Name)
			case err != nil:
				log.WarnContext(ctx, "imported object cache not created", "site", s.Name, "err", err)
			default:
				n.ObjectCache = true
			}
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

// resolvConf answers the cut-over's lookups from the server's resolvers,
// read again for every lookup so that a change is picked up.
type resolvConf string

// Lookup implements cpmigrate.Resolver.
func (p resolvConf) Lookup(ctx context.Context, host string) ([]netip.Addr, time.Duration, error) {
	c, err := dnsq.FromResolvConf(string(p))
	if err != nil {
		return nil, 0, err
	}

	r, err := c.Lookup(ctx, host)
	if errors.Is(err, dnsq.ErrNotFound) {
		return nil, 0, cpmigrate.ErrDomainNotFound
	}

	if err != nil {
		return nil, 0, err
	}

	return r.Addrs, r.TTL, nil
}
