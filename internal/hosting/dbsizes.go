// SPDX-License-Identifier: AGPL-3.0-or-later

package hosting

import (
	"context"
	"errors"
	"sync"
	"time"

	"github.com/maki-it/hostashi/internal/agentapi"
	"github.com/maki-it/hostashi/internal/store"
)

// dbSizesFor is how long a measurement of the database sizes is reused:
// information_schema reads every table, and the sizes change slowly.
const dbSizesFor = 5 * time.Minute

// ErrNoDBSizes: the panel cannot measure the local databases.
var ErrNoDBSizes = errors.New("hosting: database sizes are not available")

// DBSizeAgent is the part of the agent that measures the local databases.
type DBSizeAgent interface {
	DBSizes(ctx context.Context) (map[string]int64, error)
}

// DBSizes calls db.sizes.
func (a RPCAgent) DBSizes(ctx context.Context) (map[string]int64, error) {
	resp, err := agentapi.DBSizes.Call(ctx, a.C, agentapi.DBSizesRequest{})
	if err != nil {
		return nil, err
	}

	out := make(map[string]int64, len(resp.Databases))
	for _, d := range resp.Databases {
		out[d.Name] = d.Bytes
	}

	return out, nil
}

// DBSizes is one measurement of the local databases (ADR-0015).
type DBSizes struct {
	Bytes      map[string]int64 // by database name
	MeasuredAt time.Time
}

// dbSizeCache keeps the last measurement.
type dbSizeCache struct {
	mu   sync.Mutex
	last DBSizes
}

// EnableDBSizes sets the agent that measures the local databases.
func (s *Service) EnableDBSizes(a DBSizeAgent) { s.dbSizer = a }

// DatabaseSizes returns the sizes of the local databases, measured at most
// every five minutes. Databases on external servers are not measured:
// Hostashi does not manage their storage (ADR-0015, ADR-0041).
func (s *Service) DatabaseSizes(ctx context.Context) (DBSizes, error) {
	if s.dbSizer == nil {
		return DBSizes{}, ErrNoDBSizes
	}

	c := &s.dbSizes
	c.mu.Lock()
	defer c.mu.Unlock()

	now := s.now()
	if c.last.Bytes != nil && now.Sub(c.last.MeasuredAt) < dbSizesFor && !now.Before(c.last.MeasuredAt) {
		return c.last, nil
	}

	b, err := s.dbSizer.DBSizes(ctx)
	if err != nil {
		return DBSizes{}, err
	}

	if b == nil {
		b = map[string]int64{}
	}

	c.last = DBSizes{Bytes: b, MeasuredAt: now}

	return c.last, nil
}

// DatabaseUsage is the size of a site's local databases.
type DatabaseUsage struct {
	Bytes      int64
	Databases  int // local databases counted
	MeasuredAt time.Time
}

// SiteDatabaseUsage adds up the sizes of the site's local databases. A
// site without local databases uses 0 bytes.
func (s *Service) SiteDatabaseUsage(ctx context.Context, name string) (DatabaseUsage, error) {
	site, err := s.Get(ctx, name)
	if err != nil {
		return DatabaseUsage{}, err
	}

	dbs, err := s.st.Databases(ctx, site.Name)
	if err != nil {
		return DatabaseUsage{}, err
	}

	z, err := s.DatabaseSizes(ctx)
	if err != nil {
		return DatabaseUsage{}, err
	}

	u := DatabaseUsage{MeasuredAt: z.MeasuredAt}

	for _, d := range dbs {
		if d.Server != store.LocalDBServer {
			continue
		}

		u.Bytes += z.Bytes[d.Name]
		u.Databases++
	}

	return u, nil
}
