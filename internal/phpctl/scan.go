// SPDX-License-Identifier: AGPL-3.0-or-later

package phpctl

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"strings"
)

// The malware scans (ADR-0064) go through the same exception as the
// deploys: systemctl by absolute path, fixed verbs, and only unit names
// that match scanUnitRe or clamavInstallRe. The agent never runs clamscan
// or apt itself; systemd runs clamscan as the site user from the fixed
// template and the agent's drop-in, and the fixed script clamav-install as
// root with one of four instances.
var (
	scanUnitRe      = regexp.MustCompile(`^hostashi-scan@[a-z][a-z0-9_-]{1,31}\.service$`)
	scanPropRe      = regexp.MustCompile(`^(?:ActiveState|SubState|Result|ExecMainStatus)$`)
	clamavInstallRe = regexp.MustCompile(`^hostashi-clamav-install@(?:install|remove|realtime-on|realtime-off)\.service$`)
	// The real-time mode: clamd and clamonacc from the distribution, with
	// the drop-ins clamav-install writes.
	clamavRealtimeRe = regexp.MustCompile(`^clamav-(?:daemon|clamonacc)\.service$`)
)

// ScanController implements scanunit.Controller.
type ScanController struct{}

// DaemonReload makes systemd read changed drop-ins.
func (ScanController) DaemonReload(ctx context.Context) error {
	return run(ctx, Systemctl, "daemon-reload")
}

// ResetFailed clears the failed state of the last run.
func (ScanController) ResetFailed(ctx context.Context, unit string) error {
	return scanctl(ctx, unit, "reset-failed")
}

// StartNoBlock queues the start of a scan and returns at once.
func (ScanController) StartNoBlock(ctx context.Context, unit string) error {
	return scanctl(ctx, unit, "start", "--no-block")
}

// Stop ends a running scan. A unit that never ran is stopped already.
func (ScanController) Stop(ctx context.Context, unit string) error {
	err := scanctl(ctx, unit, "stop")

	var ee *ExitError
	if errors.As(err, &ee) && ee.Code == systemctlNotLoaded {
		return nil
	}

	return err
}

// Show returns properties of a scan unit.
func (ScanController) Show(ctx context.Context, unit string, props ...string) (map[string]string, error) {
	if !scanUnitRe.MatchString(unit) {
		return nil, fmt.Errorf("phpctl: refusing unit name %q", unit)
	}

	return show(ctx, unit, scanPropRe, props)
}

func scanctl(ctx context.Context, unit string, args ...string) error {
	if !scanUnitRe.MatchString(unit) {
		return fmt.Errorf("phpctl: refusing unit name %q", unit)
	}

	return run(ctx, Systemctl, append(args, unit)...)
}

// ClamAVInstallUnits implements scanunit.EngineController: the fixed unit
// hostashi-clamav-install@<install|remove|realtime-on|realtime-off>.service.
type ClamAVInstallUnits struct{}

func clamavUnit(instance string) (string, error) {
	unit := "hostashi-clamav-install@" + instance + ".service"
	if !clamavInstallRe.MatchString(unit) {
		return "", fmt.Errorf("phpctl: refusing unit name %q", unit)
	}

	return unit, nil
}

// Start resets a failed earlier run and queues the unit (systemctl start
// --no-block): apt may run for minutes.
func (ClamAVInstallUnits) Start(ctx context.Context, instance string) error {
	unit, err := clamavUnit(instance)
	if err != nil {
		return err
	}

	for _, other := range []string{"install", "remove", "realtime-on", "realtime-off"} {
		u, _ := clamavUnit(other)
		_ = run(ctx, Systemctl, "reset-failed", u)
	}

	return run(ctx, Systemctl, "start", "--no-block", unit)
}

// State returns the unit's ActiveState.
func (ClamAVInstallUnits) State(ctx context.Context, instance string) (string, error) {
	unit, err := clamavUnit(instance)
	if err != nil {
		return "", err
	}

	out, err := output(ctx, Systemctl, "show", "--property=ActiveState", "--value", unit)
	if err != nil {
		return "", err
	}

	return strings.TrimSpace(out), nil
}

// show runs systemctl show for the checked properties.
func show(ctx context.Context, unit string, allowed *regexp.Regexp, props []string) (map[string]string, error) {
	args := []string{"show"}

	for _, p := range props {
		if !allowed.MatchString(p) {
			return nil, fmt.Errorf("phpctl: refusing property %q", p)
		}

		args = append(args, "--property="+p)
	}

	out, err := output(ctx, Systemctl, append(args, unit)...)
	if err != nil {
		return nil, err
	}

	m := map[string]string{}

	for l := range strings.SplitSeq(out, "\n") {
		if k, v, ok := strings.Cut(l, "="); ok {
			m[k] = v
		}
	}

	return m, nil
}

// ClamAVRealtimeUnits implements scanunit.RealtimeController: restart and
// state of clamav-daemon.service and clamav-clamonacc.service.
type ClamAVRealtimeUnits struct{}

// Restart restarts the unit, for example after its configuration changed.
func (ClamAVRealtimeUnits) Restart(ctx context.Context, unit string) error {
	if !clamavRealtimeRe.MatchString(unit) {
		return fmt.Errorf("phpctl: refusing unit name %q", unit)
	}

	_ = run(ctx, Systemctl, "reset-failed", unit)

	return run(ctx, Systemctl, "restart", "--no-block", unit)
}

// State returns the unit's ActiveState.
func (ClamAVRealtimeUnits) State(ctx context.Context, unit string) (string, error) {
	if !clamavRealtimeRe.MatchString(unit) {
		return "", fmt.Errorf("phpctl: refusing unit name %q", unit)
	}

	out, err := output(ctx, Systemctl, "show", "--property=ActiveState", "--value", unit)
	if err != nil {
		return "", err
	}

	return strings.TrimSpace(out), nil
}
