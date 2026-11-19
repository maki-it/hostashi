// SPDX-License-Identifier: AGPL-3.0-or-later

package agentapi

import (
	"fmt"

	"github.com/maki-it/hostashi/internal/agentrpc"
)

// Bounds of a site's disk limits (ADR-0015). 0 means unlimited; any other
// value must lie within the range, so a typo cannot lock a site out with a
// limit of a few bytes.
const (
	MinQuotaBytes  uint64 = 64 << 20 // 64 MiB
	MaxQuotaBytes  uint64 = 1 << 50  // 1 PiB
	MinQuotaInodes uint64 = 1000
	MaxQuotaInodes uint64 = 1<<32 - 1
)

// SiteQuota is a site's disk use and its hard limits. Available is false
// when the file system of the site's pool has no project quota; then the
// other fields are zero.
type SiteQuota struct {
	Available   bool   `json:"available"`
	Bytes       uint64 `json:"bytes"`
	Inodes      uint64 `json:"inodes"`
	BytesLimit  uint64 `json:"bytes_limit"`  // 0: unlimited
	InodesLimit uint64 `json:"inodes_limit"` // 0: unlimited
}

// SiteQuotaSetRequest sets the hard limits of a site. 0 means unlimited.
type SiteQuotaSetRequest struct {
	User   string `json:"user"`
	Bytes  uint64 `json:"bytes"`
	Inodes uint64 `json:"inodes"`
}

// Validate checks the request.
func (r SiteQuotaSetRequest) Validate() error {
	if err := ValidateSiteUser(r.User); err != nil {
		return err
	}

	if r.Bytes != 0 && (r.Bytes < MinQuotaBytes || r.Bytes > MaxQuotaBytes) {
		return fmt.Errorf("bytes: must be 0 (unlimited) or between %d and %d", MinQuotaBytes, MaxQuotaBytes)
	}

	if r.Inodes != 0 && (r.Inodes < MinQuotaInodes || r.Inodes > MaxQuotaInodes) {
		return fmt.Errorf("inodes: must be 0 (unlimited) or between %d and %d", MinQuotaInodes, MaxQuotaInodes)
	}

	return nil
}

// SiteQuotaSet puts the site's home into its project (ID = the site's UID)
// and sets the limits. A limit on a file system without project quota is
// refused with failed_precondition; setting both to 0 there succeeds and
// changes nothing.
var SiteQuotaSet = agentrpc.Op[SiteQuotaSetRequest, SiteQuota]{Name: "site.quota.set"}

// SiteQuotaGetRequest names the site.
type SiteQuotaGetRequest struct {
	User string `json:"user"`
}

// Validate checks the request.
func (r SiteQuotaGetRequest) Validate() error { return ValidateSiteUser(r.User) }

// SiteQuotaGet reads a site's disk use and limits.
var SiteQuotaGet = agentrpc.Op[SiteQuotaGetRequest, SiteQuota]{Name: "site.quota.get"}
