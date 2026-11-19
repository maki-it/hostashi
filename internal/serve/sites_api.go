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
	"github.com/maki-it/hostashi/internal/hosting"
	"github.com/maki-it/hostashi/internal/store"
)

type siteJSON struct {
	Name      string    `json:"name"`
	Pool      string    `json:"pool"`
	State     string    `json:"state"`
	PHP       string    `json:"php"`      // PHP version, "" for no PHP
	Disabled  bool      `json:"disabled"` // answers 503, PHP stopped
	Domains   []string  `json:"domains"`  // the first one is the primary domain
	TLS       tlsJSON   `json:"tls"`
	CreatedAt time.Time `json:"createdAt"`
	UpdatedAt time.Time `json:"updatedAt"`
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
		Name: s.Name, Pool: s.Pool, State: s.State, PHP: s.PHP, Disabled: s.Disabled, Domains: d,
		TLS: tlsJSON{
			Enabled: s.TLS, Active: svc.TLSActive(s), HSTS: s.HSTS, Domains: td, NotAfter: timeOrNil(s.TLSNotAfter),
			Error: s.TLSError, FailedAt: timeOrNil(s.TLSFailedAt),
		},
		CreatedAt: s.CreatedAt, UpdatedAt: s.UpdatedAt,
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
	case errors.Is(err, hosting.ErrExists):
		writeError(w, http.StatusConflict, "site_exists", "a site with this name exists")
	case errors.Is(err, hosting.ErrDomainTaken):
		writeError(w, http.StatusConflict, "domain_taken", err.Error())
	case errors.Is(err, hosting.ErrNoTLS):
		writeError(w, http.StatusConflict, "tls_unavailable", "certificates are not available on this panel")
	case errors.Is(err, hosting.ErrState):
		writeError(w, http.StatusConflict, "site_busy", "the site is being created or deleted")
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

// registerSites adds the site routes. API tokens need the matching scope;
// every caller must be an admin (the only role so far).
func registerSites(mux *http.ServeMux, a authn, svc *hosting.Service, log *slog.Logger) {
	admin := func(scope string, h func(http.ResponseWriter, *http.Request, auth.Principal)) http.HandlerFunc {
		return a.scoped(scope, func(w http.ResponseWriter, r *http.Request, p auth.Principal) {
			if p.User.Role != "admin" {
				writeError(w, http.StatusForbidden, "forbidden", "only administrators can manage sites")

				return
			}

			h(w, r, p)
		})
	}

	mux.HandleFunc("GET /api/v1/sites", admin("sites:read", func(w http.ResponseWriter, r *http.Request, _ auth.Principal) {
		sites, err := svc.List(r.Context())
		if err != nil {
			siteFail(log, w, err, "list sites")

			return
		}

		out := make([]siteJSON, 0, len(sites))
		for _, s := range sites {
			out = append(out, toSiteJSON(svc, s))
		}

		writeJSON(w, http.StatusOK, map[string][]siteJSON{"sites": out})
	}))

	mux.HandleFunc("GET /api/v1/sites/{name}", admin("sites:read", func(w http.ResponseWriter, r *http.Request, _ auth.Principal) {
		s, err := svc.Get(r.Context(), r.PathValue("name"))
		if err != nil {
			siteFail(log, w, err, "get site")

			return
		}

		writeJSON(w, http.StatusOK, toSiteJSON(svc, s))
	}))

	mux.HandleFunc("POST /api/v1/sites", admin("sites:write", func(w http.ResponseWriter, r *http.Request, p auth.Principal) {
		var in struct {
			Name    string   `json:"name"`
			Pool    string   `json:"pool"`
			PHP     string   `json:"php"`
			Domains []string `json:"domains"`
			TLS     bool     `json:"tls"`  // ask for a certificate right away
			HSTS    bool     `json:"hsts"` // sent once the site is on HTTPS
		}
		if !decodeJSON(w, r, &in) {
			return
		}

		ctx, cancel := context.WithTimeout(r.Context(), agentTimeout)
		defer cancel()

		s, err := svc.Create(ctx, p.User.ID, hosting.CreateRequest{
			Name: in.Name, Pool: in.Pool, PHP: in.PHP, Domains: in.Domains, TLS: in.TLS, HSTS: in.HSTS,
		})
		if err != nil {
			siteFail(log, w, err, "create site")

			return
		}

		writeJSON(w, http.StatusCreated, toSiteJSON(svc, s))
	}))

	mux.HandleFunc("PUT /api/v1/sites/{name}/domains", admin("sites:write", func(w http.ResponseWriter, r *http.Request, p auth.Principal) {
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

	mux.HandleFunc("PUT /api/v1/sites/{name}/php", admin("sites:write", func(w http.ResponseWriter, r *http.Request, p auth.Principal) {
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

	mux.HandleFunc("PUT /api/v1/sites/{name}/tls", admin("sites:write", func(w http.ResponseWriter, r *http.Request, p auth.Principal) {
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
	mux.HandleFunc("PUT /api/v1/sites/{name}/hsts", admin("sites:write", func(w http.ResponseWriter, r *http.Request, p auth.Principal) {
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

	// Disabling keeps everything of the site but serves 503 and stops PHP.
	mux.HandleFunc("PUT /api/v1/sites/{name}/disabled", admin("sites:write", func(w http.ResponseWriter, r *http.Request, p auth.Principal) {
		var in struct {
			Disabled *bool `json:"disabled"`
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

		s, err := svc.SetDisabled(ctx, p.User.ID, r.PathValue("name"), *in.Disabled)
		if err != nil {
			siteFail(log, w, err, "set disabled")

			return
		}

		writeJSON(w, http.StatusOK, toSiteJSON(svc, s))
	}))

	// The versions a site can use: installed ones from the agent, plus the
	// supported list and which of them are legacy (ADR-0019).
	mux.HandleFunc("GET /api/v1/php/versions", admin("sites:read", func(w http.ResponseWriter, r *http.Request, _ auth.Principal) {
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

		writeJSON(w, http.StatusOK, map[string][]string{
			"installed": installed, "supported": agentapi.PHPVersions, "legacy": agentapi.LegacyPHPVersions,
		})
	}))

	mux.HandleFunc("DELETE /api/v1/sites/{name}", admin("sites:delete", func(w http.ResponseWriter, r *http.Request, p auth.Principal) {
		ctx, cancel := context.WithTimeout(r.Context(), agentTimeout)
		defer cancel()

		if err := svc.Delete(ctx, p.User.ID, r.PathValue("name")); err != nil {
			siteFail(log, w, err, "delete site")

			return
		}

		w.WriteHeader(http.StatusNoContent)
	}))

	// The panel's own HTTPS (ADR-0023). Disabled without a host name in
	// the public URL; then the panel is reachable through the SSH tunnel only.
	mux.HandleFunc("GET /api/v1/panel/tls", admin("sites:read", func(w http.ResponseWriter, r *http.Request, _ auth.Principal) {
		p, err := svc.PanelTLS(r.Context())
		if err != nil {
			siteFail(log, w, err, "panel tls")

			return
		}

		writeJSON(w, http.StatusOK, panelTLSJSON{
			Enabled: p.Domain != "", Domain: p.Domain, Active: p.Live, Domains: p.Domains,
			NotAfter: timeOrNil(p.NotAfter), Error: p.Error, FailedAt: timeOrNil(p.FailedAt),
		})
	}))

	// Sends the stored state to the web server again, for example after a
	// change failed while the agent was down.
	mux.HandleFunc("POST /api/v1/web/sync", admin("sites:write", func(w http.ResponseWriter, r *http.Request, _ auth.Principal) {
		ctx, cancel := context.WithTimeout(r.Context(), agentTimeout)
		defer cancel()

		if err := svc.Sync(ctx); err != nil {
			siteFail(log, w, err, "web sync")

			return
		}

		w.WriteHeader(http.StatusNoContent)
	}))
}
