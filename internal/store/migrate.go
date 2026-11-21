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
}

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

		out = append(out, migration{version: v, name: e.Name(), sql: string(b)})
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
	tx, err := db.BeginTx(ctx, nil)
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

	// The version is an int from a validated file name, not user input.
	if _, err := tx.ExecContext(ctx, "PRAGMA user_version = "+strconv.Itoa(m.version)); err != nil {
		return fmt.Errorf("store: migration %s: %w", m.name, err)
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("store: migration %s: %w", m.name, err)
	}

	return nil
}
