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
// that has not ended. Checked on 2026-11-25 against
// https://peps.python.org/api/release-cycle.json and the SHA256SUMS of
// python-build-standalone release 20261124; the build provenance of each
// new archive was verified with Sigstore.
var PythonReleases = []PythonRelease{
	{
		Line: "3.11", Version: "3.11.18", Tag: "20261124", End: "2027-10-31",
		SHA256: map[string]string{
			"x86_64":  "d873f435e58d396a18858d4f52a335f1abe8cea385cb9d411ec511f958249d54",
			"aarch64": "564634e82b57d0b7bb98172594614a693a716bf2c5b33a66a2716c19ffb0233e",
		},
	},
	{
		Line: "3.12", Version: "3.12.16", Tag: "20261124", End: "2028-10-31",
		SHA256: map[string]string{
			"x86_64":  "7e1adaea26c959d13aa1120be1ae62a1ea1bb52adcb382679fffe9217e22adff",
			"aarch64": "518e24b37e356775d82d5609cd9d48e27e211381a39ae416381ba696bf76c25c",
		},
	},
	{
		Line: "3.13", Version: "3.13.18", Tag: "20261124", End: "2029-10-31",
		SHA256: map[string]string{
			"x86_64":  "b56ceb4fc1d8f0c49b7bd4dc91cd176bad7b86a88b9fc509bae6b10b3ee36692",
			"aarch64": "300d388cef58ff23f101e1a41512dc51db9adae136b138c61af9ae1b1d9703a7",
		},
	},
	{
		Line: "3.14", Version: "3.14.10", Tag: "20261124", End: "2030-10-31",
		SHA256: map[string]string{
			"x86_64":  "73ee0629c19e9f87077f318cd50d7e703137c274479ce8e8fd5723e75c86c9bc",
			"aarch64": "2f5df923b202f9054b665c4b4c6ee3fc08bf1ec46cc640bb68ec52aecc658d30",
		},
	},
	{
		Line: "3.15", Version: "3.15.2", Tag: "20261124", End: "2031-10-31",
		SHA256: map[string]string{
			"x86_64":  "d94bcba1ca1c63f591fa63109c3eeaacc76ee6796844ac7bdf2ae10715646f05",
			"aarch64": "dac9832fe240524c092015b99ed76fcef1a32a096d744fcb39eafa980d1a0ffa",
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
