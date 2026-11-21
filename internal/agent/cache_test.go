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
	"github.com/maki-it/hostashi/internal/objcache"
)

type cacheCtl struct {
	mu    sync.Mutex
	calls []string
}

func (c *cacheCtl) add(s string) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	c.calls = append(c.calls, s)

	return nil
}

func (c *cacheCtl) DaemonReload(context.Context) error        { return c.add("reload") }
func (c *cacheCtl) Start(_ context.Context, u string) error   { return c.add("start " + u) }
func (c *cacheCtl) Stop(_ context.Context, u string) error    { return c.add("stop " + u) }
func (c *cacheCtl) Restart(_ context.Context, u string) error { return c.add("restart " + u) }
func (c *cacheCtl) Show(context.Context, string, ...string) (map[string]string, error) {
	return map[string]string{"ActiveState": "active", "MemoryCurrent": "1024"}, nil
}

func (c *cacheCtl) has(s string) bool {
	c.mu.Lock()
	defer c.mu.Unlock()

	return slices.Contains(c.calls, s)
}

func TestCacheWithoutManager(t *testing.T) {
	c, _ := startSiteAgent(t)

	_, err := agentapi.CacheStatusOp.Call(context.Background(), c, agentapi.CacheSiteRequest{Site: "blog"})
	wantCode(t, err, agentrpc.CodePrecondition)
}

func TestCacheOps(t *testing.T) {
	if os.Geteuid() != 0 {
		t.Skip("needs root")
	}

	dir := t.TempDir()
	ctl := &cacheCtl{}
	mgr := &objcache.Manager{
		Dir: filepath.Join(dir, "valkey"), UnitDir: filepath.Join(dir, "units"),
		Slice: siteSlice, Ctl: ctl,
	}
	c, _ := startSiteAgentCfg(t, Config{Cache: mgr})
	ctx := context.Background()
	unit := "hostashi-valkey@blog.service"

	_, err := agentapi.CacheApply.Call(ctx, c, agentapi.CacheApplyRequest{Site: "blog", MemoryMB: 64})
	wantCode(t, err, agentrpc.CodeNotFound)

	_, err = agentapi.CacheApply.Call(ctx, c, agentapi.CacheApplyRequest{Site: "blog", MemoryMB: 4096})
	wantCode(t, err, agentrpc.CodeInvalidArgument)

	if _, err := agentapi.SiteCreate.Call(ctx, c, agentapi.SiteCreateRequest{User: "blog"}); err != nil {
		t.Fatal(err)
	}

	st, err := agentapi.CacheApply.Call(ctx, c, agentapi.CacheApplyRequest{Site: "blog", MemoryMB: 64})
	if err != nil || st.MemoryMB != 64 || st.State != "active" || st.Socket != "/run/hostashi-valkey/blog/valkey.sock" {
		t.Fatalf("apply: %+v %v", st, err)
	}

	if !ctl.has("start " + unit) {
		t.Errorf("calls %q", ctl.calls)
	}

	if _, err := agentapi.CacheFlush.Call(ctx, c, agentapi.CacheSiteRequest{Site: "blog"}); err != nil || !ctl.has("restart "+unit) {
		t.Fatalf("flush: %v %q", err, ctl.calls)
	}

	// Disabling the site stops its cache.
	if err := mgr.SetPaused(ctx, "blog", true); err != nil {
		t.Fatal(err)
	}

	_, err = agentapi.CacheFlush.Call(ctx, c, agentapi.CacheSiteRequest{Site: "blog"})
	wantCode(t, err, agentrpc.CodePrecondition)

	// Deleting the site removes its cache.
	if _, err := agentapi.SiteDelete.Call(ctx, c, agentapi.SiteDeleteRequest{User: "blog"}); err != nil {
		t.Fatal(err)
	}

	if _, err := os.Stat(filepath.Join(dir, "valkey", "blog.conf")); !os.IsNotExist(err) {
		t.Errorf("config left after site delete: %v", err)
	}

	_, err = agentapi.CacheFlush.Call(ctx, c, agentapi.CacheSiteRequest{Site: "blog"})
	wantCode(t, err, agentrpc.CodeNotFound)
}
