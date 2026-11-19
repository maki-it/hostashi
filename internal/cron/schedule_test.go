// SPDX-License-Identifier: AGPL-3.0-or-later

package cron

import (
	"context"
	"errors"
	"os/exec"
	"slices"
	"strings"
	"testing"
	"time"
)

var scheduleCases = []struct {
	in   string
	want []string
}{
	{"* * * * *", []string{"*-*-* *:*:00"}},
	{"*/5 * * * *", []string{"*-*-* *:00,05,10,15,20,25,30,35,40,45,50,55:00"}},
	{"0 3 * * *", []string{"*-*-* 03:00:00"}},
	{"15,45 8-10 * * 1-5", []string{"Mon..Fri *-*-* 08,09,10:15,45:00"}},
	{"0 0 1 * *", []string{"*-*-01 00:00:00"}},
	{"30 2 * jan,JUL *", []string{"*-01,07-* 02:30:00"}},
	{"0 12 * * sun", []string{"Sun *-*-* 12:00:00"}},
	{"0 12 * * 7", []string{"Sun *-*-* 12:00:00"}},
	{"0 12 * * 5-7", []string{"Fri..Sun *-*-* 12:00:00"}},
	{"0 12 * * 0-7", []string{"*-*-* 12:00:00"}},
	{"0 0 * * */2", []string{"Tue,Thu,Sat,Sun *-*-* 00:00:00"}},
	{"1-30/10 * * * *", []string{"*-*-* *:01,11,21:00"}},
	{"50/5 * * * *", []string{"*-*-* *:50,55:00"}},
	{"0-59 0-23 1-31 1-12 *", []string{"*-*-* *:*:00"}},
	// Both day fields restricted: either matches, as cron does.
	{"0 6 1,15 * mon", []string{"*-*-01,15 06:00:00", "Mon *-*-* 06:00:00"}},
	// A step on "*" still counts as unrestricted for that rule.
	{"0 6 1 * */1", []string{"*-*-01 06:00:00"}},
	{"0 0 * * sat,sun,mon", []string{"Mon,Sat,Sun *-*-* 00:00:00"}},
	{"0 0 * * 1,2,4,5,6", []string{"Mon,Tue,Thu..Sat *-*-* 00:00:00"}},
	{"@hourly", []string{"*-*-* *:00:00"}},
	{"@daily", []string{"*-*-* 00:00:00"}},
	{"@midnight", []string{"*-*-* 00:00:00"}},
	{"@weekly", []string{"Sun *-*-* 00:00:00"}},
	{"@monthly", []string{"*-*-01 00:00:00"}},
	{"@Yearly", []string{"*-01-01 00:00:00"}},
	{"  5  4 * * *  ", []string{"*-*-* 04:05:00"}},
}

func TestOnCalendar(t *testing.T) {
	for _, c := range scheduleCases {
		got, err := OnCalendar(c.in)
		if err != nil {
			t.Errorf("%q: %v", c.in, err)

			continue
		}

		if !slices.Equal(got, c.want) {
			t.Errorf("%q: got %q, want %q", c.in, got, c.want)
		}
	}
}

func TestOnCalendarRejects(t *testing.T) {
	for _, in := range []string{
		"", "@reboot", "@every 5m", "* * * *", "* * * * * *", "60 * * * *",
		"* 24 * * *", "* * 0 * *", "* * 32 * *", "* * * 13 *", "* * * * 8",
		"5-1 * * * *", "*/0 * * * *", "*/61 * * * *", "a * * * *", "1,,2 * * * *",
		"-1 * * * *", "1- * * * *", "* * * foo *", "* * * * monday", "1/2/3 * * * *",
		"+1 * * * *", "0x1 * * * *", "* * * * mon-", "; rm -rf / * * * *",
		"0 0 * * *\n0 1", strings.Repeat("1,", 120) + "1 * * * *",
	} {
		if got, err := OnCalendar(in); !errors.Is(err, ErrSchedule) {
			t.Errorf("%q accepted: %q %v", in, got, err)
		}
	}
}

// TestOnCalendarSystemd checks every translation with systemd's own parser
// and, for a few schedules, the next elapse against a known time.
func TestOnCalendarSystemd(t *testing.T) {
	analyze, err := exec.LookPath("systemd-analyze")
	if err != nil {
		t.Skip("no systemd-analyze")
	}

	for _, c := range scheduleCases {
		for _, l := range c.want {
			ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
			out, err := exec.CommandContext(ctx, analyze, "calendar", l).CombinedOutput()

			cancel()

			if err != nil {
				t.Errorf("%q: systemd rejects %q: %v\n%s", c.in, l, err, out)

				continue
			}

			// systemd normalizes; the normalized form must equal ours, so
			// that the unit file shows what systemd runs.
			if !strings.Contains(string(out), "Normalized form: "+l+"\n") {
				t.Errorf("%q: systemd normalizes %q differently:\n%s", c.in, l, out)
			}
		}
	}
}

func FuzzOnCalendar(f *testing.F) {
	for _, c := range scheduleCases {
		f.Add(c.in)
	}

	f.Fuzz(func(t *testing.T, in string) {
		lines, err := OnCalendar(in)
		if err != nil {
			return
		}

		if len(lines) < 1 || len(lines) > 2 {
			t.Fatalf("%q: %d lines", in, len(lines))
		}

		for _, l := range lines {
			if strings.Trim(l, "0123456789*-:,. MTWFSadehinortu") != "" {
				t.Fatalf("%q: unexpected characters in %q", in, l)
			}
		}
	})
}
