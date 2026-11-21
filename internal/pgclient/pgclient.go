// SPDX-License-Identifier: AGPL-3.0-or-later

// Package pgclient follows the installation of the PostgreSQL client tools
// from apt.postgresql.org (ADR-0051). Like internal/phpinstall it never runs
// apt itself: it starts the fixed unit
// hostashi-pg-client-install@<major>.service through Units and reads the
// log file the unit writes. Which majors are installed it learns from the
// files under /usr/lib/postgresql, without starting a process.
package pgclient

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync"

	"github.com/maki-it/hostashi/internal/agentapi"
	"github.com/maki-it/hostashi/internal/phpinstall"
)

// Paths on the server.
const (
	// LogDir holds one log per major; the unit writes <LogDir>/<major>.log.
	LogDir = "/var/lib/hostashi-agent/pg-client-install"
	// LibDir holds one directory per installed major, with bin/pg_dump.
	LibDir = "/usr/lib/postgresql"
)

// DoneMarker starts the script's last line after a successful run.
const DoneMarker = "pg-client-install: done:"

var (
	// ErrBusy: an installation is already running.
	ErrBusy = errors.New("pgclient: an installation is already running")
	// ErrInstalled: the major is installed already.
	ErrInstalled = errors.New("pgclient: the PostgreSQL client of this major is already installed")
)

// Units starts and reads the install units (phpctl.PGClientUnits).
type Units interface {
	Start(ctx context.Context, major string) error
	Active(ctx context.Context, major string) (bool, error)
}

// Manager serves pg.client.install and pg.client.status.
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

// Installed returns the majors whose pg_dump is an executable regular file
// under LibDir, ascending by number.
func (m *Manager) Installed() []string {
	entries, err := os.ReadDir(m.path(LibDir))
	if err != nil {
		return []string{}
	}

	var nums []int

	for _, e := range entries {
		n, err := strconv.Atoi(e.Name())
		if err != nil || n <= 0 || n > 999 || strconv.Itoa(n) != e.Name() {
			continue
		}

		st, err := os.Lstat(m.path(filepath.Join(LibDir, e.Name(), "bin", "pg_dump")))
		if err != nil {
			continue
		}

		// Debian's pg_dump is a real file; a symlink there is not ours.
		if st.Mode().IsRegular() && st.Mode().Perm()&0o111 != 0 {
			nums = append(nums, n)
		}
	}

	slices.Sort(nums)

	out := make([]string, 0, len(nums))
	for _, n := range nums {
		out = append(out, strconv.Itoa(n))
	}

	return out
}

// Start starts the installation of major, or of agentapi.PGClientNewest
// when major is empty. Only one runs at a time.
func (m *Manager) Start(ctx context.Context, major string) (agentapi.PGClientStatus, error) {
	if major == "" {
		major = agentapi.PGClientNewest()
	}

	if err := agentapi.ValidatePGClientMajor(major); err != nil {
		return agentapi.PGClientStatus{}, err
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	installed := m.Installed()
	if slices.Contains(installed, major) {
		return agentapi.PGClientStatus{}, ErrInstalled
	}

	for _, v := range agentapi.PGClientMajors {
		active, err := m.Units.Active(ctx, v)
		if err != nil {
			return agentapi.PGClientStatus{}, err
		}

		if active {
			return agentapi.PGClientStatus{}, ErrBusy
		}
	}

	if err := m.Units.Start(ctx, major); err != nil {
		return agentapi.PGClientStatus{}, err
	}

	return agentapi.PGClientStatus{Installed: installed, Major: major, State: agentapi.PHPInstallRunning, Log: []string{}}, nil
}

// Status reports the installed majors and the last installation: a
// running unit, else the major whose log is the newest.
func (m *Manager) Status(ctx context.Context) (agentapi.PGClientStatus, error) {
	installed := m.Installed()

	for _, v := range agentapi.PGClientMajors {
		active, err := m.Units.Active(ctx, v)
		if err != nil {
			return agentapi.PGClientStatus{}, err
		}

		if active {
			lines, _ := m.tail(v)

			return agentapi.PGClientStatus{Installed: installed, Major: v, State: agentapi.PHPInstallRunning, Log: lines}, nil
		}
	}

	last, newest := "", int64(0)

	for _, v := range agentapi.PGClientMajors {
		st, err := os.Lstat(m.path(filepath.Join(LogDir, v+".log")))
		if err != nil || !st.Mode().IsRegular() {
			continue
		}

		if t := st.ModTime().UnixNano(); last == "" || t > newest {
			last, newest = v, t
		}
	}

	if last == "" {
		return agentapi.PGClientStatus{Installed: installed, State: agentapi.PHPInstallIdle, Log: []string{}}, nil
	}

	lines, err := m.tail(last)
	if err != nil {
		return agentapi.PGClientStatus{}, err
	}

	state := agentapi.PHPInstallFailed
	if n := len(lines); n > 0 && strings.HasPrefix(lines[n-1], DoneMarker) {
		state = agentapi.PHPInstallSucceeded
	}

	return agentapi.PGClientStatus{Installed: installed, Major: last, State: state, Log: lines}, nil
}

func (m *Manager) tail(major string) ([]string, error) {
	return phpinstall.TailLog(m.path(filepath.Join(LogDir, major+".log")))
}
