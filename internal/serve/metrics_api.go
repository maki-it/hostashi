// SPDX-License-Identifier: AGPL-3.0-or-later

package serve

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"strconv"

	"github.com/maki-it/hostashi/internal/openapi"

	"github.com/maki-it/hostashi/internal/auth"
	"github.com/maki-it/hostashi/internal/authz"
	"github.com/maki-it/hostashi/internal/metrics"
	"github.com/maki-it/hostashi/internal/store"
)

// MetricsPath is where Prometheus scrapes the panel (its default path).
const MetricsPath = "/metrics"

// metricsScope is the token scope that opens MetricsPath (ADR-0022).
const metricsScope = auth.MetricsScope

// metricsEnabledKey is the setting that switches the endpoint on. It is off
// until an admin turns it on (ADR-0022, 03-features.md).
const metricsEnabledKey = "metrics.enabled"

// Settings is the part of the store the settings routes use.
type Settings interface {
	Setting(ctx context.Context, key string) (value string, ok bool, err error)
	SetSetting(ctx context.Context, key, value string) error
	Audit(ctx context.Context, userID *int64, action, target string, detail map[string]string) error
	AuditChange(ctx context.Context, userID *int64, action, target string, detail map[string]string, diff store.Diff) error
}

func metricsEnabled(ctx context.Context, st Settings) (bool, error) {
	v, ok, err := st.Setting(ctx, metricsEnabledKey)
	if err != nil || !ok {
		return false, err
	}

	var on bool
	if err := json.Unmarshal([]byte(v), &on); err != nil {
		return false, fmt.Errorf("setting %s: %w", metricsEnabledKey, err)
	}

	return on, nil
}

// writeMetrics answers a scrape from the cached exposition.
func writeMetrics(w http.ResponseWriter, m *metrics.Metrics, log *slog.Logger) {
	body, err := m.Exposition()
	if err != nil {
		log.Error("metrics gather failed", "err", err)
		writeError(w, http.StatusInternalServerError, "internal", "internal error")

		return
	}

	w.Header().Set("Content-Type", metrics.ContentType())
	w.Header().Set("Content-Length", strconv.Itoa(len(body)))
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(body)
}

// registerMetrics adds the scrape endpoint and its switch. The endpoint
// takes only an API token with metrics:read; a browser session does not
// open it, the panel's own graphs use the ring buffer (04-architecture 4.7).
type metricsSettingRequest struct {
	Enabled *bool `json:"enabled"`
}

func registerMetrics(mux *routeMux, a authn, m *metrics.Metrics, st Settings, log *slog.Logger) {
	mux.HandleFunc("GET "+MetricsPath, func(w http.ResponseWriter, r *http.Request) {
		on, err := metricsEnabled(r.Context(), st)
		if err != nil {
			log.ErrorContext(r.Context(), "read metrics setting", "err", err)
			writeError(w, http.StatusInternalServerError, "internal", "internal error")

			return
		}

		if !on {
			writeError(w, http.StatusNotFound, "not_found", "the metrics endpoint is switched off")

			return
		}

		if _, present := bearer(r); !present {
			w.Header().Set("WWW-Authenticate", "Bearer")
			writeError(w, http.StatusUnauthorized, "unauthenticated", "the metrics endpoint needs an API token with the scope "+metricsScope)

			return
		}

		a.allow(metricsScope, func(w http.ResponseWriter, _ *http.Request, _ auth.Principal) {
			writeMetrics(w, m, log)
		})(w, r)
	})

	type metricsSettingJSON struct {
		Enabled bool `json:"enabled"`
	}

	mux.Op("GET /api/v1/settings/metrics", openapi.Operation{
		ID: "getMetricsSetting", Tag: "settings", Summary: "Whether /metrics is on", Out: metricsSettingJSON{},
	}, func(w http.ResponseWriter, r *http.Request) {
		if _, ok := a.sessionAllow(w, r, authz.SettingsRead); !ok {
			return
		}

		on, err := metricsEnabled(r.Context(), st)
		if err != nil {
			log.ErrorContext(r.Context(), "read metrics setting", "err", err)
			writeError(w, http.StatusInternalServerError, "internal", "internal error")

			return
		}

		writeJSON(w, http.StatusOK, metricsSettingJSON{Enabled: on})
	})

	mux.Op("PUT /api/v1/settings/metrics", openapi.Operation{
		ID: "setMetricsSetting", Tag: "settings", Summary: "Switch /metrics on or off", In: metricsSettingRequest{}, Out: metricsSettingJSON{},
	}, func(w http.ResponseWriter, r *http.Request) {
		u, ok := a.sessionAllow(w, r, authz.SettingsWrite)
		if !ok {
			return
		}

		uid := u.ID

		var in metricsSettingRequest
		if !decodeJSON(w, r, &in) {
			return
		}

		if in.Enabled == nil {
			writeError(w, http.StatusUnprocessableEntity, "invalid_argument", "enabled is required")

			return
		}

		on := *in.Enabled

		was, err := metricsEnabled(r.Context(), st)
		if err != nil {
			log.ErrorContext(r.Context(), "read metrics setting", "err", err)
			writeError(w, http.StatusInternalServerError, "internal", "internal error")

			return
		}

		if err := st.SetSetting(r.Context(), metricsEnabledKey, strconv.FormatBool(on)); err != nil {
			log.ErrorContext(r.Context(), "store metrics setting", "err", err)
			writeError(w, http.StatusInternalServerError, "internal", "internal error")

			return
		}

		_ = st.AuditChange(r.Context(), &uid, "settings.metrics", "", map[string]string{
			"enabled": strconv.FormatBool(on), "ip": clientIP(r),
		}, store.DiffOf(map[string]string{"enabled": strconv.FormatBool(was)}, map[string]string{"enabled": strconv.FormatBool(on)}))

		writeJSON(w, http.StatusOK, metricsSettingJSON{Enabled: on})
	})
}

// CheckMetricsListen accepts only a loopback IP literal with a port. Unlike
// CheckListen it refuses host names such as "localhost", whose address
// comes from /etc/hosts or DNS, because this listener answers without a
// token (ADR-0022).
func CheckMetricsListen(addr string) error {
	host, port, err := net.SplitHostPort(addr)
	if err != nil {
		return fmt.Errorf("metrics listen address %q: %w", addr, err)
	}

	if port == "" || port == "0" {
		return fmt.Errorf("metrics listen address %q: missing port", addr)
	}

	ip := net.ParseIP(host)
	if ip == nil || !ip.IsLoopback() {
		return fmt.Errorf("metrics listen address %q: only a loopback IP address such as 127.0.0.1 is allowed", addr)
	}

	return nil
}

// LocalMetricsHandler serves only GET /metrics without a token, for a
// scraper on the same host (ADR-0022). It must only ever run on a listener
// that CheckMetricsListen accepted. It answers 404 while the metrics
// setting is off, like the token path.
func LocalMetricsHandler(m *metrics.Metrics, st Settings, log *slog.Logger) http.Handler {
	if log == nil {
		log = slog.New(slog.DiscardHandler)
	}

	mux := http.NewServeMux()
	mux.HandleFunc("GET "+MetricsPath, func(w http.ResponseWriter, r *http.Request) {
		on, err := metricsEnabled(r.Context(), st)
		if err != nil {
			log.ErrorContext(r.Context(), "read metrics setting", "err", err)
			writeError(w, http.StatusInternalServerError, "internal", "internal error")

			return
		}

		if !on {
			writeError(w, http.StatusNotFound, "not_found", "the metrics endpoint is switched off")

			return
		}

		writeMetrics(w, m, log)
	})

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("Cache-Control", "no-store")
		mux.ServeHTTP(w, r)
	})
}
