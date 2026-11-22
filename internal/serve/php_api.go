// SPDX-License-Identifier: AGPL-3.0-or-later

package serve

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/maki-it/hostashi/internal/openapi"

	"github.com/maki-it/hostashi/internal/agentapi"
	"github.com/maki-it/hostashi/internal/agentrpc"
	"github.com/maki-it/hostashi/internal/auth"
	"github.com/maki-it/hostashi/internal/doctor"
	"github.com/maki-it/hostashi/internal/store"
)

// PHPInstaller is the agent's part of installing PHP versions (ADR-0036).
type PHPInstaller interface {
	Versions(ctx context.Context) (agentapi.PHPVersionsResponse, error)
	Install(ctx context.Context, version string) (agentapi.PHPInstallStatus, error)
	Status(ctx context.Context) (agentapi.PHPInstallStatus, error)
	Remove(ctx context.Context, version string) (agentapi.PHPInstallStatus, error)
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

// Remove implements PHPInstaller.
func (p RPCPHPInstaller) Remove(ctx context.Context, version string) (agentapi.PHPInstallStatus, error) {
	return agentapi.PHPRemove.Call(ctx, p.C, agentapi.PHPRemoveRequest{Version: version})
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
type phpInstallRequest struct {
	Version string `json:"version"`
	// ConfirmEOL is needed for a version without security fixes.
	ConfirmEOL bool `json:"confirmEol"`
}

type phpRemoveRequest struct {
	Version string `json:"version"`
}

// registerPHPInstall adds the install routes and POST /api/v1/php/remove.
// sites lists the sites, so a version in use is refused; nil skips that
// check (the agent checks the pools again).
func registerPHPInstall(mux *routeMux, a authn, p PHPInstaller, st Settings, sites func(context.Context) ([]store.Site, error), log *slog.Logger) {
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

	mux.Op("GET /api/v1/php/install", openapi.Operation{
		ID: "getPHPInstall", Tag: "php", Summary: "Installed PHP versions and the state of a running installation", Out: phpInstallJSON{},
	}, a.allow("php.install:read", func(w http.ResponseWriter, r *http.Request, _ auth.Principal) {
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

	mux.Op("POST /api/v1/php/install", openapi.Operation{
		ID: "installPHP", Tag: "php", Summary: "Install a PHP version", In: phpInstallRequest{}, Out: agentapi.PHPInstallStatus{}, Status: http.StatusAccepted,
	}, a.allow("php:install", func(w http.ResponseWriter, r *http.Request, pr auth.Principal) {
		var in phpInstallRequest

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

	mux.Op("POST /api/v1/php/remove", openapi.Operation{
		ID: "removePHP", Tag: "php", Summary: "Remove a PHP version that no site uses", In: phpRemoveRequest{}, Out: agentapi.PHPInstallStatus{}, Status: http.StatusAccepted,
	}, a.allow("php:remove", func(w http.ResponseWriter, r *http.Request, pr auth.Principal) {
		var in phpRemoveRequest

		if !decodeJSON(w, r, &in) {
			return
		}

		if err := agentapi.ValidatePHPVersion(in.Version); err != nil {
			writeError(w, http.StatusUnprocessableEntity, "invalid_argument", "version must be one of the supported PHP versions")

			return
		}

		if in.Version == distroPHP() {
			writeError(w, http.StatusConflict, "php_default", "PHP "+in.Version+" is the distribution's default and stays installed")

			return
		}

		if sites != nil {
			all, err := sites(r.Context())
			if err != nil {
				log.ErrorContext(r.Context(), "php remove: list sites", "err", err)
				writeError(w, http.StatusInternalServerError, "internal", "internal error")

				return
			}

			var users []string

			for _, s := range all {
				if s.PHP == in.Version {
					users = append(users, s.Name)
				}
			}

			if len(users) > 0 {
				writeError(w, http.StatusConflict, "php_in_use", "sites still run on PHP "+in.Version+": "+strings.Join(users, ", "))

				return
			}
		}

		ctx, cancel := context.WithTimeout(r.Context(), agentTimeout)
		defer cancel()

		s, err := p.Remove(ctx, in.Version)
		if err != nil {
			fail(w, err, "php remove")

			return
		}

		uid := pr.User.ID
		if err := st.Audit(r.Context(), &uid, "php.remove", "", map[string]string{"version": in.Version}); err != nil {
			log.ErrorContext(r.Context(), "audit log", "action", "php.remove", "err", err)
		}

		if s.Log == nil {
			s.Log = []string{}
		}

		w.Header().Set("Cache-Control", "no-store")
		writeJSON(w, http.StatusAccepted, s)
	}))
}

// phpVersionInfo is the end-of-life data the version list carries.
// osReleasePath names the distribution; a variable for tests.
var osReleasePath = "/etc/os-release"

// distroPHP is the PHP version the server's distribution ships
// (agentapi.DistroPHP), or "" when the distribution is unknown.
func distroPHP() string {
	b, err := os.ReadFile(osReleasePath)
	if err != nil {
		return ""
	}

	vals := doctor.ParseOSRelease(b)

	return agentapi.DistroPHPVersion(vals["ID"], vals["VERSION_ID"])
}

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
