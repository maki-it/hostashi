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
	"github.com/maki-it/hostashi/internal/updates"
)

// The scan engine is ClamAV from the distribution. The fixed root unit
// hostashi-clamav-install@<install|remove>.service runs the script
// clamav-install, which installs or purges exactly clamav and
// clamav-freshclam and records the result in EngineDir: "state" (on or
// off), "version" (the package version) and "install.log". The instances
// realtime-on and realtime-off do the same for the real-time mode and
// record "realtime".
const (
	// DefaultEngineDir is the script's state directory.
	DefaultEngineDir = "/var/lib/hostashi-agent/clamav-install"
	// EngineInstall and EngineRemove are the instances of the unit.
	EngineInstall = "install"
	EngineRemove  = "remove"
)

var (
	// ErrEngineBusy: an install or removal is running.
	ErrEngineBusy = errors.New("scanunit: ClamAV is being installed or removed")
	// ErrEngineOff: the real-time mode needs ClamAV switched on first.
	ErrEngineOff = errors.New("scanunit: switch the malware scan on first")
)

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
	// Realtime reads clamonacc's state; nil reports it as not running.
	Realtime RealtimeController
	// DpkgStatus is dpkg's status file (default /var/lib/dpkg/status):
	// the version shown is the installed one, also after apt updated it.
	DpkgStatus string
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

// version is the installed clamav package's version; the one
// clamav-install recorded when dpkg's status cannot be read.
func (e *Engine) version() string {
	if v, err := updates.Installed(e.DpkgStatus, "clamav"); err == nil && v != "" {
		return v
	}

	return e.read("version")
}

// On reports whether ClamAV is installed and switched on.
func (e *Engine) On() bool { return e.read("state") == agentapi.ScanEngineOn }

// RealtimeOn reports whether the real-time mode is installed and on.
func (e *Engine) RealtimeOn() bool { return e.On() && e.read("realtime") == agentapi.ScanEngineOn }

// realtimeState is the state of the real-time mode from its install unit
// and the script's record.
func (e *Engine) realtimeState(ctx context.Context) (state, errText string, err error) {
	for _, inst := range []string{EngineRealtimeOn, EngineRealtimeOff} {
		st, err := e.Ctl.State(ctx, inst)
		if err != nil {
			return "", "", err
		}

		switch st {
		case "activating", "active", "reloading", "deactivating":
			if inst == EngineRealtimeOn {
				return agentapi.ScanEngineInstalling, "", nil
			}

			return agentapi.ScanEngineRemoving, "", nil
		case "failed":
			return agentapi.ScanEngineFailed, tail(filepath.Join(e.dir(), "install.log")), nil
		}
	}

	if e.RealtimeOn() {
		return agentapi.ScanEngineOn, "", nil
	}

	return agentapi.ScanEngineOff, "", nil
}

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

	out := agentapi.ScanEngineStatus{State: agentapi.ScanEngineOn, Version: clip(e.version(), 64)}
	if t := newestSignature(e.dbDir()); !t.IsZero() {
		out.Signatures = t.UTC().Format(time.RFC3339)
	}

	rt, rtErr, err := e.realtimeState(ctx)
	if err != nil {
		return agentapi.ScanEngineStatus{}, err
	}

	out.Realtime = rt
	if rt == agentapi.ScanEngineFailed {
		out.Error = rtErr
	}

	if rt == agentapi.ScanEngineOn && e.Realtime != nil {
		st, err := e.Realtime.State(ctx, ClamonaccUnit)
		out.RealtimeActive = err == nil && st == "active"
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
	}

	if req.Realtime != nil {
		return e.setRealtime(ctx, cur, *req.Realtime)
	}

	switch cur.State {
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

// setRealtime switches the real-time mode; ClamAV must be on.
func (e *Engine) setRealtime(ctx context.Context, cur agentapi.ScanEngineStatus, on bool) (agentapi.ScanEngineStatus, error) {
	switch cur.Realtime {
	case agentapi.ScanEngineInstalling, agentapi.ScanEngineRemoving:
		return cur, ErrEngineBusy
	case agentapi.ScanEngineOn:
		if on {
			return cur, nil
		}
	case agentapi.ScanEngineOff, "":
		if !on {
			return cur, nil
		}
	}

	if on && cur.State != agentapi.ScanEngineOn {
		return cur, ErrEngineOff
	}

	inst, state := EngineRealtimeOff, agentapi.ScanEngineRemoving
	if on {
		inst, state = EngineRealtimeOn, agentapi.ScanEngineInstalling
	}

	if err := e.Ctl.Start(ctx, inst); err != nil {
		return cur, err
	}

	cur.Realtime, cur.RealtimeActive = state, false

	return cur, nil
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
