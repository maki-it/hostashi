// SPDX-License-Identifier: AGPL-3.0-or-later

package serve

import (
	"context"
	"errors"
	"log/slog"
	"math"
	"net/http"
	"strconv"
	"time"

	"github.com/maki-it/hostashi/internal/agentapi"
	"github.com/maki-it/hostashi/internal/auth"
	"github.com/maki-it/hostashi/internal/hosting"
	"github.com/maki-it/hostashi/internal/store"
)

// pageCacheJSON is the page cache of a site (ADR-0046), with the fixed
// choices so a client need not hard-code them.
type pageCacheJSON struct {
	Enabled   bool       `json:"enabled"`
	Preset    string     `json:"preset"`
	Minutes   int        `json:"minutes"`
	SizeMB    int        `json:"sizeMB"`
	Exclude   []string   `json:"exclude"`
	UpdatedAt *time.Time `json:"updatedAt,omitempty"`
	Choices   struct {
		Presets []string `json:"presets"`
		Minutes []int    `json:"minutes"`
		SizesMB []int    `json:"sizesMB"`
		// MaxExclude is the most excluded paths a site may have.
		MaxExclude int `json:"maxExclude"`
	} `json:"choices"`
}

func toPageCacheJSON(c store.PageCache) pageCacheJSON {
	out := pageCacheJSON{Enabled: c.Enabled, Preset: c.Preset, Minutes: c.Minutes, SizeMB: c.SizeMB, Exclude: c.Exclude}
	if out.Exclude == nil {
		out.Exclude = []string{}
	}

	if !c.UpdatedAt.IsZero() {
		at := c.UpdatedAt
		out.UpdatedAt = &at
	}

	out.Choices.Presets = agentapi.PageCachePresets
	out.Choices.Minutes = agentapi.PageCacheMinutes
	out.Choices.SizesMB = agentapi.PageCacheSizesMB
	out.Choices.MaxExclude = agentapi.MaxPageCacheExcludes

	return out
}

// registerPageCache adds the page cache routes. Switch, preset, cache time
// and excluded paths belong to everyone who works on the site; the size
// counts against the disk limit and belongs to managers and admins.
// Purging has its own scope, so a CMS can hold a token that can do nothing
// else, limited to its site.
func registerPageCache(mux *routeMux, a authn, svc *hosting.Service, log *slog.Logger) {
	mux.HandleFunc("GET /api/v1/sites/{name}/page-cache", a.allow("sites:read", func(w http.ResponseWriter, r *http.Request, _ auth.Principal) {
		c, err := svc.PageCache(r.Context(), r.PathValue("name"))
		if err != nil {
			siteFail(log, w, err, "read page cache")

			return
		}

		writeJSON(w, http.StatusOK, toPageCacheJSON(c))
	}))

	mux.HandleFunc("PUT /api/v1/sites/{name}/page-cache", a.allow("sites.pagecache:write", func(w http.ResponseWriter, r *http.Request, p auth.Principal) {
		var in struct {
			Enabled *bool    `json:"enabled"`
			Preset  string   `json:"preset"`
			Minutes int      `json:"minutes"`
			Exclude []string `json:"exclude"`
		}
		if !decodeJSON(w, r, &in) {
			return
		}

		if in.Enabled == nil {
			writeError(w, http.StatusUnprocessableEntity, "invalid_argument", `"enabled", "preset" and "minutes" are required`)

			return
		}

		ctx, cancel := context.WithTimeout(r.Context(), agentTimeout)
		defer cancel()

		c, err := svc.SetPageCache(ctx, p.User.ID, r.PathValue("name"), hosting.PageCacheSettings{
			Enabled: *in.Enabled, Preset: in.Preset, Minutes: in.Minutes, Exclude: in.Exclude,
		})
		if err != nil {
			siteFail(log, w, err, "set page cache")

			return
		}

		writeJSON(w, http.StatusOK, toPageCacheJSON(c))
	}))

	mux.HandleFunc("PUT /api/v1/sites/{name}/page-cache/size", a.allow("sites.pagecache.size:write", func(w http.ResponseWriter, r *http.Request, p auth.Principal) {
		var in struct {
			SizeMB int `json:"sizeMB"`
		}
		if !decodeJSON(w, r, &in) {
			return
		}

		ctx, cancel := context.WithTimeout(r.Context(), agentTimeout)
		defer cancel()

		c, err := svc.SetPageCacheSize(ctx, p.User.ID, r.PathValue("name"), in.SizeMB)
		if err != nil {
			siteFail(log, w, err, "set page cache size")

			return
		}

		writeJSON(w, http.StatusOK, toPageCacheJSON(c))
	}))

	mux.HandleFunc("POST /api/v1/sites/{name}/page-cache/purge", a.allow("sites.pagecache:purge", func(w http.ResponseWriter, r *http.Request, p auth.Principal) {
		ctx, cancel := context.WithTimeout(r.Context(), agentTimeout)
		defer cancel()

		err := svc.PurgePageCache(ctx, p.User.ID, r.PathValue("name"))

		var re *hosting.RetryError
		if errors.As(err, &re) {
			w.Header().Set("Retry-After", strconv.Itoa(int(math.Ceil(re.After.Seconds()))))
			writeError(w, http.StatusTooManyRequests, "rate_limited", "the cache of this site was purged a moment ago, try again shortly")

			return
		}

		if err != nil {
			siteFail(log, w, err, "purge page cache")

			return
		}

		w.WriteHeader(http.StatusNoContent)
	}))
}
