// SPDX-License-Identifier: AGPL-3.0-or-later

package agentapi

import (
	"errors"
	"slices"

	"github.com/maki-it/hostashi/internal/agentrpc"
)

// NodeRelease is the pinned Node.js release of one LTS line (ADR-0054).
// Hostashi installs the official build from nodejs.org and checks it
// against SHA256 before it unpacks anything. The checksums come from the
// release's SHASUMS256.txt, whose signature by a Node.js release key was
// checked when the list was updated.
type NodeRelease struct {
	Major    string // the line, for example "24"
	Version  string // the exact release, for example "24.22.3"
	Codename string
	// End is the line's end of life (YYYY-MM-DD) from the Node.js release
	// schedule.
	End string
	// SHA256 of node-v<Version>-linux-<arch>.tar.gz by arch (x64, arm64).
	SHA256 map[string]string
}

// NodeReleases are the LTS lines Hostashi installs, oldest first. Checked
// on 2026-11-22 against https://nodejs.org/dist/index.json and the signed
// SHASUMS256.txt of each release.
var NodeReleases = []NodeRelease{
	{
		Major: "22", Version: "22.23.5", Codename: "Jod", End: "2027-04-30",
		SHA256: map[string]string{
			"x64":   "c54118104acc524633b5abe106bbf44a53126ec8b4e5fa105cb92b5d7de8a1a1",
			"arm64": "8bd3dc69eee8f3f226315a80d7d1d4faccf217a4c8fae500168ac2e38f3bdba3",
		},
	},
	{
		Major: "24", Version: "24.22.3", Codename: "Krypton", End: "2028-04-30",
		SHA256: map[string]string{
			"x64":   "061ec3e3062115705b56b51fa9cc330ab50d34b6969387d4e4b1a50c6809acc2",
			"arm64": "261af2af250dfd7e9f25cf151d1b2713a0adf8f10a9ad116c4947f68627affe8",
		},
	},
	{
		Major: "26", Version: "26.13.1", Codename: "Lithium", End: "2029-04-30",
		SHA256: map[string]string{
			"x64":   "518674de2b621728a5e4486f5fb3b7d2900bb3cc4aa97969edaa463db99f9778",
			"arm64": "80a330b35415382878df18d41afbd106ea89bfb3be0087d6608e289a14c5cc7d",
		},
	},
}

// ErrNodeMajor: not one of the lines in NodeReleases.
var ErrNodeMajor = errors.New("not a Node.js line Hostashi installs")

// NodeReleaseOf returns the release of a line.
func NodeReleaseOf(major string) (NodeRelease, error) {
	i := slices.IndexFunc(NodeReleases, func(r NodeRelease) bool { return r.Major == major })
	if i < 0 {
		return NodeRelease{}, ErrNodeMajor
	}

	return NodeReleases[i], nil
}

// ValidateNodeMajor accepts the lines in NodeReleases only, so the major
// can name a unit instance and a directory.
func ValidateNodeMajor(major string) error {
	_, err := NodeReleaseOf(major)

	return err
}

// Node.js installation (ADR-0054): node.install starts the unit
// hostashi-node-install@<major>, node.status reports the lines and the last
// installation.
var (
	NodeInstall  = agentrpc.Op[NodeInstallRequest, NodeStatus]{Name: "node.install"}
	NodeStatusOp = agentrpc.Op[NodeStatusRequest, NodeStatus]{Name: "node.status"}
	// NodeRemove deletes an installed line that no app runs on.
	NodeRemove = agentrpc.Op[NodeRemoveRequest, NodeStatus]{Name: "node.remove"}
)

// NodeDoneMarker starts the installer's last line after a successful run.
const NodeDoneMarker = "node-install: done:"

// NodeInstallRequest installs or updates one line.
type NodeInstallRequest struct {
	Major string `json:"major"`
}

// Validate implements agentrpc's request check.
func (r NodeInstallRequest) Validate() error { return ValidateNodeMajor(r.Major) }

// NodeRemoveRequest removes one installed line.
type NodeRemoveRequest struct {
	Major string `json:"major"`
}

// Validate implements agentrpc's request check.
func (r NodeRemoveRequest) Validate() error { return ValidateNodeMajor(r.Major) }

// NodeStatusRequest asks for the installed lines and the last installation.
type NodeStatusRequest struct{}

// Validate implements agentrpc's request check.
func (NodeStatusRequest) Validate() error { return nil }

// NodeInstalled is one installed line and its release.
type NodeInstalled struct {
	Major   string `json:"major"`
	Version string `json:"version"`
}

// NodeStatus lists the installed lines and the last installation, in the
// states of PHP installs (PHPInstallIdle and so on).
type NodeStatus struct {
	Installed []NodeInstalled `json:"installed"`
	// Last is the line of the running or last installation; "" for none.
	Last  string   `json:"last"`
	State string   `json:"state"`
	Log   []string `json:"log"`
}
