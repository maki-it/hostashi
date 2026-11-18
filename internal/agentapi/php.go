// SPDX-License-Identifier: AGPL-3.0-or-later

package agentapi

import (
	"fmt"
	"slices"

	"github.com/maki-it/hostashi/internal/agentrpc"
)

// PHPVersions are the PHP versions Hostashi can run a site on (ADR-0019):
// 8.3 to 8.5 as standard, 8.2, 8.1 and 7.4 as legacy. Whether a version is
// installed is a separate question (PHPVersionsList).
var PHPVersions = []string{"7.4", "8.1", "8.2", "8.3", "8.4", "8.5"}

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
}

// PHPVersionsList reports the installed PHP versions.
var PHPVersionsList = agentrpc.Op[PHPVersionsRequest, PHPVersionsResponse]{Name: "php.versions"}
