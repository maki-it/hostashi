// SPDX-License-Identifier: AGPL-3.0-or-later

package agent

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/maki-it/hostashi/internal/agentapi"
	"github.com/maki-it/hostashi/internal/php"
	"github.com/maki-it/hostashi/internal/sitestats"
)

func TestMetricsSnapshot(t *testing.T) {
	phpDir := filepath.Join(t.TempDir(), "php")
	cg := t.TempDir()
	disk := &sitestats.DiskCache{}
	p := &php.Manager{Dir: phpDir, SocketDir: "/run/hostashi-php", WebGroup: "www-data", Installed: func() []string { return []string{"8.4"} }}

	c, pool := startSiteAgentCfg(t, Config{PHP: p, Disk: disk, CgroupRoot: cg})
	ctx := context.Background()

	if _, err := agentapi.SiteCreate.Call(ctx, c, agentapi.SiteCreateRequest{User: "shop"}); err != nil {
		t.Fatal(err)
	}

	// No PHP pool and no disk measurement yet.
	res, err := agentapi.MetricsSnapshot.Call(ctx, c, agentapi.MetricsSnapshotRequest{})
	if err != nil || len(res.Sites) != 1 {
		t.Fatalf("%+v %v", res, err)
	}

	if s := res.Sites[0]; s.Name != "shop" || s.PHPVersion != "" || s.PHPUp || !s.DiskMeasuredAt.IsZero() {
		t.Fatalf("before %+v", s)
	}

	if err := os.MkdirAll(phpDir, 0o700); err != nil {
		t.Fatal(err)
	}

	if err := os.WriteFile(filepath.Join(phpDir, "state.json"), []byte(`{"shop":{"user":"shop","version":"8.4","home":"/x"}}`), 0o600); err != nil {
		t.Fatal(err)
	}

	dir := sitestats.PHPCgroup(cg, "8.4", "shop")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}

	for name, v := range map[string]string{"cgroup.procs": "42\n", "memory.current": "1048576\n"} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(v), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	now := time.Now().UTC().Truncate(time.Second)

	if err := disk.Refresh(ctx, map[string]string{"shop": filepath.Join(pool, "shop")}, func() time.Time { return now }); err != nil {
		t.Fatal(err)
	}

	res, err = agentapi.MetricsSnapshot.Call(ctx, c, agentapi.MetricsSnapshotRequest{})
	if err != nil || len(res.Sites) != 1 {
		t.Fatalf("%+v %v", res, err)
	}

	shop := res.Sites[0]

	if shop.Name != "shop" || shop.PHPVersion != "8.4" || !shop.PHPUp || !shop.PHPMemoryKnown || shop.PHPMemoryBytes != 1048576 || !shop.DiskMeasuredAt.Equal(now) {
		t.Fatalf("shop %+v", shop)
	}
}

func TestSitesStats(t *testing.T) {
	cg := t.TempDir()

	c, _ := startSiteAgentCfg(t, Config{Disk: &sitestats.DiskCache{}, CgroupRoot: cg})
	ctx := context.Background()

	shop, err := agentapi.SiteCreate.Call(ctx, c, agentapi.SiteCreateRequest{User: "shop"})
	if err != nil {
		t.Fatal(err)
	}

	write := func(dir string, files map[string]string) {
		t.Helper()

		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}

		for name, v := range files {
			if err := os.WriteFile(filepath.Join(dir, name), []byte(v), 0o644); err != nil {
				t.Fatal(err)
			}
		}
	}

	write(sitestats.SliceCgroup(cg, "shop"), map[string]string{
		"cpu.stat": "usage_usec 1000\n", "memory.current": "300\n", "memory.stat": "file 200\ninactive_file 100\n",
		"pids.current": "4\n", "io.stat": "8:0 rbytes=7 wbytes=9\n",
	})
	write(sitestats.UserSliceCgroup(cg, shop.UID), map[string]string{"cpu.stat": "usage_usec 500\n", "pids.current": "2\n"})

	res, err := agentapi.SitesStats.Call(ctx, c, agentapi.SitesStatsRequest{})
	if err != nil || len(res.Sites) != 1 || res.At.IsZero() {
		t.Fatalf("%+v %v", res, err)
	}

	want := agentapi.SiteUsage{Name: "shop", CPUUsec: 1500, MemoryBytes: 200, CacheBytes: 200, MemoryKnown: true, Tasks: 6, IOReadBytes: 7, IOWriteBytes: 9, IOKnown: true}
	if shop.UID < 1000 {
		want.CPUUsec, want.Tasks = 1000, 4 // the user slice is only read for real site uids
	}

	if s := res.Sites[0]; s != want {
		t.Fatalf("shop %+v, want %+v", s, want)
	}

	// A broken file is logged; the call still answers.
	write(sitestats.SliceCgroup(cg, "shop"), map[string]string{"memory.current": "nonsense\n"})

	if res, err = agentapi.SitesStats.Call(ctx, c, agentapi.SitesStatsRequest{}); err != nil || len(res.Sites) != 1 || res.Sites[0].MemoryKnown {
		t.Fatalf("broken %+v %v", res, err)
	}
}
