// SPDX-License-Identifier: AGPL-3.0-or-later

package metrics

import (
	"context"
	"log/slog"
	"time"

	"github.com/prometheus/client_golang/prometheus"
)

// SiteUsage is one site's counters from the agent's sites.stats (ADR-0063).
// The counters start again when a site's cgroup is made anew or the agent
// restarts; Prometheus treats a smaller value as a reset.
type SiteUsage struct {
	Name         string // system name, the only site label (ADR-0022)
	CPUUsec      uint64
	MemoryBytes  uint64 // working set
	MemoryKnown  bool
	Tasks        uint64
	IOReadBytes  uint64
	IOWriteBytes uint64
	IOKnown      bool
	HTTPRequests uint64
	HTTPBytesIn  uint64
	HTTPBytesOut uint64
}

var (
	siteCPUDesc = prometheus.NewDesc("hostashi_site_cpu_seconds_total",
		"CPU time of the site's processes, including its SSH and SFTP sessions.", []string{"site"}, nil)
	siteMemDesc = prometheus.NewDesc("hostashi_site_memory_bytes",
		"Memory the site's processes use, without the file cache (working set).", []string{"site"}, nil)
	siteTasksDesc = prometheus.NewDesc("hostashi_site_tasks",
		"Processes and threads of the site.", []string{"site"}, nil)
	siteIODesc = prometheus.NewDesc("hostashi_site_io_bytes_total",
		"Bytes the site's processes read from and wrote to disk.", []string{"site", "direction"}, nil)
	siteHTTPBytesDesc = prometheus.NewDesc("hostashi_site_http_bytes_total",
		"Bytes of the site's web traffic: in from clients, out from nginx.", []string{"site", "direction"}, nil)
	siteHTTPReqDesc = prometheus.NewDesc("hostashi_site_http_requests_total",
		"Requests nginx answered for the site.", []string{"site"}, nil)
	usageUpDesc = prometheus.NewDesc("hostashi_agent_site_usage_up",
		"1 if the last reading of the sites' usage from the agent succeeded.", nil, nil)
)

// RegisterSiteUsage reads the sites' usage counters on every gather. When
// the agent does not answer, the series are left out and
// hostashi_agent_site_usage_up is 0.
func (m *Metrics) RegisterSiteUsage(read func(ctx context.Context) ([]SiteUsage, error), log *slog.Logger) {
	if log == nil {
		log = slog.New(slog.DiscardHandler)
	}

	m.reg.MustRegister(&usageCollector{read: read, log: log})
}

type usageCollector struct {
	read func(ctx context.Context) ([]SiteUsage, error)
	log  *slog.Logger
}

func (c *usageCollector) Describe(ch chan<- *prometheus.Desc) {
	for _, d := range []*prometheus.Desc{siteCPUDesc, siteMemDesc, siteTasksDesc, siteIODesc, siteHTTPBytesDesc, siteHTTPReqDesc, usageUpDesc} {
		ch <- d
	}
}

func (c *usageCollector) Collect(ch chan<- prometheus.Metric) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	sites, err := c.read(ctx)
	if err != nil {
		c.log.ErrorContext(ctx, "metrics: site usage", "err", err)

		ch <- prometheus.MustNewConstMetric(usageUpDesc, prometheus.GaugeValue, 0)

		return
	}

	ch <- prometheus.MustNewConstMetric(usageUpDesc, prometheus.GaugeValue, 1)

	for _, s := range sites {
		ch <- prometheus.MustNewConstMetric(siteCPUDesc, prometheus.CounterValue, float64(s.CPUUsec)/1e6, s.Name)

		ch <- prometheus.MustNewConstMetric(siteTasksDesc, prometheus.GaugeValue, float64(s.Tasks), s.Name)

		if s.MemoryKnown {
			ch <- prometheus.MustNewConstMetric(siteMemDesc, prometheus.GaugeValue, float64(s.MemoryBytes), s.Name)
		}

		if s.IOKnown {
			ch <- prometheus.MustNewConstMetric(siteIODesc, prometheus.CounterValue, float64(s.IOReadBytes), s.Name, "read")

			ch <- prometheus.MustNewConstMetric(siteIODesc, prometheus.CounterValue, float64(s.IOWriteBytes), s.Name, "write")
		}

		ch <- prometheus.MustNewConstMetric(siteHTTPBytesDesc, prometheus.CounterValue, float64(s.HTTPBytesIn), s.Name, "in")

		ch <- prometheus.MustNewConstMetric(siteHTTPBytesDesc, prometheus.CounterValue, float64(s.HTTPBytesOut), s.Name, "out")

		ch <- prometheus.MustNewConstMetric(siteHTTPReqDesc, prometheus.CounterValue, float64(s.HTTPRequests), s.Name)
	}
}
