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
	// ErrTOTPState is returned when TOTP is in the wrong state for the call.
	ErrTOTPState = errors.New("store: TOTP is in the wrong state for this action")
)

// User is a panel account.
type User struct {
	ID           int64
	Username     string
	Role         string
	Disabled     bool
	PasswordHash string
	TOTPEnabled  bool
	TOTPSecret   []byte // encrypted, see internal/secrets
	TOTPLastStep int64
	// RequireSecondFactor is set by an admin for an account with the role
	// user (ADR-0035); admins and managers need one regardless.
	RequireSecondFactor bool
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
	tx, err := s.w.BeginTx(ctx, nil)
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
	return s.userBy(ctx, `username = ?`, username)
}

// UserByID returns the user or ErrNotFound.
func (s *Store) UserByID(ctx context.Context, id int64) (User, error) {
	return s.userBy(ctx, `id = ?`, id)
}

func (s *Store) userBy(ctx context.Context, where string, arg any) (User, error) {
	var (
		u                     User
		disabled, totpOn, req int
	)

	err := s.db.QueryRowContext(ctx,
		`SELECT id, username, role, disabled, password_hash, totp_enabled, totp_secret, totp_last_step,
		        require_second_factor
		   FROM users WHERE `+where, arg).
		Scan(&u.ID, &u.Username, &u.Role, &disabled, &u.PasswordHash, &totpOn, &u.TOTPSecret, &u.TOTPLastStep, &req)
	if errors.Is(err, sql.ErrNoRows) {
		return User{}, ErrNotFound
	}

	if err != nil {
		return User{}, fmt.Errorf("store: %w", err)
	}

	u.Disabled = disabled == 1
	u.TOTPEnabled = totpOn == 1
	u.RequireSecondFactor = req == 1

	return u, nil
}

// CreateSession stores a session by the SHA-256 of its token.
func (s *Store) CreateSession(ctx context.Context, userID int64, tokenHash []byte, now, expires time.Time) error {
	_, err := s.w.ExecContext(ctx,
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
		u                     User
		disabled, totpOn, req int
	)

	err := s.db.QueryRowContext(ctx,
		`SELECT u.id, u.username, u.role, u.disabled, u.totp_enabled, u.require_second_factor
		   FROM sessions s JOIN users u ON u.id = s.user_id
		  WHERE s.token_hash = ? AND s.expires_at > ?`, tokenHash, now.Unix()).
		Scan(&u.ID, &u.Username, &u.Role, &disabled, &totpOn, &req)
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
	u.TOTPEnabled = totpOn == 1
	u.RequireSecondFactor = req == 1

	return u, nil
}

// DeleteSession removes one session. A missing session is not an error.
func (s *Store) DeleteSession(ctx context.Context, tokenHash []byte) error {
	if _, err := s.w.ExecContext(ctx, `DELETE FROM sessions WHERE token_hash = ?`, tokenHash); err != nil {
		return fmt.Errorf("store: %w", err)
	}

	return nil
}

// DeleteExpiredSessions removes sessions that ended before now.
func (s *Store) DeleteExpiredSessions(ctx context.Context, now time.Time) (int64, error) {
	res, err := s.w.ExecContext(ctx, `DELETE FROM sessions WHERE expires_at <= ?`, now.Unix())
	if err != nil {
		return 0, fmt.Errorf("store: %w", err)
	}

	n, err := res.RowsAffected()
	if err != nil {
		return 0, fmt.Errorf("store: %w", err)
	}

	return n, nil
}

// Audit appends an entry. detail must not contain secrets. The client's
// address comes from ctx (WithClientIP).
func (s *Store) Audit(ctx context.Context, userID *int64, action, target string, detail map[string]string) error {
	return s.AuditChange(ctx, userID, action, target, detail, nil)
}

// AuditChange appends an entry for an update, with the fields it changed
// (DiffOf). Neither detail nor diff may contain secrets.
func (s *Store) AuditChange(ctx context.Context, userID *int64, action, target string, detail map[string]string, diff Diff) error {
	if detail == nil {
		detail = map[string]string{}
	}

	if diff == nil {
		diff = Diff{}
	}

	b, err := json.Marshal(detail)
	if err != nil {
		return fmt.Errorf("store: %w", err)
	}

	d, err := json.Marshal(diff)
	if err != nil {
		return fmt.Errorf("store: %w", err)
	}

	_, err = s.w.ExecContext(ctx,
		`INSERT INTO audit_log (user_id, actor, action, target, detail, ip, diff)
		 VALUES (?, coalesce((SELECT username FROM users WHERE id = ?), ''), ?, ?, ?, ?, ?)`,
		userID, userID, action, target, string(b), ClientIP(ctx), string(d))
	if err != nil {
		return fmt.Errorf("store: %w", err)
	}

	return nil
}

// SetPendingTOTP stores a new encrypted seed for enrollment. It replaces an
// unconfirmed seed and fails with ErrTOTPState if TOTP is already enabled.
func (s *Store) SetPendingTOTP(ctx context.Context, userID int64, sealed []byte) error {
	res, err := s.w.ExecContext(ctx,
		`UPDATE users SET totp_secret = ?, totp_last_step = 0 WHERE id = ? AND totp_enabled = 0`, sealed, userID)
	if err != nil {
		return fmt.Errorf("store: %w", err)
	}

	if n, _ := res.RowsAffected(); n != 1 {
		return ErrTOTPState
	}

	return nil
}

// EnableTOTP turns on the pending seed, records the accepted step and
// replaces the recovery codes, in one transaction.
func (s *Store) EnableTOTP(ctx context.Context, userID, step int64, codeHashes [][]byte) error {
	tx, err := s.w.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("store: %w", err)
	}

	defer func() { _ = tx.Rollback() }()

	res, err := tx.ExecContext(ctx,
		`UPDATE users SET totp_enabled = 1, totp_last_step = ?
		  WHERE id = ? AND totp_enabled = 0 AND totp_secret IS NOT NULL`, step, userID)
	if err != nil {
		return fmt.Errorf("store: %w", err)
	}

	if n, _ := res.RowsAffected(); n != 1 {
		return ErrTOTPState
	}

	if _, err := tx.ExecContext(ctx, `DELETE FROM recovery_codes WHERE user_id = ?`, userID); err != nil {
		return fmt.Errorf("store: %w", err)
	}

	for _, h := range codeHashes {
		if _, err := tx.ExecContext(ctx, `INSERT INTO recovery_codes (user_id, code_hash) VALUES (?, ?)`, userID, h); err != nil {
			return fmt.Errorf("store: %w", err)
		}
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("store: %w", err)
	}

	return nil
}

// AcceptTOTPStep records step as used. It returns false if this step or a
// later one was already accepted, which blocks replay of a captured code.
func (s *Store) AcceptTOTPStep(ctx context.Context, userID, step int64) (bool, error) {
	res, err := s.w.ExecContext(ctx,
		`UPDATE users SET totp_last_step = ? WHERE id = ? AND totp_last_step < ?`, step, userID, step)
	if err != nil {
		return false, fmt.Errorf("store: %w", err)
	}

	n, _ := res.RowsAffected()

	return n == 1, nil
}

// UseRecoveryCode deletes the code and reports whether it existed.
func (s *Store) UseRecoveryCode(ctx context.Context, userID int64, codeHash []byte) (bool, error) {
	res, err := s.w.ExecContext(ctx,
		`DELETE FROM recovery_codes WHERE user_id = ? AND code_hash = ?`, userID, codeHash)
	if err != nil {
		return false, fmt.Errorf("store: %w", err)
	}

	n, _ := res.RowsAffected()

	return n == 1, nil
}

// DisableTOTP removes the seed and the recovery codes.
func (s *Store) DisableTOTP(ctx context.Context, userID int64) error {
	tx, err := s.w.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("store: %w", err)
	}

	defer func() { _ = tx.Rollback() }()

	if _, err := tx.ExecContext(ctx,
		`UPDATE users SET totp_secret = NULL, totp_enabled = 0, totp_last_step = 0 WHERE id = ?`, userID); err != nil {
		return fmt.Errorf("store: %w", err)
	}

	if _, err := tx.ExecContext(ctx, `DELETE FROM recovery_codes WHERE user_id = ?`, userID); err != nil {
		return fmt.Errorf("store: %w", err)
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("store: %w", err)
	}

	return nil
}
