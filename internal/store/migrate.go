// SPDX-License-Identifier: AGPL-3.0-or-later

package store

import (
	"context"
	"database/sql"
	"embed"
	"errors"
	"fmt"
	"io/fs"
	"regexp"
	"sort"
	"strconv"
	"strings"
)

//go:embed migrations/*.sql
var embedded embed.FS

// migrations is the embedded schema history. Tests replace it.
var migrations fs.FS = embedded

// migrationName is NNNN_description.sql, numbered from 0001 without gaps.
var migrationName = regexp.MustCompile(`^([0-9]{4})_[a-z0-9_]+\.sql$`)

// ErrSchemaTooNew is returned when the database was migrated by a newer
// hostashi. Running an older binary against it could corrupt state.
var ErrSchemaTooNew = errors.New("store: database schema is newer than this binary")

type migration struct {
	version int
	name    string
	sql     string
	// rebuild is set by a first line of fkOffMarker. The migration then
	// runs with foreign keys off, as SQLite's procedure for rebuilding a
	// table that other tables reference requires: with them on, DROP TABLE
	// would delete the rows that reference it.
	rebuild bool
}

// fkOffMarker starts a migration that rebuilds a referenced table. After the
// migration, PRAGMA foreign_key_check must find no row, or it is rolled back.
const fkOffMarker = "-- hostashi: foreign-keys-off\n"

func loadMigrations(fsys fs.FS) ([]migration, error) {
	entries, err := fs.ReadDir(fsys, "migrations")
	if err != nil {
		return nil, fmt.Errorf("store: migrations: %w", err)
	}

	out := make([]migration, 0, len(entries))

	for _, e := range entries {
		m := migrationName.FindStringSubmatch(e.Name())
		if e.IsDir() || m == nil {
			return nil, fmt.Errorf("store: bad migration file name %q", e.Name())
		}

		v, err := strconv.Atoi(m[1])
		if err != nil {
			return nil, fmt.Errorf("store: bad migration version %q: %w", e.Name(), err)
		}

		b, err := fs.ReadFile(fsys, "migrations/"+e.Name())
		if err != nil {
			return nil, fmt.Errorf("store: %w", err)
		}

		out = append(out, migration{
			version: v, name: e.Name(), sql: string(b),
			rebuild: strings.HasPrefix(string(b), fkOffMarker),
		})
	}

	sort.Slice(out, func(i, j int) bool { return out[i].version < out[j].version })

	for i, m := range out {
		if m.version != i+1 {
			return nil, fmt.Errorf("store: migration %s: want version %04d (no gaps or duplicates)", m.name, i+1)
		}
	}

	return out, nil
}

func userVersion(ctx context.Context, q interface {
	QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row
},
) (int, error) {
	var v int
	if err := q.QueryRowContext(ctx, "PRAGMA user_version").Scan(&v); err != nil {
		return 0, fmt.Errorf("store: %w", err)
	}

	return v, nil
}

// SchemaVersion is the schema version a database has after Open with this
// binary: the number of embedded migrations. The package scripts compare it
// across versions to tell whether a downgrade can keep the database
// (ADR-0050).
func SchemaVersion() (int, error) {
	ms, err := loadMigrations(migrations)
	if err != nil {
		return 0, err
	}

	return len(ms), nil
}

// migrate applies every pending migration in its own transaction. The schema
// version is kept in PRAGMA user_version, which is part of the transaction,
// so a failed migration leaves the database at the previous version.
func migrate(ctx context.Context, db *sql.DB, fsys fs.FS) error {
	ms, err := loadMigrations(fsys)
	if err != nil {
		return err
	}

	current, err := userVersion(ctx, db)
	if err != nil {
		return err
	}

	if current > len(ms) {
		return fmt.Errorf("%w: database is at %d, binary knows %d", ErrSchemaTooNew, current, len(ms))
	}

	for _, m := range ms[current:] {
		if err := apply(ctx, db, m); err != nil {
			return err
		}
	}

	return nil
}

func apply(ctx context.Context, db *sql.DB, m migration) error {
	// The pragma has no effect inside a transaction, so it is set on the
	// connection before BEGIN and set back after it. The writer pool has
	// one connection; a new one would get foreign keys on from the DSN.
	conn, err := db.Conn(ctx)
	if err != nil {
		return fmt.Errorf("store: migration %s: %w", m.name, err)
	}

	defer func() { _ = conn.Close() }()

	if m.rebuild {
		if _, err := conn.ExecContext(ctx, "PRAGMA foreign_keys = OFF"); err != nil {
			return fmt.Errorf("store: migration %s: %w", m.name, err)
		}

		defer func() { _, _ = conn.ExecContext(context.WithoutCancel(ctx), "PRAGMA foreign_keys = ON") }()
	}

	tx, err := conn.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("store: migration %s: %w", m.name, err)
	}

	defer func() { _ = tx.Rollback() }()

	// Re-check inside the write transaction, in case two processes race.
	v, err := userVersion(ctx, tx)
	if err != nil {
		return err
	}

	if v >= m.version {
		return nil // another process applied it first
	}

	if v != m.version-1 {
		return fmt.Errorf("store: migration %s: database moved to version %d", m.name, v)
	}

	if _, err := tx.ExecContext(ctx, m.sql); err != nil {
		return fmt.Errorf("store: migration %s: %w", m.name, err)
	}

	if m.rebuild {
		if err := foreignKeysHold(ctx, tx); err != nil {
			return fmt.Errorf("store: migration %s: %w", m.name, err)
		}
	}

	// The version is an int from a validated file name, not user input.
	if _, err := tx.ExecContext(ctx, "PRAGMA user_version = "+strconv.Itoa(m.version)); err != nil {
		return fmt.Errorf("store: migration %s: %w", m.name, err)
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("store: migration %s: %w", m.name, err)
	}

	return nil
}

// foreignKeysHold reports the first row that PRAGMA foreign_key_check finds.
func foreignKeysHold(ctx context.Context, tx *sql.Tx) error {
	rows, err := tx.QueryContext(ctx, "PRAGMA foreign_key_check")
	if err != nil {
		return err
	}

	defer func() { _ = rows.Close() }()

	if rows.Next() {
		var (
			table, parent string
			rowid         sql.NullInt64
			fk            int
		)

		if err := rows.Scan(&table, &rowid, &parent, &fk); err != nil {
			return err
		}

		return fmt.Errorf("foreign key violation: %s row %d references %s", table, rowid.Int64, parent)
	}

	return rows.Err()
}
