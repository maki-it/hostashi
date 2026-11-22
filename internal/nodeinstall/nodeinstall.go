// SPDX-License-Identifier: AGPL-3.0-or-later

// Package nodeinstall installs the official Node.js builds that Hostashi
// pins (ADR-0054) and follows those installations for the panel.
//
// Install runs inside the fixed unit hostashi-node-install@<major>.service
// (`hostashi node-install <major>`): it downloads the pinned .tar.gz from
// nodejs.org, checks its SHA-256 against agentapi.NodeReleases before it
// reads a byte of the archive, unpacks it below Dir/.new-<major> and swaps
// that directory into Dir/<major>. No apt, no shell, nothing is run. The
// work is done by internal/rtinstall, which Python shares (ADR-0061).
//
// Manager serves node.install and node.status in the agent: it starts the
// unit and reads the log the unit writes, like internal/phpinstall.
package nodeinstall

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"runtime"

	"github.com/maki-it/hostashi/internal/agentapi"
	"github.com/maki-it/hostashi/internal/rtinstall"
)

// Paths on the server.
const (
	// Dir holds one directory per line, Dir/<major>, root-owned and
	// readable by everyone, so the site users can run node.
	Dir = "/opt/hostashi/node"
	// LogDir holds one log per line; the unit writes <LogDir>/<major>.log.
	LogDir = "/var/lib/hostashi-agent/node-install"
	// VersionFile, in a line's directory, names the installed release.
	VersionFile = rtinstall.VersionFile
	// DistURL is where nodejs.org publishes its builds.
	DistURL = "https://nodejs.org/dist"
)

var (
	// ErrBusy: an installation is already running.
	ErrBusy = rtinstall.ErrBusy
	// ErrInstalled: the pinned release of the line is installed already.
	ErrInstalled = rtinstall.ErrInstalled
	// ErrChecksum: the download is not the pinned build.
	ErrChecksum = rtinstall.ErrChecksum
	// ErrNotInstalled: the line is not installed.
	ErrNotInstalled = rtinstall.ErrNotInstalled
	// ErrInUse: an app still runs on the line.
	ErrInUse = rtinstall.ErrInUse
	// ErrArch: nodejs.org has no build Hostashi pins for this machine.
	ErrArch = rtinstall.ErrArch
)

// Runtime describes the Node.js lines for internal/rtinstall.
var Runtime = &rtinstall.Runtime{
	Name: "node", Dir: Dir, LogDir: LogDir, Bin: "bin/node",
	Releases: func() []rtinstall.Release { return releases(agentapi.NodeReleases) },
	ErrLine:  agentapi.ErrNodeMajor,
}

func releases(rs []agentapi.NodeRelease) []rtinstall.Release {
	out := make([]rtinstall.Release, 0, len(rs))
	for _, r := range rs {
		out = append(out, rtinstall.Release{Line: r.Major, Version: r.Version})
	}

	return out
}

// Manager serves node.install, node.status and node.remove. Set Runtime
// to Runtime (NewManager does).
type Manager = rtinstall.Manager

// Units starts and reads the install units.
type Units = rtinstall.Units

// NewManager returns the manager of the Node.js lines.
func NewManager(u Units) *Manager { return &Manager{Runtime: Runtime, Units: u} }

// Status converts a status of the shared core to the agent's type.
func Status(s rtinstall.Status) agentapi.NodeStatus {
	return agentapi.NodeStatus{Installed: Installed(s.Installed), Last: s.Last, State: s.State, Log: s.Log}
}

// Installed converts installed lines to the agent's type.
func Installed(in []rtinstall.Installed) []agentapi.NodeInstalled {
	out := make([]agentapi.NodeInstalled, 0, len(in))
	for _, i := range in {
		out = append(out, agentapi.NodeInstalled{Major: i.Line, Version: i.Version})
	}

	return out
}

// Unpack extracts a Node.js .tar.gz, see rtinstall.Unpack.
func Unpack(r io.Reader, dir, top string) (int, error) { return rtinstall.Unpack(r, dir, top) }

// ReadVersion returns the release installed in a line's directory.
func ReadVersion(lineDir string) (string, error) { return rtinstall.ReadVersion(lineDir) }

// Arch is the nodejs.org name of a Go architecture: x64 or arm64.
func Arch(goarch string) (string, error) {
	switch goarch {
	case "amd64":
		return "x64", nil
	case "arm64":
		return "arm64", nil
	}

	return "", fmt.Errorf("%w (%s)", ErrArch, goarch)
}

// Installer downloads and unpacks one line.
type Installer struct {
	Root   string // prefixed to Dir; empty means /
	Client *http.Client
	URL    string // DistURL unless set (tests)
	GOARCH string // runtime.GOARCH unless set (tests)
	Out    io.Writer

	releases []agentapi.NodeRelease // agentapi.NodeReleases unless set (tests)
}

func (in *Installer) releaseOf(major string) (agentapi.NodeRelease, error) {
	for _, r := range in.releases {
		if r.Major == major {
			return r, nil
		}
	}

	return agentapi.NodeReleaseOf(major)
}

// Install installs the pinned release of major, unless it is there
// already. Its last line on success starts with agentapi.NodeDoneMarker.
func (in *Installer) Install(ctx context.Context, major string) error {
	rel, err := in.releaseOf(major)
	if err != nil {
		return err
	}

	goarch := in.GOARCH
	if goarch == "" {
		goarch = runtime.GOARCH
	}

	arch, err := Arch(goarch)
	if err != nil {
		return err
	}

	sum, ok := rel.SHA256[arch]
	if !ok {
		return fmt.Errorf("%w (%s)", ErrArch, arch)
	}

	base := in.URL
	if base == "" {
		base = DistURL
	}

	name := "node-v" + rel.Version + "-linux-" + arch
	ri := rtinstall.Installer{Runtime: Runtime, Root: in.Root, Client: in.Client, Out: in.Out}

	return ri.Install(ctx, rtinstall.Release{Line: rel.Major, Version: rel.Version}, rtinstall.Archive{
		URL: base + "/v" + rel.Version + "/" + name + ".tar.gz", SHA256: sum, Top: name,
	})
}
