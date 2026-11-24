// SPDX-License-Identifier: AGPL-3.0-or-later

package servermon

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/maki-it/hostashi/internal/agentapi"
	"github.com/maki-it/hostashi/internal/store"
)

type fakeSiteAgent struct {
	mu  sync.Mutex
	res agentapi.SitesStatsResponse
	err error
}

func (a *fakeSiteAgent) SitesStats(context.Context) (agentapi.SitesStatsResponse, error) {
	a.mu.Lock()
	defer a.mu.Unlock()

	return a.res, a.err
}

func (a *fakeSiteAgent) set(sites ...agentapi.SiteUsage) {
	a.mu.Lock()
	defer a.mu.Unlock()

	a.res = agentapi.SitesStatsResponse{Sites: sites}
}

func TestDeriveSite(t *testing.T) {
	at := time.Unix(1000, 0)
	prev := agentapi.SiteUsage{Name: "shop", CPUUsec: 1_000_000, IOReadBytes: 100, HTTPRequests: 10, HTTPBytesOut: 1000}
	cur := agentapi.SiteUsage{
		Name: "shop", CPUUsec: 8_500_000, MemoryBytes: 64, CacheBytes: 8, MemoryKnown: true, Tasks: 5,
		IOReadBytes: 1600, IOWriteBytes: 150, HTTPRequests: 25, HTTPBytesIn: 300, HTTPBytesOut: 16000,
	}

	p, ok := DeriveSite(at, prev, at.Add(15*time.Second), cur)
	if !ok {
		t.Fatal("no point")
	}

	// 7.5 s of CPU in 15 s is half a core.
	want := store.SiteSample{Site: "shop", At: at.Add(15 * time.Second), CPU: 50, Mem: 64, Cache: 8, Tasks: 5, IORead: 100, IOWrite: 10, HTTPReq: 1, HTTPIn: 20, HTTPOut: 1000}
	if p != want {
		t.Fatalf("got %+v, want %+v", p, want)
	}

	// A counter that went back started again: what it holds now counts.
	restarted := cur
	restarted.CPUUsec = 1_500_000

	if p, _ := DeriveSite(at, cur, at.Add(15*time.Second), restarted); p.CPU != 10 {
		t.Fatalf("after restart %+v", p)
	}

	// Memory unknown stays 0.
	cur.MemoryKnown = false
	if p, _ := DeriveSite(at, prev, at.Add(15*time.Second), cur); p.Mem != 0 || p.Cache != 0 {
		t.Fatalf("unknown memory %+v", p)
	}

	for _, next := range []time.Time{at, at.Add(-time.Second), at.Add(maxGap + time.Second)} {
		if _, ok := DeriveSite(at, prev, next, cur); ok {
			t.Fatalf("point for %v", next)
		}
	}
}

func TestSampleSites(t *testing.T) {
	st := openStore(t)
	ctx := t.Context()
	base := time.Unix(3_000_000, 0).UTC() // a multiple of 300 and 3600

	for _, name := range []string{"shop", "blog"} {
		if _, err := st.CreateSite(ctx, name, "default", "", []string{name + ".example.com"}, base); err != nil {
			t.Fatal(err)
		}
	}

	now := base
	ag := &fakeSiteAgent{}
	m := &Monitor{SiteAgent: ag, SiteStore: st, Now: func() time.Time { return now }}

	// 21 readings 15 s apart: shop uses a quarter core and sends 2 KB/s,
	// blog half a core.
	for i := range 21 {
		ag.set(
			agentapi.SiteUsage{Name: "blog", CPUUsec: uint64(i) * 7_500_000, MemoryBytes: 100, MemoryKnown: true},
			agentapi.SiteUsage{Name: "shop", CPUUsec: uint64(i) * 3_750_000, MemoryBytes: 300, MemoryKnown: true, HTTPRequests: uint64(i) * 15, HTTPBytesOut: uint64(i) * 30_000},
		)
		m.SampleSites(ctx)
		now = now.Add(SampleEvery)
	}

	pts, step, err := m.SiteSeries(ctx, "shop", Range1h)
	if err != nil || step != 15 || len(pts) != 20 {
		t.Fatalf("1h: %d points, step %d, %v", len(pts), step, err)
	}

	if pts[0].CPU != 25 || pts[0].HTTPOut != 2000 || pts[0].HTTPReq != 1 {
		t.Fatalf("first point %+v", pts[0])
	}

	// The first 5 min bucket is closed and stored.
	pts, step, err = m.SiteSeries(ctx, "shop", Range24h)
	if err != nil || step != store.HostTier5m || len(pts) == 0 || pts[0].CPU != 25 || pts[0].Mem != 300 {
		t.Fatalf("24h: %+v step %d %v", pts, step, err)
	}

	for _, by := range []string{SortCPU, SortMemory, SortTraffic} {
		r, err := m.SiteRanking(ctx, Range1h, by)
		if err != nil || len(r) != 2 {
			t.Fatalf("%s: %+v %v", by, r, err)
		}

		first := map[string]string{SortCPU: "blog", SortMemory: "shop", SortTraffic: "shop"}[by]
		if r[0].Site != first {
			t.Fatalf("%s: %+v", by, r)
		}
	}

	r, err := m.SiteRanking(ctx, Range1h, SortTraffic)
	if err != nil || r[0].HTTPOutBytes != 20*15*2000 || r[0].HTTPRequests != 300 || r[0].CPUMax != 25 {
		t.Fatalf("totals %+v %v", r, err)
	}

	if r, err := m.SiteRanking(ctx, Range24h, SortCPU); err != nil || len(r) != 2 || r[0].Site != "blog" {
		t.Fatalf("24h ranking %+v %v", r, err)
	}

	if _, err := m.SiteRanking(ctx, "2h", SortCPU); !errors.Is(err, ErrRange) {
		t.Fatalf("range %v", err)
	}

	if _, err := m.SiteRanking(ctx, Range1h, "disk"); !errors.Is(err, ErrSort) {
		t.Fatalf("sort %v", err)
	}

	if _, _, err := m.SiteSeries(ctx, "shop", "1y"); !errors.Is(err, ErrRange) {
		t.Fatalf("series range %v", err)
	}

	// A site the agent stops listing is dropped from the live tier.
	ag.set(agentapi.SiteUsage{Name: "blog", CPUUsec: 21 * 7_500_000})
	m.SampleSites(ctx)

	if pts, _, _ := m.SiteSeries(ctx, "shop", Range1h); len(pts) != 0 {
		t.Fatalf("gone site still has %d points", len(pts))
	}

	// An agent error keeps the last state.
	ag.err = errors.New("down")
	m.SampleSites(ctx)

	if pts, _, _ := m.SiteSeries(ctx, "blog", Range1h); len(pts) == 0 {
		t.Fatal("blog lost its points on an agent error")
	}
}

func TestSampleSitesOff(t *testing.T) {
	m := &Monitor{}
	m.SampleSites(t.Context()) // no agent, no store: nothing happens

	if pts, _, err := m.SiteSeries(t.Context(), "shop", Range24h); err != nil || len(pts) != 0 {
		t.Fatalf("%+v %v", pts, err)
	}
}
