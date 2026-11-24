// SPDX-License-Identifier: AGPL-3.0-or-later

package store

import (
	"errors"
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

		r, err := s.CreateScanRun(ctx, site.ID, ScanSourcePanel, 0, now)
		if err != nil {
			t.Fatal(err)
		}

		if _, err := s.CreateScanRun(ctx, site.ID, ScanSourceAPI, 0, now); !errors.Is(err, ErrScanBusy) {
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
	r, err := s.CreateScanRun(ctx, site.ID, ScanSourceSchedule, 0, now)
	if err != nil {
		t.Fatal(err)
	}

	if err := s.FailScanRun(ctx, r.ID, "clamscan exited with 50", now); err != nil {
		t.Fatal(err)
	}

	for range MaxScanHistory {
		r, err := s.CreateScanRun(ctx, site.ID, ScanSourceSchedule, 0, now)
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
