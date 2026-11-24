// SPDX-License-Identifier: AGPL-3.0-or-later

package store

import (
	"errors"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"
)

func TestSiteDeploys(t *testing.T) {
	s := open(t, dbPath(t))
	ctx := t.Context()
	now := time.Unix(2_000_000, 0).UTC()

	site, err := s.CreateSite(ctx, "blog", "default", "8.4", []string{"blog.example.com"}, now)
	if err != nil {
		t.Fatal(err)
	}

	d, err := s.CreateSiteDeploy(ctx, SiteDeploy{SiteID: site.ID, Kind: DeployKindDeploy, Source: DeploySourcePanel}, now)
	if err != nil {
		t.Fatal(err)
	}

	if d.Release != strconv.FormatInt(d.ID, 10) || d.State != DeployQueued || !d.Active() {
		t.Fatalf("created %+v", d)
	}

	if _, err := s.CreateSiteDeploy(ctx, SiteDeploy{SiteID: site.ID, Kind: DeployKindDeploy, Source: DeploySourceAPI}, now); !errors.Is(err, ErrDeployBusy) {
		t.Fatalf("second run: %v", err)
	}

	if err := s.StartSiteDeploy(ctx, d.ID, now.Add(time.Second)); err != nil {
		t.Fatal(err)
	}

	if active, _ := s.ActiveSiteDeploys(ctx); len(active) != 1 || active[0].State != DeployRunning || active[0].Started.IsZero() {
		t.Fatalf("active %+v", active)
	}

	sha := strings.Repeat("ab", 20)
	if err := s.FinishSiteDeploy(ctx, SiteDeploy{ID: d.ID, State: DeploySucceeded, Commit: sha, Author: "Kim", Message: strings.Repeat("x", 2000)}, now.Add(time.Minute)); err != nil {
		t.Fatal(err)
	}

	got, err := s.SiteDeploy(ctx, site.ID, d.ID)
	if err != nil || got.State != DeploySucceeded || got.Commit != sha || len(got.Message) != 1024 || got.Finished.IsZero() {
		t.Fatalf("finished %+v %v", got, err)
	}

	// Finishing twice is not found; so is a run of another site.
	if err := s.FinishSiteDeploy(ctx, SiteDeploy{ID: d.ID, State: DeployFailed}, now); !errors.Is(err, ErrNotFound) {
		t.Fatalf("finish twice: %v", err)
	}

	if _, err := s.SiteDeploy(ctx, site.ID+1, d.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("other site: %v", err)
	}

	if err := s.FinishSiteDeploy(ctx, SiteDeploy{ID: d.ID, State: DeployRunning}, now); err == nil {
		t.Fatal("finish as running accepted")
	}

	// A rollback keeps its target release and the commit fields.
	r, err := s.CreateSiteDeploy(ctx, SiteDeploy{SiteID: site.ID, Kind: DeployKindRollback, Source: DeploySourcePanel, Release: d.Release, Commit: sha, Actor: 0}, now)
	if err != nil || r.Release != d.Release {
		t.Fatalf("rollback %+v %v", r, err)
	}

	if err := s.FinishSiteDeploy(ctx, SiteDeploy{ID: r.ID, State: DeployFailed, Error: "release gone"}, now); err != nil {
		t.Fatal(err)
	}

	if got, _ := s.SiteDeploy(ctx, site.ID, r.ID); got.Commit != sha || got.Error != "release gone" {
		t.Fatalf("rollback after finish %+v", got)
	}

	if kept, _ := s.KeptReleases(ctx, site.ID, 5); !slices.Equal(kept, []string{d.Release}) {
		t.Fatalf("kept %v", kept)
	}

	// The history keeps the newest MaxDeployHistory runs.
	for range MaxDeployHistory + 5 {
		x, err := s.CreateSiteDeploy(ctx, SiteDeploy{SiteID: site.ID, Kind: DeployKindDeploy, Source: DeploySourceWebhook}, now)
		if err != nil {
			t.Fatal(err)
		}

		if err := s.FinishSiteDeploy(ctx, SiteDeploy{ID: x.ID, State: DeployFailed}, now); err != nil {
			t.Fatal(err)
		}
	}

	all, err := s.SiteDeploys(ctx, site.ID, 1000)
	if err != nil || len(all) != MaxDeployHistory || all[0].ID < all[1].ID {
		t.Fatalf("history %d %v", len(all), err)
	}
}
