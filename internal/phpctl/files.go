// SPDX-License-Identifier: AGPL-3.0-or-later

package phpctl

import (
	"context"
	"fmt"
	"regexp"
	"strings"
)

// The file manager's units (ADR-0040) go through the same exception as the
// PHP and cron units: systemctl by absolute path, fixed verbs, and unit
// names that match filesUnitRe. The helper itself is started by systemd on
// a connection to its socket, never here.
var filesUnitRe = regexp.MustCompile(`^hostashi-files@[a-z][a-z0-9_-]{1,31}\.(?:service|socket)$`)

// FilesController implements filesunit.Controller.
type FilesController struct{}

// DaemonReload makes systemd read a changed drop-in.
func (FilesController) DaemonReload(ctx context.Context) error {
	return run(ctx, Systemctl, "daemon-reload")
}

// Start starts a socket unit (a no-op when it listens already).
func (FilesController) Start(ctx context.Context, unit string) error {
	return filesctl(ctx, unit, ".socket", "start")
}

// Restart restarts the helper so that it picks up a changed drop-in. A
// helper that does not run stays stopped (try-restart).
func (FilesController) Restart(ctx context.Context, unit string) error {
	return filesctl(ctx, unit, ".service", "try-restart")
}

// Stop stops a socket or service unit.
func (FilesController) Stop(ctx context.Context, unit string) error {
	return filesctl(ctx, unit, "", "stop")
}

func filesctl(ctx context.Context, unit, suffix, verb string) error {
	if !filesUnitRe.MatchString(unit) || !strings.HasSuffix(unit, suffix) {
		return fmt.Errorf("phpctl: refusing unit name %q", unit)
	}

	return run(ctx, Systemctl, verb, unit)
}
