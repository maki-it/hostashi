// SPDX-License-Identifier: AGPL-3.0-or-later

package serve

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"runtime"

	"github.com/maki-it/hostashi/internal/agentapi"
	"github.com/maki-it/hostashi/internal/auth"
	"github.com/maki-it/hostashi/internal/hosting"
	"github.com/maki-it/hostashi/internal/openapi"
	"github.com/maki-it/hostashi/internal/store"
)

// limitsJSON are resource limits (ADR-0015). 0 means unlimited, except for
// phpWorkers; "unlimited" names every limit that is 0, so a client can mark
// it visibly.
type limitsJSON struct {
	DiskBytes       int64  `json:"diskBytes"`
	DiskInodes      int64  `json:"diskInodes"`
	MemoryBytes     int64  `json:"memoryBytes"`
	PHPWorkers      int64  `json:"phpWorkers"`
	Tasks           int64  `json:"tasks"`
	CPUPriority     string `json:"cpuPriority"`     // low, normal, high
	CPUQuotaPercent int64  `json:"cpuQuotaPercent"` // 0: no hard CPU limit
	// DatabaseBytes warns about the size of the site's local databases,
	// it is not enforced (ADR-0057).
	DatabaseBytes int64 `json:"databaseBytes"`
	// DatabaseConnections is MAX_USER_CONNECTIONS of each database user.
	DatabaseConnections int64    `json:"databaseConnections"`
	Unlimited           []string `json:"unlimited,omitempty"`
}

func toLimitsJSON(l store.Limits) limitsJSON {
	out := limitsJSON{
		DiskBytes: l.DiskBytes, DiskInodes: l.DiskInodes, MemoryBytes: l.MemoryBytes, PHPWorkers: l.PHPWorkers,
		Tasks: l.Tasks, CPUPriority: l.CPUPriority, CPUQuotaPercent: l.CPUQuotaPercent,
		DatabaseBytes: l.DatabaseBytes, DatabaseConnections: l.DatabaseConnections, Unlimited: []string{},
	}

	for _, u := range []struct {
		name string
		v    int64
	}{
		{"diskBytes", l.DiskBytes},
		{"diskInodes", l.DiskInodes},
		{"memoryBytes", l.MemoryBytes},
		{"tasks", l.Tasks},
		{"databaseBytes", l.DatabaseBytes},
		{"databaseConnections", l.DatabaseConnections},
	} {
		if u.v == 0 {
			out.Unlimited = append(out.Unlimited, u.name)
		}
	}

	return out
}

func (l limitsJSON) limits() store.Limits {
	return store.Limits{
		DiskBytes: l.DiskBytes, DiskInodes: l.DiskInodes, MemoryBytes: l.MemoryBytes, PHPWorkers: l.PHPWorkers,
		Tasks: l.Tasks, CPUPriority: l.CPUPriority, CPUQuotaPercent: l.CPUQuotaPercent,
		DatabaseBytes: l.DatabaseBytes, DatabaseConnections: l.DatabaseConnections,
	}
}

// presetCachesJSON are the starting values a site gets with the preset
// (ADR-0045, ADR-0046). They are not limits: a site can change them
// afterwards.
type presetCachesJSON struct {
	ObjectCacheMB int64 `json:"objectCacheMb"` // 0: off
	PageCacheMB   int64 `json:"pageCacheMb"`   // 0: off
}

type presetJSON struct {
	Name      string           `json:"name"`
	Default   bool             `json:"default"`
	Limits    limitsJSON       `json:"limits"`
	Caches    presetCachesJSON `json:"caches"`
	CreatedAt int64            `json:"createdAt"`
	UpdatedAt int64            `json:"updatedAt"`
}

// presetListJSON names the server's CPUs too: the panel shows CPU
// limits in cores (ADR-0037).
type presetListJSON struct {
	Presets []presetJSON `json:"presets"`
	CPUs    int          `json:"cpus"`
}

func toPresetJSON(p store.LimitPreset) presetJSON {
	return presetJSON{
		Name: p.Name, Default: p.Default, Limits: toLimitsJSON(p.Limits), Caches: presetCachesJSON{ObjectCacheMB: p.Caches.ObjectCacheMB, PageCacheMB: p.Caches.PageCacheMB},
		CreatedAt: p.CreatedAt.Unix(), UpdatedAt: p.UpdatedAt.Unix(),
	}
}

// presetInputJSON is the body of a create or a full replace: missing caches
// mean off.
type presetInputJSON struct {
	Name    string           `json:"name"`
	Default bool             `json:"default"`
	Limits  limitsJSON       `json:"limits"`
	Caches  presetCachesJSON `json:"caches"`
}

func (c presetCachesJSON) caches() store.PresetCaches {
	return store.PresetCaches{ObjectCacheMB: c.ObjectCacheMB, PageCacheMB: c.PageCacheMB}
}

// Warning levels of the disk use (ADR-0015): from 80 and 95 percent of a
// limit.
const (
	usageWarnPercent     = 80
	usageCriticalPercent = 95
)

type usageJSON struct {
	// Enforced is false when the file system has no project quota: the
	// disk limits are then not enforced and the numbers are zero.
	Enforced      bool   `json:"enforced"`
	Bytes         uint64 `json:"bytes"`
	Inodes        uint64 `json:"inodes"`
	BytesPercent  int    `json:"bytesPercent"`  // of the limit; 0 without one
	InodesPercent int    `json:"inodesPercent"` // of the limit; 0 without one
	Level         string `json:"level"`         // ok, warning, critical
}

func percent(used, limit uint64) int {
	if limit == 0 {
		return 0
	}

	return int(min(used*100/limit, 999))
}

func toUsageJSON(q agentapi.SiteQuota) usageJSON {
	u := usageJSON{
		Enforced: q.Available, Bytes: q.Bytes, Inodes: q.Inodes,
		BytesPercent: percent(q.Bytes, q.BytesLimit), InodesPercent: percent(q.Inodes, q.InodesLimit), Level: "ok",
	}

	switch top := max(u.BytesPercent, u.InodesPercent); {
	case top >= usageCriticalPercent:
		u.Level = "critical"
	case top >= usageWarnPercent:
		u.Level = "warning"
	}

	return u
}

type siteLimitsJSON struct {
	// Set is false for a site created before limits existed: it has none.
	Set        bool       `json:"set"`
	Preset     string     `json:"preset"` // "": custom values
	Limits     limitsJSON `json:"limits"`
	AppliedAt  int64      `json:"appliedAt"`  // 0: not applied
	ApplyError string     `json:"applyError"` // why the last apply failed
	UpdatedAt  int64      `json:"updatedAt"`
	Usage      *usageJSON `json:"usage,omitempty"`
	// DatabaseUsage is the size of the site's local databases (ADR-0057).
	DatabaseUsage *dbUsageJSON `json:"databaseUsage,omitempty"`
}

type dbUsageJSON struct {
	Bytes      int64  `json:"bytes"`
	Databases  int    `json:"databases"`  // local databases counted
	Percent    int    `json:"percent"`    // of databaseBytes; 0 without a threshold
	Level      string `json:"level"`      // ok, warning, critical
	MeasuredAt int64  `json:"measuredAt"` // Unix seconds
}

func toDBUsageJSON(u hosting.DatabaseUsage, threshold int64) dbUsageJSON {
	out := dbUsageJSON{Bytes: u.Bytes, Databases: u.Databases, Level: "ok", MeasuredAt: u.MeasuredAt.Unix()}
	if threshold > 0 && u.Bytes >= 0 {
		out.Percent = percent(uint64(u.Bytes), uint64(threshold))
	}

	switch {
	case out.Percent >= usageCriticalPercent:
		out.Level = "critical"
	case out.Percent >= usageWarnPercent:
		out.Level = "warning"
	}

	return out
}

func toSiteLimitsJSON(v hosting.SiteLimitsView) siteLimitsJSON {
	out := siteLimitsJSON{Set: v.Set, Preset: v.Preset, ApplyError: v.ApplyError}
	if !v.Set {
		return out
	}

	out.Limits = toLimitsJSON(v.Limits)
	out.UpdatedAt = v.UpdatedAt.Unix()

	if !v.AppliedAt.IsZero() {
		out.AppliedAt = v.AppliedAt.Unix()
	}

	return out
}

// siteLimitsInputJSON names a preset or gives custom values, not both.
type siteLimitsInputJSON struct {
	Preset string      `json:"preset"`
	Custom *limitsJSON `json:"custom"`
}

func limitsFail(log *slog.Logger, w http.ResponseWriter, err error, what string) {
	switch {
	case errors.Is(err, hosting.ErrNoLimits):
		writeError(w, http.StatusServiceUnavailable, "limits_unavailable", "resource limits are not available on this panel")
	case errors.Is(err, hosting.ErrPresetNotFound):
		writeError(w, http.StatusNotFound, "not_found", "no such limit preset")
	case errors.Is(err, hosting.ErrPresetExists):
		writeError(w, http.StatusConflict, "preset_exists", "a limit preset with this name exists")
	case errors.Is(err, hosting.ErrPresetDefault):
		writeError(w, http.StatusConflict, "preset_is_default", "make another preset the default first")
	default:
		siteFail(log, w, err, what)
	}
}

// registerLimits adds the limit presets and the limits of a site
// (ADR-0015). Only administrators manage them.
func registerLimits(mux *routeMux, a authn, svc *hosting.Service, log *slog.Logger) {
	mux.Op("GET /api/v1/limit-presets", openapi.Operation{
		ID: "listLimitPresets", Tag: "limits", Summary: "List the limit presets", Out: presetListJSON{},
	}, a.allow("limits.presets:read", func(w http.ResponseWriter, r *http.Request, _ auth.Principal) {
		ps, err := svc.LimitPresets(r.Context())
		if err != nil {
			limitsFail(log, w, err, "list limit presets")

			return
		}

		out := make([]presetJSON, 0, len(ps))
		for _, p := range ps {
			out = append(out, toPresetJSON(p))
		}

		// The panel shows CPU limits in cores and names how many the
		// server has (ADR-0037).
		writeJSON(w, http.StatusOK, presetListJSON{Presets: out, CPUs: runtime.NumCPU()})
	}))

	mux.Op("GET /api/v1/limit-presets/{name}", openapi.Operation{
		ID: "getLimitPreset", Tag: "limits", Summary: "Get a limit preset", Out: presetJSON{},
	}, a.allow("limits.presets:read", func(w http.ResponseWriter, r *http.Request, _ auth.Principal) {
		p, err := svc.LimitPreset(r.Context(), r.PathValue("name"))
		if err != nil {
			limitsFail(log, w, err, "get limit preset")

			return
		}

		writeJSON(w, http.StatusOK, toPresetJSON(p))
	}))

	mux.Op("POST /api/v1/limit-presets", openapi.Operation{
		ID: "createLimitPreset", Tag: "limits", Summary: "Create a limit preset", In: presetInputJSON{}, Out: presetJSON{}, Status: http.StatusCreated,
	}, a.allow("limits.presets:write", func(w http.ResponseWriter, r *http.Request, p auth.Principal) {
		var in presetInputJSON
		if !decodeJSON(w, r, &in) {
			return
		}

		out, err := svc.CreateLimitPreset(r.Context(), p.User.ID, in.Name, in.Limits.limits(), in.Caches.caches(), in.Default)
		if err != nil {
			limitsFail(log, w, err, "create limit preset")

			return
		}

		writeJSON(w, http.StatusCreated, toPresetJSON(out))
	}))

	// PUT replaces name, values and default flag; the sites that follow the
	// preset get the new values. "default": false keeps a default preset
	// the default: another preset takes the flag over.
	mux.Op("PUT /api/v1/limit-presets/{name}", openapi.Operation{
		ID: "updateLimitPreset", Tag: "limits", Summary: "Change a limit preset", In: presetInputJSON{}, Out: presetJSON{},
	}, a.allow("limits.presets:write", func(w http.ResponseWriter, r *http.Request, p auth.Principal) {
		var in presetInputJSON
		if !decodeJSON(w, r, &in) {
			return
		}

		ctx, cancel := context.WithTimeout(r.Context(), agentTimeout)
		defer cancel()

		out, err := svc.UpdateLimitPreset(ctx, p.User.ID, r.PathValue("name"), in.Name, in.Limits.limits(), in.Caches.caches(), in.Default)
		if err != nil {
			limitsFail(log, w, err, "change limit preset")

			return
		}

		writeJSON(w, http.StatusOK, toPresetJSON(out))
	}))

	mux.Op("DELETE /api/v1/limit-presets/{name}", openapi.Operation{
		ID: "deleteLimitPreset", Tag: "limits", Summary: "Delete an unused limit preset",
	}, a.allow("limits.presets:write", func(w http.ResponseWriter, r *http.Request, p auth.Principal) {
		if err := svc.DeleteLimitPreset(r.Context(), p.User.ID, r.PathValue("name")); err != nil {
			limitsFail(log, w, err, "delete limit preset")

			return
		}

		w.WriteHeader(http.StatusNoContent)
	}))

	// The limits with the disk use as the server reports it. Without an
	// answer of the agent the limits come without usage.
	mux.Op("GET /api/v1/sites/{name}/limits", openapi.Operation{
		ID: "getSiteLimits", Tag: "limits", Summary: "Get the site's limits and usage", Out: siteLimitsJSON{},
	}, a.allow("limits:read", func(w http.ResponseWriter, r *http.Request, _ auth.Principal) {
		site := r.PathValue("name")

		v, err := svc.SiteLimits(r.Context(), site)
		if err != nil {
			limitsFail(log, w, err, "get site limits")

			return
		}

		out := toSiteLimitsJSON(v)

		ctx, cancel := context.WithTimeout(r.Context(), agentTimeout)
		defer cancel()

		if q, err := svc.SiteUsage(ctx, site); err == nil {
			u := toUsageJSON(q)
			out.Usage = &u
		} else if !errors.Is(err, hosting.ErrNoLimits) {
			log.WarnContext(r.Context(), "site usage", "site", site, "err", err)
		}

		if du, err := svc.SiteDatabaseUsage(ctx, site); err == nil {
			j := toDBUsageJSON(du, v.Limits.DatabaseBytes)
			out.DatabaseUsage = &j
		} else if !errors.Is(err, hosting.ErrNoDBSizes) {
			log.WarnContext(r.Context(), "site database usage", "site", site, "err", err)
		}

		writeJSON(w, http.StatusOK, out)
	}))

	// PUT gives the site a preset or custom values. When the agent fails,
	// the values stay as the site's desired state with the error recorded;
	// the answer is the error and the same PUT retries.
	mux.Op("PUT /api/v1/sites/{name}/limits", openapi.Operation{
		ID: "setSiteLimits", Tag: "limits", Summary: "Set the site's limits or preset", In: siteLimitsInputJSON{}, Out: siteLimitsJSON{},
	}, a.allow("limits:write", func(w http.ResponseWriter, r *http.Request, p auth.Principal) {
		var in siteLimitsInputJSON
		if !decodeJSON(w, r, &in) {
			return
		}

		var custom *store.Limits

		if in.Custom != nil {
			l := in.Custom.limits()
			custom = &l
		}

		ctx, cancel := context.WithTimeout(r.Context(), agentTimeout)
		defer cancel()

		v, err := svc.SetSiteLimits(ctx, p.User.ID, r.PathValue("name"), in.Preset, custom)
		if err != nil {
			limitsFail(log, w, err, "set site limits")

			return
		}

		writeJSON(w, http.StatusOK, toSiteLimitsJSON(v))
	}))
}
