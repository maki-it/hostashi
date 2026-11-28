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
	"slices"
	"strconv"
	"strings"

	"github.com/maki-it/hostashi/internal/agentapi"
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

// Journalctl reads the cron jobs' output (ADR-0028 Addendum 1).
const Journalctl = "/usr/bin/journalctl"

// journalMax is how much of journalctl's JSON is read: 200 messages of a
// few KiB each with the journal's own fields.
const journalMax = 1 << 20

// Journal returns the messages that the processes of a cron service wrote
// in one invocation, the last agentapi.CronOutputMaxLines of them. Only
// journalctl with fixed arguments runs; the unit and the invocation id
// are checked against narrow patterns. Matching _SYSTEMD_UNIT leaves out
// systemd's own lines about the unit.
//
// The invocation is not matched by journalctl: journald looks up
// _SYSTEMD_INVOCATION_ID in the cgroup of the process that wrote a line,
// and when that process has already exited (a short command such as date)
// the line has the unit, from the stream, but no invocation id. Such
// lines belong to the run that started at start (CLOCK_MONOTONIC
// microseconds of this boot, ExecMainStartTimestampMonotonic), because
// runs of one service never overlap; lines of other invocations are left
// out.
func (CronController) Journal(ctx context.Context, service, invocation string, start uint64) ([]string, bool, error) {
	if !cronUnitRe.MatchString(service) || !strings.HasSuffix(service, ".service") {
		return nil, false, fmt.Errorf("phpctl: refusing unit name %q", service)
	}

	if !agentapi.ValidInvocation(invocation) {
		return nil, false, fmt.Errorf("phpctl: refusing invocation id %q", invocation)
	}

	out, full, err := outputMax(ctx, journalMax, Journalctl,
		"--no-pager", "--quiet", "--boot", "--output=json",
		"--output-fields=MESSAGE,_SYSTEMD_INVOCATION_ID",
		"--lines="+strconv.Itoa(agentapi.CronOutputMaxLines),
		"_SYSTEMD_UNIT="+service)
	if err != nil {
		return nil, false, err
	}

	lines, cut := parseJournal(out, full, invocation, start)

	return lines, cut, nil
}

// JournalRange returns the messages of one earlier run of a cron service
// (ADR-0028 Addendum 2): lines journald received from since up to before
// until (Unix microseconds; until 0 means no end) with the run's
// invocation id or with none. Runs of one service never overlap, so the
// time range between this run's start and the next run's start holds no
// id-less line of another run.
func (CronController) JournalRange(ctx context.Context, service, invocation string, since, until int64) ([]string, bool, error) {
	if !cronUnitRe.MatchString(service) || !strings.HasSuffix(service, ".service") {
		return nil, false, fmt.Errorf("phpctl: refusing unit name %q", service)
	}

	if !agentapi.ValidInvocation(invocation) {
		return nil, false, fmt.Errorf("phpctl: refusing invocation id %q", invocation)
	}

	if since <= 0 || (until != 0 && until <= since) {
		return nil, false, fmt.Errorf("phpctl: refusing time range %d to %d", since, until)
	}

	args := []string{
		"--no-pager", "--quiet", "--output=json",
		"--output-fields=MESSAGE,_SYSTEMD_INVOCATION_ID",
		"--lines=" + strconv.Itoa(agentapi.CronOutputMaxLines),
		"--since=" + journalTime(since),
	}

	if until != 0 {
		// --until is inclusive; the next run's own lines come after its
		// start entry, so one microsecond less leaves them out.
		args = append(args, "--until="+journalTime(until-1))
	}

	out, full, err := outputMax(ctx, journalMax, Journalctl, append(args, "_SYSTEMD_UNIT="+service)...)
	if err != nil {
		return nil, false, err
	}

	lines, cut := parseJournal(out, full, invocation, 0)

	return lines, cut, nil
}

// journalTime formats Unix microseconds for journalctl --since/--until.
func journalTime(us int64) string {
	return fmt.Sprintf("@%d.%06d", us/1_000_000, us%1_000_000)
}

// Runs returns the last runs of a cron service, newest first, at most
// agentapi.CronMaxRuns (ADR-0028 Addendum 2). They come from the entries
// systemd itself writes about the unit; matching _PID=1 and _UID=0, which
// journald sets and a sender cannot, keeps a job from adding entries that
// look like runs.
func (CronController) Runs(ctx context.Context, service string) ([]agentapi.CronRunEntry, error) {
	if !cronUnitRe.MatchString(service) || !strings.HasSuffix(service, ".service") {
		return nil, fmt.Errorf("phpctl: refusing unit name %q", service)
	}

	out, _, err := outputMax(ctx, journalMax, Journalctl,
		"--no-pager", "--quiet", "--output=json",
		"--output-fields=INVOCATION_ID,MESSAGE_ID,JOB_RESULT,UNIT_RESULT,EXIT_CODE,EXIT_STATUS",
		"--lines="+strconv.Itoa(runEntries),
		"_PID=1", "_UID=0", "UNIT="+service)
	if err != nil {
		return nil, err
	}

	return parseRuns(out), nil
}

// runEntries is how many of systemd's entries about a unit are read: a
// run takes three to five of them.
const runEntries = 8 * agentapi.CronMaxRuns

// Message ids of systemd's entries about a unit, from systemd's catalog.
const (
	msgUnitStarting = "7d4958e842da4a758f6c1cdc7b36dcc5"
	msgProcessExit  = "98e322203f7a4ed290d09fe03c09fe15"
	msgUnitStarted  = "39f53479d3a045ac8e11786248231fbf"
	msgUnitFailed   = "be02cf6855d2428ba40df7e9d022f03d"
)

// parseRuns reads journalctl --output=json of systemd's entries about one
// unit, oldest first, and returns the runs newest first. A run counts only
// when its "Starting" entry was read; one without an end is running when
// it is the newest, otherwise its end is unknown.
func parseRuns(out string) []agentapi.CronRunEntry {
	var runs []agentapi.CronRunEntry

	index := map[string]int{}

	for r := range strings.SplitSeq(out, "\n") {
		var e struct {
			Invocation string `json:"INVOCATION_ID"`
			MessageID  string `json:"MESSAGE_ID"`
			JobResult  string `json:"JOB_RESULT"`
			UnitResult string `json:"UNIT_RESULT"`
			ExitCode   string `json:"EXIT_CODE"`
			ExitStatus string `json:"EXIT_STATUS"`
			Realtime   string `json:"__REALTIME_TIMESTAMP"`
		}

		if strings.TrimSpace(r) == "" || json.Unmarshal([]byte(r), &e) != nil || !agentapi.ValidInvocation(e.Invocation) {
			continue
		}

		t, err := strconv.ParseInt(e.Realtime, 10, 64)
		if err != nil || t <= 0 {
			continue
		}

		i, seen := index[e.Invocation]

		if e.MessageID == msgUnitStarting {
			if !seen {
				index[e.Invocation] = len(runs)
				runs = append(runs, agentapi.CronRunEntry{Invocation: e.Invocation, Start: t})
			}

			continue
		}

		if !seen {
			continue // the start is older than what was read
		}

		run := &runs[i]

		switch e.MessageID {
		case msgProcessExit:
			if e.ExitCode == "exited" {
				if n, err := strconv.Atoi(e.ExitStatus); err == nil {
					run.ExitStatus = &n
				}
			}
		case msgUnitStarted, msgUnitFailed:
			run.End = t

			switch {
			case e.JobResult == "done":
				run.Result = "success"
				zero := 0

				if run.ExitStatus == nil {
					run.ExitStatus = &zero
				}
			case run.Result == "":
				run.Result = "failed"
			}
		}

		if e.UnitResult != "" && e.UnitResult != "success" {
			run.Result = e.UnitResult
		}
	}

	slices.Reverse(runs)

	if len(runs) > agentapi.CronMaxRuns {
		runs = runs[:agentapi.CronMaxRuns]
	}

	if len(runs) > 0 && runs[0].End == 0 {
		runs[0].Running = true
	}

	if runs == nil {
		runs = []agentapi.CronRunEntry{}
	}

	return runs
}

// parseJournal reads journalctl --output=json: one object per line. It
// keeps the messages of one invocation: those with its id, and those
// without an id that journald received at or after the run's start. A
// MESSAGE that is not UTF-8 comes as an array of bytes; one too large for
// journalctl's JSON comes as null and is left out. When the output was
// cut (full), the last, partial line is dropped.
func parseJournal(out string, full bool, invocation string, start uint64) ([]string, bool) {
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
			Message    json.RawMessage `json:"MESSAGE"`
			Invocation string          `json:"_SYSTEMD_INVOCATION_ID"`
			Monotonic  string          `json:"__MONOTONIC_TIMESTAMP"`
		}

		if json.Unmarshal([]byte(r), &e) != nil {
			cut = true

			continue
		}

		if !ofInvocation(e.Invocation, e.Monotonic, invocation, start) {
			continue
		}

		if len(e.Message) == 0 || string(e.Message) == "null" {
			continue // too large for journalctl's JSON
		}

		var text string
		if json.Unmarshal(e.Message, &text) != nil {
			// Not UTF-8: journalctl gives the bytes as an array of numbers,
			// which decodes into a byte slice element by element.
			var b []byte
			if json.Unmarshal(e.Message, &b) != nil {
				continue
			}

			text = string(b)
		}

		lines = append(lines, strings.TrimRight(text, "\n"))
	}

	return lines, cut
}

// ofInvocation tells whether a journal entry belongs to the invocation
// that started at start. An entry without an invocation id counts when
// journald received it at or after the start.
func ofInvocation(id, monotonic, invocation string, start uint64) bool {
	if id != "" {
		return id == invocation
	}

	m, err := strconv.ParseUint(monotonic, 10, 64)

	return err == nil && m >= start
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
