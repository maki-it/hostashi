// SPDX-License-Identifier: AGPL-3.0-or-later

package agent

import (
	"context"
	"log/slog"
	"time"

	"github.com/maki-it/hostashi/internal/agentapi"
	"github.com/maki-it/hostashi/internal/agentrpc"
	"github.com/maki-it/hostashi/internal/php"
	"github.com/maki-it/hostashi/internal/sites"
	"github.com/maki-it/hostashi/internal/siteslice"
	"github.com/maki-it/hostashi/internal/sitestats"
)

// statsOps serves metrics.snapshot (ADR-0022) and sites.stats (ADR-0063).
type statsOps struct {
	h          *siteOps
	disk       *sitestats.DiskCache
	cgroupRoot string
}

func (o *statsOps) register(s *agentrpc.Server) {
	agentrpc.Handle(s, agentapi.MetricsSnapshot, func(ctx context.Context, _ agentrpc.Caller, _ agentapi.MetricsSnapshotRequest) (agentapi.MetricsSnapshotResponse, error) {
		return o.snapshot(ctx), nil
	})
	agentrpc.Handle(s, agentapi.SitesStats, func(ctx context.Context, _ agentrpc.Caller, _ agentapi.SitesStatsRequest) (agentapi.SitesStatsResponse, error) {
		return o.usage(ctx), nil
	})
}

// usage reads the counters of every site's slice and its user slice
// (ADR-0063). A cgroup that cannot be read is logged and counts as empty,
// so one odd site never hides the others.
func (o *statsOps) usage(ctx context.Context) agentapi.SitesStatsResponse {
	all := o.h.m.List()
	if len(all) > agentapi.MaxSites {
		all = all[:agentapi.MaxSites]
	}

	out := agentapi.SitesStatsResponse{At: time.Now().UTC(), Sites: make([]agentapi.SiteUsage, 0, len(all))}

	for _, site := range all {
		u, err := sitestats.ReadUsage(sitestats.SliceCgroup(o.cgroupRoot, site.User))
		if err != nil {
			o.h.log.WarnContext(ctx, "usage: read site slice", "site", site.User, "err", err)
		}

		if site.UID >= siteslice.MinUserUID {
			s, err := sitestats.ReadUsage(sitestats.UserSliceCgroup(o.cgroupRoot, site.UID))
			if err != nil {
				o.h.log.WarnContext(ctx, "usage: read user slice", "site", site.User, "err", err)
			}

			u = u.Add(s)
		}

		out.Sites = append(out.Sites, agentapi.SiteUsage{
			Name: site.User, CPUUsec: u.CPUUsec, MemoryBytes: u.MemoryBytes, CacheBytes: u.CacheBytes, MemoryKnown: u.MemoryKnown,
			Tasks: u.Tasks, IOReadBytes: u.IOReadBytes, IOWriteBytes: u.IOWriteBytes, IOKnown: u.IOKnown,
		})
	}

	return out
}

func (o *statsOps) snapshot(ctx context.Context) agentapi.MetricsSnapshotResponse {
	var pools map[string]php.Pool

	if o.h.php != nil {
		var err error
		if pools, err = o.h.php.Current(); err != nil {
			o.h.log.WarnContext(ctx, "metrics: read php state", "err", err)
		}
	}

	var caches map[string]agentapi.CacheStatus

	if o.h.objc != nil {
		var err error
		if caches, err = o.h.objc.Sites(); err != nil {
			o.h.log.WarnContext(ctx, "metrics: read object cache state", "err", err)
		}
	}

	all := o.h.m.List()
	if len(all) > agentapi.MaxSites {
		all = all[:agentapi.MaxSites]
	}

	out := agentapi.MetricsSnapshotResponse{Sites: make([]agentapi.SiteStats, 0, len(all))}

	for _, site := range all {
		st := agentapi.SiteStats{Name: site.User}

		if p, ok := pools[site.User]; ok {
			st.PHPVersion = p.Version

			v, err := sitestats.ReadPHP(o.cgroupRoot, p.Version, site.User)
			if err != nil {
				o.h.log.WarnContext(ctx, "metrics: read php cgroup", "site", site.User, "err", err)
			}

			st.PHPUp, st.PHPMemoryBytes, st.PHPMemoryKnown = v.Up, v.MemoryBytes, v.MemoryKnown
		}

		if c, ok := caches[site.User]; ok {
			st.CacheMB = c.MemoryMB

			v, err := sitestats.ReadCache(o.cgroupRoot, site.User)
			if err != nil {
				o.h.log.WarnContext(ctx, "metrics: read object cache cgroup", "site", site.User, "err", err)
			}

			st.CacheUp, st.CacheMemoryBytes, st.CacheMemoryKnown = v.Up, v.MemoryBytes, v.MemoryKnown
		}

		d := o.disk.Get(site.User)
		st.DiskUsedBytes, st.DiskMeasuredAt = d.UsedBytes, d.MeasuredAt

		out.Sites = append(out.Sites, st)
	}

	return out
}

// homes maps every registered site to its home.
func homes(m *sites.Manager) map[string]string {
	out := map[string]string{}

	for _, site := range m.List() {
		if home, ok := m.Home(site.User); ok {
			out[site.User] = home
		}
	}

	return out
}

// diskLoop measures the homes right away and then every DiskEvery.
func diskLoop(ctx context.Context, c *sitestats.DiskCache, list func() map[string]string, log *slog.Logger) {
	if log == nil {
		log = slog.New(slog.DiscardHandler)
	}

	t := time.NewTicker(agentapi.DiskEvery)
	defer t.Stop()

	for {
		if err := c.Refresh(ctx, list(), time.Now); err != nil && ctx.Err() == nil {
			log.WarnContext(ctx, "metrics: measure site homes", "err", err)
		}

		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
	}
}
