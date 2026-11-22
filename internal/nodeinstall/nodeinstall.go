// SPDX-License-Identifier: AGPL-3.0-or-later

// Package nodeinstall installs the official Node.js builds that Hostashi
// pins (ADR-0054) and follows those installations for the panel.
//
// Install runs inside the fixed unit hostashi-node-install@<major>.service
// (`hostashi node-install <major>`): it downloads the pinned .tar.gz from
// nodejs.org, checks its SHA-256 against agentapi.NodeReleases before it
// reads a byte of the archive, unpacks it below Dir/.new-<major> and swaps
// that directory into Dir/<major>. No apt, no shell, nothing is run.
//
// Manager serves node.install and node.status in the agent: it starts the
// unit and reads the log the unit writes, like internal/phpinstall.
package nodeinstall

import (
	"archive/tar"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"

	"golang.org/x/sys/unix"

	"github.com/maki-it/hostashi/internal/agentapi"
	"github.com/maki-it/hostashi/internal/phpinstall"
)

// Paths on the server.
const (
	// Dir holds one directory per line, Dir/<major>, root-owned and
	// readable by everyone, so the site users can run node.
	Dir = "/opt/hostashi/node"
	// LogDir holds one log per line; the unit writes <LogDir>/<major>.log.
	LogDir = "/var/lib/hostashi-agent/node-install"
	// VersionFile, in a line's directory, names the installed release.
	VersionFile = ".hostashi-version"
	// DistURL is where nodejs.org publishes its builds.
	DistURL = "https://nodejs.org/dist"
)

// Bounds of one archive [E: the 24.x linux-x64 .tar.gz is about 55 MB and
// unpacks to about 200 MB in some 7,000 entries].
const (
	maxArchive = 256 << 20
	maxUnpack  = 1 << 30
	maxEntries = 50_000
)

var (
	// ErrBusy: an installation is already running.
	ErrBusy = errors.New("nodeinstall: an installation is already running")
	// ErrInstalled: the pinned release of the line is installed already.
	ErrInstalled = errors.New("nodeinstall: this Node.js release is already installed")
	// ErrChecksum: the download is not the pinned build.
	ErrChecksum = errors.New("nodeinstall: SHA-256 of the download does not match")
	// ErrArch: nodejs.org has no build Hostashi pins for this machine.
	ErrArch = errors.New("nodeinstall: no pinned Node.js build for this architecture")
)

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

func (in *Installer) dir() string {
	if in.Root == "" {
		return Dir
	}

	return filepath.Join(in.Root, Dir)
}

func (in *Installer) logf(format string, a ...any) {
	if in.Out != nil {
		fmt.Fprintf(in.Out, "node-install: "+format+"\n", a...)
	}
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

	dir := in.dir()
	if v, _ := ReadVersion(filepath.Join(dir, major)); v == rel.Version {
		in.logf("Node.js %s is already installed", rel.Version)
		in.logf("done: %s", rel.Version)

		return nil
	}

	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("nodeinstall: %w", err)
	}

	name := "node-v" + rel.Version + "-linux-" + arch

	base := in.URL
	if base == "" {
		base = DistURL
	}

	in.logf("downloading %s/v%s/%s.tar.gz", base, rel.Version, name)

	tmp, err := in.download(ctx, base+"/v"+rel.Version+"/"+name+".tar.gz", dir, sum)
	if err != nil {
		return err
	}

	defer os.Remove(tmp.Name())
	defer tmp.Close()

	in.logf("SHA-256 matches %s", sum)

	stage := filepath.Join(dir, ".new-"+major)
	if err := os.RemoveAll(stage); err != nil {
		return fmt.Errorf("nodeinstall: %w", err)
	}

	defer os.RemoveAll(stage)

	if err := os.Mkdir(stage, 0o755); err != nil {
		return fmt.Errorf("nodeinstall: %w", err)
	}

	if _, err := tmp.Seek(0, io.SeekStart); err != nil {
		return fmt.Errorf("nodeinstall: %w", err)
	}

	n, err := Unpack(tmp, stage, name)
	if err != nil {
		return err
	}

	if _, err := os.Stat(filepath.Join(stage, "bin", "node")); err != nil {
		return fmt.Errorf("nodeinstall: the archive has no bin/node: %w", err)
	}

	//nolint:gosec // the site users read it, like the rest of the tree
	if err := os.WriteFile(filepath.Join(stage, VersionFile), []byte(rel.Version+"\n"), 0o644); err != nil {
		return fmt.Errorf("nodeinstall: %w", err)
	}

	in.logf("unpacked %d entries", n)

	if err := swap(stage, filepath.Join(dir, major)); err != nil {
		return err
	}

	in.logf("installed in %s", filepath.Join(Dir, major))
	in.logf("done: %s", rel.Version)

	return nil
}

// download fetches url into a temporary file in dir and checks its
// SHA-256 against sum.
func (in *Installer) download(ctx context.Context, url, dir, sum string) (*os.File, error) {
	client := in.Client
	if client == nil {
		client = &http.Client{Timeout: 10 * time.Minute}
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, fmt.Errorf("nodeinstall: %w", err)
	}

	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("nodeinstall: download: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("nodeinstall: download: %s", resp.Status)
	}

	f, err := os.CreateTemp(dir, ".download-*")
	if err != nil {
		return nil, fmt.Errorf("nodeinstall: %w", err)
	}

	h := sha256.New()

	n, err := io.Copy(io.MultiWriter(f, h), io.LimitReader(resp.Body, maxArchive+1))
	if err == nil && n > maxArchive {
		err = fmt.Errorf("nodeinstall: the download is larger than %d bytes", maxArchive)
	}

	if err == nil && hex.EncodeToString(h.Sum(nil)) != sum {
		err = ErrChecksum
	}

	if err != nil {
		_ = f.Close()
		_ = os.Remove(f.Name())

		return nil, fmt.Errorf("nodeinstall: download: %w", err)
	}

	return f, nil
}

// Unpack extracts a Node.js .tar.gz into dir, which must exist and be
// empty. Every entry must lie below top/, which is cut off. It takes only
// directories, regular files and symbolic links that stay below dir; modes
// become 0755 for directories and executables and 0644 for other files,
// so no setuid bit and nothing writable for others survives. It returns
// the number of entries.
func Unpack(r io.Reader, dir, top string) (int, error) {
	zr, err := gzip.NewReader(r)
	if err != nil {
		return 0, fmt.Errorf("nodeinstall: %w", err)
	}
	defer zr.Close()

	root, err := os.OpenRoot(dir)
	if err != nil {
		return 0, fmt.Errorf("nodeinstall: %w", err)
	}
	defer root.Close()

	tr := tar.NewReader(zr)

	var total int64

	n := 0

	for {
		h, err := tr.Next()
		if errors.Is(err, io.EOF) {
			break
		}

		if err != nil {
			return n, fmt.Errorf("nodeinstall: archive: %w", err)
		}

		if n++; n > maxEntries {
			return n, fmt.Errorf("nodeinstall: archive has more than %d entries", maxEntries)
		}

		name, err := entryName(h.Name, top)
		if err != nil {
			return n, err
		}

		if name == "" {
			continue // top/ itself
		}

		switch h.Typeflag {
		case tar.TypeDir:
			if err := root.MkdirAll(name, 0o755); err != nil {
				return n, fmt.Errorf("nodeinstall: %w", err)
			}
		case tar.TypeReg:
			if total += h.Size; h.Size < 0 || total > maxUnpack {
				return n, fmt.Errorf("nodeinstall: archive unpacks to more than %d bytes", maxUnpack)
			}

			if err := writeFile(root, name, h, tr); err != nil {
				return n, err
			}
		case tar.TypeSymlink:
			if err := linkInside(name, h.Linkname); err != nil {
				return n, err
			}

			if err := mkParent(root, name); err != nil {
				return n, err
			}

			if err := root.Symlink(h.Linkname, name); err != nil {
				return n, fmt.Errorf("nodeinstall: %w", err)
			}
		case tar.TypeXGlobalHeader:
			continue
		default:
			return n, fmt.Errorf("nodeinstall: archive entry %q has type %q", h.Name, h.Typeflag)
		}
	}

	return n, nil
}

// entryName returns an entry's path below top, cleaned; "" for top
// itself.
func entryName(name, top string) (string, error) {
	clean := path.Clean(strings.TrimSuffix(name, "/"))
	if clean == top {
		return "", nil
	}

	rest, ok := strings.CutPrefix(clean, top+"/")
	if !ok || !fs.ValidPath(rest) {
		return "", fmt.Errorf("nodeinstall: archive entry %q is not below %s/", name, top)
	}

	return rest, nil
}

// linkInside refuses an absolute link or one that leaves the tree.
func linkInside(name, target string) error {
	if target == "" || path.IsAbs(target) {
		return fmt.Errorf("nodeinstall: link %q points to %q", name, target)
	}

	if !fs.ValidPath(path.Join(path.Dir(name), target)) {
		return fmt.Errorf("nodeinstall: link %q points outside the archive", name)
	}

	// ".." only in front: after another link, ".." would leave from where
	// that link points, which the lexical check above cannot see.
	seen := false

	for p := range strings.SplitSeq(target, "/") {
		switch {
		case p == "..":
			if seen {
				return fmt.Errorf("nodeinstall: link %q points to %q", name, target)
			}
		case p != "" && p != ".":
			seen = true
		}
	}

	return nil
}

func mkParent(root *os.Root, name string) error {
	if d := path.Dir(name); d != "." {
		if err := root.MkdirAll(d, 0o755); err != nil {
			return fmt.Errorf("nodeinstall: %w", err)
		}
	}

	return nil
}

func writeFile(root *os.Root, name string, h *tar.Header, r io.Reader) error {
	if err := mkParent(root, name); err != nil {
		return err
	}

	mode := os.FileMode(0o644)
	if h.Mode&0o111 != 0 {
		mode = 0o755
	}

	f, err := root.OpenFile(name, os.O_WRONLY|os.O_CREATE|os.O_EXCL, mode)
	if err != nil {
		return fmt.Errorf("nodeinstall: %w", err)
	}

	if _, err := io.CopyN(f, r, h.Size); err != nil {
		_ = f.Close()

		return fmt.Errorf("nodeinstall: %s: %w", name, err)
	}

	if err := f.Chmod(mode); err != nil { // the umask may have cut bits
		_ = f.Close()

		return fmt.Errorf("nodeinstall: %w", err)
	}

	if err := f.Close(); err != nil {
		return fmt.Errorf("nodeinstall: %w", err)
	}

	return nil
}

// swap puts stage at dst. An existing dst is exchanged with stage in one
// step (renameat2 RENAME_EXCHANGE), so a running app never sees a half
// tree; the old tree is then at stage and is removed by the caller.
func swap(stage, dst string) error {
	if err := os.Chmod(stage, 0o755); err != nil {
		return fmt.Errorf("nodeinstall: %w", err)
	}

	_, err := os.Lstat(dst)
	switch {
	case errors.Is(err, os.ErrNotExist):
		err = os.Rename(stage, dst)
	case err == nil:
		err = unix.Renameat2(unix.AT_FDCWD, stage, unix.AT_FDCWD, dst, unix.RENAME_EXCHANGE)
	}

	if err != nil {
		return fmt.Errorf("nodeinstall: install %s: %w", dst, err)
	}

	return nil
}

// ReadVersion returns the release installed in a line's directory, "" if
// none.
func ReadVersion(lineDir string) (string, error) {
	b, err := os.ReadFile(filepath.Join(lineDir, VersionFile))
	if err != nil {
		return "", err
	}

	return strings.TrimSpace(string(b)), nil
}

// Units starts and reads the install units.
type Units interface {
	Start(ctx context.Context, major string) error
	Active(ctx context.Context, major string) (bool, error)
}

// Manager serves node.install and node.status.
type Manager struct {
	Units Units
	Root  string // prefixed to every path; empty means /

	mu sync.Mutex
}

func (m *Manager) path(p string) string {
	if m.Root == "" {
		return p
	}

	return filepath.Join(m.Root, p)
}

// Installed lists the lines that have a release installed, oldest line
// first.
func (m *Manager) Installed() []agentapi.NodeInstalled {
	out := []agentapi.NodeInstalled{}

	for _, r := range agentapi.NodeReleases {
		d := m.path(filepath.Join(Dir, r.Major))
		if _, err := os.Stat(filepath.Join(d, "bin", "node")); err != nil {
			continue
		}

		if v, err := ReadVersion(d); err == nil && v != "" {
			out = append(out, agentapi.NodeInstalled{Major: r.Major, Version: v})
		}
	}

	return out
}

// Start starts the installation of a line. Only one runs at a time; a
// line whose pinned release is installed is refused.
func (m *Manager) Start(ctx context.Context, major string) (agentapi.NodeStatus, error) {
	rel, err := agentapi.NodeReleaseOf(major)
	if err != nil {
		return agentapi.NodeStatus{}, err
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	for _, i := range m.Installed() {
		if i.Major == major && i.Version == rel.Version {
			return agentapi.NodeStatus{}, ErrInstalled
		}
	}

	if busy, err := m.running(ctx); err != nil {
		return agentapi.NodeStatus{}, err
	} else if busy != "" {
		return agentapi.NodeStatus{}, ErrBusy
	}

	if err := m.Units.Start(ctx, major); err != nil {
		return agentapi.NodeStatus{}, err
	}

	return agentapi.NodeStatus{Installed: m.Installed(), Last: major, State: agentapi.PHPInstallRunning, Log: []string{}}, nil
}

func (m *Manager) running(ctx context.Context) (string, error) {
	for _, r := range agentapi.NodeReleases {
		active, err := m.Units.Active(ctx, r.Major)
		if err != nil {
			return "", err
		}

		if active {
			return r.Major, nil
		}
	}

	return "", nil
}

func (m *Manager) logPath(major string) string {
	return m.path(filepath.Join(LogDir, major+".log"))
}

// Status reports the installed lines and the last installation: a running
// unit, else the line whose log is the newest. The lines are read last: the
// unit swaps a line in before it writes its final line and ends, so a
// finished installation is always listed (read first, a unit that ends in
// between showed "succeeded" without its line).
func (m *Manager) Status(ctx context.Context) (agentapi.NodeStatus, error) {
	st, err := m.lastRun(ctx)
	if err != nil {
		return agentapi.NodeStatus{}, err
	}

	st.Installed = m.Installed()

	return st, nil
}

func (m *Manager) lastRun(ctx context.Context) (agentapi.NodeStatus, error) {
	st := agentapi.NodeStatus{State: agentapi.PHPInstallIdle, Log: []string{}}

	busy, err := m.running(ctx)
	if err != nil {
		return agentapi.NodeStatus{}, err
	}

	if busy != "" {
		lines, _ := phpinstall.TailLog(m.logPath(busy))
		st.Last, st.State, st.Log = busy, agentapi.PHPInstallRunning, lines

		return st, nil
	}

	var newest int64

	for _, r := range agentapi.NodeReleases {
		fi, err := os.Lstat(m.logPath(r.Major))
		if err != nil || !fi.Mode().IsRegular() {
			continue
		}

		if t := fi.ModTime().UnixNano(); st.Last == "" || t > newest {
			st.Last, newest = r.Major, t
		}
	}

	if st.Last == "" {
		return st, nil
	}

	lines, err := phpinstall.TailLog(m.logPath(st.Last))
	if err != nil {
		return agentapi.NodeStatus{}, err
	}

	st.State, st.Log = agentapi.PHPInstallFailed, lines
	if n := len(lines); n > 0 && strings.HasPrefix(lines[n-1], agentapi.NodeDoneMarker) {
		st.State = agentapi.PHPInstallSucceeded
	}

	return st, nil
}
