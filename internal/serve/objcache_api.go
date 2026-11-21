// SPDX-License-Identifier: AGPL-3.0-or-later

package serve

import (
	"context"
	"errors"
	"log/slog"
	"net/http"

	"github.com/maki-it/hostashi/internal/agentapi"
	"github.com/maki-it/hostashi/internal/auth"
	"github.com/maki-it/hostashi/internal/hosting"
)

// objectCacheJSON is a site's object cache (ADR-0045). Status is null when
// the agent did not answer.
type objectCacheJSON struct {
	Enabled          bool   `json:"enabled"`
	MemoryMB         int    `json:"memoryMb"`
	MinMB            int    `json:"minMb"`
	MaxMB            int    `json:"maxMb"`
	MemoryLimitBytes int64  `json:"memoryLimitBytes"` // the site's limit, 0 for none
	Socket           string `json:"socket"`           // where the site's apps connect

	Status *objectCacheStatusJSON `json:"status"`
}

type objectCacheStatusJSON struct {
	// State is systemd's ActiveState, empty when the cache does not run.
	State     string `json:"state"`
	Paused    bool   `json:"paused"`
	UsedBytes uint64 `json:"usedBytes"`
}

func toObjectCacheJSON(site string, v hosting.ObjectCacheView) objectCacheJSON {
	out := objectCacheJSON{
		Enabled: v.Enabled, MemoryMB: v.MemoryMB, MinMB: agentapi.MinCacheMB, MaxMB: agentapi.MaxCacheMB,
		MemoryLimitBytes: v.MemoryLimitBytes, Socket: hosting.CacheSocket(site),
	}

	if v.Status != nil {
		out.Status = toObjectCacheStatusJSON(*v.Status)
	}

	return out
}

func toObjectCacheStatusJSON(s agentapi.CacheStatus) *objectCacheStatusJSON {
	return &objectCacheStatusJSON{State: s.State, Paused: s.Paused, UsedBytes: s.UsedBytes}
}

func cacheFail(log *slog.Logger, w http.ResponseWriter, err error, what string) {
	switch {
	case errors.Is(err, hosting.ErrNoObjectCache):
		writeError(w, http.StatusServiceUnavailable, "object_cache_unavailable", "the object cache is not available on this panel")
	case errors.Is(err, hosting.ErrObjectCacheOff):
		writeError(w, http.StatusConflict, "object_cache_off", "the object cache of the site is off")
	default:
		siteFail(log, w, err, what)
	}
}

// registerObjectCache adds the object cache routes of a site (ADR-0045).
// Everyone with a grant turns it on or off and flushes it; the size is a
// route of its own for admins and managers, because the cache's memory
// counts against the site's limit.
func registerObjectCache(mux *routeMux, a authn, svc *hosting.Service, log *slog.Logger) {
	mux.HandleFunc("GET /api/v1/sites/{name}/object-cache", a.allow("cache:read", func(w http.ResponseWriter, r *http.Request, _ auth.Principal) {
		ctx, cancel := context.WithTimeout(r.Context(), agentTimeout)
		defer cancel()

		site := r.PathValue("name")

		v, err := svc.ObjectCache(ctx, site)
		if err != nil {
			cacheFail(log, w, err, "read object cache")

			return
		}

		writeJSON(w, http.StatusOK, toObjectCacheJSON(site, v))
	}))

	mux.HandleFunc("PUT /api/v1/sites/{name}/object-cache", a.allow("cache:write", func(w http.ResponseWriter, r *http.Request, p auth.Principal) {
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

		site := r.PathValue("name")

		v, err := svc.SetObjectCache(ctx, p.User.ID, site, *in.Enabled, 0)
		if err != nil {
			cacheFail(log, w, err, "set object cache")

			return
		}

		writeJSON(w, http.StatusOK, toObjectCacheJSON(site, v))
	}))

	mux.HandleFunc("PUT /api/v1/sites/{name}/object-cache/size", a.allow("cache.size:write", func(w http.ResponseWriter, r *http.Request, p auth.Principal) {
		var in struct {
			MemoryMB *int `json:"memoryMb"`
		}
		if !decodeJSON(w, r, &in) {
			return
		}

		if in.MemoryMB == nil || *in.MemoryMB == 0 {
			writeError(w, http.StatusUnprocessableEntity, "invalid_argument", `"memoryMb" is required`)

			return
		}

		ctx, cancel := context.WithTimeout(r.Context(), agentTimeout)
		defer cancel()

		site := r.PathValue("name")

		cur, err := svc.ObjectCache(ctx, site)
		if err != nil {
			cacheFail(log, w, err, "read object cache")

			return
		}

		v, err := svc.SetObjectCache(ctx, p.User.ID, site, cur.Enabled, *in.MemoryMB)
		if err != nil {
			cacheFail(log, w, err, "set object cache size")

			return
		}

		writeJSON(w, http.StatusOK, toObjectCacheJSON(site, v))
	}))

	// A flush restarts the cache: it is empty afterwards.
	mux.HandleFunc("POST /api/v1/sites/{name}/object-cache/flush", a.allow("cache:write", func(w http.ResponseWriter, r *http.Request, p auth.Principal) {
		ctx, cancel := context.WithTimeout(r.Context(), agentTimeout)
		defer cancel()

		st, err := svc.FlushObjectCache(ctx, p.User.ID, r.PathValue("name"))
		if err != nil {
			cacheFail(log, w, err, "flush object cache")

			return
		}

		writeJSON(w, http.StatusOK, map[string]any{"status": toObjectCacheStatusJSON(st)})
	}))
}
