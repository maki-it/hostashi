// SPDX-License-Identifier: AGPL-3.0-or-later

package phpctl

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os/exec"
	"regexp"
	"strings"
)

// The cron jobs' units (ADR-0028) go through the same exception as the PHP
// units: systemctl by absolute path, fixed verbs, and unit names that match
// cronUnitRe. The command a job runs never passes through here; it is in a
// script file that the unit names.
var (
	cronUnitRe = regexp.MustCompile(`^hostashi-cron-[a-z][a-z0-9_-]{1,31}-[1-9][0-9]{0,18}\.(?:service|timer)$`)
	cronPropRe = regexp.MustCompile(`^[A-Za-z]{1,64}$`)
)

// CronController implements cronjobs.Controller.
type CronController struct{}

// DaemonReload makes systemd read changed unit files.
func (CronController) DaemonReload(ctx context.Context) error {
	return run(ctx, Systemctl, "daemon-reload")
}

// EnableNow enables a timer and starts it.
func (CronController) EnableNow(ctx context.Context, unit string) error {
	return cronctl(ctx, unit, ".timer", "enable", "--now")
}

// DisableNow stops a timer and disables it.
func (CronController) DisableNow(ctx context.Context, unit string) error {
	return cronctl(ctx, unit, ".timer", "disable", "--now")
}

// StartNoBlock queues a start of a job's service and returns at once.
func (CronController) StartNoBlock(ctx context.Context, unit string) error {
	return cronctl(ctx, unit, ".service", "start", "--no-block")
}

// Show returns properties of a cron unit, timestamps as "@<unix seconds>".
func (CronController) Show(ctx context.Context, unit string, props ...string) (map[string]string, error) {
	if !cronUnitRe.MatchString(unit) {
		return nil, fmt.Errorf("phpctl: refusing unit name %q", unit)
	}

	args := []string{"show", "--timestamp=unix"}

	for _, p := range props {
		if !cronPropRe.MatchString(p) {
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

// NextElapse reads the next elapse of cron timers from systemctl
// list-timers --output=json, which gives it in microseconds since the
// epoch (null for none). systemctl show prints NextElapseUSecRealtime as a
// local date even with --timestamp=unix, so Show cannot read it.
func (CronController) NextElapse(ctx context.Context, timers ...string) (map[string]int64, error) {
	if len(timers) == 0 {
		return map[string]int64{}, nil
	}

	args := []string{"list-timers", "--all", "--no-pager", "--output=json", "--"}

	for _, t := range timers {
		if !cronUnitRe.MatchString(t) || !strings.HasSuffix(t, ".timer") {
			return nil, fmt.Errorf("phpctl: refusing unit name %q", t)
		}

		args = append(args, t)
	}

	out, err := output(ctx, Systemctl, args...)
	if err != nil {
		return nil, err
	}

	return parseTimers(out)
}

// parseTimers reads list-timers' JSON: unit name to Unix seconds, timers
// without a next elapse left out.
func parseTimers(out string) (map[string]int64, error) {
	var rows []struct {
		Unit string `json:"unit"`
		Next *int64 `json:"next"`
	}

	if strings.TrimSpace(out) == "" {
		return map[string]int64{}, nil // no timer matched
	}

	if err := json.Unmarshal([]byte(out), &rows); err != nil {
		return nil, fmt.Errorf("phpctl: list-timers: %w", err)
	}

	m := make(map[string]int64, len(rows))

	for _, r := range rows {
		if r.Next != nil && *r.Next > 0 {
			m[r.Unit] = *r.Next / 1_000_000
		}
	}

	return m, nil
}

func cronctl(ctx context.Context, unit, suffix string, args ...string) error {
	if !cronUnitRe.MatchString(unit) || !strings.HasSuffix(unit, suffix) {
		return fmt.Errorf("phpctl: refusing unit name %q", unit)
	}

	return run(ctx, Systemctl, append(args, unit)...)
}

// output runs a command like run and returns its standard output, at most
// 64 KiB.
func output(ctx context.Context, path string, args ...string) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, commandTimeout)
	defer cancel()

	var out, errOut bytes.Buffer

	cmd := exec.CommandContext(ctx, path, args...)
	cmd.Env = []string{"PATH=/usr/sbin:/usr/bin:/sbin:/bin", "LC_ALL=C"}
	cmd.Dir = "/"
	cmd.Stdout = &limitedWriter{buf: &out, max: 64 << 10}
	cmd.Stderr = &limitedWriter{buf: &errOut, max: 4096}

	if err := cmd.Run(); err != nil {
		var ee *exec.ExitError
		if errors.As(err, &ee) {
			return "", fmt.Errorf("phpctl: %s %s exited %d: %s", path, args[0], ee.ExitCode(), strings.TrimSpace(errOut.String()))
		}

		return "", fmt.Errorf("phpctl: %s: %w", path, err)
	}

	return out.String(), nil
}
