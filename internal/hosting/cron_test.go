// SPDX-License-Identifier: AGPL-3.0-or-later

package hosting

import (
	"context"
	"errors"
	"testing"

	"github.com/maki-it/hostashi/internal/agentapi"
	"github.com/maki-it/hostashi/internal/cron"
)

type fakeCron struct {
	applied map[string][]agentapi.CronJob
	runs    []string
	fail    error
}

func (f *fakeCron) CronApply(_ context.Context, req agentapi.CronApplyRequest) error {
	if f.fail != nil {
		return f.fail
	}

	f.applied[req.Site] = req.Jobs

	return nil
}

func (f *fakeCron) CronRun(_ context.Context, site string, _ int64) error {
	f.runs = append(f.runs, site)

	return f.fail
}

func (f *fakeCron) CronStatus(_ context.Context, site string) (agentapi.CronSiteStatus, error) {
	return agentapi.CronSiteStatus{Site: site, Jobs: []agentapi.CronJobStatus{}}, f.fail
}

func TestCronJobs(t *testing.T) {
	s, _, st := newService(t)
	ctx := t.Context()

	if _, err := s.Create(ctx, 1, CreateRequest{Name: "shop", Domains: []string{"shop.example.com"}}); err != nil {
		t.Fatal(err)
	}

	in := CronInput{Schedule: "*/5 * * * *", Command: "php wp-cron.php"}

	if _, err := s.CreateCronJob(ctx, 1, "shop", in); !errors.Is(err, ErrNoCron) {
		t.Fatalf("without agent: %v", err)
	}

	ag := &fakeCron{applied: map[string][]agentapi.CronJob{}}
	s.EnableCron(ag)

	j, err := s.CreateCronJob(ctx, 1, "shop", in)
	if err != nil {
		t.Fatal(err)
	}

	if got := ag.applied["shop"]; len(got) != 1 || got[0].ID != j.ID || got[0].Command != in.Command {
		t.Fatalf("applied %+v", got)
	}

	for name, bad := range map[string]CronInput{
		"reboot":  {Schedule: "@reboot", Command: "x"},
		"newline": {Schedule: "@daily", Command: "a\nb"},
		"timeout": {Schedule: "@daily", Command: "x", TimeoutSec: 10},
	} {
		if _, err := s.CreateCronJob(ctx, 1, "shop", bad); !errors.Is(err, ErrInvalid) {
			t.Errorf("%s: %v", name, err)
		}
	}

	if _, err := s.CreateCronJob(ctx, 1, "nope", in); !errors.Is(err, ErrNotFound) {
		t.Errorf("unknown site: %v", err)
	}

	// A refused apply leaves the database as it was.
	ag.fail = errDown

	if _, err := s.CreateCronJob(ctx, 1, "shop", in); !errors.Is(err, errDown) {
		t.Fatalf("failed create: %v", err)
	}

	paused := in
	paused.Paused = true

	if _, err := s.UpdateCronJob(ctx, 1, "shop", j.ID, paused); !errors.Is(err, errDown) {
		t.Fatalf("failed update: %v", err)
	}

	if err := s.DeleteCronJob(ctx, 1, "shop", j.ID); !errors.Is(err, errDown) {
		t.Fatalf("failed delete: %v", err)
	}

	jobs, err := s.CronJobs(ctx, "shop")
	if err != nil || len(jobs) != 1 || jobs[0].Paused {
		t.Fatalf("after failures %+v %v", jobs, err)
	}

	ag.fail = nil

	got, err := s.UpdateCronJob(ctx, 1, "shop", j.ID, paused)
	if err != nil || !got.Paused || !ag.applied["shop"][0].Paused {
		t.Fatalf("pause %+v %v", got, err)
	}

	if _, err := s.UpdateCronJob(ctx, 1, "shop", j.ID+99, paused); !errors.Is(err, ErrCronNotFound) {
		t.Fatalf("unknown job: %v", err)
	}

	if err := s.RunCronJob(ctx, 1, "shop", j.ID); err != nil || len(ag.runs) != 1 {
		t.Fatalf("run %v %v", ag.runs, err)
	}

	if err := s.DeleteCronJob(ctx, 1, "shop", j.ID); err != nil || len(ag.applied["shop"]) != 0 {
		t.Fatalf("delete %v %+v", err, ag.applied)
	}

	// The limit per site.
	for range cron.MaxJobsPerSite {
		if _, err := s.CreateCronJob(ctx, 1, "shop", in); err != nil {
			t.Fatal(err)
		}
	}

	if _, err := s.CreateCronJob(ctx, 1, "shop", in); !errors.Is(err, ErrCronLimit) {
		t.Fatalf("limit: %v", err)
	}

	// Sync sends every site's list again.
	ag.applied = map[string][]agentapi.CronJob{}
	if err := s.SyncCron(ctx); err != nil || len(ag.applied["shop"]) != cron.MaxJobsPerSite {
		t.Fatalf("sync %v %d", err, len(ag.applied["shop"]))
	}

	// The audit log never holds the command.
	var n int
	if err := st.RawCount(ctx, `SELECT count(*) FROM audit_log WHERE detail LIKE ?`, "%wp-cron%", &n); err != nil || n != 0 {
		t.Fatalf("command in audit log: %d %v", n, err)
	}
}
