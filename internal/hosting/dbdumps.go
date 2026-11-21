// SPDX-License-Identifier: AGPL-3.0-or-later

package hosting

import (
	"context"
	"errors"
	"fmt"

	"github.com/maki-it/hostashi/internal/agentapi"
	"github.com/maki-it/hostashi/internal/agentrpc"
	"github.com/maki-it/hostashi/internal/store"
)

// ErrJobNotFound means there is no such dump job for the database.
var ErrJobNotFound = errors.New("hosting: unknown dump job")

// DBImport calls db.import.
func (a RPCAgent) DBImport(ctx context.Context, database, path string) (agentapi.DBJob, error) {
	return agentapi.DBImport.Call(ctx, a.C, agentapi.DBDumpRequest{Database: database, Path: path})
}

// DBLoad calls db.import with asOwner.
func (a RPCAgent) DBLoad(ctx context.Context, database, path string) (agentapi.DBJob, error) {
	return agentapi.DBImport.Call(ctx, a.C, agentapi.DBDumpRequest{Database: database, Path: path, AsOwner: true})
}

// DBExport calls db.export.
func (a RPCAgent) DBExport(ctx context.Context, database, path string) (agentapi.DBJob, error) {
	return agentapi.DBExport.Call(ctx, a.C, agentapi.DBDumpRequest{Database: database, Path: path})
}

// DBJob calls db.job.
func (a RPCAgent) DBJob(ctx context.Context, id string) (agentapi.DBJob, error) {
	return agentapi.DBJobStatus.Call(ctx, a.C, agentapi.DBJobRequest{ID: id})
}

// ImportDatabase starts reading a dump below the site's home into database
// (ADR-0025). The agent runs the job; DatabaseJob reports how it goes.
func (s *Service) ImportDatabase(ctx context.Context, actor int64, database, path string) (agentapi.DBJob, error) {
	return s.startDump(ctx, actor, "database.import", database, path)
}

// LoadDatabaseDump starts loading a dump from another server, such as one
// of a CloudPanel import, into database: as the database's owner account,
// so that its views, triggers and routines keep working (ADR-0030).
func (s *Service) LoadDatabaseDump(ctx context.Context, actor int64, database, path string) (agentapi.DBJob, error) {
	return s.startDump(ctx, actor, "database.load", database, path)
}

// ExportDatabase starts writing database to a new file below the site's
// home (ADR-0025).
func (s *Service) ExportDatabase(ctx context.Context, actor int64, database, path string) (agentapi.DBJob, error) {
	return s.startDump(ctx, actor, "database.export", database, path)
}

func (s *Service) startDump(ctx context.Context, actor int64, action, database, path string) (agentapi.DBJob, error) {
	if err := (agentapi.DBDumpRequest{Database: database, Path: path}).Validate(); err != nil {
		return agentapi.DBJob{}, fmt.Errorf("%w: %w", ErrInvalid, err)
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	d, err := s.st.DatabaseByName(ctx, database)
	if errors.Is(err, store.ErrNotFound) {
		return agentapi.DBJob{}, ErrDatabaseNotFound
	}

	if err != nil {
		return agentapi.DBJob{}, err
	}

	site, err := s.Get(ctx, d.Site)
	if err != nil {
		return agentapi.DBJob{}, err
	}

	if site.State != store.SiteActive {
		return agentapi.DBJob{}, ErrState
	}

	if d.Server != store.LocalDBServer {
		return s.startRemoteDump(ctx, actor, action, d, d.Site, path)
	}

	start := s.ag.DBImport

	switch action {
	case "database.export":
		start = s.ag.DBExport
	case "database.load":
		start = s.ag.DBLoad
	}

	j, err := start(ctx, database, path)
	if err != nil {
		return agentapi.DBJob{}, err
	}

	s.audit(ctx, actor, action, database, map[string]string{"site": d.Site, "path": path, "job": j.ID})

	return j, nil
}

// DatabaseJob reports a dump job of database. A job of another database is
// not found, so a caller learns nothing about it.
func (s *Service) DatabaseJob(ctx context.Context, database, id string) (agentapi.DBJob, error) {
	if (agentapi.DBJobRequest{ID: id}).Validate() != nil {
		return agentapi.DBJob{}, ErrJobNotFound
	}

	d, err := s.Database(ctx, database)
	if err != nil {
		return agentapi.DBJob{}, err
	}

	if d.Server != store.LocalDBServer {
		return s.remoteDatabaseJob(ctx, database, id)
	}

	j, err := s.ag.DBJob(ctx, id)
	if isCode(err, agentrpc.CodeNotFound) || (err == nil && j.Database != database) {
		return agentapi.DBJob{}, ErrJobNotFound
	}

	return j, err
}
