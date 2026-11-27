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
	"github.com/maki-it/hostashi/internal/servermon"
	"github.com/maki-it/hostashi/internal/store"
)

// SiteUsage is the panel's view of resource usage per site (ADR-0063,
// internal/servermon).
type SiteUsage interface {
	SiteSeries(ctx context.Context, site, rng string) ([]store.SiteSample, int, error)
	SiteRanking(ctx context.Context, rng, by string) ([]store.SiteTotal, error)
}

// SitesStats implements servermon.SiteAgent.
func (a RPCServerAgent) SitesStats(ctx context.Context) (agentapi.SitesStatsResponse, error) {
	return agentapi.SitesStats.Call(ctx, a.C, agentapi.SitesStatsRequest{})
}

type siteUsageJSON struct {
	Site   string             `json:"site"`
	Range  string             `json:"range"`
	Step   int                `json:"step"`
	CPUs   int                `json:"cpus"`  // cpu is percent of one core; this many make the server
	Cores  float64            `json:"cores"` // cores the site may use: its CPU limit, else cpus
	Points []store.SiteSample `json:"points"`
}

type usageRankingJSON struct {
	Range string         `json:"range"`
	Sort  string         `json:"sort"`
	CPUs  int            `json:"cpus"` // as in siteUsageJSON
	Sites []usageRowJSON `json:"sites"`
}

// usageRowJSON is one site of the ranking with the cores it may use.
type usageRowJSON struct {
	store.SiteTotal

	Cores float64 `json:"cores"` // as in siteUsageJSON
}

// siteCPU returns a site's hard CPU limit in percent of one core (0: none),
// and false for a site that is not in the store.
type siteCPU func(ctx context.Context, name string) (quota int64, ok bool, err error)

// hostingSiteCPU is siteCPU over the hosting service.
func hostingSiteCPU(svc *hosting.Service) siteCPU {
	return func(ctx context.Context, name string) (int64, bool, error) {
		l, err := svc.SiteLimits(ctx, name)
		if errors.Is(err, hosting.ErrNotFound) {
			return 0, false, nil
		}

		if err != nil {
			return 0, false, err
		}

		return l.Limits.CPUQuotaPercent, true, nil
	}
}

// coresFor is how many cores a site may use: its CPU limit when that is
// below the server's cores (ADR-0015), else all of them.
func coresFor(quota int64, cpus int) float64 {
	if quota > 0 && float64(quota)/100 < float64(cpus) {
		return float64(quota) / 100
	}

	return float64(cpus)
}

// registerUsage adds the usage routes (ADR-0063): one site's points for
// everyone who sees the site, the ranking of all sites for admins.
func registerUsage(mux *routeMux, a authn, u SiteUsage, cpuOf siteCPU, log *slog.Logger) {
	rangeOf := func(r *http.Request) string {
		if rng := r.URL.Query().Get("range"); rng != "" {
			return rng
		}

		return servermon.Range1h
	}

	mux.Op("GET /api/v1/sites/{name}/usage", openapi.Operation{
		ID: "getSiteUsage", Tag: "sites", Summary: "CPU, memory, disk I/O and web traffic of a site over a range", Out: siteUsageJSON{},
		Query: []openapi.Param{{Name: "range", Description: "1h, 24h, 7d or 30d; default 1h"}},
	}, a.allow("sites.usage:read", func(w http.ResponseWriter, r *http.Request, _ auth.Principal) {
		site := r.PathValue("name")

		quota, ok, err := cpuOf(r.Context(), site)
		if err != nil {
			log.ErrorContext(r.Context(), "site usage: look up site", "err", err)
			writeError(w, http.StatusInternalServerError, "internal", "internal error")

			return
		}

		if !ok {
			writeError(w, http.StatusNotFound, "not_found", "no such site")

			return
		}

		rng := rangeOf(r)

		pts, step, err := u.SiteSeries(r.Context(), site, rng)
		if errors.Is(err, servermon.ErrRange) {
			writeError(w, http.StatusBadRequest, "invalid_argument", "range must be 1h, 24h, 7d or 30d")

			return
		}

		if err != nil {
			log.ErrorContext(r.Context(), "site usage", "err", err)
			writeError(w, http.StatusInternalServerError, "internal", "the samples could not be read")

			return
		}

		cpus := runtime.NumCPU()

		w.Header().Set("Cache-Control", "no-store")
		writeJSON(w, http.StatusOK, siteUsageJSON{Site: site, Range: rng, Step: step, CPUs: cpus, Cores: coresFor(quota, cpus), Points: pts})
	}))

	mux.Op("GET /api/v1/usage/sites", openapi.Operation{
		ID: "rankSiteUsage", Tag: "services", Summary: "All sites by their usage over a range, the biggest first", Out: usageRankingJSON{},
		Query: []openapi.Param{{Name: "range", Description: "1h, 24h, 7d or 30d; default 1h"}, {Name: "sort", Description: "cpu, memory, traffic or io; default cpu"}},
	}, a.allow("usage:read", func(w http.ResponseWriter, r *http.Request, _ auth.Principal) {
		rng := rangeOf(r)

		by := r.URL.Query().Get("sort")
		if by == "" {
			by = servermon.SortCPU
		}

		list, err := u.SiteRanking(r.Context(), rng, by)

		switch {
		case errors.Is(err, servermon.ErrRange):
			writeError(w, http.StatusBadRequest, "invalid_argument", "range must be 1h, 24h, 7d or 30d")

			return
		case errors.Is(err, servermon.ErrSort):
			writeError(w, http.StatusBadRequest, "invalid_argument", "sort must be cpu, memory, traffic or io")

			return
		case err != nil:
			log.ErrorContext(r.Context(), "usage ranking", "err", err)
			writeError(w, http.StatusInternalServerError, "internal", "the samples could not be read")

			return
		}

		cpus := runtime.NumCPU()
		rows := make([]usageRowJSON, 0, len(list))

		for _, t := range list {
			// A site gone since the sample counts with no limit.
			quota, _, err := cpuOf(r.Context(), t.Site)
			if err != nil {
				log.ErrorContext(r.Context(), "usage ranking: look up limits", "site", t.Site, "err", err)
				writeError(w, http.StatusInternalServerError, "internal", "internal error")

				return
			}

			rows = append(rows, usageRowJSON{SiteTotal: t, Cores: coresFor(quota, cpus)})
		}

		w.Header().Set("Cache-Control", "no-store")
		writeJSON(w, http.StatusOK, usageRankingJSON{Range: rng, Sort: by, CPUs: cpus, Sites: rows})
	}))
}
