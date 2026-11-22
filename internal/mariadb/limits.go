// SPDX-License-Identifier: AGPL-3.0-or-later

package mariadb

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"slices"
	"strconv"
	"strings"

	"github.com/maki-it/hostashi/internal/agentapi"
)

// Querier reads rows. *sql.DB implements it.
type Querier interface {
	QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error)
}

// ErrNoQuery: the Manager's connection cannot read rows (only in tests).
var ErrNoQuery = errors.New("mariadb: the connection cannot read rows")

// Sizes returns the size in bytes of each registered database: data and
// indexes of its tables, as information_schema reports them (ADR-0015).
// For InnoDB these are the server's estimates, not the files on disk. A
// database without tables has size 0.
func (m *Manager) Sizes(ctx context.Context) (map[string]int64, error) {
	q, ok := m.db.(Querier)
	if !ok {
		return nil, ErrNoQuery
	}

	m.mu.Lock()
	names := make([]any, 0, len(m.reg.dbs))
	out := make(map[string]int64, len(m.reg.dbs))

	for name := range m.reg.dbs {
		names = append(names, name)
		out[name] = 0
	}
	m.mu.Unlock()

	if len(names) == 0 {
		return out, nil
	}

	// The names are placeholders, not part of the statement.
	query := "SELECT TABLE_SCHEMA, COALESCE(SUM(DATA_LENGTH + INDEX_LENGTH), 0) FROM information_schema.TABLES WHERE TABLE_SCHEMA IN (?" +
		strings.Repeat(", ?", len(names)-1) + ") GROUP BY TABLE_SCHEMA"

	rows, err := q.QueryContext(ctx, query, names...)
	if err != nil {
		return nil, classify("database sizes", err)
	}
	defer rows.Close()

	for rows.Next() {
		var (
			name string
			size int64
		)

		if err := rows.Scan(&name, &size); err != nil {
			return nil, classify("database sizes", err)
		}

		if _, ok := out[name]; ok {
			out[name] = size
		}
	}

	if err := rows.Err(); err != nil {
		return nil, classify("database sizes", err)
	}

	return out, nil
}

// SetSiteConnections sets MAX_USER_CONNECTIONS of every registered user of
// the site's databases, 0 for unlimited (ADR-0057). It returns the users,
// sorted. On a failure the users before it keep the new value; the caller
// repeats the whole call.
func (m *Manager) SetSiteConnections(ctx context.Context, site string, maxConn uint32) ([]string, error) {
	if err := (agentapi.DBSiteConnectionsRequest{Site: site, MaxConnections: maxConn}).Validate(); err != nil {
		return nil, fmt.Errorf("%w: %w", ErrInvalid, err)
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	users := []string{}

	for _, d := range m.reg.list() {
		if d.Site == site {
			users = append(users, d.Users...)
		}
	}

	slices.Sort(users)

	for _, u := range users {
		acct, err := quoteAccount(u)
		if err != nil {
			return nil, err
		}

		if err := m.exec(ctx, "set connection limit", "ALTER USER "+acct+" WITH MAX_USER_CONNECTIONS "+strconv.FormatUint(uint64(maxConn), 10)); err != nil {
			return nil, err
		}
	}

	return users, nil
}
