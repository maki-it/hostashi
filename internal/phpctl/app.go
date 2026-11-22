// SPDX-License-Identifier: AGPL-3.0-or-later

package phpctl

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"strings"
)

// The sites' apps (ADR-0054) go through the same exception as the PHP
// units: systemctl by absolute path, fixed verbs, and unit names that
// match appUnitRe. The agent never runs node or npm itself; systemd starts
// them as the site user from the fixed template and the agent's drop-in.
var (
	appUnitRe = regexp.MustCompile(`^hostashi-node@[a-z][a-z0-9_-]{1,31}\.service$`)
	appPropRe = regexp.MustCompile(`^(?:ActiveState|SubState|MemoryCurrent|NRestarts|MainPID)$`)
)

// AppController implements appunit.Controller.
type AppController struct{}

// DaemonReload makes systemd read changed drop-ins.
func (AppController) DaemonReload(ctx context.Context) error {
	return run(ctx, Systemctl, "daemon-reload")
}

// Start enables the unit and starts it now.
func (AppController) Start(ctx context.Context, unit string) error {
	return appctl(ctx, unit, "enable", "--now")
}

// systemctlNotLoaded is systemctl's exit code for a unit it has not
// loaded: an app that never ran.
const systemctlNotLoaded = 5

// Stop disables the unit and stops it now. A unit that was never loaded
// (an app set up stopped) is stopped already.
func (AppController) Stop(ctx context.Context, unit string) error {
	err := appctl(ctx, unit, "disable", "--now")

	var ee *ExitError
	if errors.As(err, &ee) && ee.Code == systemctlNotLoaded {
		return nil
	}

	return err
}

// Restart restarts the unit (and starts a stopped one).
func (AppController) Restart(ctx context.Context, unit string) error {
	return appctl(ctx, unit, "restart")
}

// ResetFailed clears a failed unit's state and its start limit, so a
// fixed app may start again at once.
func (AppController) ResetFailed(ctx context.Context, unit string) error {
	return appctl(ctx, unit, "reset-failed")
}

// Show returns properties of an app unit.
func (AppController) Show(ctx context.Context, unit string, props ...string) (map[string]string, error) {
	if !appUnitRe.MatchString(unit) {
		return nil, fmt.Errorf("phpctl: refusing unit name %q", unit)
	}

	args := []string{"show"}

	for _, p := range props {
		if !appPropRe.MatchString(p) {
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

func appctl(ctx context.Context, unit string, args ...string) error {
	if !appUnitRe.MatchString(unit) {
		return fmt.Errorf("phpctl: refusing unit name %q", unit)
	}

	return run(ctx, Systemctl, append(args, unit)...)
}
