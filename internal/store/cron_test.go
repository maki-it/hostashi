// SPDX-License-Identifier: AGPL-3.0-or-later

package store

import (
	"errors"
	"testing"
	"time"
)

func TestCronJobs(t *testing.T) {
	s := open(t, dbPath(t))
	ctx := t.Context()
	now := time.Unix(2_000_000, 0)

	site, err := s.CreateSite(ctx, "blog", "default", "", []string{"blog.example.com"}, now)
	if err != nil {
		t.Fatal(err)
	}

	nice := 5

	a, err := s.InsertCronJob(ctx, CronJob{SiteID: site.ID, Schedule: "@hourly", Command: "php a.php"}, 2, now)
	if err != nil || a.ID == 0 || a.Nice != nil || a.Paused {
		t.Fatalf("insert %+v %v", a, err)
	}

	b, err := s.InsertCronJob(ctx, CronJob{SiteID: site.ID, Schedule: "@daily", Command: "b", TimeoutSec: 60, Nice: &nice, Paused: true}, 2, now)
	if err != nil || b.Nice == nil || *b.Nice != 5 || !b.Paused || b.TimeoutSec != 60 {
		t.Fatalf("insert %+v %v", b, err)
	}

	if _, err := s.InsertCronJob(ctx, CronJob{SiteID: site.ID, Schedule: "@daily", Command: "c"}, 2, now); !errors.Is(err, ErrTooMany) {
		t.Fatalf("limit: %v", err)
	}

	changed := a
	changed.Command, changed.Paused = "php other.php", true

	old, err := s.UpdateCronJob(ctx, changed, now.Add(time.Minute))
	if err != nil || old.Command != "php a.php" {
		t.Fatalf("update %+v %v", old, err)
	}

	if got, _ := s.CronJob(ctx, site.ID, a.ID); got.Command != "php other.php" || !got.Paused {
		t.Fatalf("after update %+v", got)
	}

	if err := s.RestoreCronJob(ctx, old); err != nil {
		t.Fatal(err)
	}

	if got, _ := s.CronJob(ctx, site.ID, a.ID); got.Command != "php a.php" || got.Paused {
		t.Fatalf("restored %+v", got)
	}

	gone, err := s.DeleteCronJob(ctx, site.ID, b.ID)
	if err != nil || gone.ID != b.ID {
		t.Fatalf("delete %+v %v", gone, err)
	}

	if _, err := s.CronJob(ctx, site.ID, b.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("deleted: %v", err)
	}

	if err := s.RestoreCronJob(ctx, gone); err != nil {
		t.Fatal(err)
	}

	if _, err := s.DeleteCronJob(ctx, site.ID+1, a.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("other site: %v", err)
	}

	jobs, err := s.CronJobs(ctx, site.ID)
	if err != nil || len(jobs) != 2 || jobs[0].ID != a.ID || jobs[1].ID != b.ID {
		t.Fatalf("list %+v %v", jobs, err)
	}

	// A deleted site takes its jobs with it.
	if err := s.DeleteSite(ctx, "blog"); err != nil {
		t.Fatal(err)
	}

	if jobs, _ := s.CronJobs(ctx, site.ID); len(jobs) != 0 {
		t.Fatalf("left %+v", jobs)
	}
}
