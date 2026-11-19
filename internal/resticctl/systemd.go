// SPDX-License-Identifier: AGPL-3.0-or-later

package resticctl

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os/exec"
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
)

// Units starts the backup run and switches its timer. It belongs to this
// package's exception because it only ever starts systemctl with one of
// four fixed argument lists.
type Units struct{}

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
