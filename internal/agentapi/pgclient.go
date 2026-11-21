// SPDX-License-Identifier: AGPL-3.0-or-later

package agentapi

import (
	"fmt"
	"slices"

	"github.com/maki-it/hostashi/internal/agentrpc"
)

// PGClientMajors are the PostgreSQL client majors the agent may install
// from apt.postgresql.org (ADR-0051), oldest first. pg_dump dumps servers of
// its own major and older ones, so the newest is the one Hostashi installs.
// A new major is added in a Hostashi release; packaging/postgresql/
// pg-client-install has the same list (checked by a test).
var PGClientMajors = []string{"19"}

// PGClientNewest is the newest of PGClientMajors.
func PGClientNewest() string { return PGClientMajors[len(PGClientMajors)-1] }

// ValidatePGClientMajor accepts a major from PGClientMajors.
func ValidatePGClientMajor(m string) error {
	if !slices.Contains(PGClientMajors, m) {
		return fmt.Errorf("postgresql client: unsupported major %q", m)
	}

	return nil
}

// PGClientInstallRequest installs the client tools of one major. An empty
// Major means PGClientNewest.
type PGClientInstallRequest struct {
	Major string `json:"major,omitempty"`
}

// Validate accepts an empty major or one from PGClientMajors.
func (r PGClientInstallRequest) Validate() error {
	if r.Major == "" {
		return nil
	}

	return ValidatePGClientMajor(r.Major)
}

// PGClientStatusRequest has no parameters.
type PGClientStatusRequest struct{}

// Validate accepts the empty request.
func (PGClientStatusRequest) Validate() error { return nil }

// PGClientStatus is what is installed and the last installation: its
// major, its state (PHPInstallIdle, PHPInstallRunning, PHPInstallSucceeded
// or PHPInstallFailed, the same states as for PHP) and the last lines of
// its log.
type PGClientStatus struct {
	// Installed are the majors whose pg_dump is installed, as numbers in
	// ascending order. Majors outside PGClientMajors (for example from a
	// PostgreSQL server package) count too: their pg_dump works the same.
	Installed []string `json:"installed"`
	Major     string   `json:"major,omitempty"`
	State     string   `json:"state"`
	Log       []string `json:"log"`
}

// PGClientInstall starts the installation (pg.client.install); it returns
// at once, PGClientStatusOp follows it.
var PGClientInstall = agentrpc.Op[PGClientInstallRequest, PGClientStatus]{Name: "pg.client.install"}

// PGClientStatusOp reports the installed majors and the last installation
// (pg.client.status).
var PGClientStatusOp = agentrpc.Op[PGClientStatusRequest, PGClientStatus]{Name: "pg.client.status"}
