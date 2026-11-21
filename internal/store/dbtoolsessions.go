// SPDX-License-Identifier: AGPL-3.0-or-later

package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"
)

// DBToolSession is a session of the web database tool as the panel opened
// it (ADR-0038 addendum 1). OpenedBy is nil once that account is gone.
type DBToolSession struct {
	User     string
	Database string
	Remote   bool
	OpenedBy *int64
	// OpenedByName is the account's name, empty when it is gone.
	OpenedByName string
	OpenedAt     time.Time
	Expires      time.Time
}

// AddDBToolSession records an opened session.
func (s *Store) AddDBToolSession(ctx context.Context, t DBToolSession) error {
	_, err := s.w.ExecContext(ctx,
		`INSERT INTO db_tool_sessions (db_user, database, remote, opened_by, opened_at, expires_at) VALUES (?, ?, ?, ?, ?, ?)`,
		t.User, t.Database, t.Remote, t.OpenedBy, t.OpenedAt.Unix(), t.Expires.Unix())
	if err != nil {
		return fmt.Errorf("store: %w", err)
	}

	return nil
}

// DBToolSessionsOf returns the recorded sessions on database, the first
// to end first, with the name of the account that opened each.
func (s *Store) DBToolSessionsOf(ctx context.Context, database string) ([]DBToolSession, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT t.db_user, t.database, t.remote, t.opened_by, COALESCE(u.username, ''), t.opened_at, t.expires_at
		FROM db_tool_sessions t LEFT JOIN users u ON u.id = t.opened_by
		WHERE t.database = ? ORDER BY t.expires_at, t.db_user`, database)
	if err != nil {
		return nil, fmt.Errorf("store: %w", err)
	}

	defer rows.Close()

	out := []DBToolSession{}

	for rows.Next() {
		var (
			t               DBToolSession
			by              sql.NullInt64
			opened, expires int64
		)

		if err := rows.Scan(&t.User, &t.Database, &t.Remote, &by, &t.OpenedByName, &opened, &expires); err != nil {
			return nil, fmt.Errorf("store: %w", err)
		}

		if by.Valid {
			t.OpenedBy = &by.Int64
		}

		t.OpenedAt, t.Expires = time.Unix(opened, 0).UTC(), time.Unix(expires, 0).UTC()
		out = append(out, t)
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("store: %w", err)
	}

	return out, nil
}

// DBToolSessionByUser returns one recorded session.
func (s *Store) DBToolSessionByUser(ctx context.Context, user string) (DBToolSession, error) {
	var (
		t               DBToolSession
		by              sql.NullInt64
		opened, expires int64
	)

	err := s.db.QueryRowContext(ctx, `
		SELECT t.db_user, t.database, t.remote, t.opened_by, COALESCE(u.username, ''), t.opened_at, t.expires_at
		FROM db_tool_sessions t LEFT JOIN users u ON u.id = t.opened_by WHERE t.db_user = ?`, user).
		Scan(&t.User, &t.Database, &t.Remote, &by, &t.OpenedByName, &opened, &expires)
	if errors.Is(err, sql.ErrNoRows) {
		return DBToolSession{}, ErrNotFound
	}

	if err != nil {
		return DBToolSession{}, fmt.Errorf("store: %w", err)
	}

	if by.Valid {
		t.OpenedBy = &by.Int64
	}

	t.OpenedAt, t.Expires = time.Unix(opened, 0).UTC(), time.Unix(expires, 0).UTC()

	return t, nil
}

// SetDBToolSessionExpires moves the recorded end of a session, and of its
// row in db_remote_tools when it has one.
func (s *Store) SetDBToolSessionExpires(ctx context.Context, user string, expires, remoteDrop time.Time) error {
	tx, err := s.w.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("store: %w", err)
	}

	defer func() { _ = tx.Rollback() }()

	res, err := tx.ExecContext(ctx, `UPDATE db_tool_sessions SET expires_at = ? WHERE db_user = ?`, expires.Unix(), user)
	if err != nil {
		return fmt.Errorf("store: %w", err)
	}

	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}

	if _, err := tx.ExecContext(ctx, `UPDATE db_remote_tools SET expires_at = ? WHERE db_user = ?`, remoteDrop.Unix(), user); err != nil {
		return fmt.Errorf("store: %w", err)
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("store: %w", err)
	}

	return nil
}

// DeleteDBToolSession forgets a session. A missing row is not an error.
func (s *Store) DeleteDBToolSession(ctx context.Context, user string) error {
	if _, err := s.w.ExecContext(ctx, `DELETE FROM db_tool_sessions WHERE db_user = ?`, user); err != nil {
		return fmt.Errorf("store: %w", err)
	}

	return nil
}
