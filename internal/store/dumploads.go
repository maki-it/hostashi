// SPDX-License-Identifier: AGPL-3.0-or-later

package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"
)

// States of the dump loads of a CloudPanel import.
const (
	// DumpLoadsWatching waits for the import to finish copying.
	DumpLoadsWatching = "watching"
	// DumpLoadsLoaded has started the loads (some may have failed to start).
	DumpLoadsLoaded = "loaded"
	// DumpLoadsStopped gave up: the import failed or did not end in time.
	DumpLoadsStopped = "stopped"
)

// MaxDumpLoads bounds how many imports' loads are kept.
const MaxDumpLoads = 100

// DumpLoads is the loading of one import's dumps. Loads is JSON, written
// and read by the caller.
type DumpLoads struct {
	ImportID  string
	State     string
	StartedBy *int64
	CreatedAt time.Time
	Loads     []byte
}

// WatchDumpLoads records that the dumps of a new import are to be loaded
// once it is done. An existing row is kept. The oldest rows beyond
// MaxDumpLoads are removed.
func (s *Store) WatchDumpLoads(ctx context.Context, importID string, startedBy *int64, at time.Time) error {
	tx, err := s.w.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("store: %w", err)
	}

	defer func() { _ = tx.Rollback() }()

	if _, err := tx.ExecContext(ctx,
		`INSERT INTO cloudpanel_dump_loads (import_id, state, started_by, created_at) VALUES (?, 'watching', ?, ?)
		 ON CONFLICT (import_id) DO NOTHING`, importID, startedBy, at.Unix()); err != nil {
		return fmt.Errorf("store: %w", err)
	}

	if err := pruneDumpLoads(ctx, tx); err != nil {
		return err
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("store: %w", err)
	}

	return nil
}

func pruneDumpLoads(ctx context.Context, tx *sql.Tx) error {
	_, err := tx.ExecContext(ctx,
		`DELETE FROM cloudpanel_dump_loads WHERE import_id NOT IN (
		   SELECT import_id FROM cloudpanel_dump_loads ORDER BY created_at DESC, rowid DESC LIMIT ?)`, MaxDumpLoads)
	if err != nil {
		return fmt.Errorf("store: %w", err)
	}

	return nil
}

// DumpLoadsOf returns the loads of one import, ErrNotFound if there are none.
func (s *Store) DumpLoadsOf(ctx context.Context, importID string) (DumpLoads, error) {
	row := s.db.QueryRowContext(ctx,
		`SELECT import_id, state, started_by, created_at, loads FROM cloudpanel_dump_loads WHERE import_id = ?`, importID)

	d, err := scanDumpLoads(row)
	if errors.Is(err, sql.ErrNoRows) {
		return DumpLoads{}, ErrNotFound
	}

	return d, err
}

// WatchingDumpLoads lists the imports whose dumps still wait, oldest first.
func (s *Store) WatchingDumpLoads(ctx context.Context) ([]DumpLoads, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT import_id, state, started_by, created_at, loads FROM cloudpanel_dump_loads WHERE state = 'watching' ORDER BY created_at, rowid`)
	if err != nil {
		return nil, fmt.Errorf("store: %w", err)
	}
	defer rows.Close()

	var out []DumpLoads

	for rows.Next() {
		d, err := scanDumpLoads(rows)
		if err != nil {
			return nil, err
		}

		out = append(out, d)
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("store: %w", err)
	}

	return out, nil
}

// SaveDumpLoads writes the state and loads of an import, adding the row
// when a load was started by hand for an import without one.
func (s *Store) SaveDumpLoads(ctx context.Context, d DumpLoads) error {
	loads := d.Loads
	if loads == nil {
		loads = []byte("[]")
	}

	tx, err := s.w.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("store: %w", err)
	}

	defer func() { _ = tx.Rollback() }()

	if _, err := tx.ExecContext(ctx,
		`INSERT INTO cloudpanel_dump_loads (import_id, state, started_by, created_at, loads) VALUES (?, ?, ?, ?, ?)
		 ON CONFLICT (import_id) DO UPDATE SET state = excluded.state, loads = excluded.loads`,
		d.ImportID, d.State, d.StartedBy, d.CreatedAt.Unix(), string(loads)); err != nil {
		return fmt.Errorf("store: %w", err)
	}

	if err := pruneDumpLoads(ctx, tx); err != nil {
		return err
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("store: %w", err)
	}

	return nil
}

// StopDumpLoads ends the wait of an import that is still watching.
func (s *Store) StopDumpLoads(ctx context.Context, importID string) error {
	if _, err := s.w.ExecContext(ctx,
		`UPDATE cloudpanel_dump_loads SET state = 'stopped' WHERE import_id = ? AND state = 'watching'`, importID); err != nil {
		return fmt.Errorf("store: %w", err)
	}

	return nil
}

func scanDumpLoads(r rowScanner) (DumpLoads, error) {
	var (
		d       DumpLoads
		by      sql.NullInt64
		created int64
		loads   string
	)

	if err := r.Scan(&d.ImportID, &d.State, &by, &created, &loads); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return DumpLoads{}, err
		}

		return DumpLoads{}, fmt.Errorf("store: %w", err)
	}

	if by.Valid {
		d.StartedBy = &by.Int64
	}

	d.CreatedAt = time.Unix(created, 0).UTC()
	d.Loads = []byte(loads)

	return d, nil
}
