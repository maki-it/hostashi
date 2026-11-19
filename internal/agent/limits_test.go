// SPDX-License-Identifier: AGPL-3.0-or-later

package agent

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/maki-it/hostashi/internal/agentapi"
	"github.com/maki-it/hostashi/internal/agentrpc"
	"github.com/maki-it/hostashi/internal/siteslice"
)

type nopReload struct{}

func (nopReload) DaemonReload(context.Context) error { return nil }

func TestSiteSliceNameMatchesCron(t *testing.T) {
	for _, u := range []string{"shop", "my-shop", "a-b-c"} {
		if siteSlice(u) != siteslice.Name(u) {
			t.Errorf("cron slice %q differs from %q", siteSlice(u), siteslice.Name(u))
		}
	}
}

func TestSiteLimits(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	c, _ := startSiteAgentCfg(t, Config{Slices: &siteslice.Manager{Dir: dir, Ctl: nopReload{}}})

	if _, err := agentapi.SiteCreate.Call(ctx, c, agentapi.SiteCreateRequest{User: "shop"}); err != nil {
		t.Fatal(err)
	}

	if got, err := agentapi.SiteLimitsGet.Call(ctx, c, agentapi.SiteLimitsGetRequest{User: "shop"}); err != nil || got != (agentapi.SiteLimits{}) {
		t.Fatalf("limits before set = %+v, %v", got, err)
	}

	want := agentapi.SiteLimits{MemoryMax: 512 << 20, TasksMax: 128, CPUWeight: agentapi.CPUWeightHigh}

	got, err := agentapi.SiteLimitsSet.Call(ctx, c, agentapi.SiteLimitsSetRequest{User: "shop", Limits: want})
	if err != nil || got != want {
		t.Fatalf("set = %+v, %v", got, err)
	}

	if got, err := agentapi.SiteLimitsGet.Call(ctx, c, agentapi.SiteLimitsGetRequest{User: "shop"}); err != nil || got != want {
		t.Fatalf("get = %+v, %v", got, err)
	}

	_, err = agentapi.SiteLimitsSet.Call(ctx, c, agentapi.SiteLimitsSetRequest{User: "nosuch", Limits: want})
	wantCode(t, err, agentrpc.CodeNotFound)

	_, err = agentapi.SiteLimitsSet.Call(ctx, c, agentapi.SiteLimitsSetRequest{User: "shop", Limits: agentapi.SiteLimits{TasksMax: 1}})
	wantCode(t, err, agentrpc.CodeInvalidArgument)

	if _, err := agentapi.SiteDelete.Call(ctx, c, agentapi.SiteDeleteRequest{User: "shop"}); err != nil {
		t.Fatal(err)
	}

	if _, err := os.Stat(filepath.Join(dir, "hostashi-site-shop.slice")); !os.IsNotExist(err) {
		t.Errorf("slice of deleted site still there: %v", err)
	}
}

func TestSiteLimitsWithoutSlices(t *testing.T) {
	ctx := context.Background()
	c, _ := startSiteAgentCfg(t, Config{})

	if _, err := agentapi.SiteCreate.Call(ctx, c, agentapi.SiteCreateRequest{User: "shop"}); err != nil {
		t.Fatal(err)
	}

	_, err := agentapi.SiteLimitsGet.Call(ctx, c, agentapi.SiteLimitsGetRequest{User: "shop"})
	wantCode(t, err, agentrpc.CodePrecondition)
}
