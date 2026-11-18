// SPDX-License-Identifier: AGPL-3.0-or-later

package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"
)

var (
	// ErrNotFound is returned when a row does not exist.
	ErrNotFound = errors.New("store: not found")
	// ErrSetupDone is returned when the first user already exists.
	ErrSetupDone = errors.New("store: initial setup already done")
)

// User is a panel account.
type User struct {
	ID           int64
	Username     string
	Role         string
	Disabled     bool
	PasswordHash string
}

// CountUsers returns the number of accounts.
func (s *Store) CountUsers(ctx context.Context) (int, error) {
	var n int
	if err := s.db.QueryRowContext(ctx, `SELECT count(*) FROM users`).Scan(&n); err != nil {
		return 0, fmt.Errorf("store: %w", err)
	}

	return n, nil
}

// CreateFirstUser creates the initial admin. It fails with ErrSetupDone if
// any user exists, decided inside one write transaction so two setup
// requests cannot both succeed.
func (s *Store) CreateFirstUser(ctx context.Context, username, passwordHash string) (User, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return User{}, fmt.Errorf("store: %w", err)
	}

	defer func() { _ = tx.Rollback() }()

	var n int
	if err := tx.QueryRowContext(ctx, `SELECT count(*) FROM users`).Scan(&n); err != nil {
		return User{}, fmt.Errorf("store: %w", err)
	}

	if n > 0 {
		return User{}, ErrSetupDone
	}

	res, err := tx.ExecContext(ctx,
		`INSERT INTO users (username, password_hash, role) VALUES (?, ?, 'admin')`, username, passwordHash)
	if err != nil {
		return User{}, fmt.Errorf("store: %w", err)
	}

	id, err := res.LastInsertId()
	if err != nil {
		return User{}, fmt.Errorf("store: %w", err)
	}

	if err := tx.Commit(); err != nil {
		return User{}, fmt.Errorf("store: %w", err)
	}

	return User{ID: id, Username: username, Role: "admin", PasswordHash: passwordHash}, nil
}

// UserByUsername returns the user or ErrNotFound.
func (s *Store) UserByUsername(ctx context.Context, username string) (User, error) {
	var (
		u        User
		disabled int
	)

	err := s.db.QueryRowContext(ctx,
		`SELECT id, username, role, disabled, password_hash FROM users WHERE username = ?`, username).
		Scan(&u.ID, &u.Username, &u.Role, &disabled, &u.PasswordHash)
	if errors.Is(err, sql.ErrNoRows) {
		return User{}, ErrNotFound
	}

	if err != nil {
		return User{}, fmt.Errorf("store: %w", err)
	}

	u.Disabled = disabled == 1

	return u, nil
}

// CreateSession stores a session by the SHA-256 of its token.
func (s *Store) CreateSession(ctx context.Context, userID int64, tokenHash []byte, now, expires time.Time) error {
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO sessions (token_hash, user_id, created_at, expires_at) VALUES (?, ?, ?, ?)`,
		tokenHash, userID, now.Unix(), expires.Unix())
	if err != nil {
		return fmt.Errorf("store: %w", err)
	}

	return nil
}

// SessionUser returns the user of a valid, unexpired session whose account
// is not disabled, or ErrNotFound.
func (s *Store) SessionUser(ctx context.Context, tokenHash []byte, now time.Time) (User, error) {
	var (
		u        User
		disabled int
	)

	err := s.db.QueryRowContext(ctx,
		`SELECT u.id, u.username, u.role, u.disabled
		   FROM sessions s JOIN users u ON u.id = s.user_id
		  WHERE s.token_hash = ? AND s.expires_at > ?`, tokenHash, now.Unix()).
		Scan(&u.ID, &u.Username, &u.Role, &disabled)
	if errors.Is(err, sql.ErrNoRows) {
		return User{}, ErrNotFound
	}

	if err != nil {
		return User{}, fmt.Errorf("store: %w", err)
	}

	if disabled == 1 {
		return User{}, ErrNotFound
	}

	u.Disabled = false

	return u, nil
}

// DeleteSession removes one session. A missing session is not an error.
func (s *Store) DeleteSession(ctx context.Context, tokenHash []byte) error {
	if _, err := s.db.ExecContext(ctx, `DELETE FROM sessions WHERE token_hash = ?`, tokenHash); err != nil {
		return fmt.Errorf("store: %w", err)
	}

	return nil
}

// DeleteExpiredSessions removes sessions that ended before now.
func (s *Store) DeleteExpiredSessions(ctx context.Context, now time.Time) (int64, error) {
	res, err := s.db.ExecContext(ctx, `DELETE FROM sessions WHERE expires_at <= ?`, now.Unix())
	if err != nil {
		return 0, fmt.Errorf("store: %w", err)
	}

	n, err := res.RowsAffected()
	if err != nil {
		return 0, fmt.Errorf("store: %w", err)
	}

	return n, nil
}

// Audit appends an entry. detail must not contain secrets.
func (s *Store) Audit(ctx context.Context, userID *int64, action, target string, detail map[string]string) error {
	if detail == nil {
		detail = map[string]string{}
	}

	b, err := json.Marshal(detail)
	if err != nil {
		return fmt.Errorf("store: %w", err)
	}

	_, err = s.db.ExecContext(ctx,
		`INSERT INTO audit_log (user_id, action, target, detail) VALUES (?, ?, ?, ?)`,
		userID, action, target, string(b))
	if err != nil {
		return fmt.Errorf("store: %w", err)
	}

	return nil
}
