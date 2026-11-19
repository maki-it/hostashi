// SPDX-License-Identifier: AGPL-3.0-or-later

package resticctl

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os/exec"
	"regexp"
	"strings"
	"time"
)

// Systemctl is systemd's control tool.
const Systemctl = "/usr/bin/systemctl"

// The backup units (packaging/systemd). Their names are fixed; nothing in
// the argument lists below is variable.
const (
	RunUnit   = "hostashi-backup.service"
	TimerUnit = "hostashi-backup.timer"
	// RestoreUnit is the template of one restore; the instance is the
	// restore's id.
	RestoreUnit = "hostashi-restore@"
	// ImportUnit is the template of one CloudPanel import (ADR-0026); the
	// instance is the import's id. It sits here because this package
	// already starts the restore job the same way.
	ImportUnit = "hostashi-import@"
)

// restoreIDRe is the only variable part of any argument list here.
var restoreIDRe = regexp.MustCompile(`^[0-9a-f]{16}$`)

// ValidRestoreID reports whether id can name a restore unit.
func ValidRestoreID(id string) bool { return restoreIDRe.MatchString(id) }

// Units starts the backup run and switches its timer. It belongs to this
// package's exception because it only ever starts systemctl with fixed
// argument lists; the one variable part, a restore or import id, is 16 hex
// digits.
type Units struct{}

// StartRestore starts restore id in its own unit and returns at once.
func (Units) StartRestore(ctx context.Context, id string) error {
	if !restoreIDRe.MatchString(id) {
		return fmt.Errorf("%w: restore id", ErrInvalid)
	}

	return systemctl(ctx, "start", "--no-block", RestoreUnit+id+".service")
}

// StartImport starts CloudPanel import id in its own unit and returns at
// once. The id has the same form as a restore id.
func (Units) StartImport(ctx context.Context, id string) error {
	if !restoreIDRe.MatchString(id) {
		return fmt.Errorf("%w: import id", ErrInvalid)
	}

	return systemctl(ctx, "start", "--no-block", ImportUnit+id+".service")
}

// StartRun starts a run now and returns at once; systemd ignores the
// request while a run is active.
func (Units) StartRun(ctx context.Context) error {
	return systemctl(ctx, "start", "--no-block", RunUnit)
}

// Reload makes systemd read the timer's drop-in again.
func (Units) Reload(ctx context.Context) error {
	return systemctl(ctx, "daemon-reload")
}

// EnableTimer turns the daily timer on, now and at boot.
func (Units) EnableTimer(ctx context.Context) error {
	return systemctl(ctx, "enable", "--now", TimerUnit)
}

// DisableTimer turns the daily timer off, now and at boot.
func (Units) DisableTimer(ctx context.Context) error {
	return systemctl(ctx, "disable", "--now", TimerUnit)
}

func systemctl(ctx context.Context, args ...string) error {
	ctx, cancel := context.WithTimeout(ctx, 60*time.Second)
	defer cancel()

	var out bytes.Buffer

	cmd := exec.CommandContext(ctx, Systemctl, args...)
	cmd.Env = []string{"PATH=/usr/sbin:/usr/bin:/sbin:/bin", "LC_ALL=C"}
	cmd.Dir = "/"
	cmd.Stdout = &limitedWriter{buf: &out, max: 4096}
	cmd.Stderr = cmd.Stdout

	if err := cmd.Run(); err != nil {
		var ee *exec.ExitError
		if errors.As(err, &ee) {
			return fmt.Errorf("resticctl: systemctl %s exited %d: %s", args[0], ee.ExitCode(), strings.TrimSpace(out.String()))
		}

		return fmt.Errorf("resticctl: systemctl: %w", err)
	}

	return nil
}
