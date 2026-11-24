// SPDX-License-Identifier: AGPL-3.0-or-later

package agentapi

import "github.com/maki-it/hostashi/internal/agentrpc"

// SitePermissionsReset gives every file below a site's home back to the
// site user and resets the modes to Hostashi's defaults (ADR-0067). It never
// follows a symbolic link and never leaves the home's file system.
var SitePermissionsReset = agentrpc.Op[SitePermissionsResetRequest, SitePermissionsResetResponse]{Name: "site.permissions.reset"}

// SitePermissionsResetRequest names the site.
type SitePermissionsResetRequest struct {
	Site string `json:"site"`
}

// Validate checks the site name.
func (r SitePermissionsResetRequest) Validate() error { return ValidateSiteUser(r.Site) }

// MaxPermissionsSkipped is how many skipped entries a reset reports by
// path; the count in Skipped covers all of them.
const MaxPermissionsSkipped = 50

// SitePermissionsResetResponse counts what the reset did.
type SitePermissionsResetResponse struct {
	// Checked is the number of entries looked at, the home included.
	Checked int64 `json:"checked"`
	// Changed is the number of entries whose owner, group or mode changed.
	Changed int64 `json:"changed"`
	// Skipped is the number of entries left alone (see SkippedPaths).
	Skipped int64 `json:"skipped"`
	// SkippedPaths lists up to MaxPermissionsSkipped skipped entries.
	SkippedPaths []PermissionsSkip `json:"skippedPaths,omitempty"`
}

// PermissionsSkip is one entry the reset did not touch.
type PermissionsSkip struct {
	// Path is relative to the home.
	Path   string `json:"path"`
	Reason string `json:"reason"`
}
