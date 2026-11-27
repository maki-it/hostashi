// SPDX-License-Identifier: AGPL-3.0-or-later

package store

import (
	"errors"
	"slices"
	"testing"
	"time"

	"github.com/maki-it/hostashi/internal/agentapi"
)

func TestCopySiteSettings(t *testing.T) {
	s, _, uid := grantsEnv(t)
	ctx := t.Context()
	now := time.Unix(2_000_000, 0)

	src, err := s.SiteByName(ctx, "alpha")
	if err != nil {
		t.Fatal(err)
	}

	if _, _, err := s.SetSiteGrants(ctx, uid, []string{"alpha"}, now); err != nil {
		t.Fatal(err)
	}

	if _, err := s.SetSiteWebOptions(ctx, "alpha", WebOptions{Dir: "public", AssetCache: true}, now); err != nil {
		t.Fatal(err)
	}

	hdr := agentapi.WebHeaders{NoSniff: true, FrameOptions: agentapi.FrameDeny}
	if _, err := s.SetSiteHeaders(ctx, SiteHeaders{SiteID: src.ID, Headers: hdr}, now); err != nil {
		t.Fatal(err)
	}

	for _, paused := range []bool{false, true} {
		if _, err := s.InsertCronJob(ctx, CronJob{SiteID: src.ID, Schedule: "*/5 * * * *", Command: "php cron.php", Paused: paused}, 10, now); err != nil {
			t.Fatal(err)
		}
	}

	dst, err := s.CreateSite(ctx, "alpha-stage", "default", "8.4", []string{"stage.alpha.example"}, now)
	if err != nil {
		t.Fatal(err)
	}

	if err := s.CopySiteSettings(ctx, src.ID, dst.ID, CloneSettings{PauseCron: true}, now); err != nil {
		t.Fatal(err)
	}

	got, err := s.SiteByName(ctx, "alpha-stage")
	if err != nil {
		t.Fatal(err)
	}

	if got.WebDir != "public" || !got.AssetCache || !slices.Equal(got.Domains, []string{"stage.alpha.example"}) {
		t.Errorf("site %+v", got)
	}

	if h, err := s.SiteHeaders(ctx, dst.ID); err != nil || h.Headers != hdr {
		t.Errorf("headers %+v %v", h, err)
	}

	jobs, err := s.CronJobs(ctx, dst.ID)
	if err != nil || len(jobs) != 2 || !jobs[0].Paused || !jobs[1].Paused || jobs[0].Command != "php cron.php" {
		t.Errorf("cron jobs %+v %v", jobs, err)
	}

	grants, err := s.SiteGrants(ctx, uid)
	if err != nil || !slices.Equal(grants, []string{"alpha", "alpha-stage"}) {
		t.Errorf("grants %v %v", grants, err)
	}

	// Without PauseCron the jobs keep their state; a site without rows
	// copies nothing and fails nothing.
	plain, err := s.CreateSite(ctx, "alpha-copy", "default", "8.4", []string{"copy.alpha.example"}, now)
	if err != nil {
		t.Fatal(err)
	}

	if err := s.CopySiteSettings(ctx, src.ID, plain.ID, CloneSettings{}, now); err != nil {
		t.Fatal(err)
	}

	if jobs, _ := s.CronJobs(ctx, plain.ID); len(jobs) != 2 || jobs[0].Paused || !jobs[1].Paused {
		t.Errorf("cron jobs without pause %+v", jobs)
	}

	beta, _ := s.SiteByName(ctx, "beta")
	if err := s.CopySiteSettings(ctx, beta.ID, plain.ID, CloneSettings{}, now); err != nil {
		t.Fatal(err)
	}
}

func TestSiteClones(t *testing.T) {
	s, _, _ := grantsEnv(t)
	ctx := t.Context()
	now := time.Unix(2_000_000, 0)

	src, _ := s.SiteByName(ctx, "alpha")
	dst, _ := s.SiteByName(ctx, "beta")

	if _, err := s.SiteClone(ctx, dst.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("no clone: %v", err)
	}

	dbs := []CloneDatabase{{Source: "alpha_wp", Target: "beta_wp", User: "beta_wp"}}
	if err := s.CreateSiteClone(ctx, SiteClone{SiteID: dst.ID, SourceID: src.ID, Staging: true, Databases: dbs, SourceMaintenance: true, StartedBy: 1}, now); err != nil {
		t.Fatal(err)
	}

	if err := s.CreateSiteClone(ctx, SiteClone{SiteID: dst.ID, SourceID: src.ID}, now); !errors.Is(err, ErrCloneExists) {
		t.Fatalf("second row: %v", err)
	}

	c, err := s.SiteClone(ctx, dst.ID)
	if err != nil || c.State != CloneCopying || !c.Staging || c.SourceID != src.ID || len(c.Databases) != 1 || c.Databases[0].Target != "beta_wp" || !c.SourceMaintenance || c.StartedBy != 1 || !c.FinishedAt.IsZero() {
		t.Fatalf("clone %+v %v", c, err)
	}

	// A refresh cannot start while the copy runs.
	if err := s.StartCloneRefresh(ctx, dst.ID, 1, dbs, false, now); !errors.Is(err, ErrCloneBusy) {
		t.Fatalf("refresh while copying: %v", err)
	}

	c.HomeJob, c.HomeDone, c.State, c.Config, c.SourceMaintenance = "0123456789abcdef", true, CloneDone, CloneConfigDone, false
	c.Databases[0].Done = true

	if err := s.UpdateSiteClone(ctx, c, now.Add(time.Minute)); err != nil {
		t.Fatal(err)
	}

	c, _ = s.SiteClone(ctx, dst.ID)
	if c.State != CloneDone || !c.HomeDone || !c.Databases[0].Done || c.Config != CloneConfigDone || c.SourceMaintenance || !c.FinishedAt.Equal(now.Add(time.Minute)) {
		t.Fatalf("after update %+v", c)
	}

	if err := s.StartCloneRefresh(ctx, dst.ID, 1, dbs, true, now.Add(time.Hour)); err != nil {
		t.Fatal(err)
	}

	c, _ = s.SiteClone(ctx, dst.ID)
	if c.State != CloneCopying || !c.Refresh || c.HomeJob != "" || c.HomeDone || c.Databases[0].Done || c.Config != CloneConfigNone || !c.SourceMaintenance || !c.FinishedAt.IsZero() {
		t.Fatalf("refresh %+v", c)
	}

	if err := s.StartCloneRefresh(ctx, src.ID, 1, nil, false, now); !errors.Is(err, ErrNotFound) {
		t.Fatalf("refresh of a site that is no clone: %v", err)
	}

	if all, err := s.SiteClones(ctx, src.ID); err != nil || len(all) != 1 {
		t.Fatalf("clones of the source %+v %v", all, err)
	}

	// A bad job id is refused by the column.
	c.HomeJob = "../x"
	if err := s.UpdateSiteClone(ctx, c, now); err == nil {
		t.Fatal("bad job id stored")
	}

	// Deleting the source keeps the clone without a source.
	if err := s.DeleteSite(ctx, "alpha"); err != nil {
		t.Fatal(err)
	}

	c, err = s.SiteClone(ctx, dst.ID)
	if err != nil || c.SourceID != 0 {
		t.Fatalf("after deleting the source %+v %v", c, err)
	}

	if err := s.DeleteSite(ctx, "beta"); err != nil {
		t.Fatal(err)
	}

	if _, err := s.SiteClone(ctx, dst.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("after deleting the clone: %v", err)
	}
}
