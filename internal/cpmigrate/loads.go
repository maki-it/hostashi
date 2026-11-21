// SPDX-License-Identifier: AGPL-3.0-or-later

package cpmigrate

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"sync"
	"time"

	"github.com/maki-it/hostashi/internal/agentapi"
	"github.com/maki-it/hostashi/internal/store"
)

// ErrNotDone means the import has not finished copying, or failed, so its
// dumps cannot be loaded.
var ErrNotDone = errors.New("cpmigrate: the import is not done")

// DumpLoader loads dumps into this server's databases (hosting.Service).
type DumpLoader interface {
	// LoadDatabaseDump starts loading as the database's owner account,
	// keeping views, triggers and routines working (ADR-0030).
	LoadDatabaseDump(ctx context.Context, actor int64, database, path string) (agentapi.DBJob, error)
	DatabaseJob(ctx context.Context, database, id string) (agentapi.DBJob, error)
}

// DumpLoad is one dump of an import and the load job started for it.
type DumpLoad struct {
	Database string `json:"database"`
	Path     string `json:"path"`
	// Job is the load's job; nil when starting it failed.
	Job *agentapi.DBJob `json:"job,omitempty"`
	// Error says why the load could not start.
	Error string `json:"error,omitempty"`
}

// Defaults of the watch that loads the dumps once an import is done.
const (
	DefaultLoadPoll = 5 * time.Second
	maxWatch        = 48 * time.Hour
)

type loads struct {
	l    DumpLoader
	ctx  context.Context //nolint:containedctx // watches outlive the request that starts them
	poll time.Duration

	// mu serializes LoadDumps, so a dump is never started twice.
	mu sync.Mutex
}

// EnableLoads makes every import with databases load its dumps on its own
// once the files are copied (ADR-0030). ctx bounds the watches; poll is how
// often the import is read, DefaultLoadPoll if zero. The loads are kept in
// the panel database: imports that still waited when the panel stopped are
// watched again here.
func (s *Service) EnableLoads(ctx context.Context, l DumpLoader, poll time.Duration) {
	if poll <= 0 {
		poll = DefaultLoadPoll
	}

	s.loads = &loads{l: l, ctx: ctx, poll: poll}

	waiting, err := s.st.WatchingDumpLoads(ctx)
	if err != nil {
		s.log.ErrorContext(ctx, "cloudpanel: read imports that wait for loading their dumps", "err", err)

		return
	}

	for _, w := range waiting {
		deadline := w.CreatedAt.Add(maxWatch)

		// The user who started the import is gone, or the wait is over.
		if w.StartedBy == nil || !s.now().Before(deadline) {
			s.stopLoads(ctx, w.ImportID)

			continue
		}

		//nolint:contextcheck // the watch runs under the panel context of the loads
		s.watch(*w.StartedBy, w.ImportID, deadline)
	}
}

// watchNew records that a new import's dumps are to be loaded and starts
// waiting for it.
func (s *Service) watchNew(ctx context.Context, actor int64, id string) {
	now := s.now()

	if err := s.st.WatchDumpLoads(ctx, id, &actor, now); err != nil {
		// The watch still runs; only a panel restart would lose it.
		s.log.ErrorContext(ctx, "cloudpanel: keep the import for loading its dumps", "import", id, "err", err)
	}

	//nolint:contextcheck // the watch outlives the request and runs under the panel context
	s.watch(actor, id, now.Add(maxWatch))
}

// watch waits in the background until the import has ended and then loads
// its dumps.
func (s *Service) watch(actor int64, id string, deadline time.Time) {
	ld := s.loads

	go func() {
		ctx, cancel := context.WithDeadline(ld.ctx, deadline)
		defer cancel()

		t := time.NewTicker(ld.poll)
		defer t.Stop()

		for {
			select {
			case <-ctx.Done():
				// A panel stop keeps the wait for the next start.
				if ld.ctx.Err() == nil {
					s.stopLoads(ld.ctx, id)
				}

				return
			case <-t.C:
			}

			rctx, rcancel := context.WithTimeout(ctx, 10*time.Second)
			imp, err := s.importer.Get(rctx, id)

			rcancel()

			switch {
			case err != nil:
				s.log.WarnContext(ctx, "cloudpanel: read import for loading its dumps", "import", id, "err", err)
			case imp.State == agentapi.ImportFailed:
				s.stopLoads(ctx, id)

				return
			case imp.State == agentapi.ImportDone:
				if _, err := s.LoadDumps(ctx, actor, id); err != nil {
					s.log.ErrorContext(ctx, "cloudpanel: load dumps", "import", id, "err", err)
				}

				return
			}
		}
	}()
}

func (s *Service) stopLoads(ctx context.Context, id string) {
	if err := s.st.StopDumpLoads(ctx, id); err != nil {
		s.log.ErrorContext(ctx, "cloudpanel: stop waiting for an import", "import", id, "err", err)
	}
}

// LoadDumps starts one load per dump of a finished import, into the
// database the import mapped it to. A dump whose load started already is
// not loaded again, also after a panel restart; one whose start failed is
// tried again.
func (s *Service) LoadDumps(ctx context.Context, actor int64, id string) ([]DumpLoad, error) {
	if s.importer == nil || s.loads == nil {
		return nil, ErrNoImporter
	}

	if !agentapi.ValidImportID(id) {
		return nil, ErrNotFound
	}

	imp, err := s.importer.Get(ctx, id)
	if err != nil {
		return nil, err
	}

	if imp.State != agentapi.ImportDone || len(imp.Dumps) != len(imp.Databases) {
		return nil, ErrNotDone
	}

	ld := s.loads

	ld.mu.Lock()
	defer ld.mu.Unlock()

	row, prev, err := s.keptLoads(ctx, id)
	if err != nil {
		return nil, err
	}

	if row.ImportID == "" {
		row = store.DumpLoads{ImportID: id, StartedBy: &actor, CreatedAt: s.now()}
	}

	out := make([]DumpLoad, len(imp.Databases))
	started := 0

	for i, db := range imp.Databases {
		out[i] = DumpLoad{Database: db.To, Path: imp.Dumps[i]}

		if i < len(prev) && prev[i].Job != nil {
			out[i] = prev[i]

			continue
		}

		j, err := ld.l.LoadDatabaseDump(ctx, actor, db.To, imp.Dumps[i])
		if err != nil {
			out[i].Error = loadError(err)
			s.log.WarnContext(ctx, "cloudpanel: start loading a dump", "import", id, "database", db.To, "err", err)

			continue
		}

		out[i].Job = &j
		started++
	}

	raw, err := json.Marshal(out)
	if err != nil {
		return nil, fmt.Errorf("cpmigrate: %w", err)
	}

	row.State, row.Loads = store.DumpLoadsLoaded, raw

	if err := s.st.SaveDumpLoads(ctx, row); err != nil {
		// The jobs run; a later LoadDumps could start them again.
		s.log.ErrorContext(ctx, "cloudpanel: keep the started loads", "import", id, "err", err)
	}

	s.audit(ctx, actor, "cloudpanel.import.load", imp.Site, map[string]string{"import": id, "started": strconv.Itoa(started)})

	return copyLoads(out), nil
}

// keptLoads reads the stored loads of an import; none is an empty row.
func (s *Service) keptLoads(ctx context.Context, id string) (store.DumpLoads, []DumpLoad, error) {
	row, err := s.st.DumpLoadsOf(ctx, id)

	switch {
	case errors.Is(err, store.ErrNotFound):
		return store.DumpLoads{}, nil, nil
	case err != nil:
		return store.DumpLoads{}, nil, err
	}

	var prev []DumpLoad
	if err := json.Unmarshal(row.Loads, &prev); err != nil {
		return store.DumpLoads{}, nil, fmt.Errorf("cpmigrate: stored loads of import %s: %w", id, err)
	}

	return row, prev, nil
}

// Loads reports the loads of an import with each job as it is now. An
// import without loads has an empty list.
func (s *Service) Loads(ctx context.Context, id string) ([]DumpLoad, error) {
	if s.loads == nil {
		return nil, ErrNoImporter
	}

	if !agentapi.ValidImportID(id) {
		return nil, ErrNotFound
	}

	_, out, err := s.keptLoads(ctx, id)
	if err != nil {
		return nil, err
	}

	for i := range out {
		if out[i].Job == nil {
			continue
		}

		j, err := s.loads.l.DatabaseJob(ctx, out[i].Database, out[i].Job.ID)
		if err != nil {
			out[i].Error = "job state unknown"

			continue
		}

		out[i].Job = &j
	}

	return out, nil
}

func copyLoads(in []DumpLoad) []DumpLoad {
	out := make([]DumpLoad, len(in))
	for i, l := range in {
		out[i] = l
		if l.Job != nil {
			j := *l.Job
			out[i].Job = &j
		}
	}

	return out
}

// loadError is a short, fixed text for the API: the error itself can carry
// agent details.
func loadError(err error) string {
	switch {
	case errors.Is(err, context.DeadlineExceeded):
		return "the agent did not answer"
	default:
		return "the load could not start; see the panel log"
	}
}
