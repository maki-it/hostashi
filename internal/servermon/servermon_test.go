// SPDX-License-Identifier: AGPL-3.0-or-later

package servermon

import (
	"context"
	"errors"
	"path/filepath"
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/maki-it/hostashi/internal/agentapi"
	"github.com/maki-it/hostashi/internal/store"
)

type fakeAgent struct {
	mu     sync.Mutex
	stats  agentapi.HostStats
	err    error
	states []agentapi.ServiceState
	acted  []agentapi.ServiceActionRequest
}

func (a *fakeAgent) HostStats(context.Context) (agentapi.HostStats, error) {
	a.mu.Lock()
	defer a.mu.Unlock()

	return a.stats, a.err
}

func (a *fakeAgent) Services(context.Context, agentapi.ServicesStatusRequest) ([]agentapi.ServiceState, error) {
	a.mu.Lock()
	defer a.mu.Unlock()

	return append([]agentapi.ServiceState(nil), a.states...), a.err
}

func (a *fakeAgent) Action(_ context.Context, r agentapi.ServiceActionRequest) (agentapi.ServiceState, error) {
	a.mu.Lock()
	defer a.mu.Unlock()

	a.acted = append(a.acted, r)
	if a.err != nil {
		return agentapi.ServiceState{}, a.err
	}

	for i, s := range a.states {
		if s.ID == r.ID && s.Site == r.Site {
			s.StartedAt = s.StartedAt.Add(time.Minute)
			s.NRestarts = 0
			a.states[i] = s

			return s, nil
		}
	}

	return agentapi.ServiceState{}, errors.New("unknown")
}

func openStore(t *testing.T) *store.Store {
	t.Helper()

	s, err := store.Open(t.Context(), filepath.Join(t.TempDir(), "h.db"))
	if err != nil {
		t.Fatal(err)
	}

	t.Cleanup(func() { _ = s.Close() })

	return s
}

func stats(busy, idle, iowait, rx uint64) agentapi.HostStats {
	return agentapi.HostStats{
		CPUs: 2, CPU: agentapi.CPUTimes{User: busy, Idle: idle, IOWait: iowait},
		Memory: agentapi.HostMemory{TotalBytes: 1000, AvailableBytes: 400, SwapTotalBytes: 100, SwapFreeBytes: 100},
		Disks:  []agentapi.HostDisk{{Name: "root", TotalBytes: 100, FreeBytes: 30}},
		Net:    []agentapi.HostNet{{Name: "eth0", RxBytes: rx, TxBytes: rx / 2}},
	}
}

func TestDerive(t *testing.T) {
	t0 := time.Unix(2_000_000, 0).UTC()

	h, ok := Derive(t0, stats(100, 100, 0, 1000), t0.Add(10*time.Second), stats(150, 130, 20, 3000))
	if !ok {
		t.Fatal("no point")
	}

	// 100 ticks: 50 busy, 30 idle, 20 iowait.
	if h.CPUBusy != 50 || h.CPUIOWait != 20 || h.MemUsed != 600 || h.SwapUsed != 0 || h.Disks[0].Used != 70 {
		t.Fatalf("%+v", h)
	}

	if len(h.Net) != 1 || h.Net[0].Rx != 200 || h.Net[0].Tx != 100 {
		t.Fatalf("net %+v", h.Net)
	}

	// Counter reset (reboot, interface re-created): no network rate.
	h, ok = Derive(t0, stats(100, 100, 0, 5000), t0.Add(10*time.Second), stats(150, 130, 20, 10))
	if !ok || len(h.Net) != 0 {
		t.Fatalf("reset %+v", h)
	}

	// CPU counters going back: no CPU rate, still a point.
	h, ok = Derive(t0, stats(500, 500, 0, 0), t0.Add(10*time.Second), stats(10, 10, 0, 0))
	if !ok || h.CPUBusy != 0 {
		t.Fatalf("cpu back %+v", h)
	}

	for _, at := range []time.Time{t0, t0.Add(-time.Second), t0.Add(maxGap + time.Second)} {
		if _, ok := Derive(t0, stats(1, 1, 0, 0), at, stats(2, 2, 0, 0)); ok {
			t.Errorf("point at %v", at)
		}
	}
}

func TestSampleTiers(t *testing.T) {
	st := openStore(t)
	ag := &fakeAgent{}
	now := time.Date(2026, 11, 21, 10, 0, 0, 0, time.UTC)
	m := &Monitor{Agent: ag, Store: st, Now: func() time.Time { return now }}
	ctx := t.Context()

	var busy, idle, rx uint64

	for i := range 25 * 4 { // 25 minutes of 15 s samples
		busy, idle, rx = busy+25, idle+75, rx+1500
		ag.stats = stats(busy, idle, 0, rx)

		m.Sample(ctx)

		if i < 99 {
			now = now.Add(SampleEvery)
		}
	}

	pts, step, err := m.Series(ctx, Range1h)
	if err != nil || step != 15 || len(pts) != 99 {
		t.Fatalf("1h: %d points step %d %v", len(pts), step, err)
	}

	if p := pts[0]; p.CPUBusy != 25 || p.Net[0].Rx != 100 {
		t.Fatalf("point %+v", p)
	}

	five, step, err := m.Series(ctx, Range24h)
	if err != nil || step != 300 {
		t.Fatal(err, step)
	}

	// 10:00 to 10:20 closed, 10:20 open and stored as it is.
	if len(five) != 5 || !five[0].At.Equal(time.Date(2026, 11, 21, 10, 0, 0, 0, time.UTC)) || five[0].CPUBusy != 25 || five[1].Net[0].Rx != 100 {
		t.Fatalf("5m %+v", five)
	}

	hour, _, err := m.Series(ctx, Range7d)
	if err != nil || len(hour) != 1 || hour[0].CPUBusy != 25 {
		t.Fatalf("1h tier %+v %v", hour, err)
	}

	if _, _, err := m.Series(ctx, "1y"); !errors.Is(err, ErrRange) {
		t.Fatal(err)
	}

	// The ring keeps one hour.
	for range RingSize + 10 {
		now = now.Add(SampleEvery)
		busy, idle = busy+1, idle+1
		ag.stats = stats(busy, idle, 0, rx)

		m.Sample(ctx)
	}

	if len(m.ring) != RingSize {
		t.Fatalf("ring %d", len(m.ring))
	}

	// An agent error leaves everything as it was.
	ag.err = errors.New("down")

	m.Sample(ctx)

	if len(m.ring) != RingSize {
		t.Fatal("error changed the ring")
	}
}

func TestChanges(t *testing.T) {
	at := time.Unix(2_000_000, 0).UTC()
	start := at.Add(-time.Hour)
	base := agentapi.ServiceState{ID: "mariadb", Installed: true, Active: "active", StartedAt: start, NRestarts: 1}

	if ev := Changes(nil, base, at); ev != nil {
		t.Fatalf("first poll %+v", ev)
	}

	if ev := Changes(&base, base, at); ev != nil {
		t.Fatalf("no change %+v", ev)
	}

	crash := base
	crash.NRestarts, crash.StartedAt, crash.Result = 3, at, "signal"

	ev := Changes(&base, crash, at)
	if len(ev) != 1 || ev[0].Kind != store.ServiceEventCrashRestart || ev[0].Restarts != 2 || ev[0].Detail != "signal" {
		t.Fatalf("crash %+v", ev)
	}

	started := base
	started.StartedAt, started.NRestarts = at, 0

	ev = Changes(&base, started, at)
	if len(ev) != 1 || ev[0].Kind != store.ServiceEventStarted {
		t.Fatalf("started %+v", ev)
	}

	failed := base
	failed.Active, failed.Result = "failed", "exit-code"

	ev = Changes(&base, failed, at)
	if len(ev) != 1 || ev[0].Kind != store.ServiceEventFailed || ev[0].Detail != "exit-code" {
		t.Fatalf("failed %+v", ev)
	}

	if ev := Changes(&failed, failed, at); ev != nil {
		t.Fatalf("still failed %+v", ev)
	}

	ev = Changes(&failed, base, at)
	if len(ev) != 1 || ev[0].Kind != store.ServiceEventRecovered {
		t.Fatalf("recovered %+v", ev)
	}

	gone := agentapi.ServiceState{ID: "mariadb"}
	if ev := Changes(&base, gone, at); ev != nil {
		t.Fatalf("uninstalled %+v", ev)
	}
}

func TestServicesAndActions(t *testing.T) {
	st := openStore(t)
	now := time.Unix(2_000_000, 0).UTC()
	start := now.Add(-time.Hour)
	ag := &fakeAgent{states: []agentapi.ServiceState{
		{ID: "nginx", Unit: "nginx.service", Installed: true, Active: "active", StartedAt: start},
		{ID: "mariadb", Unit: "mariadb.service", Installed: true, Active: "active", StartedAt: start},
		{ID: "adminer", Unit: "hostashi-adminer.service"},
		{ID: "php", Site: "blog", Unit: "hostashi-php-8.4@blog.service", Installed: true, Active: "active", StartedAt: start},
	}}
	m := &Monitor{Agent: ag, Store: st, Now: func() time.Time { return now }}
	ctx := t.Context()

	m.PollServices(ctx)

	// systemd restarted MariaDB twice.
	ag.states[1].NRestarts, ag.states[1].StartedAt = 2, now
	now = now.Add(time.Minute)

	m.PollServices(ctx)

	rows, err := m.Services(ctx, "")
	if err != nil || len(rows) != 4 {
		t.Fatalf("%+v %v", rows, err)
	}

	if rows[1].Restarts24 != 2 || rows[0].Restarts24 != 0 || len(rows[1].Confirm) != 1 || rows[1].Confirm[0] != "stop" {
		t.Fatalf("rows %+v", rows)
	}

	if len(rows[2].Actions) != 0 {
		t.Fatalf("not installed has actions %+v", rows[2])
	}

	blog, err := m.Services(ctx, "blog")
	if err != nil || len(blog) != 1 || blog[0].ID != "php" {
		t.Fatalf("site rows %+v %v", blog, err)
	}
}

func TestActionRecordsOnce(t *testing.T) {
	st := openStore(t)
	ctx := t.Context()

	u, err := st.CreateFirstUser(ctx, "kim", "x")
	if err != nil {
		t.Fatal(err)
	}

	now := time.Unix(2_000_000, 0).UTC()
	ag := &fakeAgent{states: []agentapi.ServiceState{{ID: "nginx", Unit: "nginx.service", Installed: true, Active: "active", StartedAt: now}}}
	m := &Monitor{Agent: ag, Store: st, Now: func() time.Time { return now }}

	m.PollServices(ctx)

	now = now.Add(time.Minute)

	if _, err := m.Action(ctx, u.ID, agentapi.ServiceActionRequest{ID: "nginx", Action: "restart"}); err != nil {
		t.Fatal(err)
	}

	now = now.Add(time.Minute)

	m.PollServices(ctx)

	ev, err := m.Events(ctx, store.ServiceEventFilter{})
	if err != nil || len(ev) != 1 || ev[0].Kind != store.ServiceEventAction || ev[0].Detail != "restart" || ev[0].UserName != "kim" {
		t.Fatalf("%+v %v", ev, err)
	}

	// A failing action stores nothing.
	ag.err = errors.New("refused")
	if _, err := m.Action(ctx, u.ID, agentapi.ServiceActionRequest{ID: "nginx", Action: "reload"}); err == nil {
		t.Fatal("no error")
	}

	if ev, _ := m.Events(ctx, store.ServiceEventFilter{}); len(ev) != 1 {
		t.Fatalf("%+v", ev)
	}
}

type fakeAlerts struct {
	polls []ServicePoll
	down  []error
}

func (f *fakeAlerts) ServicesPolled(_ context.Context, p ServicePoll) { f.polls = append(f.polls, p) }

func (f *fakeAlerts) AgentUnreachable(_ context.Context, _ time.Time, err error) {
	f.down = append(f.down, err)
}

func TestAlerts(t *testing.T) {
	st := openStore(t)
	now := time.Unix(2_000_000, 0).UTC()
	start := now.Add(-time.Hour)
	ag := &fakeAgent{states: []agentapi.ServiceState{
		{ID: "nginx", Unit: "nginx.service", Installed: true, Active: "active", StartedAt: start},
		{ID: "php", Site: "blog", Unit: "hostashi-php-8.4@blog.service", Installed: true, Active: "active", StartedAt: start},
	}}
	al := &fakeAlerts{}
	m := &Monitor{Agent: ag, Store: st, Alerts: al, Now: func() time.Time { return now }}
	ctx := t.Context()

	m.PollServices(ctx)

	if len(al.polls) != 1 || len(al.polls[0].Events) != 0 || len(al.polls[0].States) != 2 || al.polls[0].Crashes != nil {
		t.Fatalf("first poll %+v", al.polls)
	}

	// PHP crashed twice; the services page read the states before the
	// next poll, so the event reaches the alerter with that poll.
	ag.states[1].NRestarts, ag.states[1].StartedAt = 2, now
	now = now.Add(30 * time.Second)

	if _, err := m.Services(ctx, ""); err != nil {
		t.Fatal(err)
	}

	ag.states[0].Active, ag.states[0].Result = "failed", "exit-code"
	now = now.Add(30 * time.Second)

	m.PollServices(ctx)

	p := al.polls[1]
	if len(p.Events) != 2 || p.Events[0].Kind != store.ServiceEventCrashRestart || p.Events[1].Kind != store.ServiceEventFailed {
		t.Fatalf("events %+v", p.Events)
	}

	if p.Crashes["php/blog"] != 2 || !p.At.Equal(now) {
		t.Fatalf("crashes %+v at %v", p.Crashes, p.At)
	}

	m.PollServices(ctx)

	if len(al.polls[2].Events) != 0 {
		t.Fatalf("events twice %+v", al.polls[2].Events)
	}

	ag.err = errors.New("dial unix: connection refused")

	m.PollServices(ctx)

	if len(al.down) != 1 || len(al.polls) != 3 {
		t.Fatalf("agent down %v, polls %d", al.down, len(al.polls))
	}

	// A canceled poll (shutdown) is not an outage.
	cctx, cancel := context.WithCancel(ctx)
	cancel()
	m.PollServices(cctx)

	if len(al.down) != 1 {
		t.Fatalf("canceled poll counted %v", al.down)
	}
}

// The virus scan's units are listed only while installed (ADR-0064).
func TestServicesOptional(t *testing.T) {
	st := openStore(t)
	now := time.Unix(2_000_000, 0).UTC()
	ag := &fakeAgent{states: []agentapi.ServiceState{
		{ID: "adminer", Unit: "hostashi-adminer.service"},
		{ID: "freshclam", Unit: "clamav-freshclam.service", Installed: true, Active: "active", StartedAt: now},
		{ID: "clamd", Unit: "clamav-daemon.service"},
		{ID: "clamonacc", Unit: "clamav-clamonacc.service"},
	}}
	m := &Monitor{Agent: ag, Store: st, Now: func() time.Time { return now }}

	rows, err := m.Services(t.Context(), "")
	if err != nil {
		t.Fatal(err)
	}

	var ids []string
	for _, r := range rows {
		ids = append(ids, r.ID)
	}

	if !slices.Equal(ids, []string{"adminer", "freshclam"}) {
		t.Fatalf("ids %v", ids)
	}

	if !slices.Equal(rows[1].Actions, []string{agentapi.ServiceStart, agentapi.ServiceRestart}) {
		t.Fatalf("freshclam actions %v", rows[1].Actions)
	}

	// Switching the real-time mode on brings clamd and clamonacc.
	ag.states[2].Installed, ag.states[3].Installed = true, true

	rows, err = m.Services(t.Context(), "")
	if err != nil || len(rows) != 4 {
		t.Fatalf("%+v %v", rows, err)
	}
}
