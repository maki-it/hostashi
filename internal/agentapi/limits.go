// SPDX-License-Identifier: AGPL-3.0-or-later

package agentapi

import (
	"fmt"

	"github.com/maki-it/hostashi/internal/agentrpc"
)

// Bounds of a site's CPU, memory and process limits (ADR-0015). 0 means no
// limit (CPU weight: the default of 100); any other value must lie within
// the range.
const (
	MinMemoryMax       uint64 = 64 << 20 // 64 MiB
	MaxMemoryMax       uint64 = 1 << 40  // 1 TiB
	MinTasksMax        uint64 = 16
	MaxTasksMax        uint64 = 1 << 20
	MaxCPUWeight       uint64 = 10000
	MaxCPUQuotaPercent uint64 = 100000 // 1000 CPUs
)

// CPU priority modes of the panel map to these weights (ADR-0015).
const (
	CPUWeightLow    uint64 = 50
	CPUWeightNormal uint64 = 100
	CPUWeightHigh   uint64 = 200
)

// SiteLimits are the limits of a site's slice.
type SiteLimits struct {
	MemoryMax       uint64 `json:"memory_max"`        // bytes, 0: none
	TasksMax        uint64 `json:"tasks_max"`         // 0: none
	CPUWeight       uint64 `json:"cpu_weight"`        // 1..10000, 0: 100
	CPUQuotaPercent uint64 `json:"cpu_quota_percent"` // 100 = one CPU, 0: none
}

// Validate checks the values.
func (l SiteLimits) Validate() error {
	if l.MemoryMax != 0 && (l.MemoryMax < MinMemoryMax || l.MemoryMax > MaxMemoryMax) {
		return fmt.Errorf("memory_max: must be 0 (none) or between %d and %d", MinMemoryMax, MaxMemoryMax)
	}

	if l.TasksMax != 0 && (l.TasksMax < MinTasksMax || l.TasksMax > MaxTasksMax) {
		return fmt.Errorf("tasks_max: must be 0 (none) or between %d and %d", MinTasksMax, MaxTasksMax)
	}

	if l.CPUWeight > MaxCPUWeight {
		return fmt.Errorf("cpu_weight: must be 0 (default) or between 1 and %d", MaxCPUWeight)
	}

	if l.CPUQuotaPercent > MaxCPUQuotaPercent {
		return fmt.Errorf("cpu_quota_percent: must be 0 (none) or between 1 and %d", MaxCPUQuotaPercent)
	}

	return nil
}

// SiteLimitsSetRequest sets the limits of a site.
type SiteLimitsSetRequest struct {
	User   string     `json:"user"`
	Limits SiteLimits `json:"limits"`
}

// Validate checks the request.
func (r SiteLimitsSetRequest) Validate() error {
	if err := ValidateSiteUser(r.User); err != nil {
		return err
	}

	return r.Limits.Validate()
}

// SiteLimitsSet writes the site's slice and applies it.
var SiteLimitsSet = agentrpc.Op[SiteLimitsSetRequest, SiteLimits]{Name: "site.limits.set"}

// SiteLimitsGetRequest names the site.
type SiteLimitsGetRequest struct {
	User string `json:"user"`
}

// Validate checks the request.
func (r SiteLimitsGetRequest) Validate() error { return ValidateSiteUser(r.User) }

// SiteLimitsGet reads the site's limits; a site without a slice file has
// none (all zero).
var SiteLimitsGet = agentrpc.Op[SiteLimitsGetRequest, SiteLimits]{Name: "site.limits.get"}
