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
