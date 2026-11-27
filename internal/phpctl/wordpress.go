// SPDX-License-Identifier: AGPL-3.0-or-later

package phpctl

import (
	"context"
	"errors"
	"fmt"
	"regexp"
)

// The WordPress quick install (ADR-0069) goes through the same exception
// as the deploys: systemctl by absolute path, fixed verbs, and only unit
// names that match wpUnitRe. The agent never runs WP-CLI or PHP itself;
// systemd runs `hostashi wp-run` as the site user from the fixed template
// and the agent's drop-in.
var (
	wpUnitRe = regexp.MustCompile(`^hostashi-wp@[a-z][a-z0-9_-]{1,31}\.service$`)
	wpPropRe = regexp.MustCompile(`^(?:ActiveState|SubState|Result)$`)
)

// WPController implements wpunit.Controller.
type WPController struct{}

// DaemonReload makes systemd read changed drop-ins.
func (WPController) DaemonReload(ctx context.Context) error {
	return run(ctx, Systemctl, "daemon-reload")
}

// ResetFailed clears the failed state of the last run.
func (WPController) ResetFailed(ctx context.Context, unit string) error {
	return wpctl(ctx, unit, "reset-failed")
}

// StartNoBlock queues the start of an install and returns at once.
func (WPController) StartNoBlock(ctx context.Context, unit string) error {
	return wpctl(ctx, unit, "start", "--no-block")
}

// Stop ends a running install. A unit that never ran is stopped already.
func (WPController) Stop(ctx context.Context, unit string) error {
	err := wpctl(ctx, unit, "stop")

	var ee *ExitError
	if errors.As(err, &ee) && ee.Code == systemctlNotLoaded {
		return nil
	}

	return err
}

// Show returns properties of an install unit.
func (WPController) Show(ctx context.Context, unit string, props ...string) (map[string]string, error) {
	if !wpUnitRe.MatchString(unit) {
		return nil, fmt.Errorf("phpctl: refusing unit name %q", unit)
	}

	return show(ctx, unit, wpPropRe, props)
}

func wpctl(ctx context.Context, unit string, args ...string) error {
	if !wpUnitRe.MatchString(unit) {
		return fmt.Errorf("phpctl: refusing unit name %q", unit)
	}

	return run(ctx, Systemctl, append(args, unit)...)
}
