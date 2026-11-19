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
