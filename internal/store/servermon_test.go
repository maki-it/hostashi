// SPDX-License-Identifier: AGPL-3.0-or-later

package store

import (
	"testing"
	"time"
)

func TestServiceEvents(t *testing.T) {
	s := open(t, dbPath(t))
	ctx := t.Context()

	u, err := s.CreateFirstUser(ctx, "kim", "x")
	if err != nil {
		t.Fatal(err)
	}

	base := time.Unix(2_000_000, 0).UTC()
	for i, e := range []ServiceEvent{
		{At: base, Service: "mariadb", Kind: ServiceEventCrashRestart, Restarts: 2},
		{At: base.Add(time.Minute), Service: "mariadb", Kind: ServiceEventAction, Detail: "restart", UserID: &u.ID},
		{At: base.Add(2 * time.Minute), Service: "php", Site: "blog", Kind: ServiceEventCrashRestart, Restarts: 1},
		{At: base.Add(3 * time.Minute), Service: "php", Site: "blog", Kind: ServiceEventFailed, Detail: "exit-code"},
	} {
		if err := s.AddServiceEvent(ctx, e); err != nil {
			t.Fatalf("event %d: %v", i, err)
		}
	}

	for _, bad := range []ServiceEvent{
		{At: base, Service: "mariadb", Kind: "deleted"},
		{At: base, Service: "Bad", Kind: ServiceEventStarted},
		{At: base, Service: "mariadb", Kind: ServiceEventCrashRestart, Restarts: -1},
	} {
		if err := s.AddServiceEvent(ctx, bad); err == nil {
			t.Errorf("%+v accepted", bad)
		}
	}

	all, err := s.ServiceEvents(ctx, ServiceEventFilter{})
	if err != nil || len(all) != 4 || all[0].Kind != ServiceEventFailed || all[3].Restarts != 2 {
		t.Fatalf("%+v %v", all, err)
	}

	if all[2].UserName != "kim" || all[2].UserID == nil || *all[2].UserID != u.ID {
		t.Fatalf("action event %+v", all[2])
	}

	blog, err := s.ServiceEvents(ctx, ServiceEventFilter{Service: "php", Site: "blog", Limit: 1})
	if err != nil || len(blog) != 1 || blog[0].Kind != ServiceEventFailed {
		t.Fatalf("%+v %v", blog, err)
	}

	recent, err := s.ServiceEvents(ctx, ServiceEventFilter{Since: base.Add(90 * time.Second)})
	if err != nil || len(recent) != 2 {
		t.Fatalf("since: %+v %v", recent, err)
	}

	n, err := s.CrashRestartsSince(ctx, base)
	if err != nil || n["mariadb"] != 2 || n["php/blog"] != 1 || len(n) != 2 {
		t.Fatalf("restarts %v %v", n, err)
	}

	if err := s.PruneServiceEvents(ctx, base.Add(90*time.Second)); err != nil {
		t.Fatal(err)
	}

	if all, _ := s.ServiceEvents(ctx, ServiceEventFilter{}); len(all) != 2 {
		t.Fatalf("after prune %+v", all)
	}
}

func TestServiceEventsRowCap(t *testing.T) {
	s := open(t, dbPath(t))
	ctx := t.Context()
	base := time.Unix(2_000_000, 0).UTC()

	for i := range ServiceEventsMaxRows + 3 {
		if err := s.AddServiceEvent(ctx, ServiceEvent{At: base.Add(time.Duration(i) * time.Second), Service: "nginx", Kind: ServiceEventStarted}); err != nil {
			t.Fatal(err)
		}
	}

	if err := s.PruneServiceEvents(ctx, base); err != nil {
		t.Fatal(err)
	}

	var n int
	if err := s.RawCount(ctx, `SELECT COUNT(*) FROM service_events WHERE at >= ?`, 0, &n); err != nil || n != ServiceEventsMaxRows {
		t.Fatalf("rows %d %v", n, err)
	}

	// The oldest went.
	if err := s.RawCount(ctx, `SELECT COUNT(*) FROM service_events WHERE at = ?`, base.Unix(), &n); err != nil || n != 0 {
		t.Fatalf("oldest kept: %d %v", n, err)
	}
}

func TestHostSamples(t *testing.T) {
	s := open(t, dbPath(t))
	ctx := t.Context()
	base := time.Unix(2_000_000, 0).UTC()

	h := HostSample{
		At: base, CPUBusy: 12.5, CPUIOWait: 1, CPUSteal: 0.5, Load1: 0.7,
		MemUsed: 1 << 30, MemTotal: 4 << 30, SwapUsed: 0, SwapTotal: 1 << 30,
		Disks: []HostDiskSample{{Name: "root", Used: 10, Total: 100}},
		Net:   []HostNetSample{{Name: "eth0", Rx: 100.5, Tx: 50}},
	}

	if err := s.PutHostSample(ctx, HostTier5m, h); err != nil {
		t.Fatal(err)
	}

	// Same time again replaces.
	h.CPUBusy = 20
	if err := s.PutHostSample(ctx, HostTier5m, h); err != nil {
		t.Fatal(err)
	}

	later := HostSample{At: base.Add(5 * time.Minute)}
	if err := s.PutHostSample(ctx, HostTier5m, later); err != nil {
		t.Fatal(err)
	}

	if err := s.PutHostSample(ctx, HostTier1h, h); err != nil {
		t.Fatal(err)
	}

	if err := s.PutHostSample(ctx, 60, h); err == nil {
		t.Fatal("unknown tier accepted")
	}

	got, err := s.HostSamples(ctx, HostTier5m, base)
	if err != nil || len(got) != 2 {
		t.Fatalf("%+v %v", got, err)
	}

	if g := got[0]; g.CPUBusy != 20 || g.MemTotal != 4<<30 || len(g.Disks) != 1 || g.Disks[0] != h.Disks[0] || g.Net[0] != h.Net[0] {
		t.Fatalf("first %+v", g)
	}

	if g := got[1]; g.Disks == nil || len(g.Disks) != 0 || g.Net == nil {
		t.Fatalf("empty lists %+v", g)
	}

	if err := s.PruneHostSamples(ctx, HostTier5m, base.Add(time.Minute)); err != nil {
		t.Fatal(err)
	}

	if got, _ := s.HostSamples(ctx, HostTier5m, time.Time{}); len(got) != 1 {
		t.Fatalf("after prune %+v", got)
	}

	if got, _ := s.HostSamples(ctx, HostTier1h, time.Time{}); len(got) != 1 {
		t.Fatalf("other tier pruned %+v", got)
	}
}
