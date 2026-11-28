// SPDX-License-Identifier: AGPL-3.0-or-later

package cronjobs

import (
	"context"
	"errors"
	"math"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"unicode/utf8"

	"github.com/maki-it/hostashi/internal/agentapi"
)

type fakeCtl struct {
	calls []string
	props map[string]map[string]string
	next  map[string]int64
	// journal holds the lines per "unit invocation".
	journal map[string][]string
	cut     bool
	// start is what the last Journal call got as the run's start.
	start uint64
	// runs is what Runs returns; ranges holds "inv since until" of each
	// JournalRange call, whose lines come from rangeLines.
	runs       []agentapi.CronRunEntry
	ranges     []string
	rangeLines []string
}

func (f *fakeCtl) DaemonReload(context.Context) error {
	f.calls = append(f.calls, "reload")
	return nil
}

func (f *fakeCtl) EnableNow(_ context.Context, u string) error {
	f.calls = append(f.calls, "enable "+u)

	return nil
}

func (f *fakeCtl) DisableNow(_ context.Context, u string) error {
	f.calls = append(f.calls, "disable "+u)

	return nil
}

func (f *fakeCtl) StartNoBlock(_ context.Context, u string) error {
	f.calls = append(f.calls, "start "+u)

	return nil
}

func (f *fakeCtl) Show(_ context.Context, u string, _ ...string) (map[string]string, error) {
	return f.props[u], nil
}

func (f *fakeCtl) Journal(_ context.Context, u, inv string, start uint64) ([]string, bool, error) {
	f.calls = append(f.calls, "journal "+u+" "+inv)
	f.start = start

	return f.journal[u+" "+inv], f.cut, nil
}

func (f *fakeCtl) JournalRange(_ context.Context, _, inv string, since, until int64) ([]string, bool, error) {
	f.ranges = append(f.ranges, inv+" "+strconv.FormatInt(since, 10)+" "+strconv.FormatInt(until, 10))

	return f.rangeLines, false, nil
}

func (f *fakeCtl) Runs(context.Context, string) ([]agentapi.CronRunEntry, error) {
	return slices.Clone(f.runs), nil
}

func (f *fakeCtl) NextElapse(_ context.Context, timers ...string) (map[string]int64, error) {
	f.calls = append(f.calls, "next "+strings.Join(timers, " "))

	return f.next, nil
}

func newManager(t *testing.T) (*Manager, *fakeCtl) {
	t.Helper()

	if os.Geteuid() != 0 {
		t.Skip("needs root")
	}

	dir := t.TempDir()
	must(t, os.Mkdir(filepath.Join(dir, "units"), 0o755))

	ctl := &fakeCtl{}

	return &Manager{
		StateFile: filepath.Join(dir, "cron.json"),
		ScriptDir: filepath.Join(dir, "scripts"),
		UnitDir:   filepath.Join(dir, "units"),
		Ctl:       ctl,
	}, ctl
}

func must(t *testing.T, err error) {
	t.Helper()

	if err != nil {
		t.Fatal(err)
	}
}

var shop = Site{Name: "shop", Home: "/home/shop", UID: 1001, GID: 1001, Slice: `hostashi-site-shop.slice`}

func TestApply(t *testing.T) {
	m, ctl := newManager(t)
	ctx := t.Context()

	// A unit of another site whose name starts with "shop-".
	other := filepath.Join(m.UnitDir, "hostashi-cron-shop-2-1.timer")
	must(t, os.WriteFile(other, []byte("x"), 0o644))

	jobs := []agentapi.CronJob{
		{ID: 1, Schedule: "*/5 * * * *", Command: "php wp-cron.php"},
		{ID: 2, Schedule: "@daily", Command: "echo 100%", Paused: true},
	}
	must(t, m.Apply(ctx, shop, jobs))

	svc, err := os.ReadFile(filepath.Join(m.UnitDir, "hostashi-cron-shop-1.service"))
	must(t, err)

	if !strings.Contains(string(svc), "\nUser=shop\n") || !strings.Contains(string(svc), "\nSlice=hostashi-site-shop.slice\nNice=10\n") ||
		!strings.Contains(string(svc), "ExecStart=/bin/sh "+filepath.Join(m.ScriptDir, "shop", "1.sh")+"\n") {
		t.Errorf("service:\n%s", svc)
	}

	script := filepath.Join(m.ScriptDir, "shop", "2.sh")

	b, err := os.ReadFile(script)
	must(t, err)

	if !strings.HasSuffix(string(b), "\necho 100%\n") {
		t.Errorf("script %q", b)
	}

	for p, want := range map[string][3]uint32{
		script:                             {0, 1001, 0o640},
		filepath.Join(m.ScriptDir, "shop"): {0, 1001, 0o750},
		m.ScriptDir:                        {0, 0, 0o755},
		m.StateFile:                        {0, 0, 0o600},
	} {
		var st syscall.Stat_t
		must(t, syscall.Lstat(p, &st))

		if st.Uid != want[0] || st.Gid != want[1] || st.Mode&0o777 != want[2] {
			t.Errorf("%s: %d:%d %o", p, st.Uid, st.Gid, st.Mode&0o777)
		}
	}

	want := []string{"reload", "enable hostashi-cron-shop-1.timer", "disable hostashi-cron-shop-2.timer"}
	if !slices.Equal(ctl.calls, want) {
		t.Errorf("calls %q", ctl.calls)
	}

	// Drop job 1: its units and script go; the other site's unit stays.
	ctl.calls = nil

	must(t, m.Apply(ctx, shop, jobs[1:]))

	for _, p := range []string{
		filepath.Join(m.UnitDir, "hostashi-cron-shop-1.service"),
		filepath.Join(m.UnitDir, "hostashi-cron-shop-1.timer"),
		filepath.Join(m.ScriptDir, "shop", "1.sh"),
	} {
		if _, err := os.Stat(p); !errors.Is(err, os.ErrNotExist) {
			t.Errorf("%s left: %v", p, err)
		}
	}

	if _, err := os.Stat(other); err != nil {
		t.Errorf("other site's unit: %v", err)
	}

	if ctl.calls[0] != "disable hostashi-cron-shop-1.timer" {
		t.Errorf("calls %q", ctl.calls)
	}

	// Disabled: every timer stops, run now is refused; enabled again,
	// the paused job stays stopped.
	must(t, m.Apply(ctx, shop, jobs))

	ctl.calls = nil

	must(t, m.SetDisabled(ctx, "shop", true))

	if !slices.Equal(ctl.calls, []string{"disable hostashi-cron-shop-1.timer", "disable hostashi-cron-shop-2.timer"}) {
		t.Errorf("disable calls %q", ctl.calls)
	}

	if err := m.Run(ctx, "shop", 1); !errors.Is(err, ErrDisabled) {
		t.Errorf("run while disabled: %v", err)
	}

	// An apply while disabled keeps the timers stopped.
	ctl.calls = nil

	must(t, m.Apply(ctx, shop, jobs))

	if slices.ContainsFunc(ctl.calls, func(c string) bool { return strings.HasPrefix(c, "enable") }) {
		t.Errorf("apply while disabled: %q", ctl.calls)
	}

	ctl.calls = nil

	must(t, m.SetDisabled(ctx, "shop", false))

	if !slices.Equal(ctl.calls, []string{"enable hostashi-cron-shop-1.timer", "disable hostashi-cron-shop-2.timer"}) {
		t.Errorf("enable calls %q", ctl.calls)
	}

	// A paused job can still be run once.
	ctl.calls = nil

	must(t, m.Run(ctx, "shop", 2))

	if !slices.Equal(ctl.calls, []string{"start hostashi-cron-shop-2.service"}) {
		t.Errorf("run calls %q", ctl.calls)
	}

	if err := m.Run(ctx, "shop", 9); !errors.Is(err, ErrUnknownJob) {
		t.Errorf("unknown job: %v", err)
	}

	// Removing the site deletes everything of it.
	must(t, m.Remove(ctx, "shop"))

	entries, err := os.ReadDir(m.UnitDir)
	must(t, err)

	if len(entries) != 1 {
		t.Errorf("units left: %v", entries)
	}

	if _, err := os.Stat(filepath.Join(m.ScriptDir, "shop")); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("script dir left: %v", err)
	}

	if err := m.Run(ctx, "shop", 2); !errors.Is(err, ErrUnknownJob) {
		t.Errorf("run after remove: %v", err)
	}
}

func TestApplyRefuses(t *testing.T) {
	m, ctl := newManager(t)

	if err := m.Apply(t.Context(), Site{Name: "shop", Home: "/home/shop"}, nil); !errors.Is(err, ErrRootSite) {
		t.Errorf("uid 0: %v", err)
	}

	if err := m.Apply(t.Context(), shop, []agentapi.CronJob{{ID: 1, Schedule: "@reboot", Command: "x"}}); err == nil {
		t.Error("@reboot accepted")
	}

	// A bad home is caught while rendering, before anything is written.
	bad := shop
	bad.Home = "/home/shop x"

	if err := m.Apply(t.Context(), bad, []agentapi.CronJob{{ID: 1, Schedule: "@daily", Command: "x"}}); err == nil {
		t.Error("bad home accepted")
	}

	if len(ctl.calls) != 0 {
		t.Errorf("calls %q", ctl.calls)
	}

	if _, err := os.Stat(m.ScriptDir); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("script dir created: %v", err)
	}
}

func TestScriptDirRefusesSymlink(t *testing.T) {
	m, _ := newManager(t)
	must(t, os.Mkdir(m.ScriptDir, 0o755))
	must(t, os.Symlink(t.TempDir(), filepath.Join(m.ScriptDir, "shop")))

	if err := m.Apply(t.Context(), shop, []agentapi.CronJob{{ID: 1, Schedule: "@daily", Command: "x"}}); err == nil {
		t.Error("symlinked script dir accepted")
	}
}

func TestStatus(t *testing.T) {
	m, ctl := newManager(t)
	must(t, m.Apply(t.Context(), shop, []agentapi.CronJob{
		{ID: 1, Schedule: "@hourly", Command: "x"},
		{ID: 2, Schedule: "@daily", Command: "y", Paused: true},
	}))

	ctl.props = map[string]map[string]string{
		"hostashi-cron-shop-1.service": {
			"ActiveState": "inactive", "Result": "exit-code", "ExecMainStatus": "3",
			"ExecMainStartTimestamp": "@1700000000", "ExecMainExitTimestamp": "@1700000060",
		},
		"hostashi-cron-shop-1.timer": {"ActiveState": "active"},
		"hostashi-cron-shop-2.service": {
			"ActiveState": "inactive", "ExecMainStatus": "0", "ExecMainStartTimestamp": "",
		},
		"hostashi-cron-shop-2.timer": {"ActiveState": "inactive"},
	}
	// A paused job's timer may still report a next elapse; it is not shown.
	ctl.next = map[string]int64{"hostashi-cron-shop-1.timer": 1700003600, "hostashi-cron-shop-2.timer": 1700009999}
	ctl.calls = nil

	st, err := m.Status(t.Context(), "shop")
	must(t, err)

	if len(st.Jobs) != 2 {
		t.Fatalf("%+v", st)
	}

	if len(ctl.calls) != 1 || ctl.calls[0] != "next hostashi-cron-shop-1.timer hostashi-cron-shop-2.timer" {
		t.Errorf("calls %q", ctl.calls)
	}

	j := st.Jobs[0]
	if j.Result != "exit-code" || j.ExitStatus == nil || *j.ExitStatus != 3 || j.LastStart != 1700000000 || j.LastEnd != 1700000060 || j.Next != 1700003600 {
		t.Errorf("job 1 %+v", j)
	}

	j = st.Jobs[1]
	if !j.Paused || j.ExitStatus != nil || j.LastStart != 0 || j.Next != 0 {
		t.Errorf("job 2 %+v", j)
	}

	empty, err := m.Status(t.Context(), "blog")
	must(t, err)

	if len(empty.Jobs) != 0 || empty.Jobs == nil {
		t.Errorf("no jobs: %+v", empty)
	}
}

// TestRefreshRendersUnitsAgain: after a pool change every site's units
// carry the new set of hidden pools; sites the caller does not know stay.
func TestRefreshRendersUnitsAgain(t *testing.T) {
	m, _ := newManager(t)
	ctx := t.Context()

	must(t, m.Apply(ctx, shop, []agentapi.CronJob{{ID: 1, Schedule: "@daily", Command: "true"}}))

	gone := Site{Name: "gone", Home: "/home/gone", UID: 1002, GID: 1002}
	must(t, m.Apply(ctx, gone, []agentapi.CronJob{{ID: 2, Schedule: "@daily", Command: "true"}}))

	hidden := shop
	hidden.Hidden = []string{"/srv/vol1"}

	must(t, m.Refresh(ctx, func(name string) (Site, bool) {
		if name == "shop" {
			return hidden, true
		}

		return Site{}, false
	}))

	svc, err := os.ReadFile(filepath.Join(m.UnitDir, "hostashi-cron-shop-1.service"))
	must(t, err)

	if !strings.Contains(string(svc), "\nTemporaryFileSystem=/srv/vol1:ro\n") {
		t.Errorf("shop not refreshed:\n%s", svc)
	}

	other, err := os.ReadFile(filepath.Join(m.UnitDir, "hostashi-cron-gone-2.service"))
	must(t, err)

	if strings.Contains(string(other), "TemporaryFileSystem=") {
		t.Errorf("unknown site changed:\n%s", other)
	}
}

func TestOutput(t *testing.T) {
	m, ctl := newManager(t)
	must(t, m.Apply(t.Context(), shop, []agentapi.CronJob{{ID: 1, Schedule: "@hourly", Command: "x"}, {ID: 2, Schedule: "@daily", Command: "y"}}))

	inv := "0123456789abcdef0123456789abcdef"
	ctl.props = map[string]map[string]string{"hostashi-cron-shop-1.service": {"InvocationID": inv, "ExecMainStartTimestampMonotonic": "133924909"}}
	ctl.journal = map[string][]string{"hostashi-cron-shop-1.service " + inv: {"Hi!", "Fri Nov 27 15:35:00 CET 2026"}}

	out, err := m.Output(t.Context(), "shop", 1, "")
	must(t, err)

	if !out.Ran || out.Truncated || !slices.Equal(out.Lines, []string{"Hi!", "Fri Nov 27 15:35:00 CET 2026"}) {
		t.Errorf("%+v", out)
	}

	if ctl.start != 133924909 {
		t.Errorf("start passed to Journal: %d", ctl.start)
	}

	// Without a start time only lines with the invocation id may count.
	ctl.props["hostashi-cron-shop-1.service"]["ExecMainStartTimestampMonotonic"] = ""

	_, err = m.Output(t.Context(), "shop", 1, "")
	must(t, err)

	if ctl.start != math.MaxUint64 {
		t.Errorf("start without property: %d", ctl.start)
	}

	// Job 2 has no invocation: it has not run.
	out, err = m.Output(t.Context(), "shop", 2, "")
	must(t, err)

	if out.Ran || out.Lines == nil || len(out.Lines) != 0 {
		t.Errorf("not run: %+v", out)
	}

	if _, err := m.Output(t.Context(), "shop", 3, ""); !errors.Is(err, ErrUnknownJob) {
		t.Errorf("unknown job: %v", err)
	}

	if _, err := m.Output(t.Context(), "blog", 1, ""); !errors.Is(err, ErrUnknownJob) {
		t.Errorf("other site: %v", err)
	}

	ctl.cut = true

	out, err = m.Output(t.Context(), "shop", 1, "")
	must(t, err)

	if !out.Truncated {
		t.Errorf("cut by journalctl: %+v", out)
	}
}

func TestRunsAndRunOutput(t *testing.T) {
	m, ctl := newManager(t)
	must(t, m.Apply(t.Context(), shop, []agentapi.CronJob{{ID: 1, Schedule: "@hourly", Command: "x"}}))

	const (
		newest = "11111111111111111111111111111111"
		middle = "22222222222222222222222222222222"
		oldest = "33333333333333333333333333333333"
	)

	ctl.runs = []agentapi.CronRunEntry{
		{Invocation: newest, Start: 3_000_000, Running: true},
		{Invocation: middle, Start: 2_000_000, End: 2_100_000, Result: "success"},
		{Invocation: oldest, Start: 1_000_000, End: 1_100_000, Result: "exit-code"},
	}
	ctl.rangeLines = []string{"Hi!", "Sat Nov 28 16:42:39 CET 2026"}

	// The middle run's lines end where the newest run starts.
	out, err := m.Output(t.Context(), "shop", 1, middle)
	must(t, err)

	if !out.Ran || !slices.Equal(out.Lines, ctl.rangeLines) || len(ctl.ranges) != 1 || ctl.ranges[0] != middle+" 2000000 3000000" {
		t.Errorf("middle: %+v %q", out, ctl.ranges)
	}

	// The newest run has no next start.
	_, err = m.Output(t.Context(), "shop", 1, newest)
	must(t, err)

	if ctl.ranges[1] != newest+" 3000000 0" {
		t.Errorf("newest: %q", ctl.ranges)
	}

	if _, err := m.Output(t.Context(), "shop", 1, "44444444444444444444444444444444"); !errors.Is(err, ErrUnknownRun) {
		t.Errorf("unknown run: %v", err)
	}

	if _, err := m.Output(t.Context(), "shop", 2, middle); !errors.Is(err, ErrUnknownJob) {
		t.Errorf("unknown job: %v", err)
	}

	// Runs: the newest run is running only while systemd says so.
	ctl.props = map[string]map[string]string{"hostashi-cron-shop-1.service": {"ActiveState": "activating", "InvocationID": newest}}

	runs, err := m.Runs(t.Context(), "shop", 1)
	must(t, err)

	if len(runs.Runs) != 3 || !runs.Runs[0].Running {
		t.Errorf("running: %+v", runs)
	}

	// A reboot cut the newest run off: no end, but not running either.
	ctl.props["hostashi-cron-shop-1.service"] = map[string]string{"ActiveState": "inactive", "InvocationID": ""}

	runs, err = m.Runs(t.Context(), "shop", 1)
	must(t, err)

	if runs.Runs[0].Running {
		t.Errorf("cut off: %+v", runs)
	}

	ctl.runs = nil

	runs, err = m.Runs(t.Context(), "shop", 1)
	must(t, err)

	if runs.Runs == nil || len(runs.Runs) != 0 {
		t.Errorf("none: %+v", runs)
	}

	if _, err := m.Runs(t.Context(), "blog", 1); !errors.Is(err, ErrUnknownJob) {
		t.Errorf("other site: %v", err)
	}
}

func TestLimitOutput(t *testing.T) {
	many := make([]string, 250)
	for i := range many {
		many[i] = strconv.Itoa(i)
	}

	got, cut := limitOutput(many)
	if !cut || len(got) != agentapi.CronOutputMaxLines || got[0] != "50" || got[len(got)-1] != "249" {
		t.Errorf("lines: %v %d %q", cut, len(got), got[0])
	}

	// A long line is cut at a rune boundary; "ä" is two bytes.
	long := strings.Repeat("ä", agentapi.CronOutputMaxLineBytes)

	got, cut = limitOutput([]string{"a", long})
	if !cut || len(got) != 2 || len(got[1]) > agentapi.CronOutputMaxLineBytes || !utf8.ValidString(got[1]) {
		t.Errorf("long line: %v %d", cut, len(got[1]))
	}

	// The total stays below the limit; the last lines are kept.
	big := make([]string, 40)
	for i := range big {
		big[i] = strings.Repeat("x", agentapi.CronOutputMaxLineBytes)
	}

	big[len(big)-1] = "last"

	got, cut = limitOutput(big)

	total := 0
	for _, l := range got {
		total += len(l) + 1
	}

	if !cut || total > agentapi.CronOutputMaxBytes || got[len(got)-1] != "last" {
		t.Errorf("total: %v %d", cut, total)
	}

	got, cut = limitOutput([]string{"ok\xff"})
	if cut || got[0] != "ok\uFFFD" {
		t.Errorf("invalid UTF-8: %q", got)
	}

	if got, cut := limitOutput(nil); cut || got == nil || len(got) != 0 {
		t.Errorf("empty: %v %v", got, cut)
	}
}
