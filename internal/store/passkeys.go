// SPDX-License-Identifier: AGPL-3.0-or-later

package store

import (
	"context"
	"crypto/rand"
	"database/sql"
	"errors"
	"fmt"
	"time"
)

// MaxPasskeys is the number of passkeys one user may register.
const MaxPasskeys = 20

// ErrTooManyPasskeys is returned when the user already has MaxPasskeys.
var ErrTooManyPasskeys = errors.New("store: too many passkeys")

// Passkey is a registered WebAuthn credential (public data only).
type Passkey struct {
	ID           int64
	UserID       int64
	CredentialID []byte
	Name         string
	Credential   string // JSON, see internal/auth
	CreatedAt    time.Time
	LastUsedAt   *time.Time
}

// WebAuthnHandle returns the random user handle of a user and creates it on
// first use.
func (s *Store) WebAuthnHandle(ctx context.Context, userID int64) ([]byte, error) {
	h := make([]byte, 32)
	if _, err := rand.Read(h); err != nil {
		return nil, fmt.Errorf("store: %w", err)
	}

	if _, err := s.w.ExecContext(ctx,
		`INSERT INTO webauthn_handles (user_id, handle) VALUES (?, ?) ON CONFLICT (user_id) DO NOTHING`, userID, h); err != nil {
		return nil, fmt.Errorf("store: %w", err)
	}

	var out []byte
	if err := s.db.QueryRowContext(ctx, `SELECT handle FROM webauthn_handles WHERE user_id = ?`, userID).Scan(&out); err != nil {
		return nil, fmt.Errorf("store: %w", err)
	}

	return out, nil
}

// UserByWebAuthnHandle returns the user with this handle or ErrNotFound.
func (s *Store) UserByWebAuthnHandle(ctx context.Context, handle []byte) (User, error) {
	var id int64

	err := s.db.QueryRowContext(ctx, `SELECT user_id FROM webauthn_handles WHERE handle = ?`, handle).Scan(&id)
	if errors.Is(err, sql.ErrNoRows) {
		return User{}, ErrNotFound
	}

	if err != nil {
		return User{}, fmt.Errorf("store: %w", err)
	}

	return s.UserByID(ctx, id)
}

// AddPasskey stores a new passkey. It fails with ErrTooManyPasskeys at the limit.
func (s *Store) AddPasskey(ctx context.Context, p Passkey) (Passkey, error) {
	tx, err := s.w.BeginTx(ctx, nil)
	if err != nil {
		return Passkey{}, fmt.Errorf("store: %w", err)
	}

	defer func() { _ = tx.Rollback() }()

	var n int
	if err := tx.QueryRowContext(ctx, `SELECT count(*) FROM passkeys WHERE user_id = ?`, p.UserID).Scan(&n); err != nil {
		return Passkey{}, fmt.Errorf("store: %w", err)
	}

	if n >= MaxPasskeys {
		return Passkey{}, ErrTooManyPasskeys
	}

	res, err := tx.ExecContext(ctx,
		`INSERT INTO passkeys (user_id, credential_id, name, credential, created_at) VALUES (?, ?, ?, ?, ?)`,
		p.UserID, p.CredentialID, p.Name, p.Credential, p.CreatedAt.Unix())
	if err != nil {
		return Passkey{}, fmt.Errorf("store: %w", err)
	}

	if p.ID, err = res.LastInsertId(); err != nil {
		return Passkey{}, fmt.Errorf("store: %w", err)
	}

	if err := tx.Commit(); err != nil {
		return Passkey{}, fmt.Errorf("store: %w", err)
	}

	return p, nil
}

const passkeyCols = `id, user_id, credential_id, name, credential, created_at, last_used_at` //nolint:gosec // column names, not a secret

func scanPasskey(r rowScanner) (Passkey, error) {
	var (
		p        Passkey
		created  int64
		lastUsed sql.NullInt64
	)

	if err := r.Scan(&p.ID, &p.UserID, &p.CredentialID, &p.Name, &p.Credential, &created, &lastUsed); err != nil {
		return Passkey{}, err //nolint:wrapcheck // callers wrap with context
	}

	p.CreatedAt = time.Unix(created, 0).UTC()
	p.LastUsedAt = unixPtr(lastUsed)

	return p, nil
}

// Passkeys returns the passkeys of a user, oldest first.
func (s *Store) Passkeys(ctx context.Context, userID int64) ([]Passkey, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT `+passkeyCols+` FROM passkeys WHERE user_id = ? ORDER BY id`, userID)
	if err != nil {
		return nil, fmt.Errorf("store: %w", err)
	}

	defer func() { _ = rows.Close() }()

	out := []Passkey{}

	for rows.Next() {
		p, err := scanPasskey(rows)
		if err != nil {
			return nil, fmt.Errorf("store: %w", err)
		}

		out = append(out, p)
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("store: %w", err)
	}

	return out, nil
}

// PasskeyCount returns the number of passkeys of a user.
func (s *Store) PasskeyCount(ctx context.Context, userID int64) (int, error) {
	var n int
	if err := s.db.QueryRowContext(ctx, `SELECT count(*) FROM passkeys WHERE user_id = ?`, userID).Scan(&n); err != nil {
		return 0, fmt.Errorf("store: %w", err)
	}

	return n, nil
}

// UpdatePasskeyUse stores the credential after a login (new sign counter and
// flags) and the time of use.
func (s *Store) UpdatePasskeyUse(ctx context.Context, id int64, credential string, now time.Time) error {
	if _, err := s.w.ExecContext(ctx,
		`UPDATE passkeys SET credential = ?, last_used_at = ? WHERE id = ?`, credential, now.Unix(), id); err != nil {
		return fmt.Errorf("store: %w", err)
	}

	return nil
}

// DeletePasskey removes one passkey of this user, or returns ErrNotFound.
func (s *Store) DeletePasskey(ctx context.Context, userID, id int64) (Passkey, error) {
	p, err := scanPasskey(s.db.QueryRowContext(ctx,
		`SELECT `+passkeyCols+` FROM passkeys WHERE id = ? AND user_id = ?`, id, userID))
	if errors.Is(err, sql.ErrNoRows) {
		return Passkey{}, ErrNotFound
	}

	if err != nil {
		return Passkey{}, fmt.Errorf("store: %w", err)
	}

	if _, err := s.w.ExecContext(ctx, `DELETE FROM passkeys WHERE id = ? AND user_id = ?`, id, userID); err != nil {
		return Passkey{}, fmt.Errorf("store: %w", err)
	}

	return p, nil
}
