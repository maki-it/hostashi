// SPDX-License-Identifier: AGPL-3.0-or-later

package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"
)

// EmailState is the address of an account: the confirmed one and one that
// waits for its link (ADR-0044). Empty strings mean none.
type EmailState struct {
	Confirmed      string
	Pending        string
	PendingExpires time.Time
}

// UserEmail returns the addresses of an account. A missing account is
// ErrNotFound; an expired pending address is left out.
func (s *Store) UserEmail(ctx context.Context, userID int64, now time.Time) (EmailState, error) {
	var (
		e       EmailState
		pending sql.NullString
		expires sql.NullInt64
	)

	err := s.db.QueryRowContext(ctx,
		`SELECT u.email, c.address, c.expires_at FROM users u
		 LEFT JOIN email_confirmations c ON c.user_id = u.id AND c.expires_at > ?
		 WHERE u.id = ?`, now.Unix(), userID).Scan(&e.Confirmed, &pending, &expires)
	if errors.Is(err, sql.ErrNoRows) {
		return EmailState{}, ErrNotFound
	}

	if err != nil {
		return EmailState{}, fmt.Errorf("store: %w", err)
	}

	if pending.Valid {
		e.Pending, e.PendingExpires = pending.String, time.Unix(expires.Int64, 0)
	}

	return e, nil
}

// SetPendingEmail stores an address that waits for confirmation, replacing
// an earlier one of the account.
func (s *Store) SetPendingEmail(ctx context.Context, userID int64, address string, tokenHash []byte, created, expires time.Time) error {
	res, err := s.w.ExecContext(ctx,
		`INSERT INTO email_confirmations (user_id, address, token_hash, created_at, expires_at)
		 SELECT id, ?, ?, ?, ? FROM users WHERE id = ?
		 ON CONFLICT (user_id) DO UPDATE SET address = excluded.address, token_hash = excluded.token_hash,
		   created_at = excluded.created_at, expires_at = excluded.expires_at`,
		address, tokenHash, created.Unix(), expires.Unix(), userID)
	if err != nil {
		return fmt.Errorf("store: %w", err)
	}

	if n, err := res.RowsAffected(); err != nil {
		return fmt.Errorf("store: %w", err)
	} else if n == 0 {
		return ErrNotFound
	}

	return nil
}

// ConfirmEmail makes the pending address of the link's account its address,
// in one transaction, and returns the account with the address it had
// before. An unknown or expired link, or a disabled account, is
// ErrNotFound.
func (s *Store) ConfirmEmail(ctx context.Context, tokenHash []byte, now time.Time) (u User, old, address string, err error) {
	tx, err := s.w.BeginTx(ctx, nil)
	if err != nil {
		return User{}, "", "", fmt.Errorf("store: %w", err)
	}

	defer func() { _ = tx.Rollback() }()

	var uid int64

	err = tx.QueryRowContext(ctx,
		`SELECT c.user_id, c.address, u.email FROM email_confirmations c JOIN users u ON u.id = c.user_id
		 WHERE c.token_hash = ? AND c.expires_at > ? AND u.disabled = 0`, tokenHash, now.Unix()).Scan(&uid, &address, &old)
	if errors.Is(err, sql.ErrNoRows) {
		return User{}, "", "", ErrNotFound
	}

	if err != nil {
		return User{}, "", "", fmt.Errorf("store: %w", err)
	}

	if _, err := tx.ExecContext(ctx, `UPDATE users SET email = ? WHERE id = ?`, address, uid); err != nil {
		return User{}, "", "", fmt.Errorf("store: %w", err)
	}

	if _, err := tx.ExecContext(ctx, `DELETE FROM email_confirmations WHERE user_id = ?`, uid); err != nil {
		return User{}, "", "", fmt.Errorf("store: %w", err)
	}

	if err := tx.Commit(); err != nil {
		return User{}, "", "", fmt.Errorf("store: %w", err)
	}

	u, err = s.UserByID(ctx, uid)

	return u, old, address, err
}

// RemoveEmail clears the address of an account and a pending one, and
// returns the address it had.
func (s *Store) RemoveEmail(ctx context.Context, userID int64) (string, error) {
	tx, err := s.w.BeginTx(ctx, nil)
	if err != nil {
		return "", fmt.Errorf("store: %w", err)
	}

	defer func() { _ = tx.Rollback() }()

	var old string

	err = tx.QueryRowContext(ctx, `SELECT email FROM users WHERE id = ?`, userID).Scan(&old)
	if errors.Is(err, sql.ErrNoRows) {
		return "", ErrNotFound
	}

	if err != nil {
		return "", fmt.Errorf("store: %w", err)
	}

	if _, err := tx.ExecContext(ctx, `UPDATE users SET email = '' WHERE id = ?`, userID); err != nil {
		return "", fmt.Errorf("store: %w", err)
	}

	if _, err := tx.ExecContext(ctx, `DELETE FROM email_confirmations WHERE user_id = ?`, userID); err != nil {
		return "", fmt.Errorf("store: %w", err)
	}

	if err := tx.Commit(); err != nil {
		return "", fmt.Errorf("store: %w", err)
	}

	return old, nil
}

// DeleteExpiredEmailConfirmations removes links that ran out.
func (s *Store) DeleteExpiredEmailConfirmations(ctx context.Context, now time.Time) error {
	if _, err := s.w.ExecContext(ctx, `DELETE FROM email_confirmations WHERE expires_at <= ?`, now.Unix()); err != nil {
		return fmt.Errorf("store: %w", err)
	}

	return nil
}
