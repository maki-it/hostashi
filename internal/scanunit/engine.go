// SPDX-License-Identifier: AGPL-3.0-or-later

package scanunit

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/maki-it/hostashi/internal/agentapi"
)

// The scan engine is ClamAV from the distribution. The fixed root unit
// hostashi-clamav-install@<install|remove>.service runs the script
// clamav-install, which installs or purges exactly clamav and
// clamav-freshclam and records the result in EngineDir: "state" (on or
// off), "version" (the package version) and "install.log".
const (
	// DefaultEngineDir is the script's state directory.
	DefaultEngineDir = "/var/lib/hostashi-agent/clamav-install"
	// EngineInstall and EngineRemove are the instances of the unit.
	EngineInstall = "install"
	EngineRemove  = "remove"
)

// ErrEngineBusy: an install or removal is running.
var ErrEngineBusy = errors.New("scanunit: ClamAV is being installed or removed")

// EngineController starts and reads the install unit
// (phpctl.ClamAVInstallUnits).
type EngineController interface {
	// Start queues the unit's instance and returns.
	Start(ctx context.Context, instance string) error
	// State returns the instance's ActiveState.
	State(ctx context.Context, instance string) (string, error)
}

// Engine reports and switches ClamAV. Set Ctl.
type Engine struct {
	Dir         string // default DefaultEngineDir
	DatabaseDir string // default DefaultDatabaseDir
	Ctl         EngineController
}

func (e *Engine) dir() string   { return or(e.Dir, DefaultEngineDir) }
func (e *Engine) dbDir() string { return or(e.DatabaseDir, DefaultDatabaseDir) }

func (e *Engine) read(name string) string {
	b, err := os.ReadFile(filepath.Join(e.dir(), name))
	if err != nil || len(b) > 256 {
		return ""
	}

	return strings.TrimSpace(string(b))
}

// On reports whether ClamAV is installed and switched on.
func (e *Engine) On() bool { return e.read("state") == agentapi.ScanEngineOn }

// Status reports the engine (scan.engine.status).
func (e *Engine) Status(ctx context.Context) (agentapi.ScanEngineStatus, error) {
	for _, inst := range []string{EngineInstall, EngineRemove} {
		st, err := e.Ctl.State(ctx, inst)
		if err != nil {
			return agentapi.ScanEngineStatus{}, err
		}

		switch st {
		case "activating", "active", "reloading", "deactivating":
			if inst == EngineInstall {
				return agentapi.ScanEngineStatus{State: agentapi.ScanEngineInstalling}, nil
			}

			return agentapi.ScanEngineStatus{State: agentapi.ScanEngineRemoving}, nil
		case "failed":
			return agentapi.ScanEngineStatus{State: agentapi.ScanEngineFailed, Error: tail(filepath.Join(e.dir(), "install.log"))}, nil
		}
	}

	if !e.On() {
		return agentapi.ScanEngineStatus{State: agentapi.ScanEngineOff}, nil
	}

	out := agentapi.ScanEngineStatus{State: agentapi.ScanEngineOn, Version: clip(e.read("version"), 64)}
	if t := newestSignature(e.dbDir()); !t.IsZero() {
		out.Signatures = t.UTC().Format(time.RFC3339)
	}

	return out, nil
}

// Set installs or removes ClamAV (scan.engine.set). It returns at once;
// Status follows the unit.
func (e *Engine) Set(ctx context.Context, req agentapi.ScanEngineSetRequest) (agentapi.ScanEngineStatus, error) {
	cur, err := e.Status(ctx)
	if err != nil {
		return cur, err
	}

	switch cur.State {
	case agentapi.ScanEngineInstalling, agentapi.ScanEngineRemoving:
		return cur, ErrEngineBusy
	case agentapi.ScanEngineOn:
		if req.On {
			return cur, nil
		}
	case agentapi.ScanEngineOff:
		if !req.On {
			return cur, nil
		}
	}

	inst, state := EngineRemove, agentapi.ScanEngineRemoving
	if req.On {
		inst, state = EngineInstall, agentapi.ScanEngineInstalling
	}

	if err := e.Ctl.Start(ctx, inst); err != nil {
		return cur, err
	}

	return agentapi.ScanEngineStatus{State: state}, nil
}

// newestSignature is the newest modification time of ClamAV's signature
// files (main, daily, bytecode as .cvd or .cld).
func newestSignature(dir string) time.Time {
	var newest time.Time

	entries, err := os.ReadDir(dir)
	if err != nil {
		return newest
	}

	for _, d := range entries {
		n := d.Name()
		if !strings.HasSuffix(n, ".cvd") && !strings.HasSuffix(n, ".cld") {
			continue
		}

		fi, err := d.Info()
		if err != nil || !fi.Mode().IsRegular() {
			continue
		}

		if fi.ModTime().After(newest) {
			newest = fi.ModTime()
		}
	}

	return newest
}

func clip(s string, n int) string {
	if len(s) > n {
		s = s[:n]
	}

	return strings.Map(func(r rune) rune {
		if r < 0x20 || r == 0x7f {
			return -1
		}

		return r
	}, strings.ToValidUTF8(s, ""))
}
