// SPDX-License-Identifier: AGPL-3.0-or-later

package store

import (
	"errors"
	"maps"
	"slices"
	"strings"
	"testing"
	"time"
)

func TestScanRunsAndFindings(t *testing.T) {
	s := open(t, dbPath(t))
	ctx := t.Context()
	now := time.Unix(2_000_000, 0).UTC()

	site, err := s.CreateSite(ctx, "blog", "default", "8.4", []string{"blog.example.com"}, now)
	if err != nil {
		t.Fatal(err)
	}

	if got, err := s.SiteScanSchedule(ctx, site.ID); err != nil || got != ScanScheduleDefault {
		t.Fatalf("default schedule %q %v", got, err)
	}

	if err := s.SetSiteScanSchedule(ctx, site.ID, ScanScheduleDaily, 0, now); err != nil {
		t.Fatal(err)
	}

	if got, _ := s.ScanSchedules(ctx); got[site.ID] != ScanScheduleDaily {
		t.Fatalf("schedules %v", got)
	}

	if err := s.SetSiteScanSchedule(ctx, site.ID, "hourly", 0, now); err == nil {
		t.Fatal("hourly accepted")
	}

	shaA, shaB := strings.Repeat("a", 64), strings.Repeat("b", 64)

	run := func(files ...FoundFile) []ScanFinding {
		t.Helper()

		r, err := s.CreateScanRun(ctx, site.ID, ScanSourcePanel, 0, nil, now)
		if err != nil {
			t.Fatal(err)
		}

		if _, err := s.CreateScanRun(ctx, site.ID, ScanSourceAPI, 0, nil, now); !errors.Is(err, ErrScanBusy) {
			t.Fatalf("second run: %v", err)
		}

		if err := s.StartScanRun(ctx, r.ID, now); err != nil {
			t.Fatal(err)
		}

		fresh, err := s.FinishScanRun(ctx, r, files, now)
		if err != nil {
			t.Fatal(err)
		}

		return fresh
	}

	a := FoundFile{Path: "a.php", Signature: "Php.Shell-1", SHA256: shaA, Size: 3}
	b := FoundFile{Path: "b.php", Signature: "Php.Shell-1", SHA256: shaB, Size: 4}

	if fresh := run(a, b); len(fresh) != 2 {
		t.Fatalf("first run %+v", fresh)
	}

	// Seen again: nothing new.
	if fresh := run(a, b); len(fresh) != 0 {
		t.Fatalf("second run %+v", fresh)
	}

	all, err := s.ScanFindings(ctx, site.ID)
	if err != nil || len(all) != 2 {
		t.Fatalf("findings %+v %v", all, err)
	}

	byPath := map[string]ScanFinding{}
	for _, f := range all {
		byPath[f.Path] = f
	}

	// Ignore a: the same content elsewhere is ignored too.
	if err := s.SetScanFindingState(ctx, site.ID, byPath["a.php"].ID, FindingOpen, FindingIgnored, "", 0, now); err != nil {
		t.Fatal(err)
	}

	if err := s.SetScanFindingState(ctx, site.ID, byPath["a.php"].ID, FindingOpen, FindingIgnored, "", 0, now); !errors.Is(err, ErrNotFound) {
		t.Fatalf("wrong from state: %v", err)
	}

	copyOfA := FoundFile{Path: "copy.php", Signature: a.Signature, SHA256: shaA, Size: 3}
	if fresh := run(a, b, copyOfA); len(fresh) != 0 {
		t.Fatalf("ignored copy reported %+v", fresh)
	}

	// Quarantine b; it stays quarantined while it is not seen, and is open
	// again when it comes back.
	if err := s.SetScanFindingState(ctx, site.ID, byPath["b.php"].ID, FindingOpen, FindingQuarantined, strings.Repeat("c", 32), 0, now); err != nil {
		t.Fatal(err)
	}

	if fresh := run(a, copyOfA); len(fresh) != 0 {
		t.Fatalf("third run %+v", fresh)
	}

	if f, _ := s.ScanFinding(ctx, site.ID, byPath["b.php"].ID); f.State != FindingQuarantined || f.QuarantineID == "" {
		t.Fatalf("quarantined %+v", f)
	}

	if fresh := run(a, b, copyOfA); len(fresh) != 1 || fresh[0].Path != "b.php" {
		t.Fatalf("b back %+v", fresh)
	}

	if f, _ := s.ScanFinding(ctx, site.ID, byPath["b.php"].ID); f.State != FindingOpen || f.QuarantineID != "" {
		t.Fatalf("reopened %+v", f)
	}

	// An open finding not seen again is gone.
	run(a, copyOfA)

	if f, _ := s.ScanFinding(ctx, site.ID, byPath["b.php"].ID); f.State != FindingGone {
		t.Fatalf("gone %+v", f)
	}

	if open, _ := s.OpenScanFindings(ctx); open[site.ID] != 0 {
		t.Fatalf("open %v", open)
	}

	// A failed run keeps its error; the history is capped.
	r, err := s.CreateScanRun(ctx, site.ID, ScanSourceSchedule, 0, nil, now)
	if err != nil {
		t.Fatal(err)
	}

	if err := s.FailScanRun(ctx, r.ID, "clamscan exited with 50", now); err != nil {
		t.Fatal(err)
	}

	for range MaxScanHistory {
		r, err := s.CreateScanRun(ctx, site.ID, ScanSourceSchedule, 0, nil, now)
		if err != nil {
			t.Fatal(err)
		}

		if err := s.FailScanRun(ctx, r.ID, "x", now); err != nil {
			t.Fatal(err)
		}
	}

	if runs, _ := s.ScanRuns(ctx, site.ID, 1000); len(runs) != MaxScanHistory {
		t.Fatalf("history %d", len(runs))
	}

	// Deleting the site takes its scan rows along.
	if err := s.DeleteSite(ctx, "blog"); err != nil {
		t.Fatal(err)
	}

	if all, _ := s.ScanFindings(ctx, site.ID); len(all) != 0 {
		t.Fatalf("findings after delete %+v", all)
	}
}

func TestRealtimeFindings(t *testing.T) {
	s := open(t, dbPath(t))
	ctx := t.Context()
	now := time.Date(2026, 11, 24, 10, 0, 0, 0, time.Local)

	site, err := s.CreateSite(ctx, "blog", "default", "8.4", []string{"blog.example.com"}, now)
	if err != nil {
		t.Fatal(err)
	}

	a := FoundFile{Path: "a.php", Signature: "Php.Shell-1", SHA256: strings.Repeat("a", 64), Size: 3}
	b := FoundFile{Path: "b.php", Signature: "Php.Shell-1", SHA256: strings.Repeat("b", 64), Size: 4}

	r1, fresh, err := s.RecordRealtimeFindings(ctx, site.ID, []FoundFile{a}, now)
	if err != nil || len(fresh) != 1 || r1.Source != ScanSourceRealtime || r1.State != ScanSucceeded || r1.Found != 1 {
		t.Fatalf("first %+v %+v %v", r1, fresh, err)
	}

	// The same day: the same run; a stays open although not named again.
	r2, fresh, err := s.RecordRealtimeFindings(ctx, site.ID, []FoundFile{b, a}, now.Add(time.Hour))
	if err != nil || r2.ID != r1.ID || len(fresh) != 1 || fresh[0].Path != "b.php" {
		t.Fatalf("second %+v %+v %v", r2, fresh, err)
	}

	runs, _ := s.ScanRuns(ctx, site.ID, 10)
	if len(runs) != 1 || runs[0].Found != 3 || runs[0].NewFound != 2 {
		t.Fatalf("runs %+v", runs)
	}

	// The next day: a new run.
	if r3, _, err := s.RecordRealtimeFindings(ctx, site.ID, []FoundFile{b}, now.Add(24*time.Hour)); err != nil || r3.ID == r1.ID {
		t.Fatalf("next day %+v %v", r3, err)
	}

	// A running scheduled scan is not disturbed by the real-time run.
	if _, err := s.CreateScanRun(ctx, site.ID, ScanSourcePanel, 0, nil, now); err != nil {
		t.Fatal(err)
	}
}

// A scan of a selection marks gone only what it covered (ADR-0064).
func TestScanRunPaths(t *testing.T) {
	s := open(t, dbPath(t))
	ctx := t.Context()
	now := time.Date(2026, 11, 25, 10, 0, 0, 0, time.Local)

	site, err := s.CreateSite(ctx, "blog", "default", "8.4", []string{"blog.example.com"}, now)
	if err != nil {
		t.Fatal(err)
	}

	file := func(p string) FoundFile {
		return FoundFile{Path: p, Signature: "Php.Shell-1", SHA256: strings.Repeat("c", 64), Size: 1}
	}

	run := func(paths []string, files ...FoundFile) ScanRun {
		t.Helper()

		r, err := s.CreateScanRun(ctx, site.ID, ScanSourcePanel, 0, paths, now)
		if err != nil {
			t.Fatal(err)
		}

		if err := s.StartScanRun(ctx, r.ID, now); err != nil {
			t.Fatal(err)
		}

		// The poller passes the run as it reads it back.
		active, err := s.ActiveScanRuns(ctx)
		if err != nil || len(active) != 1 {
			t.Fatalf("active %+v %v", active, err)
		}

		if _, err := s.FinishScanRun(ctx, active[0], files, now); err != nil {
			t.Fatal(err)
		}

		return active[0]
	}

	run(nil, file("htdocs/up/a.php"), file("htdocs/upload.php"), file("htdocs/x.php"), file("other/b.php"))

	r := run([]string{"htdocs/up", "htdocs/x.php"})
	if !slices.Equal(r.Paths, []string{"htdocs/up", "htdocs/x.php"}) {
		t.Fatalf("paths %q", r.Paths)
	}

	state := map[string]string{}
	all, _ := s.ScanFindings(ctx, site.ID)

	for _, f := range all {
		state[f.Path] = f.State
	}

	want := map[string]string{"htdocs/up/a.php": FindingGone, "htdocs/upload.php": FindingOpen, "htdocs/x.php": FindingGone, "other/b.php": FindingOpen}
	if !maps.Equal(state, want) {
		t.Fatalf("states %v, want %v", state, want)
	}

	runs, _ := s.ScanRuns(ctx, site.ID, 10)
	if len(runs) != 2 || len(runs[0].Paths) != 2 || runs[1].Paths != nil {
		t.Fatalf("runs %+v", runs)
	}

	// The column takes only a JSON array.
	if _, err := s.w.ExecContext(ctx, `UPDATE scan_runs SET paths = '{"a":1}' WHERE id = ?`, r.ID); err == nil {
		t.Fatal("object accepted as paths")
	}
}

func TestCovers(t *testing.T) {
	for _, tc := range []struct {
		paths []string
		p     string
		want  bool
	}{
		{nil, "a/b", true},
		{[]string{"a"}, "a", true},
		{[]string{"a"}, "a/b/c", true},
		{[]string{"a"}, "ab/c", false},
		{[]string{"a/b"}, "a", false},
		{[]string{"x", "a/b"}, "a/b/c", true},
	} {
		if got := Covers(tc.paths, tc.p); got != tc.want {
			t.Errorf("Covers(%q, %q) = %v", tc.paths, tc.p, got)
		}
	}
}
