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

// phpBound is the range of a numeric PHP setting; sizes are bytes.
type phpBound struct {
	Min uint64 `json:"min"`
	Max uint64 `json:"max"`
}

// phpSettingsJSON is a site's PHP settings (ADR-0049): what the site set,
// what applies after defaults, the defaults and the bounds for the form.
type phpSettingsJSON struct {
	Settings  agentapi.PHPSettings `json:"settings"`
	Effective agentapi.PHPSettings `json:"effective"`
	Defaults  agentapi.PHPSettings `json:"defaults"`
	Bounds    map[string]phpBound  `json:"bounds"`
	// ErrorReporting lists the presets of error_reporting.
	ErrorReporting []string   `json:"errorReportingPresets"`
	UpdatedAt      *time.Time `json:"updatedAt,omitempty"`
	// MemoryLimitBytes and PHPWorkers come from the site's limits
	// (ADR-0015), so the form can warn when memory_limit times the workers
	// exceeds the memory limit. 0 bytes: no memory limit.
	MemoryLimitBytes int64 `json:"memoryLimitBytes"`
	PHPWorkers       int64 `json:"phpWorkers"`
}

// withLimits adds the site's memory limit and PHP workers.
func withLimits(ctx context.Context, svc *hosting.Service, name string, out phpSettingsJSON) (phpSettingsJSON, error) {
	l, err := svc.SiteLimits(ctx, name)
	if err != nil {
		return out, err
	}

	out.PHPWorkers = agentapi.DefaultPHPWorkers
	if l.Set {
		out.MemoryLimitBytes = l.Limits.MemoryBytes
		if l.Limits.PHPWorkers > 0 {
			out.PHPWorkers = l.Limits.PHPWorkers
		}
	}

	return out, nil
}

var phpSettingBounds = map[string]phpBound{
	"memoryLimit":          {agentapi.MinPHPMemoryLimit, agentapi.MaxPHPMemoryLimit},
	"uploadMaxFilesize":    {agentapi.MinPHPUploadSize, agentapi.MaxPHPUploadSize},
	"postMaxSize":          {agentapi.MinPHPUploadSize, agentapi.MaxPHPUploadSize},
	"maxExecutionTime":     {agentapi.MinPHPTime, agentapi.MaxPHPTime},
	"maxInputTime":         {agentapi.MinPHPTime, agentapi.MaxPHPTime},
	"maxInputVars":         {agentapi.MinPHPInputVars, agentapi.MaxPHPInputVars},
	"maxFileUploads":       {agentapi.MinPHPFileUploads, agentapi.MaxPHPFileUploads},
	"sessionGcMaxlifetime": {agentapi.MinPHPSessionLifetime, agentapi.MaxPHPSessionLifetime},
}

func toPHPSettingsJSON(ps store.SitePHPSettings) phpSettingsJSON {
	out := phpSettingsJSON{
		Settings: ps.Settings, Effective: ps.Settings.Effective(), Defaults: agentapi.DefaultPHPSettings(), Bounds: phpSettingBounds,
		ErrorReporting: []string{agentapi.ErrorReportingProduction, agentapi.ErrorReportingDevelopment, agentapi.ErrorReportingErrors},
	}

	if !ps.UpdatedAt.IsZero() {
		t := ps.UpdatedAt
		out.UpdatedAt = &t
	}

	return out
}

// phpSettingErrorJSON names the rejected setting by its ini key.
type phpSettingErrorJSON struct {
	Error apiErrorBody `json:"error"`
	Key   string       `json:"key"`
}

func registerPHPSettings(mux *routeMux, a authn, svc *hosting.Service, log *slog.Logger) {
	mux.HandleFunc("GET /api/v1/sites/{name}/php/settings", a.allow("sites:read", func(w http.ResponseWriter, r *http.Request, _ auth.Principal) {
		ps, err := svc.PHPSettings(r.Context(), r.PathValue("name"))
		if err != nil {
			siteFail(log, w, err, "read php settings")

			return
		}

		out, err := withLimits(r.Context(), svc, r.PathValue("name"), toPHPSettingsJSON(ps))
		if err != nil {
			siteFail(log, w, err, "read site limits")

			return
		}

		writeJSON(w, http.StatusOK, out)
	}))

	// The body replaces all settings; a field left out is the default.
	// Unknown fields are refused, so only the allowlist gets through.
	mux.HandleFunc("PUT /api/v1/sites/{name}/php/settings", a.allow("sites.php:write", func(w http.ResponseWriter, r *http.Request, p auth.Principal) {
		var in agentapi.PHPSettings
		if !decodeJSON(w, r, &in) {
			return
		}

		ctx, cancel := context.WithTimeout(r.Context(), agentTimeout)
		defer cancel()

		ps, err := svc.SetPHPSettings(ctx, p.User.ID, r.PathValue("name"), in)

		var se *agentapi.PHPSettingError
		if errors.As(err, &se) {
			writeJSON(w, http.StatusUnprocessableEntity, phpSettingErrorJSON{
				Error: apiErrorBody{Code: "php_setting_rejected", Message: se.Error(), RequestID: w.Header().Get(RequestIDHeader)},
				Key:   se.Key,
			})

			return
		}

		if err != nil {
			siteFail(log, w, err, "set php settings")

			return
		}

		out, err := withLimits(r.Context(), svc, r.PathValue("name"), toPHPSettingsJSON(ps))
		if err != nil {
			siteFail(log, w, err, "read site limits")

			return
		}

		writeJSON(w, http.StatusOK, out)
	}))
}
