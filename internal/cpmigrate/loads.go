// SPDX-License-Identifier: AGPL-3.0-or-later

package cpmigrate

import (
	"context"
	"errors"
	"strconv"
	"sync"
	"time"

	"github.com/maki-it/hostashi/internal/agentapi"
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
	maxLoadsKept    = 100
)

type loads struct {
	l    DumpLoader
	ctx  context.Context //nolint:containedctx // watches outlive the request that starts them
	poll time.Duration

	mu    sync.Mutex
	byImp map[string][]DumpLoad
	order []string
}

// EnableLoads makes every import with databases load its dumps on its own
// once the files are copied (ADR-0030). ctx bounds the watches; poll is how
// often the import is read, DefaultLoadPoll if zero. The loads live in
// memory: after a panel restart, LoadDumps starts them by hand.
func (s *Service) EnableLoads(ctx context.Context, l DumpLoader, poll time.Duration) {
	if poll <= 0 {
		poll = DefaultLoadPoll
	}

	s.loads = &loads{l: l, ctx: ctx, poll: poll, byImp: map[string][]DumpLoad{}}
}

// watch waits in the background until the import has ended and then loads
// its dumps.
func (s *Service) watch(actor int64, id string) {
	ld := s.loads

	go func() {
		ctx, cancel := context.WithTimeout(ld.ctx, maxWatch)
		defer cancel()

		t := time.NewTicker(ld.poll)
		defer t.Stop()

		for {
			select {
			case <-ctx.Done():
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

// LoadDumps starts one load per dump of a finished import, into the
// database the import mapped it to. A dump whose load started already is
// not loaded again; one whose start failed is tried again.
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

	prev := ld.byImp[id]
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

	if _, ok := ld.byImp[id]; !ok {
		ld.order = append(ld.order, id)
		if len(ld.order) > maxLoadsKept {
			delete(ld.byImp, ld.order[0])
			ld.order = ld.order[1:]
		}
	}

	ld.byImp[id] = out

	s.audit(ctx, actor, "cloudpanel.import.load", imp.Site, map[string]string{"import": id, "started": strconv.Itoa(started)})

	return copyLoads(out), nil
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

	s.loads.mu.Lock()
	out := copyLoads(s.loads.byImp[id])
	s.loads.mu.Unlock()

	for i := range out {
		if out[i].Job == nil {
			continue
		}

		j, err := s.loads.l.DatabaseJob(ctx, out[i].Database, out[i].Job.ID)
		if err != nil {
			// The agent forgets jobs when it restarts.
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
