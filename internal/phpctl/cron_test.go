// SPDX-License-Identifier: AGPL-3.0-or-later

package phpctl

import (
	"fmt"
	"math"
	"strconv"
	"strings"
	"testing"

	"github.com/maki-it/hostashi/internal/agentapi"
)

func TestCronRefusesOddUnits(t *testing.T) {
	var c CronController

	ctx := t.Context()

	for _, u := range []string{
		"", "nginx.service", "hostashi-cron-shop-0.timer", "hostashi-cron-shop-01.timer",
		"hostashi-cron-shop-x.timer", "hostashi-cron-Shop-1.timer", "hostashi-cron-shop-1.timer --now",
		"hostashi-cron-shop-1.socket", "hostashi-cron-s-1.timer", "--all", "hostashi-cron-../x-1.timer",
		"hostashi-cron-shop-12345678901234567890.timer",
	} {
		for name, f := range map[string]func() error{
			"enable":  func() error { return c.EnableNow(ctx, u) },
			"disable": func() error { return c.DisableNow(ctx, u) },
			"start":   func() error { return c.StartNoBlock(ctx, u) },
			"show":    func() error { _, err := c.Show(ctx, u, "Result"); return err },
		} {
			if err := f(); err == nil || !strings.Contains(err.Error(), "refusing") {
				t.Errorf("%s %q: %v", name, u, err)
			}
		}
	}

	// Timers are enabled, services started; not the other way round.
	if err := c.EnableNow(ctx, "hostashi-cron-shop-1.service"); err == nil || !strings.Contains(err.Error(), "refusing") {
		t.Errorf("enable service: %v", err)
	}

	if err := c.StartNoBlock(ctx, "hostashi-cron-shop-1.timer"); err == nil || !strings.Contains(err.Error(), "refusing") {
		t.Errorf("start timer: %v", err)
	}

	for _, p := range []string{"", "a=b", "--all", "A B"} {
		if _, err := c.Show(ctx, "hostashi-cron-shop-1.service", p); err == nil || !strings.Contains(err.Error(), "refusing") {
			t.Errorf("property %q: %v", p, err)
		}
	}
}

func TestCronNextElapseRefusesOddUnits(t *testing.T) {
	var c CronController

	for _, u := range []string{"hostashi-cron-shop-1.service", "hostashi-cron-shop-*.timer", "--all", "nginx.timer", ""} {
		if _, err := c.NextElapse(t.Context(), u); err == nil {
			t.Errorf("%q accepted", u)
		}
	}

	if m, err := c.NextElapse(t.Context()); err != nil || len(m) != 0 {
		t.Errorf("no timers: %v %v", m, err)
	}
}

func TestParseTimers(t *testing.T) {
	// The shape of systemd 257's list-timers --output=json.
	out := `[{"next":1764284400000000,"left":1764284400000000,"last":null,"passed":null,"unit":"hostashi-cron-shop-1.timer","activates":"hostashi-cron-shop-1.service"},` +
		`{"next":null,"left":null,"last":1764200000000000,"passed":1764200000000000,"unit":"hostashi-cron-shop-2.timer","activates":"hostashi-cron-shop-2.service"},` +
		`{"next":0,"unit":"hostashi-cron-shop-3.timer"}]`

	m, err := parseTimers(out)
	if err != nil {
		t.Fatal(err)
	}

	if len(m) != 1 || m["hostashi-cron-shop-1.timer"] != 1764284400 {
		t.Errorf("%v", m)
	}

	if m, err := parseTimers("\n"); err != nil || len(m) != 0 {
		t.Errorf("empty: %v %v", m, err)
	}

	if _, err := parseTimers("0 timers listed."); err == nil {
		t.Error("text output accepted")
	}
}

func TestCronJournalRefusesOddInput(t *testing.T) {
	var c CronController

	inv := "0123456789abcdef0123456789abcdef"

	for _, u := range []string{"hostashi-cron-shop-1.timer", "nginx.service", "", "hostashi-cron-shop-1.service _UID=0"} {
		if _, _, err := c.Journal(t.Context(), u, inv, 1); err == nil || !strings.Contains(err.Error(), "refusing") {
			t.Errorf("unit %q: %v", u, err)
		}
	}

	for _, i := range []string{"", "0123456789ABCDEF0123456789ABCDEF", inv + "0", inv[:31], "0123456789abcdef0123456789abcde_", inv + " _UID=0"} {
		if _, _, err := c.Journal(t.Context(), "hostashi-cron-shop-1.service", i, 1); err == nil || !strings.Contains(err.Error(), "refusing") {
			t.Errorf("invocation %q: %v", i, err)
		}
	}
}

func TestParseJournal(t *testing.T) {
	out := `{"__CURSOR":"s=1","MESSAGE":"Hi!","_BOOT_ID":"x","__MONOTONIC_TIMESTAMP":"1"}` + "\n" +
		`{"MESSAGE":[111,107,255],"__MONOTONIC_TIMESTAMP":"1"}` + "\n" +
		`{"MESSAGE":null,"__MONOTONIC_TIMESTAMP":"1"}` + "\n" +
		`{"MESSAGE":"trailing newline\n","__MONOTONIC_TIMESTAMP":"1"}` + "\n" +
		"\n"

	lines, cut := parseJournal(out, false, "", 0)
	if cut || len(lines) != 3 || lines[0] != "Hi!" || lines[1] != "ok\xff" || lines[2] != "trailing newline" {
		t.Errorf("%q %v", lines, cut)
	}

	// Output cut at the limit: the partial last object is dropped.
	lines, cut = parseJournal(`{"MESSAGE":"a","__MONOTONIC_TIMESTAMP":"1"}`+"\n"+`{"MESSAGE":"b`, true, "", 0)
	if !cut || len(lines) != 1 || lines[0] != "a" {
		t.Errorf("cut: %q %v", lines, cut)
	}

	if lines, cut := parseJournal("", false, "", 0); cut || lines == nil || len(lines) != 0 {
		t.Errorf("empty: %q %v", lines, cut)
	}
}

// TestParseJournalInvocation uses journalctl's output for a job
// `echo "Hi!"; date` that ran twice (systemd 257, Debian 13). In the
// second run journald stored the date line without
// _SYSTEMD_INVOCATION_ID because date had exited before journald looked
// up its cgroup; matching the id in journalctl dropped that line.
func TestParseJournalInvocation(t *testing.T) {
	const (
		old = "4fff7b89e1724ae6965e9f87255de970"
		inv = "17259c2c029745bdb4cae71b40f9532e"
	)

	out := `{"__CURSOR":"s=a","__REALTIME_TIMESTAMP":"1795880550902000","__MONOTONIC_TIMESTAMP":"120000100","_BOOT_ID":"b","MESSAGE":"Hi!","_SYSTEMD_INVOCATION_ID":"` + old + `"}` + "\n" +
		`{"__CURSOR":"s=b","__REALTIME_TIMESTAMP":"1795880550902473","__MONOTONIC_TIMESTAMP":"120000573","_BOOT_ID":"b","MESSAGE":"Sat Nov 28 15:42:30 UTC 2026"}` + "\n" +
		`{"__CURSOR":"s=c","__REALTIME_TIMESTAMP":"1795880604915000","__MONOTONIC_TIMESTAMP":"133925000","_BOOT_ID":"b","MESSAGE":"Hi!","_SYSTEMD_INVOCATION_ID":"` + inv + `"}` + "\n" +
		`{"__CURSOR":"s=d","__REALTIME_TIMESTAMP":"1795880604915400","__MONOTONIC_TIMESTAMP":"133925400","_BOOT_ID":"b","MESSAGE":"Sat Nov 28 15:43:24 UTC 2026"}` + "\n"

	lines, cut := parseJournal(out, false, inv, 133924909)
	if cut || len(lines) != 2 || lines[0] != "Hi!" || lines[1] != "Sat Nov 28 15:43:24 UTC 2026" {
		t.Errorf("last run: %q %v", lines, cut)
	}

	// Without a known start only lines with the id count.
	lines, _ = parseJournal(out, false, inv, math.MaxUint64)
	if len(lines) != 1 || lines[0] != "Hi!" {
		t.Errorf("no start: %q", lines)
	}

	// A broken timestamp on an id-less line leaves it out.
	lines, _ = parseJournal(`{"MESSAGE":"x","__MONOTONIC_TIMESTAMP":"x"}`, false, inv, 1)
	if len(lines) != 0 {
		t.Errorf("bad timestamp: %q", lines)
	}
}

func TestCronJournalRangeAndRunsRefuseOddInput(t *testing.T) {
	var c CronController

	inv := "0123456789abcdef0123456789abcdef"
	svc := "hostashi-cron-shop-1.service"

	for _, u := range []string{"hostashi-cron-shop-1.timer", "nginx.service", "", svc + " _UID=0"} {
		if _, _, err := c.JournalRange(t.Context(), u, inv, 1, 0); err == nil || !strings.Contains(err.Error(), "refusing") {
			t.Errorf("range unit %q: %v", u, err)
		}

		if _, err := c.Runs(t.Context(), u); err == nil || !strings.Contains(err.Error(), "refusing") {
			t.Errorf("runs unit %q: %v", u, err)
		}
	}

	if _, _, err := c.JournalRange(t.Context(), svc, inv+"0", 1, 0); err == nil || !strings.Contains(err.Error(), "refusing") {
		t.Errorf("range invocation: %v", err)
	}

	for _, r := range [][2]int64{{0, 0}, {-5, 0}, {10, 10}, {10, 5}} {
		if _, _, err := c.JournalRange(t.Context(), svc, inv, r[0], r[1]); err == nil || !strings.Contains(err.Error(), "refusing") {
			t.Errorf("range %v: %v", r, err)
		}
	}
}

func TestJournalTime(t *testing.T) {
	for us, want := range map[int64]string{1795880604914769: "@1795880604.914769", 1795880604000001: "@1795880604.000001", 1: "@0.000001"} {
		if got := journalTime(us); got != want {
			t.Errorf("%d: %q", us, got)
		}
	}
}

// TestParseRuns uses systemd 257's entries about a unit (Debian 13): a run
// that failed with exit status 3, one that succeeded, one that timed out,
// one whose start is older than what was read, a forged-looking entry
// without a valid id, and a run still going on.
func TestParseRuns(t *testing.T) {
	const (
		failed  = "0a2d3aa75594430b9dfe6ea1a1b255f7"
		ok      = "1e68564f9432460e8f472682fd9ae514"
		timeout = "2b2d3aa75594430b9dfe6ea1a1b255f7"
		cutoff  = "3c2d3aa75594430b9dfe6ea1a1b255f7"
		running = "4d2d3aa75594430b9dfe6ea1a1b255f7"
	)

	e := func(inv, msg, extra string, t int64) string {
		return `{"INVOCATION_ID":"` + inv + `","MESSAGE_ID":"` + msg + `","__REALTIME_TIMESTAMP":"` + strconv.FormatInt(t, 10) + `"` + extra + "}\n"
	}

	out := e(cutoff, msgUnitStarted, `,"JOB_RESULT":"done"`, 900) +
		e(failed, msgUnitStarting, `,"JOB_TYPE":"start"`, 1000) +
		e(failed, msgProcessExit, `,"EXIT_CODE":"exited","EXIT_STATUS":"3"`, 1100) +
		e(failed, "d9b373ed55a64feb8242e02dbe79a49c", `,"UNIT_RESULT":"exit-code"`, 1101) +
		e(failed, msgUnitFailed, `,"JOB_RESULT":"failed"`, 1102) +
		e(ok, msgUnitStarting, "", 2000) +
		e(ok, "7ad2d189f7e94e70a38c781354912448", "", 2100) +
		e(ok, msgUnitStarted, `,"JOB_RESULT":"done"`, 2101) +
		e(timeout, msgUnitStarting, "", 3000) +
		e(timeout, msgProcessExit, `,"EXIT_CODE":"killed","EXIT_STATUS":"15"`, 3100) +
		e(timeout, "d9b373ed55a64feb8242e02dbe79a49c", `,"UNIT_RESULT":"timeout"`, 3101) +
		e(timeout, msgUnitFailed, `,"JOB_RESULT":"failed"`, 3102) +
		e("not-an-id", msgUnitStarting, "", 3500) +
		e(running, msgUnitStarting, "", 4000) +
		"{broken\n"

	runs := parseRuns(out)
	if len(runs) != 4 {
		t.Fatalf("%+v", runs)
	}

	if r := runs[0]; r.Invocation != running || r.Start != 4000 || r.End != 0 || !r.Running || r.Result != "" {
		t.Errorf("running: %+v", r)
	}

	if r := runs[1]; r.Invocation != timeout || r.End != 3102 || r.Result != "timeout" || r.ExitStatus != nil || r.Running {
		t.Errorf("timeout: %+v", r)
	}

	if r := runs[2]; r.Invocation != ok || r.Start != 2000 || r.End != 2101 || r.Result != "success" || r.ExitStatus == nil || *r.ExitStatus != 0 {
		t.Errorf("ok: %+v", r)
	}

	if r := runs[3]; r.Invocation != failed || r.End != 1102 || r.Result != "exit-code" || r.ExitStatus == nil || *r.ExitStatus != 3 {
		t.Errorf("failed: %+v", r)
	}

	if runs := parseRuns(""); runs == nil || len(runs) != 0 {
		t.Errorf("empty: %+v", runs)
	}

	// At most CronMaxRuns, the newest.
	var many strings.Builder
	for i := range 15 {
		many.WriteString(e(fmt.Sprintf("%032x", i+1), msgUnitStarting, "", int64(1000*(i+1))))
		many.WriteString(e(fmt.Sprintf("%032x", i+1), msgUnitStarted, `,"JOB_RESULT":"done"`, int64(1000*(i+1)+1)))
	}

	runs = parseRuns(many.String())
	if len(runs) != agentapi.CronMaxRuns || runs[0].Start != 15000 || runs[9].Start != 6000 || runs[0].Running {
		t.Errorf("many: %d %+v", len(runs), runs[0])
	}
}
