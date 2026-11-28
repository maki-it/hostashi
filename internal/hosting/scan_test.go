// SPDX-License-Identifier: AGPL-3.0-or-later

package hosting

import (
	"context"
	"errors"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/maki-it/hostashi/internal/agentapi"
	"github.com/maki-it/hostashi/internal/agentrpc"
	"github.com/maki-it/hostashi/internal/store"
)

type fakeScans struct {
	mu          sync.Mutex
	engine      string
	started     []agentapi.ScanStartRequest
	state       map[int64]agentapi.ScanStatus
	quarantined []agentapi.ScanQuarantineRequest
	restored    []agentapi.ScanRestoreRequest
	qErr        error
	realtime    string
	rtPolls     []agentapi.ScanRealtimePollRequest
	rtFound     []agentapi.RealtimeFinding
	signatures  string
}

func (f *fakeScans) ScanEngineStatus(context.Context) (agentapi.ScanEngineStatus, error) {
	f.mu.Lock()
	defer f.mu.Unlock()

	return agentapi.ScanEngineStatus{State: f.engine, Realtime: f.realtime, Version: "1.4.6", Signatures: f.signatures}, nil
}

func (f *fakeScans) ScanRealtimePoll(_ context.Context, req agentapi.ScanRealtimePollRequest) (agentapi.ScanRealtimeFindings, error) {
	f.mu.Lock()
	defer f.mu.Unlock()

	f.rtPolls = append(f.rtPolls, req)
	out := agentapi.ScanRealtimeFindings{Active: true, Findings: f.rtFound, Cursor: req.Cursor + int64(len(f.rtFound))}
	f.rtFound = nil

	return out, nil
}

func (f *fakeScans) ScanEngineSet(_ context.Context, req agentapi.ScanEngineSetRequest) (agentapi.ScanEngineStatus, error) {
	f.mu.Lock()
	defer f.mu.Unlock()

	if req.Realtime != nil {
		if f.engine != agentapi.ScanEngineOn {
			return agentapi.ScanEngineStatus{}, &agentrpc.Error{Code: agentrpc.CodePrecondition, Message: "off"}
		}

		f.realtime = agentapi.ScanEngineOff
		if *req.Realtime {
			f.realtime = agentapi.ScanEngineOn
		}

		return agentapi.ScanEngineStatus{State: f.engine, Realtime: f.realtime}, nil
	}

	if req.On {
		f.engine = agentapi.ScanEngineOn
	} else {
		f.engine = agentapi.ScanEngineOff
	}

	return agentapi.ScanEngineStatus{State: f.engine}, nil
}

func (f *fakeScans) ScanStart(_ context.Context, req agentapi.ScanStartRequest) (agentapi.ScanStatus, error) {
	f.mu.Lock()
	defer f.mu.Unlock()

	f.started = append(f.started, req)

	return agentapi.ScanStatus{Site: req.Site, ID: req.ID, State: agentapi.ScanRunning}, nil
}

func (f *fakeScans) ScanStatus(_ context.Context, req agentapi.ScanStatusRequest) (agentapi.ScanStatus, error) {
	f.mu.Lock()
	defer f.mu.Unlock()

	if st, ok := f.state[req.ID]; ok {
		return st, nil
	}

	return agentapi.ScanStatus{Site: req.Site, ID: req.ID, State: agentapi.ScanRunning}, nil
}

func (f *fakeScans) ScanQuarantine(_ context.Context, req agentapi.ScanQuarantineRequest) (agentapi.QuarantineItem, error) {
	f.mu.Lock()
	defer f.mu.Unlock()

	if f.qErr != nil {
		return agentapi.QuarantineItem{}, f.qErr
	}

	f.quarantined = append(f.quarantined, req)

	return agentapi.QuarantineItem{ID: strings.Repeat("a", 32), Path: req.Path, SHA256: req.SHA256}, nil
}

func (f *fakeScans) ScanRestore(_ context.Context, req agentapi.ScanRestoreRequest) (agentapi.ScanRestored, error) {
	f.mu.Lock()
	defer f.mu.Unlock()

	f.restored = append(f.restored, req)

	return agentapi.ScanRestored{Path: "x"}, nil
}

func (f *fakeScans) finish(id int64, st agentapi.ScanStatus) {
	f.mu.Lock()
	defer f.mu.Unlock()

	if f.state == nil {
		f.state = map[int64]agentapi.ScanStatus{}
	}

	f.state[id] = st
}

func scanService(t *testing.T) (*Service, *fakeScans) {
	t.Helper()

	s, _, _ := newService(t)

	f := &fakeScans{engine: agentapi.ScanEngineOff}
	s.EnableScans(f)

	for _, n := range []string{"blog", "shop"} {
		if _, err := s.Create(t.Context(), 1, CreateRequest{Name: n, PHP: "8.4", Domains: []string{n + ".example.com"}}); err != nil {
			t.Fatal(err)
		}
	}

	return s, f
}

var (
	shaA = strings.Repeat("a", 64)
	shaB = strings.Repeat("b", 64)
)

func TestScanRunAndFindings(t *testing.T) {
	s, f := scanService(t)
	ctx := t.Context()

	var mails []string

	s.ObserveMalware(func(_ context.Context, site string, _ store.ScanRun, found []store.ScanFinding) {
		for _, x := range found {
			mails = append(mails, site+":"+x.Path)
		}
	})

	if _, err := s.StartScan(ctx, 1, "blog", store.ScanSourcePanel, nil); !errors.Is(err, ErrScanEngineOff) {
		t.Fatalf("engine off: %v", err)
	}

	if _, err := s.SetScanEngine(ctx, 1, true); err != nil {
		t.Fatal(err)
	}

	r1, err := s.StartScan(ctx, 1, "blog", store.ScanSourcePanel, nil)
	if err != nil || r1.State != store.ScanQueued {
		t.Fatalf("start: %+v %v", r1, err)
	}

	if _, err := s.StartScan(ctx, 1, "blog", store.ScanSourceAPI, nil); !errors.Is(err, ErrScanBusy) {
		t.Fatalf("second: %v", err)
	}

	r2, err := s.StartScan(ctx, 1, "shop", store.ScanSourceAPI, nil)
	if err != nil {
		t.Fatal(err)
	}

	// One at a time: the first starts, the second waits.
	s.pollScans(ctx)
	s.pollScans(ctx)

	if len(f.started) != 1 || f.started[0].ID != r1.ID || f.started[0].Site != "blog" {
		t.Fatalf("started %+v", f.started)
	}

	// A running run carries the progress of the last poll.
	if v, _ := s.SiteScan(ctx, "blog"); v.Progress[r1.ID] != (agentapi.ScanProgress{}) || len(v.Progress) != 1 {
		t.Fatalf("progress before a poll: %+v", v.Progress)
	}

	f.finish(r1.ID, agentapi.ScanStatus{State: agentapi.ScanRunning, Progress: &agentapi.ScanProgress{Scanned: 40, Total: 100}})
	s.pollScans(ctx)

	if v, _ := s.SiteScan(ctx, "blog"); v.Progress[r1.ID] != (agentapi.ScanProgress{Scanned: 40, Total: 100}) {
		t.Fatalf("progress: %+v", v.Progress)
	}

	f.finish(r1.ID, agentapi.ScanStatus{State: agentapi.ScanSucceeded, Findings: []agentapi.ScanFinding{
		{Path: "a.php", Signature: "Php.Shell-1", SHA256: shaA, Size: 3},
		{Path: "b.php", Signature: "Php.Shell-1", SHA256: shaB, Size: 4},
	}})
	s.pollScans(ctx)

	if len(mails) != 2 {
		t.Fatalf("mails %v", mails)
	}

	// The queue moves on in the same poll once the first run is done.
	if len(f.started) != 2 || f.started[1].ID != r2.ID {
		t.Fatalf("second run: %+v", f.started)
	}

	v, err := s.SiteScan(ctx, "blog")
	if err != nil || len(v.Findings) != 2 || v.Runs[0].State != store.ScanSucceeded || v.Runs[0].Found != 2 || v.Runs[0].NewFound != 2 {
		t.Fatalf("site scan %+v %v", v, err)
	}

	if len(v.Progress) != 0 {
		t.Fatalf("progress of a finished run: %+v", v.Progress)
	}

	if _, ok := s.scanProgress.Load(r1.ID); ok {
		t.Fatal("progress kept after the run")
	}

	var a, b store.ScanFinding

	for _, x := range v.Findings {
		switch x.Path {
		case "a.php":
			a = x
		case "b.php":
			b = x
		}
	}

	// Ignore a, quarantine b.
	if _, err := s.IgnoreFinding(ctx, 1, "blog", a.ID, true); err != nil {
		t.Fatal(err)
	}

	if _, err := s.IgnoreFinding(ctx, 1, "blog", a.ID, true); !errors.Is(err, ErrFindingState) {
		t.Fatalf("ignore twice: %v", err)
	}

	q, err := s.QuarantineFinding(ctx, 1, "blog", b.ID)
	if err != nil || q.State != store.FindingQuarantined || len(f.quarantined) != 1 || f.quarantined[0].SHA256 != shaB {
		t.Fatalf("quarantine %+v %v", q, err)
	}

	if _, err := s.QuarantineFinding(ctx, 1, "shop", b.ID); !errors.Is(err, ErrFindingNotFound) {
		t.Fatalf("other site: %v", err)
	}

	// Next scan: a again (still ignored), a copy of a elsewhere (ignored
	// too: same content and signature), b is gone, c is new.
	r3, err := s.StartScan(ctx, 1, "blog", store.ScanSourcePanel, nil)
	if err != nil {
		t.Fatal(err)
	}

	f.finish(r2.ID, agentapi.ScanStatus{State: agentapi.ScanSucceeded})
	s.pollScans(ctx)
	s.pollScans(ctx)

	f.finish(r3.ID, agentapi.ScanStatus{State: agentapi.ScanSucceeded, Findings: []agentapi.ScanFinding{
		{Path: "a.php", Signature: "Php.Shell-1", SHA256: shaA, Size: 3},
		{Path: "copy/a.php", Signature: "Php.Shell-1", SHA256: shaA, Size: 3},
		{Path: "c.php", Signature: "Js.Inject-1", SHA256: shaB, Size: 9},
	}})

	mails = nil

	s.pollScans(ctx)

	if len(mails) != 1 || mails[0] != "blog:c.php" {
		t.Fatalf("mails %v", mails)
	}

	v, _ = s.SiteScan(ctx, "blog")

	states := map[string]string{}
	for _, x := range v.Findings {
		states[x.Path] = x.State
	}

	if states["a.php"] != store.FindingIgnored || states["copy/a.php"] != store.FindingIgnored || states["b.php"] != store.FindingQuarantined || states["c.php"] != store.FindingOpen {
		t.Fatalf("states %v", states)
	}

	if _, err := s.RestoreFinding(ctx, 1, "blog", b.ID); err != nil || len(f.restored) != 1 || f.restored[0].ID != strings.Repeat("a", 32) {
		t.Fatalf("restore %v %+v", err, f.restored)
	}

	// The file changed since the scan: the agent refuses, the finding is
	// gone.
	var c store.ScanFinding

	for _, x := range v.Findings {
		if x.Path == "c.php" {
			c = x
		}
	}

	f.qErr = &agentrpc.Error{Code: agentrpc.CodePrecondition, Message: "changed"}

	if _, err := s.QuarantineFinding(ctx, 1, "blog", c.ID); err == nil {
		t.Fatal("quarantine of a changed file")
	}

	if got, _ := s.st.ScanFinding(ctx, c.SiteID, c.ID); got.State != store.FindingGone {
		t.Fatalf("changed file: %+v", got)
	}
}

func TestScanFailures(t *testing.T) {
	s, f := scanService(t)
	ctx := t.Context()

	if _, err := s.SetScanEngine(ctx, 1, true); err != nil {
		t.Fatal(err)
	}

	r, err := s.StartScan(ctx, 1, "blog", store.ScanSourcePanel, nil)
	if err != nil {
		t.Fatal(err)
	}

	s.pollScans(ctx)
	f.finish(r.ID, agentapi.ScanStatus{State: agentapi.ScanFailed, Error: "the scan ran out of memory"})
	s.pollScans(ctx)

	v, _ := s.SiteScan(ctx, "blog")
	if v.Runs[0].State != store.ScanFailed || v.Runs[0].Error != "the scan ran out of memory" {
		t.Fatalf("failed run %+v", v.Runs[0])
	}

	// Switched off with a queued scan: it fails, nothing starts.
	r, _ = s.StartScan(ctx, 1, "blog", store.ScanSourcePanel, nil)
	f.engine = agentapi.ScanEngineOff

	s.pollScans(ctx)

	v, _ = s.SiteScan(ctx, "blog")
	if v.Runs[0].ID != r.ID || v.Runs[0].State != store.ScanFailed || len(f.started) != 1 {
		t.Fatalf("engine off: %+v %d", v.Runs[0], len(f.started))
	}

	if err := s.SetScanSchedule(ctx, 1, "blog", "hourly"); !errors.Is(err, ErrScanSchedule) {
		t.Fatalf("bad schedule: %v", err)
	}
}

func TestScanSchedule(t *testing.T) {
	s, f := scanService(t)
	ctx := t.Context()
	f.engine = agentapi.ScanEngineOn

	if err := s.SetScanSchedule(ctx, 1, "shop", store.ScanScheduleOff); err != nil {
		t.Fatal(err)
	}

	if err := s.SetScanSchedule(ctx, 1, "blog", store.ScanScheduleDaily); err != nil {
		t.Fatal(err)
	}

	slot := lastScanSlot("blog", store.ScanScheduleDaily, time.Now())

	// Just after the slot: one scan of blog, none of shop.
	s.now = func() time.Time { return slot.Add(time.Minute) }
	s.pollScans(ctx)
	s.pollScans(ctx)

	v, _ := s.SiteScan(ctx, "blog")
	if len(v.Runs) != 1 || v.Runs[0].Source != store.ScanSourceSchedule {
		t.Fatalf("blog runs %+v", v.Runs)
	}

	if w, _ := s.SiteScan(ctx, "shop"); len(w.Runs) != 0 {
		t.Fatalf("shop runs %+v", w.Runs)
	}

	// Past the window: the missed slot waits for the next one.
	f.finish(v.Runs[0].ID, agentapi.ScanStatus{State: agentapi.ScanSucceeded})
	s.pollScans(ctx)

	s.now = func() time.Time { return slot.AddDate(0, 0, 1).Add(scanWindow + time.Minute) }
	s.pollScans(ctx)

	if v, _ := s.SiteScan(ctx, "blog"); len(v.Runs) != 1 {
		t.Fatalf("late: %+v", v.Runs)
	}
}

func TestScanSlots(t *testing.T) {
	now := time.Date(2026, 11, 24, 12, 0, 0, 0, time.Local)

	for _, site := range []string{"blog", "shop", "a", "wordpress-site-with-a-long-name"} {
		for _, sched := range []string{store.ScanScheduleDaily, store.ScanScheduleWeekly} {
			last := lastScanSlot(site, sched, now)
			next := nextScanSlot(site, sched, now)

			if last.After(now) || !next.After(now) {
				t.Errorf("%s %s: last %v next %v", site, sched, last, next)
			}

			if h := last.Hour(); h < 1 || h >= 5 {
				t.Errorf("%s %s: slot at %v, not at night", site, sched, last)
			}

			span := 24 * time.Hour
			if sched == store.ScanScheduleWeekly {
				span *= 7
			}

			if d := next.Sub(last); d < span-time.Hour || d > span+time.Hour {
				t.Errorf("%s %s: %v between slots", site, sched, d)
			}
		}
	}

	if lastScanSlot("blog", store.ScanScheduleDaily, now) != lastScanSlot("blog", store.ScanScheduleDaily, now.Add(time.Hour)) {
		t.Error("slot moves within a day")
	}
}

func TestScanRealtime(t *testing.T) {
	s, f := scanService(t)
	ctx := t.Context()
	now := time.Date(2026, 11, 24, 12, 0, 0, 0, time.Local)
	s.now = func() time.Time { return now }

	var mails []string

	s.ObserveMalware(func(_ context.Context, site string, r store.ScanRun, found []store.ScanFinding) {
		if r.Source != store.ScanSourceRealtime {
			t.Errorf("run %+v", r)
		}

		for _, x := range found {
			mails = append(mails, site+":"+x.Path)
		}
	})

	if _, err := s.SetScanRealtime(ctx, 1, true); !errors.Is(err, ErrScanEngineOff) {
		t.Fatalf("real-time without ClamAV: %v", err)
	}

	if _, err := s.SetScanEngine(ctx, 1, true); err != nil {
		t.Fatal(err)
	}

	// Without the real-time mode nothing is polled.
	s.pollScans(ctx)

	if len(f.rtPolls) != 0 {
		t.Fatalf("polled while off: %+v", f.rtPolls)
	}

	if st, err := s.SetScanRealtime(ctx, 1, true); err != nil || st.Realtime != agentapi.ScanEngineOn {
		t.Fatalf("on: %+v %v", st, err)
	}

	finding := func(site, path, sha string) agentapi.RealtimeFinding {
		return agentapi.RealtimeFinding{Site: site, ScanFinding: agentapi.ScanFinding{Path: path, Signature: "Php.Shell-1", SHA256: sha, Size: 3}}
	}

	f.rtFound = []agentapi.RealtimeFinding{finding("blog", "a.php", shaA), finding("gone", "x.php", shaA)}

	s.pollScans(ctx)

	if len(f.rtPolls) != 1 || strings.Join(f.rtPolls[0].Sites, ",") != "blog,shop" || f.rtPolls[0].Cursor != 0 {
		t.Fatalf("poll %+v", f.rtPolls)
	}

	if strings.Join(mails, ",") != "blog:a.php" {
		t.Fatalf("mails %v", mails)
	}

	// Within the hour a new finding waits for the next email.
	f.rtFound = []agentapi.RealtimeFinding{finding("blog", "b.php", shaB)}
	now = now.Add(10 * time.Minute)

	s.pollScans(ctx)

	if f.rtPolls[1].Cursor != 2 || len(mails) != 1 {
		t.Fatalf("second poll %+v, mails %v", f.rtPolls[1], mails)
	}

	now = now.Add(time.Hour)

	s.pollScans(ctx)

	if strings.Join(mails, ",") != "blog:a.php,blog:b.php" {
		t.Fatalf("mails %v", mails)
	}

	v, err := s.SiteScan(ctx, "blog")
	if err != nil || len(v.Runs) != 1 || v.Runs[0].Source != store.ScanSourceRealtime || len(v.Findings) != 2 {
		t.Fatalf("site scan %+v %v", v, err)
	}

	// Real-time runs do not stand in for the schedule: blog's weekly scan
	// is still due at its slot.
	if last, _ := s.st.LastScanRuns(ctx); len(last) != 0 {
		t.Fatalf("last scans %+v", last)
	}
}

func TestMalwareDoctor(t *testing.T) {
	s, f := scanService(t)
	ctx := t.Context()
	now := time.Date(2026, 11, 24, 12, 0, 0, 0, time.UTC)
	s.now = func() time.Time { return now }

	checks := func() string {
		t.Helper()

		got, err := s.MalwareDoctor(ctx, now)
		if err != nil {
			t.Fatal(err)
		}

		var out []string
		for _, c := range got {
			out = append(out, c.ID+"/"+c.Site+"/"+c.Status+"/"+c.Summary)
		}

		return strings.Join(out, "; ")
	}

	if got := checks(); got != "" {
		t.Fatalf("off: %s", got)
	}

	if _, err := s.SetScanEngine(ctx, 1, true); err != nil {
		t.Fatal(err)
	}

	if got := checks(); got != "panel.malware//warn/ClamAV 1.4.6 has no signatures yet" {
		t.Fatalf("no signatures: %s", got)
	}

	f.signatures = now.Add(-5 * time.Hour).Format(time.RFC3339)

	if got := checks(); got != "panel.malware//ok/ClamAV 1.4.6, signatures from 2026-11-24 07:00 UTC" {
		t.Fatalf("fresh: %s", got)
	}

	f.signatures = now.Add(-4*24*time.Hour - time.Hour).Format(time.RFC3339)

	if got := checks(); got != "panel.malware//warn/ClamAV 1.4.6: the signatures are 4 days old" {
		t.Fatalf("stale: %s", got)
	}

	f.signatures = now.Format(time.RFC3339)

	f.rtFound = []agentapi.RealtimeFinding{
		{Site: "blog", ScanFinding: agentapi.ScanFinding{Path: "a.php", Signature: "Php.Shell-1", SHA256: shaA, Size: 3}},
		{Site: "blog", ScanFinding: agentapi.ScanFinding{Path: "b.php", Signature: "Php.Shell-1", SHA256: shaB, Size: 3}},
		{Site: "shop", ScanFinding: agentapi.ScanFinding{Path: "c.php", Signature: "Php.Shell-1", SHA256: shaA, Size: 3}},
	}

	if _, err := s.SetScanRealtime(ctx, 1, true); err != nil {
		t.Fatal(err)
	}

	s.pollScans(ctx)

	want := "panel.malware//ok/ClamAV 1.4.6, signatures from 2026-11-24 12:00 UTC; " +
		"site.malware/blog/warn/2 open virus scan findings; site.malware/shop/warn/1 open virus scan finding"
	if got := checks(); got != want {
		t.Fatalf("findings:\n got %s\nwant %s", got, want)
	}

	// Findings stay on the Health page after ClamAV is switched off.
	if _, err := s.SetScanEngine(ctx, 1, false); err != nil {
		t.Fatal(err)
	}

	if got := checks(); got != "site.malware/blog/warn/2 open virus scan findings; site.malware/shop/warn/1 open virus scan finding" {
		t.Fatalf("off with findings: %s", got)
	}
}

// The file manager's selection goes to the agent as it was picked.
func TestScanSelection(t *testing.T) {
	s, f := scanService(t)
	ctx := t.Context()

	if _, err := s.SetScanEngine(ctx, 1, true); err != nil {
		t.Fatal(err)
	}

	for _, bad := range [][]string{{"../x"}, {"/etc"}, {"a", "a"}, make([]string, agentapi.MaxScanTargets+1)} {
		if _, err := s.StartScan(ctx, 1, "blog", store.ScanSourcePanel, bad); !errors.Is(err, ErrInvalid) {
			t.Errorf("%q: %v", bad, err)
		}
	}

	r, err := s.StartScan(ctx, 1, "blog", store.ScanSourcePanel, []string{"htdocs/up", "htdocs/index.php"})
	if err != nil || len(r.Paths) != 2 {
		t.Fatalf("start %+v %v", r, err)
	}

	s.pollScans(ctx)

	if len(f.started) != 1 || !slices.Equal(f.started[0].Paths, []string{"htdocs/up", "htdocs/index.php"}) {
		t.Fatalf("started %+v", f.started)
	}
}

// A queued run only waits for another site when another site's run is
// running or queued ahead of it; alone it is about to start.
func TestScanWaitingForOther(t *testing.T) {
	s, f := scanService(t)
	ctx := t.Context()

	if _, err := s.SetScanEngine(ctx, 1, true); err != nil {
		t.Fatal(err)
	}

	waiting := func(site string) bool {
		t.Helper()

		v, err := s.SiteScan(ctx, site)
		if err != nil {
			t.Fatal(err)
		}

		return v.WaitingForOther
	}

	if waiting("blog") {
		t.Fatal("no run waits")
	}

	blog, err := s.StartScan(ctx, 1, "blog", store.ScanSourcePanel, nil)
	if err != nil {
		t.Fatal(err)
	}

	// Alone in the queue: it starts on the next poll.
	if waiting("blog") {
		t.Fatal("a lone queued run waits for another site")
	}

	if _, err := s.StartScan(ctx, 1, "shop", store.ScanSourcePanel, nil); err != nil {
		t.Fatal(err)
	}

	// Queued behind blog, which is queued too.
	if !waiting("shop") || waiting("blog") {
		t.Fatalf("queued: shop %v, blog %v", waiting("shop"), waiting("blog"))
	}

	s.pollScans(ctx)

	// blog runs: shop still waits, blog has no queued run.
	if !waiting("shop") || waiting("blog") {
		t.Fatalf("running: shop %v, blog %v", waiting("shop"), waiting("blog"))
	}

	f.finish(blog.ID, agentapi.ScanStatus{State: agentapi.ScanSucceeded})
	s.pollScans(ctx)

	if waiting("shop") {
		t.Fatal("shop waits after blog finished")
	}
}

func TestWaitsForOther(t *testing.T) {
	q := func(id, site int64) store.ScanRun { return store.ScanRun{ID: id, SiteID: site, State: store.ScanQueued} }
	r := func(id, site int64) store.ScanRun { return store.ScanRun{ID: id, SiteID: site, State: store.ScanRunning} }

	cases := []struct {
		name   string
		active []store.ScanRun
		want   bool
	}{
		{"nothing", nil, false},
		{"own run only", []store.ScanRun{q(1, 1)}, false},
		{"own run is running", []store.ScanRun{r(1, 1)}, false},
		{"other running", []store.ScanRun{r(1, 2), q(2, 1)}, true},
		{"other queued ahead", []store.ScanRun{q(1, 2), q(2, 1)}, true},
		{"other queued behind", []store.ScanRun{q(1, 1), q(2, 2)}, false},
		{"no own queued run, other running", []store.ScanRun{r(1, 2)}, false},
	}

	for _, c := range cases {
		if got := waitsForOther(c.active, 1); got != c.want {
			t.Errorf("%s: %v, want %v", c.name, got, c.want)
		}
	}
}
