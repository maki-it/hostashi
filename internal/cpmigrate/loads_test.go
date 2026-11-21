// SPDX-License-Identifier: AGPL-3.0-or-later

package cpmigrate_test

import (
	"bytes"
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/maki-it/hostashi/internal/agentapi"
	"github.com/maki-it/hostashi/internal/cloudpanel/plan"
	"github.com/maki-it/hostashi/internal/cpmigrate"
	"github.com/maki-it/hostashi/internal/secrets"
	"github.com/maki-it/hostashi/internal/store"
)

// stateImporter reports a state that the test changes.
type stateImporter struct {
	fakeImporter

	mu    sync.Mutex
	state string
}

func (f *stateImporter) set(state string) {
	f.mu.Lock()
	defer f.mu.Unlock()

	f.state = state
}

func (f *stateImporter) Get(_ context.Context, id string) (agentapi.CloudPanelImport, error) {
	f.mu.Lock()
	defer f.mu.Unlock()

	return agentapi.CloudPanelImport{
		ID: id, Site: "alpha", State: f.state,
		Databases: []agentapi.CloudPanelDBMap{{From: "alpha_wp", To: "alpha_db"}, {From: "alpha_shop", To: "alpha_two"}},
		Dumps:     []string{"cloudpanel-import-db/alpha_db-20261119T120000Z.sql", "cloudpanel-import-db/alpha_two-20261119T120000Z.sql"},
	}, nil
}

type fakeLoader struct {
	mu    sync.Mutex
	calls []string
	fail  map[string]bool
	done  chan struct{}
}

func (f *fakeLoader) LoadDatabaseDump(_ context.Context, _ int64, database, path string) (agentapi.DBJob, error) {
	f.mu.Lock()
	defer f.mu.Unlock()

	f.calls = append(f.calls, database+" "+path)

	if f.done != nil && len(f.calls) == 2 {
		close(f.done)
	}

	if f.fail[database] {
		return agentapi.DBJob{}, errors.New("agent says no")
	}

	return agentapi.DBJob{ID: "00000000000000a" + string(rune('0'+len(f.calls))), Kind: "import", Database: database, Path: path, AsOwner: true, State: agentapi.JobRunning}, nil
}

func (f *fakeLoader) DatabaseJob(_ context.Context, database, id string) (agentapi.DBJob, error) {
	return agentapi.DBJob{ID: id, Database: database, AsOwner: true, State: agentapi.JobDone}, nil
}

func (f *fakeLoader) count() int {
	f.mu.Lock()
	defer f.mu.Unlock()

	return len(f.calls)
}

func loadService(t *testing.T) (*cpmigrate.Service, *stateImporter, string) {
	t.Helper()

	svc, imp, rep, _ := loadServiceStore(t)

	return svc, imp, rep
}

func loadServiceStore(t *testing.T) (*cpmigrate.Service, *stateImporter, string, *store.Store) {
	t.Helper()

	existing := []string{}
	target := func(context.Context) (plan.Target, error) {
		return plan.Target{Sites: existing, PHP: []string{"8.4"}}, nil
	}

	svc, st := newService(t, target)

	rep, err := svc.Upload(t.Context(), 1, bytes.NewReader(sitesReport(t)))
	if err != nil {
		t.Fatal(err)
	}

	imp := &stateImporter{state: agentapi.ImportRunning}
	svc.Connect(func(context.Context, int64, cpmigrate.NewSite) (cpmigrate.Created, error) {
		return cpmigrate.Created{}, nil
	}, imp)

	return svc, imp, rep.ID, st
}

func TestLoadDumpsAfterImport(t *testing.T) {
	svc, imp, rep := loadService(t)
	ld := &fakeLoader{done: make(chan struct{})}

	svc.EnableLoads(t.Context(), ld, time.Millisecond)

	got, err := svc.StartImport(t.Context(), 1, rep, "alpha", []agentapi.CloudPanelDBMap{{From: "alpha_wp", To: "alpha_db"}})
	if err != nil {
		t.Fatal(err)
	}

	// Nothing is loaded while the files are still copied.
	time.Sleep(20 * time.Millisecond)

	if ld.count() != 0 {
		t.Fatal("loaded before the import was done")
	}

	if _, err := svc.LoadDumps(t.Context(), 1, got.ID); !errors.Is(err, cpmigrate.ErrNotDone) {
		t.Fatalf("load of a running import: %v", err)
	}

	imp.set(agentapi.ImportDone)

	select {
	case <-ld.done:
	case <-time.After(5 * time.Second):
		t.Fatal("dumps were not loaded")
	}

	want := []string{
		"alpha_db cloudpanel-import-db/alpha_db-20261119T120000Z.sql",
		"alpha_two cloudpanel-import-db/alpha_two-20261119T120000Z.sql",
	}

	ld.mu.Lock()
	calls := append([]string{}, ld.calls...)
	ld.mu.Unlock()

	if len(calls) != 2 || calls[0] != want[0] || calls[1] != want[1] {
		t.Fatalf("calls %q", calls)
	}

	// Loading again by hand does not load twice.
	loads, err := svc.LoadDumps(t.Context(), 1, got.ID)
	if err != nil || len(loads) != 2 || ld.count() != 2 {
		t.Fatalf("again: %+v %v (%d calls)", loads, err, ld.count())
	}

	loads, err = svc.Loads(t.Context(), got.ID)
	if err != nil || len(loads) != 2 || loads[0].Job == nil || loads[0].Job.State != agentapi.JobDone || loads[1].Database != "alpha_two" {
		t.Fatalf("loads %+v %v", loads, err)
	}
}

func TestLoadDumpsRetriesFailedStarts(t *testing.T) {
	svc, imp, _ := loadService(t)
	ld := &fakeLoader{fail: map[string]bool{"alpha_two": true}}

	svc.EnableLoads(t.Context(), ld, time.Hour)
	imp.set(agentapi.ImportDone)

	const id = "0123456789abcdef"

	loads, err := svc.LoadDumps(t.Context(), 1, id)
	if err != nil || loads[0].Job == nil || loads[1].Job != nil || loads[1].Error == "" {
		t.Fatalf("first: %+v %v", loads, err)
	}

	ld.mu.Lock()
	ld.fail = nil
	ld.mu.Unlock()

	loads, err = svc.LoadDumps(t.Context(), 1, id)
	if err != nil || loads[1].Job == nil || loads[1].Error != "" || ld.count() != 3 {
		t.Fatalf("retry: %+v %v (%d calls)", loads, err, ld.count())
	}

	if _, err := svc.LoadDumps(t.Context(), 1, "../x"); !errors.Is(err, cpmigrate.ErrNotFound) {
		t.Fatalf("bad id: %v", err)
	}

	if l, err := svc.Loads(t.Context(), "fedcba9876543210"); err != nil || len(l) != 0 {
		t.Fatalf("unknown import: %+v %v", l, err)
	}
}

func TestLoadDumpsNotEnabled(t *testing.T) {
	svc, imp, _ := loadService(t)
	imp.set(agentapi.ImportDone)

	if _, err := svc.LoadDumps(t.Context(), 1, "0123456789abcdef"); !errors.Is(err, cpmigrate.ErrNoImporter) {
		t.Fatalf("err = %v", err)
	}
}

// reopen builds a new service on the same store, as a panel start does.
func reopen(t *testing.T, st *store.Store, imp cpmigrate.Importer) *cpmigrate.Service {
	t.Helper()

	box, err := secrets.New(bytes.Repeat([]byte{7}, 32))
	if err != nil {
		t.Fatal(err)
	}

	svc := cpmigrate.New(st, box, func(context.Context) (plan.Target, error) { return plan.Target{PHP: []string{"8.4"}}, nil }, nil)
	svc.Connect(func(context.Context, int64, cpmigrate.NewSite) (cpmigrate.Created, error) {
		return cpmigrate.Created{}, nil
	}, imp)

	return svc
}

func loadsState(t *testing.T, st *store.Store, id string) string {
	t.Helper()

	d, err := st.DumpLoadsOf(t.Context(), id)
	if err != nil {
		t.Fatal(err)
	}

	return d.State
}

func waitLoadsState(t *testing.T, st *store.Store, id, want string) {
	t.Helper()

	for range 500 {
		if loadsState(t, st, id) == want {
			return
		}

		time.Sleep(10 * time.Millisecond)
	}

	t.Fatalf("state %q, want %q", loadsState(t, st, id), want)
}

func TestLoadDumpsResumeAfterPanelRestart(t *testing.T) {
	svc, imp, rep, st := loadServiceStore(t)
	ld := &fakeLoader{done: make(chan struct{})}

	ctx, stop := context.WithCancel(t.Context())
	svc.EnableLoads(ctx, ld, time.Millisecond)

	got, err := svc.StartImport(t.Context(), 1, rep, "alpha", []agentapi.CloudPanelDBMap{{From: "alpha_wp", To: "alpha_db"}})
	if err != nil {
		t.Fatal(err)
	}

	// The panel stops while the files are still copied: the wait is kept.
	stop()
	time.Sleep(20 * time.Millisecond)

	if s := loadsState(t, st, got.ID); s != store.DumpLoadsWatching {
		t.Fatalf("state after stop %q", s)
	}

	imp.set(agentapi.ImportDone)

	again := reopen(t, st, imp)
	again.EnableLoads(t.Context(), ld, time.Millisecond)

	select {
	case <-ld.done:
	case <-time.After(5 * time.Second):
		t.Fatal("dumps were not loaded after the restart")
	}

	waitLoadsState(t, st, got.ID, store.DumpLoadsLoaded)

	// Another restart neither loads twice nor forgets the jobs.
	third := reopen(t, st, imp)
	third.EnableLoads(t.Context(), ld, time.Millisecond)

	loads, err := third.LoadDumps(t.Context(), 1, got.ID)
	if err != nil || len(loads) != 2 || loads[0].Job == nil || loads[1].Job == nil || ld.count() != 2 {
		t.Fatalf("after second restart: %+v %v (%d calls)", loads, err, ld.count())
	}

	if loads, err = third.Loads(t.Context(), got.ID); err != nil || len(loads) != 2 || loads[0].Job.State != agentapi.JobDone {
		t.Fatalf("loads %+v %v", loads, err)
	}
}

func TestLoadDumpsStopWhenImportFails(t *testing.T) {
	svc, imp, rep, st := loadServiceStore(t)
	ld := &fakeLoader{}

	svc.EnableLoads(t.Context(), ld, time.Millisecond)

	got, err := svc.StartImport(t.Context(), 1, rep, "alpha", []agentapi.CloudPanelDBMap{{From: "alpha_wp", To: "alpha_db"}})
	if err != nil {
		t.Fatal(err)
	}

	imp.set(agentapi.ImportFailed)
	waitLoadsState(t, st, got.ID, store.DumpLoadsStopped)

	if ld.count() != 0 {
		t.Fatal("loaded the dumps of a failed import")
	}

	// A stopped wait is not resumed.
	reopen(t, st, imp).EnableLoads(t.Context(), ld, time.Millisecond)
	imp.set(agentapi.ImportDone)
	time.Sleep(20 * time.Millisecond)

	if ld.count() != 0 {
		t.Fatal("resumed a stopped wait")
	}
}

func TestLoadDumpsResumeSkipsOldAndOrphaned(t *testing.T) {
	_, imp, _, st := loadServiceStore(t)
	imp.set(agentapi.ImportDone)

	old := time.Now().Add(-49 * time.Hour)
	if err := st.WatchDumpLoads(t.Context(), "00000000000000a1", new(int64(1)), old); err != nil {
		t.Fatal(err)
	}

	// The user who started it was deleted.
	if err := st.WatchDumpLoads(t.Context(), "00000000000000a2", nil, time.Now()); err != nil {
		t.Fatal(err)
	}

	ld := &fakeLoader{}
	reopen(t, st, imp).EnableLoads(t.Context(), ld, time.Millisecond)
	time.Sleep(20 * time.Millisecond)

	for _, id := range []string{"00000000000000a1", "00000000000000a2"} {
		if s := loadsState(t, st, id); s != store.DumpLoadsStopped {
			t.Fatalf("%s: state %q", id, s)
		}
	}

	if ld.count() != 0 {
		t.Fatalf("%d loads", ld.count())
	}
}
