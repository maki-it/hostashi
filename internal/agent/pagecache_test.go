// SPDX-License-Identifier: AGPL-3.0-or-later

package agent

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"testing"

	"github.com/maki-it/hostashi/internal/agentapi"
	"github.com/maki-it/hostashi/internal/agentrpc"
	"github.com/maki-it/hostashi/internal/pagecache"
	"github.com/maki-it/hostashi/internal/php"
	"github.com/maki-it/hostashi/internal/web"
)

type nopPHP struct{}

func (nopPHP) Test(context.Context, string, string) error { return nil }
func (nopPHP) Start(context.Context, string) error        { return nil }
func (nopPHP) Reload(context.Context, string) error       { return nil }
func (nopPHP) Stop(context.Context, string) error         { return nil }

func testCacheDirs() *pagecache.Dirs {
	return &pagecache.Dirs{UID: os.Getuid(), GID: os.Getgid(), RootUID: os.Getuid()}
}

func TestCacheChanges(t *testing.T) {
	c := func(preset string, minutes int, exclude ...string) *web.PageCache {
		return &web.PageCache{Dir: "/p/.hostashi-cache/x", Preset: preset, Minutes: minutes, SizeMB: 256, Exclude: exclude}
	}
	on := func(user string, pc *web.PageCache) web.Site {
		return web.Site{User: user, PHPSocket: "/run/php.sock", PageCache: pc}
	}
	maint := func(s web.Site) web.Site { s.Maintenance = &web.Maintenance{Page: "p"}; return s }
	sized := func(pc *web.PageCache, mb int) *web.PageCache { x := *pc; x.SizeMB = mb; return &x }

	wp := c("wordpress", 10)

	for name, tc := range map[string]struct {
		prev, next    []web.Site
		purge, remove []string
	}{
		"nothing":            {nil, nil, nil, nil},
		"new cache":          {nil, []web.Site{on("a", wp)}, nil, nil},
		"switched on":        {[]web.Site{on("a", nil)}, []web.Site{on("a", wp)}, nil, nil},
		"unchanged":          {[]web.Site{on("a", wp)}, []web.Site{on("a", wp)}, nil, nil},
		"size only":          {[]web.Site{on("a", wp)}, []web.Site{on("a", sized(wp, 512))}, nil, nil},
		"preset":             {[]web.Site{on("a", wp)}, []web.Site{on("a", c("php", 10))}, []string{"a"}, nil},
		"cache time":         {[]web.Site{on("a", wp)}, []web.Site{on("a", c("wordpress", 30))}, []string{"a"}, nil},
		"excluded paths":     {[]web.Site{on("a", wp)}, []web.Site{on("a", c("wordpress", 10, "/x"))}, []string{"a"}, nil},
		"maintenance starts": {[]web.Site{on("a", wp)}, []web.Site{maint(on("a", wp))}, nil, nil},
		"maintenance ends":   {[]web.Site{maint(on("a", wp))}, []web.Site{on("a", wp)}, []string{"a"}, nil},
		"switched off":       {[]web.Site{on("a", wp)}, []web.Site{on("a", nil)}, nil, []string{"a"}},
		"site gone":          {[]web.Site{on("a", wp), on("b", wp)}, []web.Site{on("b", wp)}, nil, []string{"a"}},
	} {
		purge, remove := cacheChanges(web.Desired{Sites: tc.prev}, web.Desired{Sites: tc.next})
		if !slices.Equal(purge, tc.purge) || !slices.Equal(remove, tc.remove) {
			t.Errorf("%s: purge %v remove %v, want %v %v", name, purge, remove, tc.purge, tc.remove)
		}
	}
}

func TestPageCachePurge(t *testing.T) {
	base := filepath.Join(t.TempDir(), "web")
	fq := newFakeQuota(true)
	c, pool := startSiteAgentCfg(t, Config{Web: &web.Reconciler{Base: base, Driver: &web.Nginx{}}, PageCache: testCacheDirs(), Quota: fq})
	ctx := context.Background()

	// The test accounts all get the same UID, so only one site.
	if _, err := agentapi.SiteCreate.Call(ctx, c, agentapi.SiteCreateRequest{User: "blog"}); err != nil {
		t.Fatal(err)
	}

	dir := filepath.Join(pool, pagecache.DirName, "blog")
	fakeLiveGeneration(t, base, `{"sites":[
		{"user":"blog","domains":["blog.example.com"],"root":"/x","phpSocket":"/run/p.sock","pageCache":{"dir":"`+dir+`","preset":"wordpress","minutes":10,"sizeMB":256}},
		{"user":"shop","domains":["shop.example.com"],"root":"/y"}]}`)

	if err := os.MkdirAll(filepath.Join(dir, "a", "bc"), 0o700); err != nil {
		t.Fatal(err)
	}

	if err := os.WriteFile(filepath.Join(dir, "a", "bc", "entry"), []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}

	if _, err := agentapi.PageCachePurge.Call(ctx, c, agentapi.PageCachePurgeRequest{User: "blog"}); err != nil {
		t.Fatal(err)
	}

	if ents, err := os.ReadDir(dir); err != nil || len(ents) != 0 {
		t.Fatalf("after purge: %v %v", ents, err)
	}

	if fq.assigned[dir] == 0 {
		t.Errorf("purged directory not in the site's project: %v", fq.assigned)
	}

	// Off, unknown or invalid: refused, and nothing is created.
	_, err := agentapi.PageCachePurge.Call(ctx, c, agentapi.PageCachePurgeRequest{User: "shop"})
	wantCode(t, err, agentrpc.CodePrecondition)

	_, err = agentapi.PageCachePurge.Call(ctx, c, agentapi.PageCachePurgeRequest{User: "nosuch"})
	wantCode(t, err, agentrpc.CodePrecondition)

	if _, err := agentapi.PageCachePurge.Call(ctx, c, agentapi.PageCachePurgeRequest{User: "../x"}); err == nil {
		t.Fatal("invalid name accepted")
	}

	if _, err := os.Stat(filepath.Join(pool, pagecache.DirName, "shop")); !os.IsNotExist(err) {
		t.Fatalf("purge created a directory for a site without cache: %v", err)
	}
}

func TestPageCachePurgeWithoutCache(t *testing.T) {
	c, _ := startSiteAgentWith(t, &web.Reconciler{Base: filepath.Join(t.TempDir(), "web"), Driver: &web.Nginx{}})

	_, err := agentapi.PageCachePurge.Call(context.Background(), c, agentapi.PageCachePurgeRequest{User: "blog"})
	wantCode(t, err, agentrpc.CodePrecondition)
}

func TestWebApplyPreparesCacheDirectory(t *testing.T) {
	fq := newFakeQuota(true)
	p := &php.Manager{Dir: filepath.Join(t.TempDir(), "php"), SocketDir: "/run/hostashi-php", WebGroup: "www-data", Ctl: nopPHP{}}
	c, pool := startSiteAgentCfg(t, Config{
		Web: &web.Reconciler{Base: filepath.Join(t.TempDir(), "web"), Driver: &web.Nginx{}},
		PHP: p, PageCache: testCacheDirs(), Quota: fq,
	})
	ctx := context.Background()

	if _, err := agentapi.SiteCreate.Call(ctx, c, agentapi.SiteCreateRequest{User: "blog"}); err != nil {
		t.Fatal(err)
	}

	// The test driver cannot render, so the apply fails after the
	// directory was prepared.
	_, _ = agentapi.WebApply.Call(ctx, c, agentapi.WebApplyRequest{Sites: []agentapi.WebSite{{
		User: "blog", Domains: []string{"blog.example.com"}, PHP: "8.4",
		PageCache: &agentapi.WebPageCache{Preset: agentapi.PageCacheWordPress, Minutes: 10, SizeMB: 256},
	}}})

	dir := filepath.Join(pool, pagecache.DirName, "blog")
	if err := testCacheDirs().Check(pool, "blog"); err != nil {
		t.Fatal(err)
	}

	if fq.assigned[dir] == 0 {
		t.Errorf("cache directory not in the site's project: %v", fq.assigned)
	}
}

func TestWebApplyRefusesCacheWithoutDirs(t *testing.T) {
	p := &php.Manager{Dir: filepath.Join(t.TempDir(), "php"), SocketDir: "/run/hostashi-php", WebGroup: "www-data", Ctl: nopPHP{}}
	c, _ := startSiteAgentPHP(t, &web.Reconciler{Base: filepath.Join(t.TempDir(), "web"), Driver: &web.Nginx{}}, p)
	ctx := context.Background()

	if _, err := agentapi.SiteCreate.Call(ctx, c, agentapi.SiteCreateRequest{User: "blog"}); err != nil {
		t.Fatal(err)
	}

	_, err := agentapi.WebApply.Call(ctx, c, agentapi.WebApplyRequest{Sites: []agentapi.WebSite{{
		User: "blog", Domains: []string{"blog.example.com"}, PHP: "8.4",
		PageCache: &agentapi.WebPageCache{Preset: agentapi.PageCacheWordPress, Minutes: 10, SizeMB: 256},
	}}})
	wantCode(t, err, agentrpc.CodePrecondition)
}

func TestSiteDeleteRemovesCache(t *testing.T) {
	c, pool := startSiteAgentCfg(t, Config{PageCache: testCacheDirs()})
	ctx := context.Background()

	if _, err := agentapi.SiteCreate.Call(ctx, c, agentapi.SiteCreateRequest{User: "blog"}); err != nil {
		t.Fatal(err)
	}

	if _, err := testCacheDirs().Ensure(pool, "blog", 1001); err != nil {
		t.Fatal(err)
	}

	if _, err := agentapi.SiteDelete.Call(ctx, c, agentapi.SiteDeleteRequest{User: "blog"}); err != nil {
		t.Fatal(err)
	}

	if _, err := os.Stat(filepath.Join(pool, pagecache.DirName, "blog")); !os.IsNotExist(err) {
		t.Fatalf("cache left: %v", err)
	}
}

func TestDoctorPageCache(t *testing.T) {
	ctx := context.Background()
	base := filepath.Join(t.TempDir(), "web")
	dirs := testCacheDirs()
	c, pool := startSiteAgentCfg(t, Config{
		Web: &web.Reconciler{Base: base, Driver: &web.Nginx{}}, PageCache: dirs, Quota: newFakeQuota(false), Doctor: doctorEnv(t),
	})

	if _, err := agentapi.SiteCreate.Call(ctx, c, agentapi.SiteCreateRequest{User: "blog"}); err != nil {
		t.Fatal(err)
	}

	fakeLiveGeneration(t, base, `{"sites":[{"user":"blog","domains":["blog.example.com"],"root":"/x","phpSocket":"/run/p.sock",
		"pageCache":{"dir":"`+filepath.Join(pool, pagecache.DirName, "blog")+`","preset":"wordpress","minutes":10,"sizeMB":256}}]}`)

	check := func() map[string][]agentapi.DoctorCheck {
		t.Helper()

		r, err := agentapi.Doctor.Call(ctx, c, agentapi.DoctorRequest{})
		if err != nil {
			t.Fatal(err)
		}

		return byID(r)
	}

	// The directory is missing.
	if got := check()["blog/site.pagecache"]; len(got) != 1 || got[0].Status != agentapi.DoctorFail {
		t.Fatalf("missing directory: %+v", got)
	}

	if _, err := dirs.Ensure(pool, "blog", 1001); err != nil {
		t.Fatal(err)
	}

	// In order, but without project quota only max_size limits it.
	if got := check()["blog/site.pagecache"]; len(got) != 1 || got[0].Status != agentapi.DoctorWarn {
		t.Fatalf("no quota: %+v", got)
	}

	// A directory without a cache is reported.
	if _, err := dirs.Ensure(pool, "old", 1002); err != nil {
		t.Fatal(err)
	}

	if got := check()["system.pagecache"]; len(got) != 1 || got[0].Status != agentapi.DoctorWarn {
		t.Fatalf("leftover: %+v", got)
	}
}
