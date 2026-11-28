// SPDX-License-Identifier: AGPL-3.0-or-later

package agent

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"sync"
	"testing"

	"github.com/maki-it/hostashi/internal/agentapi"
	"github.com/maki-it/hostashi/internal/agentrpc"
	"github.com/maki-it/hostashi/internal/cronjobs"
)

type cronCtl struct {
	mu    sync.Mutex
	calls []string
}

func (c *cronCtl) add(s string) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	c.calls = append(c.calls, s)

	return nil
}

func (c *cronCtl) DaemonReload(context.Context) error             { return c.add("reload") }
func (c *cronCtl) EnableNow(_ context.Context, u string) error    { return c.add("enable " + u) }
func (c *cronCtl) DisableNow(_ context.Context, u string) error   { return c.add("disable " + u) }
func (c *cronCtl) StartNoBlock(_ context.Context, u string) error { return c.add("start " + u) }
func (c *cronCtl) Show(context.Context, string, ...string) (map[string]string, error) {
	return map[string]string{}, nil
}

func (c *cronCtl) NextElapse(context.Context, ...string) (map[string]int64, error) {
	return map[string]int64{}, nil
}

func (c *cronCtl) Journal(context.Context, string, string, uint64) ([]string, bool, error) {
	return nil, false, nil
}

func (c *cronCtl) JournalRange(context.Context, string, string, int64, int64) ([]string, bool, error) {
	return nil, false, nil
}

func (c *cronCtl) Runs(context.Context, string) ([]agentapi.CronRunEntry, error) {
	return nil, nil
}

func (c *cronCtl) has(s string) bool {
	c.mu.Lock()
	defer c.mu.Unlock()

	return slices.Contains(c.calls, s)
}

func TestCronWithoutManager(t *testing.T) {
	c, _ := startSiteAgent(t)

	_, err := agentapi.CronStatus.Call(context.Background(), c, agentapi.CronStatusRequest{Site: "blog"})
	wantCode(t, err, agentrpc.CodePrecondition)
}

func TestCronOps(t *testing.T) {
	if os.Geteuid() != 0 {
		t.Skip("needs root")
	}

	dir := t.TempDir()
	units := filepath.Join(dir, "units")

	if err := os.Mkdir(units, 0o755); err != nil {
		t.Fatal(err)
	}

	ctl := &cronCtl{}
	mgr := &cronjobs.Manager{StateFile: filepath.Join(dir, "cron.json"), ScriptDir: filepath.Join(dir, "scripts"), UnitDir: units, Ctl: ctl}
	c, _ := startSiteAgentCfg(t, Config{Cron: mgr})
	ctx := context.Background()

	job := agentapi.CronJob{ID: 4, Schedule: "@hourly", Command: "php wp-cron.php"}

	_, err := agentapi.CronApply.Call(ctx, c, agentapi.CronApplyRequest{Site: "blog", Jobs: []agentapi.CronJob{job}})
	wantCode(t, err, agentrpc.CodeNotFound)

	if _, err := agentapi.SiteCreate.Call(ctx, c, agentapi.SiteCreateRequest{User: "blog"}); err != nil {
		t.Fatal(err)
	}

	if _, err := agentapi.CronApply.Call(ctx, c, agentapi.CronApplyRequest{Site: "blog", Jobs: []agentapi.CronJob{job}}); err != nil {
		t.Fatal(err)
	}

	if !ctl.has("enable hostashi-cron-blog-4.timer") {
		t.Errorf("calls %q", ctl.calls)
	}

	if _, err := os.Stat(filepath.Join(units, "hostashi-cron-blog-4.service")); err != nil {
		t.Fatal(err)
	}

	st, err := agentapi.CronStatus.Call(ctx, c, agentapi.CronStatusRequest{Site: "blog"})
	if err != nil || len(st.Jobs) != 1 || st.Jobs[0].ID != 4 {
		t.Fatalf("status %+v %v", st, err)
	}

	if _, err := agentapi.CronRun.Call(ctx, c, agentapi.CronRunRequest{Site: "blog", ID: 4}); err != nil || !ctl.has("start hostashi-cron-blog-4.service") {
		t.Fatalf("run: %v %q", err, ctl.calls)
	}

	_, err = agentapi.CronRun.Call(ctx, c, agentapi.CronRunRequest{Site: "blog", ID: 5})
	wantCode(t, err, agentrpc.CodeNotFound)

	// Deleting the site removes its jobs.
	if _, err := agentapi.SiteDelete.Call(ctx, c, agentapi.SiteDeleteRequest{User: "blog"}); err != nil {
		t.Fatal(err)
	}

	if _, err := os.Stat(filepath.Join(units, "hostashi-cron-blog-4.service")); !os.IsNotExist(err) {
		t.Errorf("unit left after site delete: %v", err)
	}
}

func TestSiteSlice(t *testing.T) {
	for in, want := range map[string]string{"shop": "hostashi-site-shop.slice", "my-shop": `hostashi-site-my\x2dshop.slice`} {
		if got := siteSlice(in); got != want {
			t.Errorf("%s: %s", in, got)
		}
	}
}
