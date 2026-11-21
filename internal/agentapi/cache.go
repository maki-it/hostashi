// SPDX-License-Identifier: AGPL-3.0-or-later

package agentapi

import (
	"fmt"

	"github.com/maki-it/hostashi/internal/agentrpc"
)

// Sizes of a site's object cache in MiB (ADR-0045): Valkey's maxmemory.
const (
	DefaultCacheMB = 64
	MinCacheMB     = 32
	MaxCacheMB     = 1024
)

// ValidateCacheMB accepts 0 (no cache) or a size from MinCacheMB to
// MaxCacheMB.
func ValidateCacheMB(mb int) error {
	if mb != 0 && (mb < MinCacheMB || mb > MaxCacheMB) {
		return fmt.Errorf("cache: size must be %d to %d MiB, not %d", MinCacheMB, MaxCacheMB, mb)
	}

	return nil
}

// CacheApplyRequest sets the object cache of a site: MemoryMB 0 turns it
// off and removes it, any other size turns it on with that maxmemory. A
// changed size restarts the cache, which empties it.
type CacheApplyRequest struct {
	Site     string `json:"site"`
	MemoryMB int    `json:"memoryMb"`
}

// Validate checks the site and the size.
func (r CacheApplyRequest) Validate() error {
	if err := ValidateSiteUser(r.Site); err != nil {
		return err
	}

	return ValidateCacheMB(r.MemoryMB)
}

// CacheSiteRequest names a site for cache.flush and cache.status.
type CacheSiteRequest struct {
	Site string `json:"site"`
}

// Validate checks the site.
func (r CacheSiteRequest) Validate() error { return ValidateSiteUser(r.Site) }

// CacheStatus is the object cache of a site as the agent runs it.
type CacheStatus struct {
	Site string `json:"site"`
	// MemoryMB is the configured size, 0 when the site has no cache.
	MemoryMB int `json:"memoryMb"`
	// Socket is the Unix socket the site's apps connect to, empty without
	// a cache.
	Socket string `json:"socket,omitempty"`
	// State is systemd's ActiveState of the unit ("active", "failed",
	// "inactive", ...), empty without a cache.
	State string `json:"state,omitempty"`
	// Paused is true while the site is disabled: the cache is stopped and
	// comes back with the site.
	Paused bool `json:"paused,omitempty"`
	// UsedBytes is the unit's memory as the kernel counts it, 0 when not
	// known.
	UsedBytes uint64 `json:"usedBytes,omitempty"`
}

// Object cache operations (ADR-0045). cache.apply turns a site's cache on,
// resizes or removes it; cache.flush empties it by restarting it (the
// agent never speaks the Valkey protocol); cache.status reports it.
var (
	CacheApply    = agentrpc.Op[CacheApplyRequest, CacheStatus]{Name: "cache.apply"}
	CacheFlush    = agentrpc.Op[CacheSiteRequest, CacheStatus]{Name: "cache.flush"}
	CacheStatusOp = agentrpc.Op[CacheSiteRequest, CacheStatus]{Name: "cache.status"}
)
