// SPDX-License-Identifier: AGPL-3.0-or-later

// Package phpinstall follows the installation of more PHP versions from the
// panel (ADR-0036). It never runs apt itself: it starts the fixed unit
// hostashi-php-install@<version>.service through Units and reads the log
// file the unit writes. It also reads apt's package lists to show which
// newer versions sury.org offers. Everything else is files.
package phpinstall

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"sync"

	"github.com/maki-it/hostashi/internal/agentapi"
)

// Paths on the server.
const (
	// LogDir holds one log per version; the unit writes
	// <LogDir>/<version>.log (StandardOutput=truncate:).
	LogDir = "/var/lib/hostashi-agent/php-install"
	// SourceFile is the sury.org source the script writes.
	SourceFile = "/etc/apt/sources.list.d/hostashi-sury-php.sources"
	// AptLists is where apt keeps the downloaded package lists.
	AptLists = "/var/lib/apt/lists"
)

// DoneMarker starts the script's last line after a successful run.
const DoneMarker = "php-install: done:"

// LogLines is how many lines of the log the status returns.
const LogLines = 50

var (
	// ErrBusy: an installation is already running.
	ErrBusy = errors.New("phpinstall: an installation is already running")
	// ErrInstalled: the version is installed already.
	ErrInstalled = errors.New("phpinstall: the version is already installed")
)

// Units starts and reads the install units.
type Units interface {
	Start(ctx context.Context, version string) error
	Active(ctx context.Context, version string) (bool, error)
}

// Manager serves php.install and php.install.status.
type Manager struct {
	Units Units
	// Installed lists the installed versions (phpctl.Installed).
	Installed func() []string
	Root      string // prefixed to every path; empty means /

	mu sync.Mutex
}

func (m *Manager) path(p string) string {
	if m.Root == "" {
		return p
	}

	return filepath.Join(m.Root, p)
}

// Start starts the installation of version. Only one runs at a time.
func (m *Manager) Start(ctx context.Context, version string) (agentapi.PHPInstallStatus, error) {
	if err := agentapi.ValidatePHPVersion(version); err != nil {
		return agentapi.PHPInstallStatus{}, err
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	if m.Installed != nil && slices.Contains(m.Installed(), version) {
		return agentapi.PHPInstallStatus{}, ErrInstalled
	}

	for _, v := range agentapi.PHPVersions {
		active, err := m.Units.Active(ctx, v)
		if err != nil {
			return agentapi.PHPInstallStatus{}, err
		}

		if active {
			return agentapi.PHPInstallStatus{}, ErrBusy
		}
	}

	if err := m.Units.Start(ctx, version); err != nil {
		return agentapi.PHPInstallStatus{}, err
	}

	return agentapi.PHPInstallStatus{Version: version, State: agentapi.PHPInstallRunning, Log: []string{}}, nil
}

// Status reports the last installation: a running unit, else the version
// whose log is the newest.
func (m *Manager) Status(ctx context.Context) (agentapi.PHPInstallStatus, error) {
	for _, v := range agentapi.PHPVersions {
		active, err := m.Units.Active(ctx, v)
		if err != nil {
			return agentapi.PHPInstallStatus{}, err
		}

		if active {
			lines, _ := m.tail(v)

			return agentapi.PHPInstallStatus{Version: v, State: agentapi.PHPInstallRunning, Log: lines}, nil
		}
	}

	last, newest := "", int64(0)

	for _, v := range agentapi.PHPVersions {
		st, err := os.Lstat(m.path(filepath.Join(LogDir, v+".log")))
		if err != nil || !st.Mode().IsRegular() {
			continue
		}

		if t := st.ModTime().UnixNano(); last == "" || t > newest {
			last, newest = v, t
		}
	}

	if last == "" {
		return agentapi.PHPInstallStatus{State: agentapi.PHPInstallIdle, Log: []string{}}, nil
	}

	lines, err := m.tail(last)
	if err != nil {
		return agentapi.PHPInstallStatus{}, err
	}

	state := agentapi.PHPInstallFailed
	if n := len(lines); n > 0 && strings.HasPrefix(lines[n-1], DoneMarker) {
		state = agentapi.PHPInstallSucceeded
	}

	return agentapi.PHPInstallStatus{Version: last, State: state, Log: lines}, nil
}

// maxLogBytes bounds what tail reads: apt's output for one version is a
// few kilobytes [E]; a larger file is cut from the front.
const maxLogBytes = 1 << 20

// tail returns the last LogLines lines of a version's log, without
// control characters.
func (m *Manager) tail(version string) ([]string, error) {
	return TailLog(m.path(filepath.Join(LogDir, version+".log")))
}

// TailLog returns the last LogLines non-empty lines of an install unit's
// log, without control characters; a missing file has none. The
// PostgreSQL client install (internal/pgclient) reads its logs the same way.
func TailLog(name string) ([]string, error) {
	f, err := os.Open(name)
	if errors.Is(err, os.ErrNotExist) {
		return []string{}, nil
	}

	if err != nil {
		return nil, fmt.Errorf("phpinstall: %w", err)
	}

	defer f.Close()

	if st, err := f.Stat(); err == nil && st.Size() > maxLogBytes {
		if _, err := f.Seek(-maxLogBytes, 2); err != nil {
			return nil, fmt.Errorf("phpinstall: %w", err)
		}
	}

	lines := []string{}
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 64*1024), 64*1024)

	for sc.Scan() {
		line := strings.Map(func(r rune) rune {
			if r < 0x20 || r == 0x7f {
				return -1
			}

			return r
		}, sc.Text())
		if strings.TrimSpace(line) == "" {
			continue
		}

		lines = append(lines, line)
		if len(lines) > LogLines {
			lines = lines[1:]
		}
	}

	return lines, nil
}

// Repository reports whether the sury.org source is set up.
func (m *Manager) Repository() bool {
	st, err := os.Lstat(m.path(SourceFile))

	return err == nil && st.Mode().IsRegular()
}

var fpmRe = regexp.MustCompile(`^Package: php([0-9]{1,2})\.([0-9]{1,2})-fpm$`)

// Newer returns the versions that sury.org's package lists offer above the
// newest of agentapi.PHPVersions, sorted. Without the source it is empty.
func (m *Manager) Newer() []string {
	if !m.Repository() {
		return []string{}
	}

	files, _ := filepath.Glob(m.path(filepath.Join(AptLists, "packages.sury.org_php_dists_*_Packages")))
	maxMajor, maxMinor := split(agentapi.PHPVersions[len(agentapi.PHPVersions)-1])
	seen := map[string]bool{}

	for _, name := range files {
		f, err := os.Open(name)
		if err != nil {
			continue
		}

		sc := bufio.NewScanner(f)
		sc.Buffer(make([]byte, 64*1024), 1024*1024)

		for sc.Scan() {
			mm := fpmRe.FindStringSubmatch(sc.Text())
			if mm == nil {
				continue
			}

			maj, _ := strconv.Atoi(mm[1])
			minor, _ := strconv.Atoi(mm[2])

			if maj > maxMajor || maj == maxMajor && minor > maxMinor {
				seen[mm[1]+"."+mm[2]] = true
			}
		}

		_ = f.Close()
	}

	out := make([]string, 0, len(seen))
	for v := range seen {
		out = append(out, v)
	}

	slices.SortFunc(out, func(a, b string) int {
		a1, a2 := split(a)
		b1, b2 := split(b)

		if a1 != b1 {
			return a1 - b1
		}

		return a2 - b2
	})

	return out
}

func split(v string) (int, int) {
	a, b, _ := strings.Cut(v, ".")
	x, _ := strconv.Atoi(a)
	y, _ := strconv.Atoi(b)

	return x, y
}
