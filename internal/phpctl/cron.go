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
	"strconv"
	"strings"

	"github.com/maki-it/hostashi/internal/agentapi"
)

// The cron jobs' units (ADR-0028) go through the same exception as the PHP
// units: systemctl by absolute path, fixed verbs, and unit names that match
// cronUnitRe. The command a job runs never passes through here; it is in a
// script file that the unit names.
var (
	invocationRe = regexp.MustCompile(`^[0-9a-f]{32}$`)
	cronUnitRe   = regexp.MustCompile(`^hostashi-cron-[a-z][a-z0-9_-]{1,31}-[1-9][0-9]{0,18}\.(?:service|timer)$`)
	cronPropRe   = regexp.MustCompile(`^[A-Za-z]{1,64}$`)
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

// Journalctl reads the cron jobs' output (ADR-0028 Addendum 1).
const Journalctl = "/usr/bin/journalctl"

// journalMax is how much of journalctl's JSON is read: 200 messages of a
// few KiB each with the journal's own fields.
const journalMax = 1 << 20

// Journal returns the messages that the processes of a cron service wrote
// in one invocation, the last agentapi.CronOutputMaxLines of them. Only
// journalctl with fixed arguments runs; the unit and the invocation id
// are checked against narrow patterns. Matching _SYSTEMD_UNIT and
// _SYSTEMD_INVOCATION_ID leaves out systemd's own lines about the unit.
func (CronController) Journal(ctx context.Context, service, invocation string) ([]string, bool, error) {
	if !cronUnitRe.MatchString(service) || !strings.HasSuffix(service, ".service") {
		return nil, false, fmt.Errorf("phpctl: refusing unit name %q", service)
	}

	if !invocationRe.MatchString(invocation) {
		return nil, false, fmt.Errorf("phpctl: refusing invocation id %q", invocation)
	}

	out, full, err := outputMax(ctx, journalMax, Journalctl,
		"--no-pager", "--quiet", "--output=json", "--output-fields=MESSAGE",
		"--lines="+strconv.Itoa(agentapi.CronOutputMaxLines),
		"_SYSTEMD_UNIT="+service, "_SYSTEMD_INVOCATION_ID="+invocation)
	if err != nil {
		return nil, false, err
	}

	lines, cut := parseJournal(out, full)

	return lines, cut, nil
}

// parseJournal reads journalctl --output=json: one object per line. A
// MESSAGE that is not UTF-8 comes as an array of bytes; one too large for
// journalctl's JSON comes as null and is left out. When the output was
// cut (full), the last, partial line is dropped.
func parseJournal(out string, full bool) ([]string, bool) {
	rows := strings.Split(out, "\n")
	cut := false

	if full && len(rows) > 0 {
		rows, cut = rows[:len(rows)-1], true
	}

	lines := []string{}

	for _, r := range rows {
		if strings.TrimSpace(r) == "" {
			continue
		}

		var e struct {
			Message json.RawMessage `json:"MESSAGE"`
		}

		if json.Unmarshal([]byte(r), &e) != nil {
			cut = true

			continue
		}

		if len(e.Message) == 0 || string(e.Message) == "null" {
			continue // too large for journalctl's JSON
		}

		var text string
		if json.Unmarshal(e.Message, &text) != nil {
			var b []byte

			var ints []int
			if json.Unmarshal(e.Message, &ints) != nil {
				continue
			}

			for _, n := range ints {
				b = append(b, byte(n))
			}

			text = string(b)
		}

		lines = append(lines, strings.TrimRight(text, "\n"))
	}

	return lines, cut
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
	out, _, err := outputMax(ctx, 64<<10, path, args...)

	return out, err
}

// outputMax is output with a limit of max bytes; full reports that the
// limit was reached and the rest dropped.
func outputMax(ctx context.Context, maxOut int, path string, args ...string) (string, bool, error) {
	ctx, cancel := context.WithTimeout(ctx, commandTimeout)
	defer cancel()

	var out, errOut bytes.Buffer

	stdout := &limitedWriter{buf: &out, max: maxOut}

	cmd := exec.CommandContext(ctx, path, args...)
	cmd.Env = []string{"PATH=/usr/sbin:/usr/bin:/sbin:/bin", "LC_ALL=C"}
	cmd.Dir = "/"
	cmd.Stdout = stdout
	cmd.Stderr = &limitedWriter{buf: &errOut, max: 4096}

	if err := cmd.Run(); err != nil {
		var ee *exec.ExitError
		if errors.As(err, &ee) {
			return "", false, fmt.Errorf("phpctl: %s %s exited %d: %s", path, args[0], ee.ExitCode(), strings.TrimSpace(errOut.String()))
		}

		return "", false, fmt.Errorf("phpctl: %s: %w", path, err)
	}

	return out.String(), stdout.dropped, nil
}
