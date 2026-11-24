// SPDX-License-Identifier: AGPL-3.0-or-later

package agentapi

import (
	"errors"
	"slices"

	"github.com/maki-it/hostashi/internal/agentrpc"
)

// PythonRelease is the pinned CPython release of one line (ADR-0061).
// Hostashi installs the install_only_stripped build of python-build-standalone
// and checks it against SHA256 before it unpacks anything. The checksums
// come from the release's SHA256SUMS; the build provenance of each archive
// was checked with Sigstore when the list was updated.
type PythonRelease struct {
	Line    string // the line, for example "3.13"
	Version string // the exact release, for example "3.13.18"
	// Tag is the python-build-standalone release, for example "20261110".
	Tag string
	// End is the line's end of life (YYYY-MM-DD) from the Python release
	// schedule.
	End string
	// SHA256 of cpython-<Version>+<Tag>-<arch>-unknown-linux-gnu-install_only_stripped.tar.gz
	// by arch (x86_64, aarch64).
	SHA256 map[string]string
}

// PythonReleases are the lines Hostashi installs, oldest first: every line
// that has not ended. Checked on 2026-11-24 against
// https://peps.python.org/api/release-cycle.json and the SHA256SUMS of
// python-build-standalone release 20261123; the build provenance of each
// new archive was verified with Sigstore.
var PythonReleases = []PythonRelease{
	{
		Line: "3.11", Version: "3.11.18", Tag: "20261123", End: "2027-10-31",
		SHA256: map[string]string{
			"x86_64":  "75b92c55bc6a5f58fe4d08fc5261c983b2b17930192d62946fe7c9cf6fd66316",
			"aarch64": "cc55f9298334bfd87f932368368dc33f88d608dacc01bd6602a05e01dc9e376f",
		},
	},
	{
		Line: "3.12", Version: "3.12.16", Tag: "20261123", End: "2028-10-31",
		SHA256: map[string]string{
			"x86_64":  "c85811d35971d06612585a057db3b71806378df6ea8476275fb16aa5c9fe56a0",
			"aarch64": "97bca1d0960240b721b6e58ec2dc080519fcd11f979bf64356434ae9d0f96947",
		},
	},
	{
		Line: "3.13", Version: "3.13.18", Tag: "20261123", End: "2029-10-31",
		SHA256: map[string]string{
			"x86_64":  "65d5c786e3cbd67533c346537ad0b8826ef158287b3cebaa34b30558cc330f59",
			"aarch64": "9c9da4fc81fcdda1683ec8f97674bae46e3d246243ce8d781973817e13e2ec12",
		},
	},
	{
		Line: "3.14", Version: "3.14.10", Tag: "20261123", End: "2030-10-31",
		SHA256: map[string]string{
			"x86_64":  "92102b15f5149d124b5b4b2f00f1ff2f827cf1a99dc1a22447ad52861bd80ace",
			"aarch64": "f1641401210144ca2eeb2774ee531f8bf903fd7f05d198b7feed62b40cbf4ba8",
		},
	},
	{
		Line: "3.15", Version: "3.15.2", Tag: "20261123", End: "2031-10-31",
		SHA256: map[string]string{
			"x86_64":  "18c56e105ba7713602536f49976c69d14d73cea7d32cfe2cb68eb8a79568de47",
			"aarch64": "89bb07630c7056f5fee066e5fffc7c9a8141f6f2a5a4402eb1c72b3358e26809",
		},
	},
}

// ErrPythonLine: not one of the lines in PythonReleases.
var ErrPythonLine = errors.New("not a Python line Hostashi installs")

// PythonReleaseOf returns the release of a line.
func PythonReleaseOf(line string) (PythonRelease, error) {
	i := slices.IndexFunc(PythonReleases, func(r PythonRelease) bool { return r.Line == line })
	if i < 0 {
		return PythonRelease{}, ErrPythonLine
	}

	return PythonReleases[i], nil
}

// ValidatePythonLine accepts the lines in PythonReleases only, so the line
// can name a unit instance and a directory.
func ValidatePythonLine(line string) error {
	_, err := PythonReleaseOf(line)

	return err
}

// Python installation (ADR-0061): python.install starts the unit
// hostashi-python-install@<line>, python.status reports the lines and the
// last installation, python.remove deletes a line no app runs on.
var (
	PythonInstall  = agentrpc.Op[PythonInstallRequest, PythonStatus]{Name: "python.install"}
	PythonStatusOp = agentrpc.Op[PythonStatusRequest, PythonStatus]{Name: "python.status"}
	PythonRemove   = agentrpc.Op[PythonRemoveRequest, PythonStatus]{Name: "python.remove"}
)

// PythonDoneMarker starts the installer's last line after a successful run.
const PythonDoneMarker = "python-install: done:"

// PythonInstallRequest installs or updates one line.
type PythonInstallRequest struct {
	Line string `json:"line"`
}

// Validate implements agentrpc's request check.
func (r PythonInstallRequest) Validate() error { return ValidatePythonLine(r.Line) }

// PythonRemoveRequest removes one installed line.
type PythonRemoveRequest struct {
	Line string `json:"line"`
}

// Validate implements agentrpc's request check.
func (r PythonRemoveRequest) Validate() error { return ValidatePythonLine(r.Line) }

// PythonStatusRequest asks for the installed lines and the last
// installation.
type PythonStatusRequest struct{}

// Validate implements agentrpc's request check.
func (PythonStatusRequest) Validate() error { return nil }

// PythonInstalled is one installed line and its release.
type PythonInstalled struct {
	Line    string `json:"line"`
	Version string `json:"version"`
	// Build is the python-build-standalone release of the build, for
	// example "20261110".
	Build string `json:"build"`
}

// PythonStatus lists the installed lines and the last installation, in the
// states of PHP installs (PHPInstallIdle and so on).
type PythonStatus struct {
	Installed []PythonInstalled `json:"installed"`
	// Last is the line of the running or last installation; "" for none.
	Last  string   `json:"last"`
	State string   `json:"state"`
	Log   []string `json:"log"`
}
