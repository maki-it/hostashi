// SPDX-License-Identifier: AGPL-3.0-or-later

package metrics

import (
	"context"
	"log/slog"
	"time"

	"github.com/prometheus/client_golang/prometheus"
)

// Site is one site's numbers from the agent's metrics.snapshot.
type Site struct {
	Name           string // system name, the only site label (ADR-0022)
	PHPVersion     string // empty: the site has no PHP
	PHPUp          bool
	PHPMemoryBytes uint64
	PHPMemoryKnown bool
	DiskUsedBytes  uint64
	DiskMeasuredAt time.Time // zero: not measured yet
}

var (
	sitePHPUpDesc = prometheus.NewDesc("hostashi_site_php_up",
		"1 if the site's PHP-FPM master runs.", []string{"site", "php_version"}, nil)
	sitePHPMemDesc = prometheus.NewDesc("hostashi_site_php_memory_bytes",
		"Memory of the site's PHP-FPM unit from its cgroup.", []string{"site", "php_version"}, nil)
	siteDiskDesc = prometheus.NewDesc("hostashi_site_disk_used_bytes",
		"Disk space the site's home takes, from the last measurement.", []string{"site"}, nil)
	siteDiskAtDesc = prometheus.NewDesc("hostashi_site_disk_measured_timestamp_seconds",
		"When the site's disk usage was measured, as Unix time.", []string{"site"}, nil)
	snapshotUpDesc = prometheus.NewDesc("hostashi_agent_snapshot_up",
		"1 if the last per-site snapshot from the agent succeeded.", nil, nil)
)

// RegisterSites reads the per-site numbers on every gather through
// snapshot. When the agent does not answer, the site series are left out of
// that scrape and hostashi_agent_snapshot_up is 0.
func (m *Metrics) RegisterSites(snapshot func(ctx context.Context) ([]Site, error), log *slog.Logger) {
	if log == nil {
		log = slog.New(slog.DiscardHandler)
	}

	m.reg.MustRegister(&siteCollector{snapshot: snapshot, log: log})
}

type siteCollector struct {
	snapshot func(ctx context.Context) ([]Site, error)
	log      *slog.Logger
}

func (c *siteCollector) Describe(ch chan<- *prometheus.Desc) {
	for _, d := range []*prometheus.Desc{sitePHPUpDesc, sitePHPMemDesc, siteDiskDesc, siteDiskAtDesc, snapshotUpDesc} {
		ch <- d
	}
}

func (c *siteCollector) Collect(ch chan<- prometheus.Metric) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	sites, err := c.snapshot(ctx)
	if err != nil {
		c.log.ErrorContext(ctx, "metrics: per-site snapshot", "err", err)

		ch <- prometheus.MustNewConstMetric(snapshotUpDesc, prometheus.GaugeValue, 0)

		return
	}

	ch <- prometheus.MustNewConstMetric(snapshotUpDesc, prometheus.GaugeValue, 1)

	for _, s := range sites {
		if s.PHPVersion != "" {
			up := 0.0
			if s.PHPUp {
				up = 1
			}

			ch <- prometheus.MustNewConstMetric(sitePHPUpDesc, prometheus.GaugeValue, up, s.Name, s.PHPVersion)

			if s.PHPMemoryKnown {
				ch <- prometheus.MustNewConstMetric(sitePHPMemDesc, prometheus.GaugeValue, float64(s.PHPMemoryBytes), s.Name, s.PHPVersion)
			}
		}

		if !s.DiskMeasuredAt.IsZero() {
			ch <- prometheus.MustNewConstMetric(siteDiskDesc, prometheus.GaugeValue, float64(s.DiskUsedBytes), s.Name)

			ch <- prometheus.MustNewConstMetric(siteDiskAtDesc, prometheus.GaugeValue, float64(s.DiskMeasuredAt.Unix()), s.Name)
		}
	}
}
