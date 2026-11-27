// SPDX-License-Identifier: AGPL-3.0-or-later

package phpctl

import (
	"strings"
	"testing"
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
		if _, _, err := c.Journal(t.Context(), u, inv); err == nil || !strings.Contains(err.Error(), "refusing") {
			t.Errorf("unit %q: %v", u, err)
		}
	}

	for _, i := range []string{"", "0123456789ABCDEF0123456789ABCDEF", inv + "0", inv[:31], "0123456789abcdef0123456789abcde_", inv + " _UID=0"} {
		if _, _, err := c.Journal(t.Context(), "hostashi-cron-shop-1.service", i); err == nil || !strings.Contains(err.Error(), "refusing") {
			t.Errorf("invocation %q: %v", i, err)
		}
	}
}

func TestParseJournal(t *testing.T) {
	out := `{"__CURSOR":"s=1","MESSAGE":"Hi!","_BOOT_ID":"x"}` + "\n" +
		`{"MESSAGE":[111,107,255]}` + "\n" +
		`{"MESSAGE":null}` + "\n" +
		`{"MESSAGE":"trailing newline\n"}` + "\n" +
		"\n"

	lines, cut := parseJournal(out, false)
	if cut || len(lines) != 3 || lines[0] != "Hi!" || lines[1] != "ok\xff" || lines[2] != "trailing newline" {
		t.Errorf("%q %v", lines, cut)
	}

	// Output cut at the limit: the partial last object is dropped.
	lines, cut = parseJournal(`{"MESSAGE":"a"}`+"\n"+`{"MESSAGE":"b`, true)
	if !cut || len(lines) != 1 || lines[0] != "a" {
		t.Errorf("cut: %q %v", lines, cut)
	}

	if lines, cut := parseJournal("", false); cut || lines == nil || len(lines) != 0 {
		t.Errorf("empty: %q %v", lines, cut)
	}
}
