// SPDX-License-Identifier: AGPL-3.0-or-later

// Package cron checks cron jobs of a site and turns them into systemd timer
// and service units (ADR-0028). Schedules pass a validator for the five
// cron fields and the usual keywords, never free text; the command is
// written to a script file, so nothing a user typed reaches unit syntax.
// This package only computes; the agent writes the files.
package cron

import (
	"errors"
	"fmt"
	"slices"
	"strconv"
	"strings"
)

// ErrSchedule marks a schedule the validator does not accept.
var ErrSchedule = errors.New("cron: invalid schedule")

// Keywords and the five fields they stand for. @reboot is not accepted.
var keywords = map[string]string{
	"@hourly":   "0 * * * *",
	"@daily":    "0 0 * * *",
	"@midnight": "0 0 * * *",
	"@weekly":   "0 0 * * 0",
	"@monthly":  "0 0 1 * *",
	"@yearly":   "0 0 1 1 *",
	"@annually": "0 0 1 1 *",
}

const maxSchedule = 200

type field struct {
	name     string
	min, max int
	names    []string // names[i] is the value min+i
}

var (
	months   = []string{"jan", "feb", "mar", "apr", "may", "jun", "jul", "aug", "sep", "oct", "nov", "dec"}
	weekdays = []string{"sun", "mon", "tue", "wed", "thu", "fri", "sat"}
	// systemd's names, Sunday first like cron.
	systemdDays = []string{"Sun", "Mon", "Tue", "Wed", "Thu", "Fri", "Sat"}

	fields = [5]field{
		{name: "minute", min: 0, max: 59},
		{name: "hour", min: 0, max: 23},
		{name: "day of month", min: 1, max: 31},
		{name: "month", min: 1, max: 12, names: months},
		// 7 is Sunday as well; it is folded into 0.
		{name: "day of week", min: 0, max: 7, names: weekdays},
	}
)

// OnCalendar translates a cron schedule into the OnCalendar= values of a
// systemd timer. Usually one; two when both day of month and day of week
// are restricted, because cron then runs on either day and systemd joins
// several OnCalendar= lines with "or".
func OnCalendar(schedule string) ([]string, error) {
	s := strings.TrimSpace(schedule)
	if len(s) > maxSchedule {
		return nil, fmt.Errorf("%w: longer than %d characters", ErrSchedule, maxSchedule)
	}

	if strings.HasPrefix(s, "@") {
		k, ok := keywords[strings.ToLower(s)]
		if !ok {
			return nil, fmt.Errorf("%w: unknown keyword %q", ErrSchedule, s)
		}

		s = k
	}

	parts := strings.Fields(s)
	if len(parts) != 5 {
		return nil, fmt.Errorf("%w: want five fields (minute hour day-of-month month day-of-week) or a keyword", ErrSchedule)
	}

	var vals [5][]int // nil means every value

	for i, p := range parts {
		v, err := parseField(p, fields[i])
		if err != nil {
			return nil, err
		}

		vals[i] = v
	}

	if vals[4] != nil {
		vals[4] = foldSunday(vals[4])
		if len(vals[4]) == 7 {
			vals[4] = nil
		}
	}

	minute, hour, month := join(vals[0], 2), join(vals[1], 2), join(vals[3], 2)
	line := func(dom, dow []int) string {
		l := "*-" + month + "-" + join(dom, 2) + " " + hour + ":" + minute + ":00"
		if dow != nil {
			l = days(dow) + " " + l
		}

		return l
	}

	// cron: when both day fields are restricted (neither starts with "*"),
	// a day matches if either does. Otherwise both must match.
	if !strings.HasPrefix(parts[2], "*") && !strings.HasPrefix(parts[4], "*") {
		return []string{line(vals[2], nil), line(nil, vals[4])}, nil
	}

	return []string{line(vals[2], vals[4])}, nil
}

// parseField returns the sorted values of one field, or nil for "*".
func parseField(p string, f field) ([]int, error) {
	if p == "*" {
		return nil, nil
	}

	set := map[int]bool{}

	for item := range strings.SplitSeq(p, ",") {
		rng, stepStr, hasStep := strings.Cut(item, "/")

		step := 1

		if hasStep {
			n, err := strconv.Atoi(stepStr)
			if err != nil || n < 1 || n > f.max-f.min+1 {
				return nil, fmt.Errorf("%w: %s: step %q", ErrSchedule, f.name, stepStr)
			}

			step = n
		}

		lo, hi := f.min, f.max

		switch {
		case rng == "*":
			if f.max == 7 {
				hi = 6 // */n on weekdays covers Sunday once
			}
		case strings.Contains(rng, "-"):
			a, b, _ := strings.Cut(rng, "-")

			var err error
			if lo, err = value(a, f); err != nil {
				return nil, err
			}

			if hi, err = value(b, f); err != nil {
				return nil, err
			}

			if lo > hi {
				return nil, fmt.Errorf("%w: %s: range %q runs backwards", ErrSchedule, f.name, rng)
			}
		default:
			v, err := value(rng, f)
			if err != nil {
				return nil, err
			}

			// "5/10" means from 5 to the end, as in cron.
			lo, hi = v, v
			if hasStep {
				hi = f.max
			}
		}

		for v := lo; v <= hi; v += step {
			set[v] = true
		}
	}

	out := make([]int, 0, len(set))
	for v := range set {
		out = append(out, v)
	}

	slices.Sort(out)

	if f.max != 7 && len(out) == f.max-f.min+1 {
		return nil, nil
	}

	return out, nil
}

func value(s string, f field) (int, error) {
	if s == "" || len(s) > 3 {
		return 0, fmt.Errorf("%w: %s: %q", ErrSchedule, f.name, s)
	}

	if i := slices.Index(f.names, strings.ToLower(s)); i >= 0 {
		return f.min + i, nil
	}

	if strings.Trim(s, "0123456789") != "" {
		return 0, fmt.Errorf("%w: %s: %q", ErrSchedule, f.name, s)
	}

	v, _ := strconv.Atoi(s)
	if v < f.min || v > f.max {
		return 0, fmt.Errorf("%w: %s: %d is outside %d-%d", ErrSchedule, f.name, v, f.min, f.max)
	}

	return v, nil
}

func foldSunday(v []int) []int {
	out := []int{}

	for _, d := range v {
		if d == 7 {
			d = 0
		}

		if !slices.Contains(out, d) {
			out = append(out, d)
		}
	}

	slices.Sort(out)

	return out
}

func join(v []int, width int) string {
	if v == nil {
		return "*"
	}

	s := make([]string, len(v))
	for i, n := range v {
		s[i] = fmt.Sprintf("%0*d", width, n)
	}

	return strings.Join(s, ",")
}

// days writes weekdays as systemd normalizes them: Monday first, and runs
// of three or more days as "Mon..Fri", so the unit shows what systemd runs.
func days(v []int) string {
	var monFirst []int // 0 is Monday, 6 is Sunday

	for _, d := range v {
		monFirst = append(monFirst, (d+6)%7)
	}

	slices.Sort(monFirst)

	var out []string

	for i := 0; i < len(monFirst); {
		j := i
		for j+1 < len(monFirst) && monFirst[j+1] == monFirst[j]+1 {
			j++
		}

		name := func(k int) string { return systemdDays[(monFirst[k]+1)%7] }

		switch {
		case j-i >= 2:
			out = append(out, name(i)+".."+name(j))
		default:
			for k := i; k <= j; k++ {
				out = append(out, name(k))
			}
		}

		i = j + 1
	}

	return strings.Join(out, ",")
}
