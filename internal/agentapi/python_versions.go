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
// that has not ended. Checked on 2026-11-22 against
// https://peps.python.org/api/release-cycle.json and the SHA256SUMS of
// python-build-standalone release 20261110; the build provenance of each
// new archive was verified with Sigstore.
var PythonReleases = []PythonRelease{
	{
		Line: "3.11", Version: "3.11.18", Tag: "20261110", End: "2027-10-31",
		SHA256: map[string]string{
			"x86_64":  "3288c54cea3db350052019b3f8b558dd8a3da8afa6fda573f5fd2bcb37635b4a",
			"aarch64": "7e71f26a003949b57757e287f4f4b6689da07e7a229f5bbee7919c19ed77155d",
		},
	},
	{
		Line: "3.12", Version: "3.12.16", Tag: "20261110", End: "2028-10-31",
		SHA256: map[string]string{
			"x86_64":  "6a9e62dad033790aef722dbd370e0ce76ce17b73627ba3ae679f537049350b39",
			"aarch64": "b6a41defec4975d94b4ff1950236e7383e5c02ba40ea48c9ec33c66087a35aab",
		},
	},
	{
		Line: "3.13", Version: "3.13.18", Tag: "20261110", End: "2029-10-31",
		SHA256: map[string]string{
			"x86_64":  "f2d2199eb25880e07863b391cf7124e7ffa1338b0d65600dfcec117c4223a7fb",
			"aarch64": "55c00325f7c1fddf325dfc621aabdfbbac4dc54adda224af88ec411dc12db239",
		},
	},
	{
		Line: "3.14", Version: "3.14.10", Tag: "20261110", End: "2030-10-31",
		SHA256: map[string]string{
			"x86_64":  "c2ff2e97422f5d3896a16e0317fd212ba977bbf2688d93a6f7a03d817506bd30",
			"aarch64": "38fd50f07170adc29d481913d1c30b17ecb538fbe290315fe1f83d84fdd5b694",
		},
	},
	{
		Line: "3.15", Version: "3.15.2", Tag: "20261110", End: "2031-10-31",
		SHA256: map[string]string{
			"x86_64":  "ad23d1b73fac26440b0306ae06911031344c49a90ec19716c8362e7fa40cae43",
			"aarch64": "d1367a6c3cd86a9e35712072841f1376859f3b47a2a54e224421a0f9fd169047",
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
