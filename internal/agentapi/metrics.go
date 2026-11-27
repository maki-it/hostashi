// SPDX-License-Identifier: AGPL-3.0-or-later

package agentapi

import (
	"time"

	"github.com/maki-it/hostashi/internal/agentrpc"
)

// MetricsSnapshotRequest has no parameters.
type MetricsSnapshotRequest struct{}

// Validate accepts the empty request.
func (MetricsSnapshotRequest) Validate() error { return nil }

// SiteStats are the per-site numbers the panel turns into metrics
// (ADR-0022). It is a fixed struct: the agent never passes through free
// text or exposition format.
type SiteStats struct {
	Name string `json:"name"` // the site's system name
	// PHPVersion is the version of the site's FPM pool, empty without PHP.
	PHPVersion     string `json:"phpVersion,omitempty"`
	PHPUp          bool   `json:"phpUp"`
	PHPMemoryBytes uint64 `json:"phpMemoryBytes"`
	PHPMemoryKnown bool   `json:"phpMemoryKnown"`
	// DiskUsedBytes is the last measurement of the site's home; the agent
	// walks each home at most every DiskEvery. Zero DiskMeasuredAt: not yet.
	DiskUsedBytes  uint64    `json:"diskUsedBytes"`
	DiskMeasuredAt time.Time `json:"diskMeasuredAt,omitzero"`
	// CacheMB is the size of the site's object cache (ADR-0045), 0 for
	// none; the other Cache fields come from the unit's cgroup.
	CacheMB          int    `json:"cacheMb,omitempty"`
	CacheUp          bool   `json:"cacheUp,omitempty"`
	CacheMemoryBytes uint64 `json:"cacheMemoryBytes,omitempty"`
	CacheMemoryKnown bool   `json:"cacheMemoryKnown,omitempty"`
}

// DiskEvery is how often the agent measures the sites' homes [E].
const DiskEvery = 15 * time.Minute

// MetricsSnapshotResponse lists at most MaxSites sites, sorted by name.
type MetricsSnapshotResponse struct {
	Sites []SiteStats `json:"sites"`
}

// MetricsSnapshot reads the per-site numbers. It only reads and answers
// from cached disk measurements, so a scrape stays cheap.
var MetricsSnapshot = agentrpc.Op[MetricsSnapshotRequest, MetricsSnapshotResponse]{Name: "metrics.snapshot"}

// SitesStatsRequest has no parameters.
type SitesStatsRequest struct{}

// Validate accepts the empty request.
func (SitesStatsRequest) Validate() error { return nil }

// SiteUsage are the raw counters of one site's slice plus the slice of its
// SSH and SFTP sessions (ADR-0063). Counters only grow while the cgroup
// lives; the panel turns two readings into rates and treats a smaller
// value as a restart.
type SiteUsage struct {
	Name    string `json:"name"`
	CPUUsec uint64 `json:"cpuUsec"`
	// MemoryBytes is the working set: memory.current minus inactive_file.
	MemoryBytes  uint64 `json:"memoryBytes"`
	CacheBytes   uint64 `json:"cacheBytes"`
	MemoryKnown  bool   `json:"memoryKnown"`
	Tasks        uint64 `json:"tasks"`
	IOReadBytes  uint64 `json:"ioReadBytes"`
	IOWriteBytes uint64 `json:"ioWriteBytes"`
	IOKnown      bool   `json:"ioKnown"`
	// HTTP* count the site's traffic.log since the agent started: requests,
	// bytes the clients sent and bytes nginx sent.
	HTTPRequests uint64 `json:"httpRequests"`
	HTTPBytesIn  uint64 `json:"httpBytesIn"`
	HTTPBytesOut uint64 `json:"httpBytesOut"`
}

// SitesStatsResponse lists at most MaxSites sites, sorted by name.
type SitesStatsResponse struct {
	At    time.Time   `json:"at"`
	Sites []SiteUsage `json:"sites"`
}

// SitesStats reads the cgroup counters of every site. It only reads small
// files below /sys/fs/cgroup.
var SitesStats = agentrpc.Op[SitesStatsRequest, SitesStatsResponse]{Name: "sites.stats"}
