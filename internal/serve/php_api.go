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
)

// PHPInstaller is the agent's part of installing PHP versions (ADR-0036).
type PHPInstaller interface {
	Versions(ctx context.Context) (agentapi.PHPVersionsResponse, error)
	Install(ctx context.Context, version string) (agentapi.PHPInstallStatus, error)
	Status(ctx context.Context) (agentapi.PHPInstallStatus, error)
}

// RPCPHPInstaller talks to the agent.
type RPCPHPInstaller struct{ C *agentrpc.Client }

// Versions implements PHPInstaller.
func (p RPCPHPInstaller) Versions(ctx context.Context) (agentapi.PHPVersionsResponse, error) {
	return agentapi.PHPVersionsList.Call(ctx, p.C, agentapi.PHPVersionsRequest{})
}

// Install implements PHPInstaller.
func (p RPCPHPInstaller) Install(ctx context.Context, version string) (agentapi.PHPInstallStatus, error) {
	return agentapi.PHPInstall.Call(ctx, p.C, agentapi.PHPInstallRequest{Version: version})
}

// Status implements PHPInstaller.
func (p RPCPHPInstaller) Status(ctx context.Context) (agentapi.PHPInstallStatus, error) {
	return agentapi.PHPInstallStatusOp.Call(ctx, p.C, agentapi.PHPInstallStatusRequest{})
}

// phpEOL reports whether a version gets no security fixes any more on day now.
func phpEOL(version string, now time.Time) bool {
	until, ok := agentapi.PHPSecurityUntil[version]
	if !ok {
		return true
	}

	t, err := time.Parse(time.DateOnly, until)

	return err != nil || now.After(t.Add(24*time.Hour))
}

type phpInstallJSON struct {
	agentapi.PHPInstallStatus

	// Repository: the sury.org source is set up (ADR-0019).
	Repository bool `json:"repository"`
	// Newer are the versions sury.org offers that Hostashi does not run yet.
	Newer []string `json:"newer"`
}

// registerPHPInstall adds GET and POST /api/v1/php/install (ADR-0036).
func registerPHPInstall(mux *routeMux, a authn, p PHPInstaller, st Settings, log *slog.Logger) {
	fail := func(w http.ResponseWriter, err error, what string) {
		var ae *agentrpc.Error

		switch {
		case errors.As(err, &ae) && ae.Code == agentrpc.CodePrecondition:
			writeError(w, http.StatusConflict, "php_install_refused", ae.Message)
		case errors.As(err, &ae) && ae.Code == agentrpc.CodeInvalidArgument:
			writeError(w, http.StatusUnprocessableEntity, "invalid_argument", ae.Message)
		case errors.As(err, &ae):
			log.Warn(what+": agent refused", "code", ae.Code, "message", ae.Message)
			writeError(w, http.StatusBadGateway, "agent_error", "the agent refused the request")
		case errors.Is(err, context.DeadlineExceeded):
			writeError(w, http.StatusGatewayTimeout, "agent_timeout", "the agent did not answer in time")
		default:
			log.Error(what+" failed", "err", err)
			writeError(w, http.StatusServiceUnavailable, "agent_unavailable", "the agent could not be reached, try again")
		}
	}

	mux.HandleFunc("GET /api/v1/php/install", a.allow("php.install:read", func(w http.ResponseWriter, r *http.Request, _ auth.Principal) {
		ctx, cancel := context.WithTimeout(r.Context(), agentTimeout)
		defer cancel()

		s, err := p.Status(ctx)
		if err != nil {
			fail(w, err, "php install status")

			return
		}

		v, err := p.Versions(ctx)
		if err != nil {
			fail(w, err, "php versions")

			return
		}

		out := phpInstallJSON{PHPInstallStatus: s, Repository: v.Repository, Newer: v.Newer}
		if out.Log == nil {
			out.Log = []string{}
		}

		if out.Newer == nil {
			out.Newer = []string{}
		}

		w.Header().Set("Cache-Control", "no-store")
		writeJSON(w, http.StatusOK, out)
	}))

	mux.HandleFunc("POST /api/v1/php/install", a.allow("php:install", func(w http.ResponseWriter, r *http.Request, pr auth.Principal) {
		var in struct {
			Version string `json:"version"`
			// ConfirmEOL is needed for a version without security fixes.
			ConfirmEOL bool `json:"confirmEol"`
		}

		if !decodeJSON(w, r, &in) {
			return
		}

		if err := agentapi.ValidatePHPVersion(in.Version); err != nil {
			writeError(w, http.StatusUnprocessableEntity, "invalid_argument", "version must be one of the supported PHP versions")

			return
		}

		eol := phpEOL(in.Version, time.Now())
		if eol && !in.ConfirmEOL {
			writeError(w, http.StatusConflict, "php_eol", "PHP "+in.Version+" gets no security fixes since "+agentapi.PHPSecurityUntil[in.Version]+"; confirm to install it anyway")

			return
		}

		ctx, cancel := context.WithTimeout(r.Context(), agentTimeout)
		defer cancel()

		s, err := p.Install(ctx, in.Version)
		if err != nil {
			fail(w, err, "php install")

			return
		}

		uid := pr.User.ID
		detail := map[string]string{"version": in.Version}

		if eol {
			detail["endOfLife"] = "true"
		}

		if err := st.Audit(r.Context(), &uid, "php.install", "", detail); err != nil {
			log.ErrorContext(r.Context(), "audit log", "action", "php.install", "err", err)
		}

		if s.Log == nil {
			s.Log = []string{}
		}

		w.Header().Set("Cache-Control", "no-store")
		writeJSON(w, http.StatusAccepted, s)
	}))
}

// phpVersionInfo is the end-of-life data the version list carries.
func phpVersionInfo() (security, active map[string]string) {
	security, active = map[string]string{}, map[string]string{}

	for _, v := range agentapi.PHPVersions {
		if d, ok := agentapi.PHPSecurityUntil[v]; ok {
			security[v] = d
		}

		if d, ok := agentapi.PHPActiveUntil[v]; ok {
			active[v] = d
		}
	}

	return security, active
}
