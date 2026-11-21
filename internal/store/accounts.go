// SPDX-License-Identifier: AGPL-3.0-or-later

package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"
)

var (
	// ErrUserExists is returned when the username is taken.
	ErrUserExists = errors.New("store: username is taken")
	// ErrLastAdmin is returned when a change would leave no enabled admin.
	ErrLastAdmin = errors.New("store: the last enabled admin cannot be demoted, disabled or deleted")
)

// Account is a panel account as an admin sees it: no secrets.
type Account struct {
	ID          int64
	Username    string
	Role        string
	Disabled    bool
	TOTPEnabled bool
	Passkeys    int
	// RequireSecondFactor is the admin's demand for the role user.
	RequireSecondFactor bool
	// Invited is true while the account has no password yet or was reset.
	Invited   bool
	InviteEnd *time.Time // expiry of the open invitation, if any
}

const accountSelect = `
SELECT u.id, u.username, u.role, u.disabled, u.totp_enabled, u.require_second_factor,
       (SELECT count(*) FROM passkeys p WHERE p.user_id = u.id),
       u.password_hash = '', i.expires_at
  FROM users u LEFT JOIN invitations i ON i.user_id = u.id`

func scanAccount(sc interface{ Scan(...any) error }) (Account, error) {
	var (
		a                         Account
		disabled, totp, req, invd int
		end                       sql.NullInt64
	)

	if err := sc.Scan(&a.ID, &a.Username, &a.Role, &disabled, &totp, &req, &a.Passkeys, &invd, &end); err != nil {
		return Account{}, err
	}

	a.Disabled, a.TOTPEnabled, a.Invited = disabled == 1, totp == 1, invd == 1
	a.RequireSecondFactor = req == 1

	if end.Valid {
		t := time.Unix(end.Int64, 0).UTC()
		a.InviteEnd = &t
		a.Invited = true
	}

	return a, nil
}

// Accounts returns every account, sorted by username.
func (s *Store) Accounts(ctx context.Context) ([]Account, error) {
	rows, err := s.db.QueryContext(ctx, accountSelect+` ORDER BY u.username`)
	if err != nil {
		return nil, fmt.Errorf("store: %w", err)
	}

	defer func() { _ = rows.Close() }()

	out := []Account{}

	for rows.Next() {
		a, err := scanAccount(rows)
		if err != nil {
			return nil, fmt.Errorf("store: %w", err)
		}

		out = append(out, a)
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("store: %w", err)
	}

	return out, nil
}

// AccountByID returns one account or ErrNotFound.
func (s *Store) AccountByID(ctx context.Context, id int64) (Account, error) {
	a, err := scanAccount(s.db.QueryRowContext(ctx, accountSelect+` WHERE u.id = ?`, id))
	if errors.Is(err, sql.ErrNoRows) {
		return Account{}, ErrNotFound
	}

	if err != nil {
		return Account{}, fmt.Errorf("store: %w", err)
	}

	return a, nil
}

// Invitation is the stored half of an invitation link.
type Invitation struct {
	TokenHash []byte
	Created   time.Time
	Expires   time.Time
	// Email is the address the link is emailed to (ADR-0044); accepting
	// the link confirms it. Empty when the link is only shown.
	Email string
}

// CreateAccount adds an account without a password and its invitation, in
// one transaction. A taken username returns ErrUserExists.
func (s *Store) CreateAccount(ctx context.Context, username, role string, inv Invitation) (Account, error) {
	tx, err := s.w.BeginTx(ctx, nil)
	if err != nil {
		return Account{}, fmt.Errorf("store: %w", err)
	}

	defer func() { _ = tx.Rollback() }()

	res, err := tx.ExecContext(ctx, `INSERT INTO users (username, role) VALUES (?, ?)`, username, role)
	if err != nil {
		if isUniqueErr(err) {
			return Account{}, ErrUserExists
		}

		return Account{}, fmt.Errorf("store: %w", err)
	}

	id, err := res.LastInsertId()
	if err != nil {
		return Account{}, fmt.Errorf("store: %w", err)
	}

	if err := putInvitation(ctx, tx, id, inv); err != nil {
		return Account{}, err
	}

	if err := tx.Commit(); err != nil {
		return Account{}, fmt.Errorf("store: %w", err)
	}

	return s.AccountByID(ctx, id)
}

func isUniqueErr(err error) bool {
	return strings.Contains(err.Error(), "UNIQUE constraint failed")
}

func putInvitation(ctx context.Context, tx *sql.Tx, userID int64, inv Invitation) error {
	_, err := tx.ExecContext(ctx,
		`INSERT INTO invitations (user_id, token_hash, created_at, expires_at, email) VALUES (?, ?, ?, ?, ?)
		 ON CONFLICT (user_id) DO UPDATE SET token_hash = excluded.token_hash,
		     created_at = excluded.created_at, expires_at = excluded.expires_at, email = excluded.email`,
		userID, inv.TokenHash, inv.Created.Unix(), inv.Expires.Unix(), inv.Email)
	if err != nil {
		return fmt.Errorf("store: %w", err)
	}

	return nil
}

// AccountChange changes the role, the disabled flag or the demand for a
// second factor; nil keeps a field.
type AccountChange struct {
	Role                *string
	Disabled            *bool
	RequireSecondFactor *bool
}

// guardLastAdmin fails with ErrLastAdmin if userID is an enabled admin and
// no other enabled admin with a password exists: an admin who has not
// accepted the invitation yet may never do so. Call it inside the transaction that would
// demote, disable or delete the account.
func guardLastAdmin(ctx context.Context, tx *sql.Tx, userID int64) error {
	var role string

	var disabled int

	err := tx.QueryRowContext(ctx, `SELECT role, disabled FROM users WHERE id = ?`, userID).Scan(&role, &disabled)
	if errors.Is(err, sql.ErrNoRows) {
		return ErrNotFound
	}

	if err != nil {
		return fmt.Errorf("store: %w", err)
	}

	if role != "admin" || disabled == 1 {
		return nil
	}

	var others int
	if err := tx.QueryRowContext(ctx,
		`SELECT count(*) FROM users WHERE role = 'admin' AND disabled = 0 AND password_hash != '' AND id != ?`, userID).Scan(&others); err != nil {
		return fmt.Errorf("store: %w", err)
	}

	if others == 0 {
		return ErrLastAdmin
	}

	return nil
}

// UpdateAccount applies the change in one transaction and returns the
// account before and after. A change of role or a disable ends the
// account's sessions. It refuses with ErrLastAdmin to leave no enabled admin.
func (s *Store) UpdateAccount(ctx context.Context, id int64, c AccountChange) (before, after Account, err error) {
	before, err = s.AccountByID(ctx, id)
	if err != nil {
		return Account{}, Account{}, err
	}

	tx, err := s.w.BeginTx(ctx, nil)
	if err != nil {
		return Account{}, Account{}, fmt.Errorf("store: %w", err)
	}

	defer func() { _ = tx.Rollback() }()

	demotes := c.Role != nil && *c.Role != "admin" || c.Disabled != nil && *c.Disabled
	if demotes {
		if err := guardLastAdmin(ctx, tx, id); err != nil {
			return Account{}, Account{}, err
		}
	}

	endSessions := false

	if c.Role != nil {
		res, err := tx.ExecContext(ctx, `UPDATE users SET role = ? WHERE id = ? AND role != ?`, *c.Role, id, *c.Role)
		if err != nil {
			return Account{}, Account{}, fmt.Errorf("store: %w", err)
		}

		if n, _ := res.RowsAffected(); n > 0 {
			endSessions = true
		}
	}

	if c.Disabled != nil {
		v := 0
		if *c.Disabled {
			v = 1
		}

		res, err := tx.ExecContext(ctx, `UPDATE users SET disabled = ? WHERE id = ? AND disabled != ?`, v, id, v)
		if err != nil {
			return Account{}, Account{}, fmt.Errorf("store: %w", err)
		}

		if n, _ := res.RowsAffected(); n > 0 && *c.Disabled {
			endSessions = true
		}
	}

	if c.RequireSecondFactor != nil {
		if _, err := tx.ExecContext(ctx, `UPDATE users SET require_second_factor = ? WHERE id = ?`,
			boolInt(*c.RequireSecondFactor), id); err != nil {
			return Account{}, Account{}, fmt.Errorf("store: %w", err)
		}
	}

	if endSessions {
		if _, err := tx.ExecContext(ctx, `DELETE FROM sessions WHERE user_id = ?`, id); err != nil {
			return Account{}, Account{}, fmt.Errorf("store: %w", err)
		}
	}

	if err := tx.Commit(); err != nil {
		return Account{}, Account{}, fmt.Errorf("store: %w", err)
	}

	after, err = s.AccountByID(ctx, id)

	return before, after, err
}

// DeleteAccount removes the account with its sessions, tokens, passkeys and
// grants. The audit log keeps the actor's name. It refuses with
// ErrLastAdmin to remove the last enabled admin.
func (s *Store) DeleteAccount(ctx context.Context, id int64) (Account, error) {
	a, err := s.AccountByID(ctx, id)
	if err != nil {
		return Account{}, err
	}

	tx, err := s.w.BeginTx(ctx, nil)
	if err != nil {
		return Account{}, fmt.Errorf("store: %w", err)
	}

	defer func() { _ = tx.Rollback() }()

	if err := guardLastAdmin(ctx, tx, id); err != nil {
		return Account{}, err
	}

	if _, err := tx.ExecContext(ctx, `DELETE FROM users WHERE id = ?`, id); err != nil {
		return Account{}, fmt.Errorf("store: %w", err)
	}

	if err := tx.Commit(); err != nil {
		return Account{}, fmt.Errorf("store: %w", err)
	}

	return a, nil
}

// ResetAccount removes the password and every second factor of the
// account, ends its sessions and stores a new invitation, in one
// transaction. API tokens stay; disable the account to stop them too.
func (s *Store) ResetAccount(ctx context.Context, id int64, inv Invitation) error {
	tx, err := s.w.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("store: %w", err)
	}

	defer func() { _ = tx.Rollback() }()

	res, err := tx.ExecContext(ctx,
		`UPDATE users SET password_hash = '', totp_secret = NULL, totp_enabled = 0, totp_last_step = 0 WHERE id = ?`, id)
	if err != nil {
		return fmt.Errorf("store: %w", err)
	}

	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}

	for _, q := range []string{
		`DELETE FROM sessions WHERE user_id = ?`,
		`DELETE FROM recovery_codes WHERE user_id = ?`,
		`DELETE FROM passkeys WHERE user_id = ?`,
	} {
		if _, err := tx.ExecContext(ctx, q, id); err != nil {
			return fmt.Errorf("store: %w", err)
		}
	}

	if err := putInvitation(ctx, tx, id, inv); err != nil {
		return err
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("store: %w", err)
	}

	return nil
}

// InvitedUser returns the account of an open, unexpired invitation, or
// ErrNotFound.
func (s *Store) InvitedUser(ctx context.Context, tokenHash []byte, now time.Time) (User, error) {
	var id int64

	err := s.db.QueryRowContext(ctx,
		`SELECT user_id FROM invitations WHERE token_hash = ? AND expires_at > ?`, tokenHash, now.Unix()).Scan(&id)
	if errors.Is(err, sql.ErrNoRows) {
		return User{}, ErrNotFound
	}

	if err != nil {
		return User{}, fmt.Errorf("store: %w", err)
	}

	return s.UserByID(ctx, id)
}

// AcceptInvitation sets the password of the invitation's account and
// removes the invitation, in one transaction, so a link works once. An
// unknown or expired link, or a disabled account, returns ErrNotFound.
func (s *Store) AcceptInvitation(ctx context.Context, tokenHash []byte, passwordHash string, now time.Time) (User, error) {
	tx, err := s.w.BeginTx(ctx, nil)
	if err != nil {
		return User{}, fmt.Errorf("store: %w", err)
	}

	defer func() { _ = tx.Rollback() }()

	var (
		id    int64
		email string
	)

	err = tx.QueryRowContext(ctx,
		`SELECT i.user_id, i.email FROM invitations i JOIN users u ON u.id = i.user_id
		  WHERE i.token_hash = ? AND i.expires_at > ? AND u.disabled = 0`, tokenHash, now.Unix()).Scan(&id, &email)
	if errors.Is(err, sql.ErrNoRows) {
		return User{}, ErrNotFound
	}

	if err != nil {
		return User{}, fmt.Errorf("store: %w", err)
	}

	// An emailed link proves the mailbox: its address becomes the account's.
	if _, err := tx.ExecContext(ctx,
		`UPDATE users SET password_hash = ?, email = CASE WHEN ? != '' THEN ? ELSE email END WHERE id = ?`,
		passwordHash, email, email, id); err != nil {
		return User{}, fmt.Errorf("store: %w", err)
	}

	if _, err := tx.ExecContext(ctx, `DELETE FROM invitations WHERE user_id = ?`, id); err != nil {
		return User{}, fmt.Errorf("store: %w", err)
	}

	if err := tx.Commit(); err != nil {
		return User{}, fmt.Errorf("store: %w", err)
	}

	return s.UserByID(ctx, id)
}

// ClearInvitationEmail forgets the address of an account's open invitation,
// when the email could not be queued and the link is only shown.
func (s *Store) ClearInvitationEmail(ctx context.Context, userID int64) error {
	if _, err := s.w.ExecContext(ctx, `UPDATE invitations SET email = '' WHERE user_id = ?`, userID); err != nil {
		return fmt.Errorf("store: %w", err)
	}

	return nil
}

// DeleteExpiredInvitations removes invitations past their end.
func (s *Store) DeleteExpiredInvitations(ctx context.Context, now time.Time) error {
	if _, err := s.w.ExecContext(ctx, `DELETE FROM invitations WHERE expires_at <= ?`, now.Unix()); err != nil {
		return fmt.Errorf("store: %w", err)
	}

	return nil
}
