// SPDX-License-Identifier: AGPL-3.0-or-later

// Package pythoninstall installs the CPython builds of
// python-build-standalone that Hostashi pins (ADR-0061) and follows those
// installations for the panel.
//
// Install runs inside the fixed unit hostashi-python-install@<line>.service
// (`hostashi python-install <line>`): it downloads the pinned
// install_only_stripped .tar.gz from the project's GitHub release, checks
// its SHA-256 against agentapi.PythonReleases before it reads a byte of
// the archive, unpacks it and swaps it into Dir/<line>, all through
// internal/rtinstall, which Node.js shares. No apt, no shell, nothing is
// run.
package pythoninstall

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"runtime"
	"strings"

	"github.com/maki-it/hostashi/internal/agentapi"
	"github.com/maki-it/hostashi/internal/rtinstall"
)

// Paths on the server.
const (
	// Dir holds one directory per line, Dir/<line>, root-owned and
	// readable by everyone, so the site users can run python.
	Dir = "/opt/hostashi/python"
	// LogDir holds one log per line; the unit writes <LogDir>/<line>.log.
	LogDir = "/var/lib/hostashi-agent/python-install"
	// DistURL is where python-build-standalone publishes its builds.
	DistURL = "https://github.com/astral-sh/python-build-standalone/releases/download"
	// top is the directory every install_only archive unpacks into.
	top = "python"
)

// Runtime describes the Python lines for internal/rtinstall.
var Runtime = &rtinstall.Runtime{
	Name: "python", Dir: Dir, LogDir: LogDir, Bin: "bin/python3",
	Releases: func() []rtinstall.Release { return releases(agentapi.PythonReleases) },
	ErrLine:  agentapi.ErrPythonLine,
}

func releases(rs []agentapi.PythonRelease) []rtinstall.Release {
	out := make([]rtinstall.Release, 0, len(rs))
	for _, r := range rs {
		out = append(out, release(r))
	}

	return out
}

// release names a build by version and python-build-standalone tag, so a
// new build of the same CPython version (for example with a newer OpenSSL)
// counts as outdated and is installed again.
func release(r agentapi.PythonRelease) rtinstall.Release {
	return rtinstall.Release{Line: r.Line, Version: r.Version + "+" + r.Tag}
}

// Manager serves python.install, python.status and python.remove.
type Manager = rtinstall.Manager

// NewManager returns the manager of the Python lines.
func NewManager(u rtinstall.Units) *Manager { return &Manager{Runtime: Runtime, Units: u} }

// Status converts a status of the shared core to the agent's type.
func Status(s rtinstall.Status) agentapi.PythonStatus {
	out := agentapi.PythonStatus{Installed: make([]agentapi.PythonInstalled, 0, len(s.Installed)), Last: s.Last, State: s.State, Log: s.Log}
	for _, i := range s.Installed {
		v, build, _ := strings.Cut(i.Version, "+")
		out.Installed = append(out.Installed, agentapi.PythonInstalled{Line: i.Line, Version: v, Build: build})
	}

	if out.Log == nil {
		out.Log = []string{}
	}

	return out
}

// Arch is the python-build-standalone name of a Go architecture.
func Arch(goarch string) (string, error) {
	switch goarch {
	case "amd64":
		return "x86_64", nil
	case "arm64":
		return "aarch64", nil
	}

	return "", fmt.Errorf("%w (%s)", rtinstall.ErrArch, goarch)
}

// Archive returns the download of a release for an architecture. The
// "+" of the file name is escaped, as GitHub's URLs have it.
func Archive(base string, rel agentapi.PythonRelease, arch string) (rtinstall.Archive, error) {
	sum, ok := rel.SHA256[arch]
	if !ok {
		return rtinstall.Archive{}, fmt.Errorf("%w (%s)", rtinstall.ErrArch, arch)
	}

	name := "cpython-" + rel.Version + "%2B" + rel.Tag + "-" + arch + "-unknown-linux-gnu-install_only_stripped.tar.gz"

	return rtinstall.Archive{URL: base + "/" + rel.Tag + "/" + name, SHA256: sum, Top: top}, nil
}

// Installer downloads and unpacks one line.
type Installer struct {
	Root   string // prefixed to Dir; empty means /
	Client *http.Client
	URL    string // DistURL unless set (tests)
	GOARCH string // runtime.GOARCH unless set (tests)
	Out    io.Writer

	releases []agentapi.PythonRelease // agentapi.PythonReleases unless set (tests)
}

func (in *Installer) releaseOf(line string) (agentapi.PythonRelease, error) {
	for _, r := range in.releases {
		if r.Line == line {
			return r, nil
		}
	}

	return agentapi.PythonReleaseOf(line)
}

// Install installs the pinned release of line, unless it is there
// already. Its last line on success starts with agentapi.PythonDoneMarker.
func (in *Installer) Install(ctx context.Context, line string) error {
	rel, err := in.releaseOf(line)
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

	base := in.URL
	if base == "" {
		base = DistURL
	}

	a, err := Archive(base, rel, arch)
	if err != nil {
		return err
	}

	ri := rtinstall.Installer{Runtime: Runtime, Root: in.Root, Client: in.Client, Out: in.Out}

	return ri.Install(ctx, release(rel), a)
}
