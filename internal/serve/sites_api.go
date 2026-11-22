// SPDX-License-Identifier: AGPL-3.0-or-later

package serve

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"time"

	"github.com/maki-it/hostashi/internal/agentapi"
	"github.com/maki-it/hostashi/internal/agentrpc"
	"github.com/maki-it/hostashi/internal/auth"
	"github.com/maki-it/hostashi/internal/authz"
	"github.com/maki-it/hostashi/internal/hosting"
	"github.com/maki-it/hostashi/internal/store"
)

type siteJSON struct {
	Name  string `json:"name"`
	Pool  string `json:"pool"`
	State string `json:"state"`
	Type  string `json:"type"` // static, php, node, app, proxy, redirect (ADR-0054)
	// Web is the document root folder below htdocs and the static-site
	// options (ADR-0054 addendum 1).
	Web      webJSON `json:"web"`
	PHP      string  `json:"php"`      // PHP version, "" for no PHP
	Disabled bool    `json:"disabled"` // answers 503, PHP stopped
	// DisabledPage is the maintenance page shown while disabled (ADR-0047).
	DisabledPage string `json:"disabledPage"`
	// Maintenance: other addresses get the page with 503 (ADR-0027).
	Maintenance maintenanceJSON `json:"maintenance"`
	Domains     []string        `json:"domains"` // the first one is the primary domain
	TLS         tlsJSON         `json:"tls"`
	// PHPPrograms: the site's PHP may start programs (ADR-0033).
	PHPPrograms bool      `json:"phpPrograms"`
	CreatedAt   time.Time `json:"createdAt"`
	UpdatedAt   time.Time `json:"updatedAt"`
}

type webJSON struct {
	Dir        string `json:"dir"`
	SPA        bool   `json:"spa"`
	AssetCache bool   `json:"assetCache"`
	// AssetCacheSeconds and AssetCacheExts are the effective values: the
	// defaults when the site keeps them.
	AssetCacheSeconds int      `json:"assetCacheSeconds"`
	AssetCacheExts    []string `json:"assetCacheExts"`
}

func toWebJSON(s store.Site) webJSON {
	w := webJSON{Dir: s.WebDir, SPA: s.SPA, AssetCache: s.AssetCache, AssetCacheSeconds: s.AssetCacheSeconds, AssetCacheExts: s.AssetCacheExts}
	if w.AssetCacheSeconds == 0 {
		w.AssetCacheSeconds = agentapi.DefaultAssetCacheSeconds
	}

	if len(w.AssetCacheExts) == 0 {
		w.AssetCacheExts = agentapi.DefaultAssetCacheExts
	}

	return w
}

// tlsJSON is the HTTPS state of a site: wanted (enabled), served (active),
// HSTS, the live certificate, and the reason of the last failed attempt.
type tlsJSON struct {
	Enabled  bool       `json:"enabled"`
	Active   bool       `json:"active"`
	HSTS     bool       `json:"hsts"`
	Domains  []string   `json:"domains"`
	NotAfter *time.Time `json:"notAfter"`
	Error    string     `json:"error,omitempty"`
	FailedAt *time.Time `json:"failedAt,omitempty"`
	// Source is where the certificate comes from ("letsencrypt" or
	// "custom"); CertSource where the live one came from ("" for none).
	Source     string `json:"source"`
	CertSource string `json:"certSource"`
	// Trusted is false for an own certificate without a public chain.
	Trusted bool `json:"trusted"`
	// RenewAt is the last "renew now"; RenewPending while it runs.
	RenewAt      *time.Time `json:"renewAt,omitempty"`
	RenewPending bool       `json:"renewPending"`
}

// panelTLSJSON is the HTTPS state of the panel's own address: whether
// nginx serves one (enabled), whether HTTPS is live (active), the live
// certificate and the reason of the last failed attempt.
type panelTLSJSON struct {
	Enabled  bool       `json:"enabled"`
	Domain   string     `json:"domain"`
	Active   bool       `json:"active"`
	Domains  []string   `json:"domains"`
	NotAfter *time.Time `json:"notAfter"`
	Error    string     `json:"error,omitempty"`
	FailedAt *time.Time `json:"failedAt,omitempty"`
	// As for sites (ADR-0039).
	Source       string     `json:"source"`
	CertSource   string     `json:"certSource"`
	Trusted      bool       `json:"trusted"`
	RenewAt      *time.Time `json:"renewAt,omitempty"`
	RenewPending bool       `json:"renewPending"`
}

func toPanelTLSJSON(p hosting.PanelTLS) panelTLSJSON {
	return panelTLSJSON{
		Enabled: p.Domain != "", Domain: p.Domain, Active: p.Live, Domains: p.Domains,
		NotAfter: timeOrNil(p.NotAfter), Error: p.Error, FailedAt: timeOrNil(p.FailedAt),
		Source: p.WantedSource(), CertSource: p.LiveSource(), Trusted: !p.Untrusted,
		RenewAt: timeOrNil(p.RenewAt), RenewPending: p.RenewForced,
	}
}

func timeOrNil(t time.Time) *time.Time {
	if t.IsZero() {
		return nil
	}

	return &t
}

func toSiteJSON(svc *hosting.Service, s store.Site) siteJSON {
	d := s.Domains
	if d == nil {
		d = []string{}
	}

	td := s.TLSDomains
	if td == nil {
		td = []string{}
	}

	return siteJSON{
		Name: s.Name, Pool: s.Pool, State: s.State, Type: s.Type, Web: toWebJSON(s), PHP: s.PHP, Disabled: s.Disabled, DisabledPage: hosting.DisabledPageID(s), Domains: d, Maintenance: toMaintenanceJSON(s),
		TLS: tlsJSON{
			Enabled: s.TLS, Active: svc.TLSActive(s), HSTS: s.HSTS, Domains: td, NotAfter: timeOrNil(s.TLSNotAfter),
			Error: s.TLSError, FailedAt: timeOrNil(s.TLSFailedAt),
			Source: s.TLSSource, CertSource: s.TLSCertSource, Trusted: s.TLSTrusted,
			RenewAt: timeOrNil(s.TLSRenewAt), RenewPending: s.TLSRenewForced,
		},
		PHPPrograms: s.PHPPrograms,
		CreatedAt:   s.CreatedAt, UpdatedAt: s.UpdatedAt,
	}
}

// agentTimeout bounds one site change: account, web server switch and checks.
const agentTimeout = 25 * time.Second

// siteFail maps service and agent errors. Agent messages are fixed texts
// chosen by the agent (agentrpc.Error), so they may be shown.
func siteFail(log *slog.Logger, w http.ResponseWriter, err error, what string) {
	var ae *agentrpc.Error

	switch {
	case errors.Is(err, hosting.ErrInvalid):
		writeError(w, http.StatusUnprocessableEntity, "invalid_argument", err.Error())
	case errors.Is(err, hosting.ErrNotFound):
		writeError(w, http.StatusNotFound, "not_found", "no such site")
	case errors.Is(err, hosting.ErrPageNotFound):
		writeError(w, http.StatusNotFound, "page_not_found", "no such page")
	case errors.Is(err, hosting.ErrExists):
		writeError(w, http.StatusConflict, "site_exists", "a site with this name exists")
	case errors.Is(err, hosting.ErrDomainTaken):
		writeError(w, http.StatusConflict, "domain_taken", err.Error())
	case errors.Is(err, hosting.ErrDomainReserved):
		writeError(w, http.StatusConflict, "domain_reserved", "this domain is the panel's own address")
	case errors.Is(err, hosting.ErrNoTLS):
		writeError(w, http.StatusConflict, "tls_unavailable", "certificates are not available on this panel")
	case errors.Is(err, hosting.ErrNoPanel):
		writeError(w, http.StatusConflict, "panel_address_missing", "the panel has no address of its own")
	case errors.Is(err, hosting.ErrTooSoon):
		writeError(w, http.StatusTooManyRequests, "renew_too_soon", "renewed less than 10 minutes ago, try again later")
	case errors.Is(err, hosting.ErrCertInUse):
		writeError(w, http.StatusConflict, "certificate_in_use", "turn HTTPS off before deleting the certificate")
	case errors.Is(err, hosting.ErrNotLetsEncrypt):
		writeError(w, http.StatusConflict, "not_letsencrypt", "only a site with HTTPS from Let's Encrypt can be renewed")
	case errors.Is(err, hosting.ErrState):
		writeError(w, http.StatusConflict, "site_busy", "the site is being created or deleted")
	case errors.Is(err, hosting.ErrSiteType):
		writeError(w, http.StatusConflict, "wrong_site_type", "not possible for this type of site")
	case errors.As(err, &ae):
		log.Warn(what+": agent refused", "code", ae.Code, "message", ae.Message)

		switch ae.Code {
		case agentrpc.CodeConflict, agentrpc.CodePrecondition:
			writeError(w, http.StatusConflict, "agent_"+ae.Code, ae.Message)
		case agentrpc.CodeNotFound:
			writeError(w, http.StatusConflict, "agent_not_found", ae.Message)
		case agentrpc.CodeInvalidArgument:
			writeError(w, http.StatusUnprocessableEntity, "invalid_argument", ae.Message)
		default:
			writeError(w, http.StatusBadGateway, "agent_error", "the agent could not complete the change")
		}
	case errors.Is(err, context.DeadlineExceeded):
		writeError(w, http.StatusGatewayTimeout, "agent_timeout", "the agent did not answer in time")
	default:
		log.Error(what+" failed", "err", err)
		writeError(w, http.StatusServiceUnavailable, "agent_unavailable", "the change could not be completed, try again")
	}
}

// registerSites adds the site routes. Who may call which one is in
// internal/authz (ADR-0035).
func registerSites(mux *routeMux, a authn, svc *hosting.Service, log *slog.Logger) {
	mux.HandleFunc("GET /api/v1/sites", a.allow("sites:list", func(w http.ResponseWriter, r *http.Request, p auth.Principal) {
		sites, err := svc.List(r.Context())
		if err != nil {
			siteFail(log, w, err, "list sites")

			return
		}

		sees, err := a.siteFilter(r, p)
		if err != nil {
			a.fail(w, err, "list sites")

			return
		}

		out := make([]siteJSON, 0, len(sites))
		for _, s := range sites {
			if sees == nil || sees(s.Name) {
				out = append(out, toSiteJSON(svc, s))
			}
		}

		writeJSON(w, http.StatusOK, map[string][]siteJSON{"sites": out})
	}))

	mux.HandleFunc("GET /api/v1/sites/{name}", a.allow("sites:read", func(w http.ResponseWriter, r *http.Request, _ auth.Principal) {
		s, err := svc.Get(r.Context(), r.PathValue("name"))
		if err != nil {
			siteFail(log, w, err, "get site")

			return
		}

		writeJSON(w, http.StatusOK, toSiteJSON(svc, s))
	}))

	mux.HandleFunc("POST /api/v1/sites", a.allow("sites:create", func(w http.ResponseWriter, r *http.Request, p auth.Principal) {
		var in struct {
			Name    string   `json:"name"`
			Pool    string   `json:"pool"`
			PHP     string   `json:"php"`
			Domains []string `json:"domains"`
			TLS     bool     `json:"tls"`  // ask for a certificate right away
			HSTS    bool     `json:"hsts"` // sent once the site is on HTTPS
			// Headers are the first security headers (ADR-0056); left out,
			// the site gets agentapi.DefaultWebHeaders, {} sends none.
			Headers *agentapi.WebHeaders `json:"headers"`
		}
		if !decodeJSON(w, r, &in) {
			return
		}

		if in.Headers == nil {
			d := agentapi.DefaultWebHeaders
			in.Headers = &d
		}

		ctx, cancel := context.WithTimeout(r.Context(), agentTimeout)
		defer cancel()

		s, err := svc.Create(ctx, p.User.ID, hosting.CreateRequest{
			Name: in.Name, Pool: in.Pool, PHP: in.PHP, Domains: in.Domains, TLS: in.TLS, HSTS: in.HSTS, Headers: in.Headers,
		})
		if err != nil {
			headersFail(log, w, err, "create site")

			return
		}

		writeJSON(w, http.StatusCreated, toSiteJSON(svc, s))
	}))

	mux.HandleFunc("PUT /api/v1/sites/{name}/domains", a.allow("sites.domains:write", func(w http.ResponseWriter, r *http.Request, p auth.Principal) {
		var in struct {
			Domains []string `json:"domains"`
		}
		if !decodeJSON(w, r, &in) {
			return
		}

		ctx, cancel := context.WithTimeout(r.Context(), agentTimeout)
		defer cancel()

		s, err := svc.SetDomains(ctx, p.User.ID, r.PathValue("name"), in.Domains)
		if err != nil {
			siteFail(log, w, err, "set domains")

			return
		}

		writeJSON(w, http.StatusOK, toSiteJSON(svc, s))
	}))

	mux.HandleFunc("PUT /api/v1/sites/{name}/php", a.allow("sites.php:write", func(w http.ResponseWriter, r *http.Request, p auth.Principal) {
		var in struct {
			PHP *string `json:"php"`
		}
		if !decodeJSON(w, r, &in) {
			return
		}

		if in.PHP == nil {
			writeError(w, http.StatusUnprocessableEntity, "invalid_argument", `"php" is required ("" turns PHP off)`)

			return
		}

		ctx, cancel := context.WithTimeout(r.Context(), agentTimeout)
		defer cancel()

		s, err := svc.SetPHP(ctx, p.User.ID, r.PathValue("name"), *in.PHP)
		if err != nil {
			siteFail(log, w, err, "set php")

			return
		}

		writeJSON(w, http.StatusOK, toSiteJSON(svc, s))
	}))

	// The document root folder and the static-site options (ADR-0054).
	mux.HandleFunc("PUT /api/v1/sites/{name}/web", a.allow("sites.web:write", func(w http.ResponseWriter, r *http.Request, p auth.Principal) {
		var in struct {
			Dir        *string `json:"dir"`
			SPA        *bool   `json:"spa"`
			AssetCache *bool   `json:"assetCache"`
			// Optional: missing keeps the defaults.
			AssetCacheSeconds int      `json:"assetCacheSeconds"`
			AssetCacheExts    []string `json:"assetCacheExts"`
		}
		if !decodeJSON(w, r, &in) {
			return
		}

		if in.Dir == nil || in.SPA == nil || in.AssetCache == nil {
			writeError(w, http.StatusUnprocessableEntity, "invalid_argument", `"dir", "spa" and "assetCache" are required`)

			return
		}

		ctx, cancel := context.WithTimeout(r.Context(), agentTimeout)
		defer cancel()

		s, err := svc.SetWebOptions(ctx, p.User.ID, r.PathValue("name"), store.WebOptions{
			Dir: *in.Dir, SPA: *in.SPA, AssetCache: *in.AssetCache, AssetCacheSeconds: in.AssetCacheSeconds, AssetCacheExts: in.AssetCacheExts,
		})
		if err != nil {
			siteFail(log, w, err, "set web options")

			return
		}

		writeJSON(w, http.StatusOK, toSiteJSON(svc, s))
	}))

	mux.HandleFunc("PUT /api/v1/sites/{name}/tls", a.allow("sites.tls:write", func(w http.ResponseWriter, r *http.Request, p auth.Principal) {
		var in struct {
			Enabled *bool `json:"enabled"`
		}
		if !decodeJSON(w, r, &in) {
			return
		}

		if in.Enabled == nil {
			writeError(w, http.StatusUnprocessableEntity, "invalid_argument", `"enabled" is required`)

			return
		}

		ctx, cancel := context.WithTimeout(r.Context(), agentTimeout)
		defer cancel()

		// On: the certificate comes in the background; "tls.active" turns
		// true when the site is served over HTTPS.
		s, err := svc.SetTLS(ctx, p.User.ID, r.PathValue("name"), *in.Enabled)
		if err != nil {
			siteFail(log, w, err, "set tls")

			return
		}

		code := http.StatusOK
		if *in.Enabled && !svc.TLSActive(s) {
			code = http.StatusAccepted
		}

		writeJSON(w, code, toSiteJSON(svc, s))
	}))

	// HSTS can be set before HTTPS is active; it is sent only over HTTPS.
	mux.HandleFunc("PUT /api/v1/sites/{name}/hsts", a.allow("sites.hsts:write", func(w http.ResponseWriter, r *http.Request, p auth.Principal) {
		var in struct {
			Enabled *bool `json:"enabled"`
		}
		if !decodeJSON(w, r, &in) {
			return
		}

		if in.Enabled == nil {
			writeError(w, http.StatusUnprocessableEntity, "invalid_argument", `"enabled" is required`)

			return
		}

		ctx, cancel := context.WithTimeout(r.Context(), agentTimeout)
		defer cancel()

		s, err := svc.SetHSTS(ctx, p.User.ID, r.PathValue("name"), *in.Enabled)
		if err != nil {
			siteFail(log, w, err, "set hsts")

			return
		}

		writeJSON(w, http.StatusOK, toSiteJSON(svc, s))
	}))

	// Programs from PHP (exec, system, ...) are off unless allowed here.
	mux.HandleFunc("PUT /api/v1/sites/{name}/php-programs", a.allow("sites.programs:write", func(w http.ResponseWriter, r *http.Request, p auth.Principal) {
		var in struct {
			Enabled *bool `json:"enabled"`
		}
		if !decodeJSON(w, r, &in) {
			return
		}

		if in.Enabled == nil {
			writeError(w, http.StatusUnprocessableEntity, "invalid_argument", `"enabled" is required`)

			return
		}

		ctx, cancel := context.WithTimeout(r.Context(), agentTimeout)
		defer cancel()

		s, err := svc.SetPHPPrograms(ctx, p.User.ID, r.PathValue("name"), *in.Enabled)
		if err != nil {
			siteFail(log, w, err, "set php programs")

			return
		}

		writeJSON(w, http.StatusOK, toSiteJSON(svc, s))
	}))

	// Disabling keeps everything of the site but serves 503 and stops PHP.
	mux.HandleFunc("PUT /api/v1/sites/{name}/disabled", a.allow("sites.disabled:write", func(w http.ResponseWriter, r *http.Request, p auth.Principal) {
		var in struct {
			Disabled *bool `json:"disabled"`
			// Page is the maintenance page shown while disabled
			// (ADR-0047); empty: the built-in page.
			Page string `json:"page"`
		}
		if !decodeJSON(w, r, &in) {
			return
		}

		if in.Disabled == nil {
			writeError(w, http.StatusUnprocessableEntity, "invalid_argument", `"disabled" is required`)

			return
		}

		ctx, cancel := context.WithTimeout(r.Context(), agentTimeout)
		defer cancel()

		s, err := svc.SetDisabled(ctx, p.User.ID, r.PathValue("name"), *in.Disabled, in.Page)
		if err != nil {
			siteFail(log, w, err, "set disabled")

			return
		}

		writeJSON(w, http.StatusOK, toSiteJSON(svc, s))
	}))

	// The versions a site can use: installed ones from the agent, plus the
	// supported list and which of them are legacy (ADR-0019).
	mux.HandleFunc("GET /api/v1/php/versions", a.allow("php.versions:read", func(w http.ResponseWriter, r *http.Request, _ auth.Principal) {
		ctx, cancel := context.WithTimeout(r.Context(), agentTimeout)
		defer cancel()

		installed, err := svc.PHPVersions(ctx)
		if err != nil {
			siteFail(log, w, err, "php versions")

			return
		}

		if installed == nil {
			installed = []string{}
		}

		security, active := phpVersionInfo()
		writeJSON(w, http.StatusOK, map[string]any{
			"installed": installed, "supported": agentapi.PHPVersions, "legacy": agentapi.LegacyPHPVersions,
			// End of security fixes and of active support per version (ADR-0036).
			"securityUntil": security, "activeUntil": active,
			// The distribution's own version, marked in the panel (ADR-0037).
			"distro": distroPHP(),
		})
	}))

	mux.HandleFunc("DELETE /api/v1/sites/{name}", a.allow("sites:delete", func(w http.ResponseWriter, r *http.Request, p auth.Principal) {
		ctx, cancel := context.WithTimeout(r.Context(), agentTimeout)
		defer cancel()

		if err := svc.Delete(ctx, p.User.ID, r.PathValue("name")); err != nil {
			siteFail(log, w, err, "delete site")

			return
		}

		w.WriteHeader(http.StatusNoContent)
	}))

	// Deletes the site and parks the chosen domains of it (ADR-0048). It
	// deletes like DELETE and parks like PUT parked-domains, so it needs
	// both permissions.
	mux.HandleFunc("POST /api/v1/sites/{name}/park", a.allow("sites:park", func(w http.ResponseWriter, r *http.Request, p auth.Principal) {
		if !p.Allows(authz.ScopeOf("parked:write")) {
			writeError(w, http.StatusForbidden, "insufficient_scope", "the token lacks the scope "+authz.ScopeOf("parked:write"))

			return
		}

		if !authz.Allowed(authz.Role(p.User.Role), "parked:write") {
			writeError(w, http.StatusForbidden, "forbidden", "your role does not allow this")

			return
		}

		var in struct {
			Domains []string `json:"domains"`
			Page    string   `json:"page"`
			TLS     *bool    `json:"tls"`
			Confirm string   `json:"confirm"`
		}
		if !decodeJSON(w, r, &in) {
			return
		}

		ctx, cancel := context.WithTimeout(r.Context(), agentTimeout)
		defer cancel()

		req := hosting.ParkRequest{Domains: in.Domains, Page: in.Page, TLS: in.TLS == nil || *in.TLS, Confirm: in.Confirm}
		if err := svc.DeleteAndPark(ctx, p.User.ID, r.PathValue("name"), req); err != nil {
			pageFail(log, w, err, "delete and park site")

			return
		}

		w.WriteHeader(http.StatusNoContent)
	}))

	// The panel's own HTTPS (ADR-0023). Disabled without a host name in
	// the public URL; then the panel is reachable through the SSH tunnel only.
	mux.HandleFunc("GET /api/v1/panel/tls", a.allow("panel.tls:read", func(w http.ResponseWriter, r *http.Request, _ auth.Principal) {
		p, err := svc.PanelTLS(r.Context())
		if err != nil {
			siteFail(log, w, err, "panel tls")

			return
		}

		writeJSON(w, http.StatusOK, toPanelTLSJSON(p))
	}))

	// Sends the stored state to the web server again, for example after a
	// change failed while the agent was down.
	mux.HandleFunc("POST /api/v1/web/sync", a.allow("web:sync", func(w http.ResponseWriter, r *http.Request, _ auth.Principal) {
		ctx, cancel := context.WithTimeout(r.Context(), agentTimeout)
		defer cancel()

		if err := svc.Sync(ctx); err != nil {
			siteFail(log, w, err, "web sync")

			return
		}

		// The cron jobs too (ADR-0028); a site that fails is logged.
		if err := svc.SyncCron(ctx); err != nil {
			log.WarnContext(ctx, "cron sync", "err", err)
		}

		// And the SSH keys (ADR-0032).
		if err := svc.SyncSSH(ctx); err != nil {
			log.WarnContext(ctx, "ssh sync", "err", err)
		}

		w.WriteHeader(http.StatusNoContent)
	}))
}
