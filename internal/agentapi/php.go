// SPDX-License-Identifier: AGPL-3.0-or-later

package agentapi

import (
	"fmt"
	"slices"

	"github.com/maki-it/hostashi/internal/agentrpc"
)

// LegacyPHPVersions get no or only security fixes upstream; the panel warns
// before a site is put on one (ADR-0019, rule 3).
var LegacyPHPVersions = []string{"7.4", "8.1", "8.2"}

// ValidatePHPVersion accepts a version from PHPVersions. The empty string
// is not a version: callers that allow "no PHP" check for it first.
func ValidatePHPVersion(v string) error {
	if !slices.Contains(PHPVersions, v) {
		return fmt.Errorf("php: unsupported version %q", v)
	}

	return nil
}

// PHPVersionsRequest has no parameters.
type PHPVersionsRequest struct{}

// Validate accepts the empty request.
func (PHPVersionsRequest) Validate() error { return nil }

// PHPVersionsResponse lists the versions from PHPVersions whose FPM binary
// is installed, in the order of PHPVersions.
type PHPVersionsResponse struct {
	Installed []string `json:"installed"`
	// Repository is true when the sury.org source is set up (ADR-0019).
	Repository bool `json:"repository"`
	// Newer are the versions sury.org offers above PHPVersions, from apt's
	// package lists (ADR-0036). Hostashi does not run them yet.
	Newer []string `json:"newer"`
}

// PHPVersionsList reports the installed PHP versions.
var PHPVersionsList = agentrpc.Op[PHPVersionsRequest, PHPVersionsResponse]{Name: "php.versions"}

// PHPInstallRequest installs a version from PHPVersions (ADR-0036).
type PHPInstallRequest struct {
	Version string `json:"version"`
}

// Validate accepts a version from PHPVersions.
func (r PHPInstallRequest) Validate() error { return ValidatePHPVersion(r.Version) }

// PHPInstallStatusRequest has no parameters.
type PHPInstallStatusRequest struct{}

// Validate accepts the empty request.
func (PHPInstallStatusRequest) Validate() error { return nil }

// The states of the last installation.
const (
	PHPInstallIdle      = "idle"      // none has run
	PHPInstallRunning   = "running"   // the unit is active
	PHPInstallSucceeded = "succeeded" // the script printed its final line
	PHPInstallFailed    = "failed"    // it ended without that line
)

// PHPInstallStatus is the last installation: its version, its state and
// the last lines of its log (the script's and apt's output).
type PHPInstallStatus struct {
	Version string   `json:"version,omitempty"`
	State   string   `json:"state"`
	Log     []string `json:"log"`
}

// PHPInstall starts the installation of a version (php.install); it
// returns at once, PHPInstallStatusOp follows it.
var PHPInstall = agentrpc.Op[PHPInstallRequest, PHPInstallStatus]{Name: "php.install"}

// PHPInstallStatusOp reports the last installation (php.install.status).
var PHPInstallStatusOp = agentrpc.Op[PHPInstallStatusRequest, PHPInstallStatus]{Name: "php.install.status"}
