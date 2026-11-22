// SPDX-License-Identifier: AGPL-3.0-or-later

// Package rtinstall installs pinned language runtimes side by side, one
// directory per line, and follows those installations for the panel. It
// is the shared core of internal/nodeinstall (ADR-0054) and
// internal/pythoninstall (ADR-0061).
//
// Install runs inside a fixed install unit as root without capabilities:
// it downloads one archive, checks its SHA-256 before it reads a byte of
// it, unpacks it below <Dir>/.new-<line> and swaps that directory into
// <Dir>/<line>. No apt, no shell, nothing is run.
//
// Manager serves the install, status and remove operations in the agent:
// it starts the units and reads the logs they write.
package rtinstall

import (
	"archive/tar"
	"cmp"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"log/slog"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"time"

	"golang.org/x/sys/unix"

	"github.com/maki-it/hostashi/internal/agentapi"
	"github.com/maki-it/hostashi/internal/phpinstall"
)

// VersionFile, in a line's directory, names the installed release.
const VersionFile = ".hostashi-version"

// Bounds of one archive [E: the Node.js 24.x linux-x64 .tar.gz is about
// 55 MB and unpacks to about 200 MB in some 7,000 entries; CPython 3.13
// from python-build-standalone is 36 MB, 100 MB and 4,600 entries].
const (
	maxArchive = 256 << 20
	maxUnpack  = 1 << 30
	maxEntries = 50_000
)

var (
	// ErrBusy: an installation is already running.
	ErrBusy = errors.New("an installation is already running")
	// ErrInstalled: the pinned release of the line is installed already.
	ErrInstalled = errors.New("this release is already installed")
	// ErrChecksum: the download is not the pinned build.
	ErrChecksum = errors.New("SHA-256 of the download does not match")
	// ErrNotInstalled: the line is not installed.
	ErrNotInstalled = errors.New("this line is not installed")
	// ErrInUse: an app still runs on the line.
	ErrInUse = errors.New("apps still run on this line")
	// ErrArch: there is no pinned build for this machine.
	ErrArch = errors.New("no pinned build for this architecture")
)

// Release is the pinned release of one line.
type Release struct {
	Line    string // "24", "3.13"
	Version string // "24.22.3", "3.13.18"
}

// Runtime describes one runtime that is installed side by side.
type Runtime struct {
	// Name starts every log line ("<Name>-install: ") and error.
	Name string
	// Dir holds one directory per line, root-owned and readable by
	// everyone, so the site users can run it.
	Dir string
	// LogDir holds one log per line, <LogDir>/<line>.log, written by the
	// install unit.
	LogDir string
	// Bin, relative to a line's directory, must exist for the line to
	// count as installed.
	Bin string
	// Releases are the pinned lines, oldest first.
	Releases func() []Release
	// ErrLine is returned for a line that is not in Releases.
	ErrLine error
}

// DoneMarker starts the installer's last line after a successful run.
func (rt *Runtime) DoneMarker() string { return rt.Name + "-install: done:" }

// Release returns the pinned release of a line.
func (rt *Runtime) Release(line string) (Release, error) {
	rels := rt.Releases()

	i := slices.IndexFunc(rels, func(r Release) bool { return r.Line == line })
	if i < 0 {
		return Release{}, rt.ErrLine
	}

	return rels[i], nil
}

func (rt *Runtime) errorf(format string, a ...any) error {
	return fmt.Errorf(rt.Name+"install: "+format, a...)
}

// Archive is the build of one release for this machine.
type Archive struct {
	URL    string
	SHA256 string
	// Top is the archive's top directory, which is cut off.
	Top string
}

// Installer downloads and unpacks one line.
type Installer struct {
	Runtime *Runtime
	Root    string // prefixed to Dir; empty means /
	Client  *http.Client
	Out     io.Writer
}

func (in *Installer) dir() string {
	if in.Root == "" {
		return in.Runtime.Dir
	}

	return filepath.Join(in.Root, in.Runtime.Dir)
}

func (in *Installer) logf(format string, a ...any) {
	if in.Out != nil {
		fmt.Fprintf(in.Out, in.Runtime.Name+"-install: "+format+"\n", a...)
	}
}

// Install installs rel of a line from a, unless it is there already. Its
// last line on success starts with Runtime.DoneMarker.
func (in *Installer) Install(ctx context.Context, rel Release, a Archive) error {
	rt := in.Runtime
	dir := in.dir()

	if v, _ := ReadVersion(filepath.Join(dir, rel.Line)); v == rel.Version {
		in.logf("%s is already installed", rel.Version)
		in.logf("done: %s", rel.Version)

		return nil
	}

	if err := os.MkdirAll(dir, 0o755); err != nil {
		return rt.errorf("%w", err)
	}

	in.logf("downloading %s", a.URL)

	tmp, err := in.download(ctx, a.URL, dir, a.SHA256)
	if err != nil {
		return err
	}

	defer os.Remove(tmp.Name())
	defer tmp.Close()

	in.logf("SHA-256 matches %s", a.SHA256)

	stage := filepath.Join(dir, ".new-"+rel.Line)
	if err := os.RemoveAll(stage); err != nil {
		return rt.errorf("%w", err)
	}

	defer os.RemoveAll(stage)

	if err := os.Mkdir(stage, 0o755); err != nil {
		return rt.errorf("%w", err)
	}

	if _, err := tmp.Seek(0, io.SeekStart); err != nil {
		return rt.errorf("%w", err)
	}

	n, err := Unpack(tmp, stage, a.Top)
	if err != nil {
		return err
	}

	if _, err := os.Stat(filepath.Join(stage, rt.Bin)); err != nil {
		return rt.errorf("the archive has no %s: %w", rt.Bin, err)
	}

	//nolint:gosec // the site users read it, like the rest of the tree
	if err := os.WriteFile(filepath.Join(stage, VersionFile), []byte(rel.Version+"\n"), 0o644); err != nil {
		return rt.errorf("%w", err)
	}

	in.logf("unpacked %d entries", n)

	if err := swap(stage, filepath.Join(dir, rel.Line)); err != nil {
		return err
	}

	in.logf("installed in %s", filepath.Join(rt.Dir, rel.Line))
	in.logf("done: %s", rel.Version)

	return nil
}

// download fetches url into a temporary file in dir and checks its
// SHA-256 against sum.
func (in *Installer) download(ctx context.Context, url, dir, sum string) (*os.File, error) {
	rt := in.Runtime

	client := in.Client
	if client == nil {
		client = &http.Client{Timeout: 10 * time.Minute}
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, rt.errorf("%w", err)
	}

	resp, err := client.Do(req)
	if err != nil {
		return nil, rt.errorf("download: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, rt.errorf("download: %s", resp.Status)
	}

	f, err := os.CreateTemp(dir, ".download-*")
	if err != nil {
		return nil, rt.errorf("%w", err)
	}

	h := sha256.New()

	n, err := io.Copy(io.MultiWriter(f, h), io.LimitReader(resp.Body, maxArchive+1))
	if err == nil && n > maxArchive {
		err = fmt.Errorf("the download is larger than %d bytes", maxArchive)
	}

	if err == nil && hex.EncodeToString(h.Sum(nil)) != sum {
		err = ErrChecksum
	}

	if err != nil {
		_ = f.Close()
		_ = os.Remove(f.Name())

		return nil, rt.errorf("download: %w", err)
	}

	return f, nil
}

// Unpack extracts a .tar.gz into dir, which must exist and be empty. Every
// entry must lie below top/, which is cut off. It takes only directories,
// regular files and symbolic links that stay below dir; modes become 0755
// for directories and executables and 0644 for other files, so no setuid
// bit and nothing writable for others survives. It returns the number of
// entries.
func Unpack(r io.Reader, dir, top string) (int, error) {
	zr, err := gzip.NewReader(r)
	if err != nil {
		return 0, fmt.Errorf("rtinstall: %w", err)
	}
	defer zr.Close()

	root, err := os.OpenRoot(dir)
	if err != nil {
		return 0, fmt.Errorf("rtinstall: %w", err)
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
			return n, fmt.Errorf("rtinstall: archive: %w", err)
		}

		if n++; n > maxEntries {
			return n, fmt.Errorf("rtinstall: archive has more than %d entries", maxEntries)
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
				return n, fmt.Errorf("rtinstall: %w", err)
			}
		case tar.TypeReg:
			if total += h.Size; h.Size < 0 || total > maxUnpack {
				return n, fmt.Errorf("rtinstall: archive unpacks to more than %d bytes", maxUnpack)
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
				return n, fmt.Errorf("rtinstall: %w", err)
			}
		case tar.TypeXGlobalHeader:
			continue
		default:
			return n, fmt.Errorf("rtinstall: archive entry %q has type %q", h.Name, h.Typeflag)
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
		return "", fmt.Errorf("rtinstall: archive entry %q is not below %s/", name, top)
	}

	return rest, nil
}

// linkInside refuses an absolute link or one that leaves the tree.
func linkInside(name, target string) error {
	if target == "" || path.IsAbs(target) {
		return fmt.Errorf("rtinstall: link %q points to %q", name, target)
	}

	if !fs.ValidPath(path.Join(path.Dir(name), target)) {
		return fmt.Errorf("rtinstall: link %q points outside the archive", name)
	}

	// ".." only in front: after another link, ".." would leave from where
	// that link points, which the lexical check above cannot see.
	seen := false

	for p := range strings.SplitSeq(target, "/") {
		switch {
		case p == "..":
			if seen {
				return fmt.Errorf("rtinstall: link %q points to %q", name, target)
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
			return fmt.Errorf("rtinstall: %w", err)
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
		return fmt.Errorf("rtinstall: %w", err)
	}

	if _, err := io.CopyN(f, r, h.Size); err != nil {
		_ = f.Close()

		return fmt.Errorf("rtinstall: %s: %w", name, err)
	}

	if err := f.Chmod(mode); err != nil { // the umask may have cut bits
		_ = f.Close()

		return fmt.Errorf("rtinstall: %w", err)
	}

	if err := f.Close(); err != nil {
		return fmt.Errorf("rtinstall: %w", err)
	}

	return nil
}

// swap puts stage at dst. An existing dst is exchanged with stage in one
// step (renameat2 RENAME_EXCHANGE), so a running app never sees a half
// tree; the old tree is then at stage and is removed by the caller.
func swap(stage, dst string) error {
	if err := os.Chmod(stage, 0o755); err != nil {
		return fmt.Errorf("rtinstall: %w", err)
	}

	_, err := os.Lstat(dst)
	switch {
	case errors.Is(err, os.ErrNotExist):
		err = os.Rename(stage, dst)
	case err == nil:
		err = unix.Renameat2(unix.AT_FDCWD, stage, unix.AT_FDCWD, dst, unix.RENAME_EXCHANGE)
	}

	if err != nil {
		return fmt.Errorf("rtinstall: install %s: %w", dst, err)
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
	Start(ctx context.Context, line string) error
	Active(ctx context.Context, line string) (bool, error)
}

// Installed is one installed line and its release.
type Installed struct {
	Line    string
	Version string
}

// Status lists the installed lines and the last installation, in the
// states of PHP installs (agentapi.PHPInstallIdle and so on).
type Status struct {
	Installed []Installed
	// Last is the line of the running or last installation; "" for none.
	Last  string
	State string
	Log   []string
}

// Manager serves the install, status and remove operations of one
// runtime.
type Manager struct {
	Runtime *Runtime
	Units   Units
	Root    string // prefixed to every path; empty means /
	// Done runs after an installation of a line succeeded, from the panel
	// or from Update: the agent restarts the apps of the line on it.
	Done func(ctx context.Context, line string)
	// Poll is how often a running installation is checked; 2 s unless set.
	Poll time.Duration
	Log  *slog.Logger

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
func (m *Manager) Installed() []Installed {
	out := []Installed{}

	for _, r := range m.Runtime.Releases() {
		d := m.path(filepath.Join(m.Runtime.Dir, r.Line))
		if _, err := os.Stat(filepath.Join(d, m.Runtime.Bin)); err != nil {
			continue
		}

		if v, err := ReadVersion(d); err == nil && v != "" {
			out = append(out, Installed{Line: r.Line, Version: v})
		}
	}

	return out
}

// Start starts the installation of a line. Only one runs at a time; a
// line whose pinned release is installed is refused.
func (m *Manager) Start(ctx context.Context, line string) (Status, error) {
	rel, err := m.Runtime.Release(line)
	if err != nil {
		return Status{}, err
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	for _, i := range m.Installed() {
		if i.Line == line && i.Version == rel.Version {
			return Status{}, ErrInstalled
		}
	}

	if busy, err := m.running(ctx); err != nil {
		return Status{}, err
	} else if busy != "" {
		return Status{}, ErrBusy
	}

	if err := m.Units.Start(ctx, line); err != nil {
		return Status{}, err
	}

	// The panel does not wait for the unit; the agent follows it, so the
	// apps of the line restart on the new release.
	go m.follow(context.WithoutCancel(ctx), line)

	return Status{Installed: m.Installed(), Last: line, State: agentapi.PHPInstallRunning, Log: []string{}}, nil
}

// maxRun bounds how long follow waits: the units' TimeoutStartSec.
const maxRun = 15 * time.Minute

// follow waits until the unit of a line ends and runs Done when it
// installed the pinned release. It reports whether it did.
func (m *Manager) follow(ctx context.Context, line string) bool {
	ctx, cancel := context.WithTimeout(ctx, maxRun+time.Minute)
	defer cancel()

	poll := cmp.Or(m.Poll, 2*time.Second)
	name := m.Runtime.Name

	for {
		active, err := m.Units.Active(ctx, line)
		if err != nil {
			m.log().ErrorContext(ctx, name+" install state", "line", line, "err", err)

			return false
		}

		if !active {
			break
		}

		select {
		case <-ctx.Done():
			return false
		case <-time.After(poll):
		}
	}

	rel, err := m.Runtime.Release(line)
	if err != nil {
		return false
	}

	for _, i := range m.Installed() {
		if i.Line == line && i.Version == rel.Version {
			m.log().InfoContext(ctx, name+" line installed", "line", line, "version", rel.Version)

			if m.Done != nil {
				m.Done(ctx, line)
			}

			return true
		}
	}

	m.log().WarnContext(ctx, name+" install did not finish", "line", line)

	return false
}

// Outdated lists the installed lines whose release is not the pinned one,
// oldest line first.
func (m *Manager) Outdated() []string {
	var out []string

	for _, i := range m.Installed() {
		if rel, err := m.Runtime.Release(i.Line); err == nil && i.Version != rel.Version {
			out = append(out, i.Line)
		}
	}

	return out
}

// Update installs the pinned release of every outdated line, one after the
// other, as a Hostashi update brings new pins (updates without a click,
// Kim 2026-11-22). A line that fails is logged and left for the panel and
// doctor; the next start of the agent tries again.
func (m *Manager) Update(ctx context.Context) {
	name := m.Runtime.Name

	for _, line := range m.Outdated() {
		if ctx.Err() != nil {
			return
		}

		m.mu.Lock()

		busy, err := m.running(ctx)
		if err == nil && busy == "" {
			err = m.Units.Start(ctx, line)
		} else if err == nil {
			err = ErrBusy
		}

		m.mu.Unlock()

		if err != nil {
			m.log().WarnContext(ctx, name+" update not started", "line", line, "err", err)

			continue
		}

		m.log().InfoContext(ctx, name+" update started", "line", line)
		m.follow(ctx, line)
	}
}

func (m *Manager) log() *slog.Logger {
	if m.Log != nil {
		return m.Log
	}

	return slog.New(slog.DiscardHandler)
}

func (m *Manager) running(ctx context.Context) (string, error) {
	for _, r := range m.Runtime.Releases() {
		active, err := m.Units.Active(ctx, r.Line)
		if err != nil {
			return "", err
		}

		if active {
			return r.Line, nil
		}
	}

	return "", nil
}

func (m *Manager) logPath(line string) string {
	return m.path(filepath.Join(m.Runtime.LogDir, line+".log"))
}

// Status reports the installed lines and the last installation: a running
// unit, else the line whose log is the newest. The lines are read last: the
// unit swaps a line in before it writes its final line and ends, so a
// finished installation is always listed (read first, a unit that ends in
// between showed "succeeded" without its line).
func (m *Manager) Status(ctx context.Context) (Status, error) {
	st, err := m.lastRun(ctx)
	if err != nil {
		return Status{}, err
	}

	st.Installed = m.Installed()

	return st, nil
}

func (m *Manager) lastRun(ctx context.Context) (Status, error) {
	st := Status{State: agentapi.PHPInstallIdle, Log: []string{}}

	busy, err := m.running(ctx)
	if err != nil {
		return Status{}, err
	}

	if busy != "" {
		lines, _ := phpinstall.TailLog(m.logPath(busy))
		st.Last, st.State, st.Log = busy, agentapi.PHPInstallRunning, lines

		return st, nil
	}

	var newest int64

	for _, r := range m.Runtime.Releases() {
		fi, err := os.Lstat(m.logPath(r.Line))
		if err != nil || !fi.Mode().IsRegular() {
			continue
		}

		if t := fi.ModTime().UnixNano(); st.Last == "" || t > newest {
			st.Last, newest = r.Line, t
		}
	}

	if st.Last == "" {
		return st, nil
	}

	lines, err := phpinstall.TailLog(m.logPath(st.Last))
	if err != nil {
		return Status{}, err
	}

	st.State, st.Log = agentapi.PHPInstallFailed, lines
	if n := len(lines); n > 0 && strings.HasPrefix(lines[n-1], m.Runtime.DoneMarker()) {
		st.State = agentapi.PHPInstallSucceeded
	}

	return st, nil
}

// Remove deletes an installed line. users are the sites whose app runs on
// it: a line in use is refused, so no app loses its runtime. The tree is
// first renamed out of the way, so a half-removed line never looks
// installed, and the line's install log goes with it.
func (m *Manager) Remove(ctx context.Context, line string, users []string) (Status, error) {
	rt := m.Runtime

	if _, err := rt.Release(line); err != nil {
		return Status{}, err
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	if busy, err := m.running(ctx); err != nil {
		return Status{}, err
	} else if busy != "" {
		return Status{}, ErrBusy
	}

	if len(users) > 0 {
		return Status{}, fmt.Errorf("%w: %s", ErrInUse, strings.Join(users, ", "))
	}

	dir := m.path(filepath.Join(rt.Dir, line))
	if fi, err := os.Lstat(dir); errors.Is(err, os.ErrNotExist) {
		return Status{}, ErrNotInstalled
	} else if err != nil {
		return Status{}, rt.errorf("%w", err)
	} else if !fi.IsDir() {
		return Status{}, rt.errorf("%s is not a directory", dir)
	}

	old := m.path(filepath.Join(rt.Dir, ".old-"+line))
	if err := os.RemoveAll(old); err != nil {
		return Status{}, rt.errorf("%w", err)
	}

	if err := os.Rename(dir, old); err != nil {
		return Status{}, rt.errorf("remove %s: %w", dir, err)
	}

	if err := os.RemoveAll(old); err != nil {
		return Status{}, rt.errorf("remove %s: %w", old, err)
	}

	if err := os.Remove(m.logPath(line)); err != nil && !errors.Is(err, os.ErrNotExist) {
		return Status{}, rt.errorf("%w", err)
	}

	return m.Status(ctx)
}
