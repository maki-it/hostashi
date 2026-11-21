// SPDX-License-Identifier: AGPL-3.0-or-later

package agentapi

import "github.com/maki-it/hostashi/internal/agentrpc"

// HostStatsRequest has no parameters.
type HostStatsRequest struct{}

// Validate accepts the empty request.
func (HostStatsRequest) Validate() error { return nil }

// CPUTimes are the counters of the "cpu" line of /proc/stat, in clock
// ticks since boot. The panel turns two samples into percentages.
type CPUTimes struct {
	User    uint64 `json:"user"`
	Nice    uint64 `json:"nice"`
	System  uint64 `json:"system"`
	Idle    uint64 `json:"idle"`
	IOWait  uint64 `json:"iowait"`
	IRQ     uint64 `json:"irq"`
	SoftIRQ uint64 `json:"softirq"`
	Steal   uint64 `json:"steal"`
}

// Total is the sum of all counters.
func (c CPUTimes) Total() uint64 {
	return c.User + c.Nice + c.System + c.Idle + c.IOWait + c.IRQ + c.SoftIRQ + c.Steal
}

// HostMemory is from /proc/meminfo, in bytes.
type HostMemory struct {
	TotalBytes     uint64 `json:"totalBytes"`
	AvailableBytes uint64 `json:"availableBytes"`
	SwapTotalBytes uint64 `json:"swapTotalBytes"`
	SwapFreeBytes  uint64 `json:"swapFreeBytes"`
}

// HostDisk is one mount the panel shows: "/" and the storage pools
// (ADR-0016). Name is "root" or the pool's name, never a path.
type HostDisk struct {
	Name       string `json:"name"`
	TotalBytes uint64 `json:"totalBytes"`
	FreeBytes  uint64 `json:"freeBytes"` // available to unprivileged users
	// Inodes, for the warning when a file system runs out of them.
	TotalInodes uint64 `json:"totalInodes"`
	FreeInodes  uint64 `json:"freeInodes"`
}

// HostNet is one interface of /proc/net/dev, counters since boot.
type HostNet struct {
	Name      string `json:"name"`
	RxBytes   uint64 `json:"rxBytes"`
	TxBytes   uint64 `json:"txBytes"`
	RxPackets uint64 `json:"rxPackets"`
	TxPackets uint64 `json:"txPackets"`
}

// Limits on the answer of host.stats.
const (
	MaxHostDisks = 1 + 16
	MaxHostNets  = 16
)

// HostStats are raw counters of the server (ADR-0052). The agent reads
// them on every call and keeps nothing.
type HostStats struct {
	CPUs          int        `json:"cpus"`
	CPU           CPUTimes   `json:"cpu"`
	Load1         float64    `json:"load1"`
	Load5         float64    `json:"load5"`
	Load15        float64    `json:"load15"`
	UptimeSeconds float64    `json:"uptimeSeconds"`
	Memory        HostMemory `json:"memory"`
	Disks         []HostDisk `json:"disks"`
	Net           []HostNet  `json:"net"`
}

// HostStatsOp reads the server's counters. It only reads small files in
// /proc and calls statfs.
var HostStatsOp = agentrpc.Op[HostStatsRequest, HostStats]{Name: "host.stats"}
