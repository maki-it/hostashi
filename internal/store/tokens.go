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

// MaxActiveTokens is the number of unrevoked tokens one user may hold.
const MaxActiveTokens = 100

// ErrTooManyTokens is returned when the user already holds MaxActiveTokens.
var ErrTooManyTokens = errors.New("store: too many API tokens")

// APIToken is the stored description of a token. It never holds the secret.
type APIToken struct {
	ID         int64
	UserID     int64
	Name       string
	Prefix     string
	Scopes     []string
	CreatedAt  time.Time
	ExpiresAt  *time.Time // nil: never expires
	LastUsedAt *time.Time
	LastUsedIP string
	RevokedAt  *time.Time
}

func unixPtr(v sql.NullInt64) *time.Time {
	if !v.Valid {
		return nil
	}

	t := time.Unix(v.Int64, 0).UTC()

	return &t
}

// CreateAPIToken stores a new token. It fails with ErrTooManyTokens when the
// user has MaxActiveTokens unrevoked tokens.
func (s *Store) CreateAPIToken(ctx context.Context, t APIToken, tokenHash []byte) (APIToken, error) {
	scopes, err := json.Marshal(t.Scopes)
	if err != nil {
		return APIToken{}, fmt.Errorf("store: %w", err)
	}

	var exp sql.NullInt64
	if t.ExpiresAt != nil {
		exp = sql.NullInt64{Int64: t.ExpiresAt.Unix(), Valid: true}
	}

	tx, err := s.w.BeginTx(ctx, nil)
	if err != nil {
		return APIToken{}, fmt.Errorf("store: %w", err)
	}

	defer func() { _ = tx.Rollback() }()

	var n int
	if err := tx.QueryRowContext(ctx,
		`SELECT count(*) FROM api_tokens WHERE user_id = ? AND revoked_at IS NULL`, t.UserID).Scan(&n); err != nil {
		return APIToken{}, fmt.Errorf("store: %w", err)
	}

	if n >= MaxActiveTokens {
		return APIToken{}, ErrTooManyTokens
	}

	res, err := tx.ExecContext(ctx,
		`INSERT INTO api_tokens (user_id, name, prefix, token_hash, scopes, created_at, expires_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?)`,
		t.UserID, t.Name, t.Prefix, tokenHash, string(scopes), t.CreatedAt.Unix(), exp)
	if err != nil {
		return APIToken{}, fmt.Errorf("store: %w", err)
	}

	id, err := res.LastInsertId()
	if err != nil {
		return APIToken{}, fmt.Errorf("store: %w", err)
	}

	if err := tx.Commit(); err != nil {
		return APIToken{}, fmt.Errorf("store: %w", err)
	}

	t.ID = id

	return t, nil
}

const apiColumns = `id, user_id, name, prefix, scopes, created_at, expires_at, last_used_at, last_used_ip, revoked_at`

type rowScanner interface{ Scan(dest ...any) error }

func scanToken(r rowScanner) (APIToken, error) {
	var (
		t                    APIToken
		scopes               string
		created              int64
		exp, lastUsed, revok sql.NullInt64
	)

	if err := r.Scan(&t.ID, &t.UserID, &t.Name, &t.Prefix, &scopes, &created, &exp, &lastUsed, &t.LastUsedIP, &revok); err != nil {
		return APIToken{}, err //nolint:wrapcheck // callers wrap with context
	}

	if err := json.Unmarshal([]byte(scopes), &t.Scopes); err != nil {
		return APIToken{}, fmt.Errorf("stored scopes: %w", err)
	}

	t.CreatedAt = time.Unix(created, 0).UTC()
	t.ExpiresAt, t.LastUsedAt, t.RevokedAt = unixPtr(exp), unixPtr(lastUsed), unixPtr(revok)

	return t, nil
}

// APITokenByHash returns a token that is neither revoked nor expired, together
// with its owner. ErrNotFound covers unknown, revoked, expired and a disabled owner.
func (s *Store) APITokenByHash(ctx context.Context, tokenHash []byte, now time.Time) (APIToken, User, error) {
	row := s.db.QueryRowContext(ctx,
		`SELECT `+apiColumns+` FROM api_tokens
		  WHERE token_hash = ? AND revoked_at IS NULL AND (expires_at IS NULL OR expires_at > ?)`,
		tokenHash, now.Unix())

	t, err := scanToken(row)
	if errors.Is(err, sql.ErrNoRows) {
		return APIToken{}, User{}, ErrNotFound
	}

	if err != nil {
		return APIToken{}, User{}, fmt.Errorf("store: %w", err)
	}

	u, err := s.UserByID(ctx, t.UserID)
	if err != nil {
		return APIToken{}, User{}, err
	}

	if u.Disabled {
		return APIToken{}, User{}, ErrNotFound
	}

	u.PasswordHash, u.TOTPSecret = "", nil

	return t, u, nil
}

// ListAPITokens returns all tokens of a user, newest first, revoked ones included.
func (s *Store) ListAPITokens(ctx context.Context, userID int64) ([]APIToken, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT `+apiColumns+` FROM api_tokens WHERE user_id = ? ORDER BY id DESC`, userID)
	if err != nil {
		return nil, fmt.Errorf("store: %w", err)
	}

	defer func() { _ = rows.Close() }()

	out := []APIToken{}

	for rows.Next() {
		t, err := scanToken(rows)
		if err != nil {
			return nil, fmt.Errorf("store: %w", err)
		}

		out = append(out, t)
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("store: %w", err)
	}

	return out, nil
}

// ActiveAPITokens returns the tokens of all users that are neither revoked
// nor expired at now.
func (s *Store) ActiveAPITokens(ctx context.Context, now time.Time) ([]APIToken, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT `+apiColumns+` FROM api_tokens WHERE revoked_at IS NULL AND (expires_at IS NULL OR expires_at > ?) ORDER BY id`, now.Unix())
	if err != nil {
		return nil, fmt.Errorf("store: %w", err)
	}

	defer func() { _ = rows.Close() }()

	out := []APIToken{}

	for rows.Next() {
		t, err := scanToken(rows)
		if err != nil {
			return nil, fmt.Errorf("store: %w", err)
		}

		out = append(out, t)
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("store: %w", err)
	}

	return out, nil
}

// RevokeAPIToken revokes a token of this user. Revoking twice or another
// user's token returns ErrNotFound, so ids of other users cannot be probed.
func (s *Store) RevokeAPIToken(ctx context.Context, userID, id int64, now time.Time) (APIToken, error) {
	res, err := s.w.ExecContext(ctx,
		`UPDATE api_tokens SET revoked_at = ? WHERE id = ? AND user_id = ? AND revoked_at IS NULL`,
		now.Unix(), id, userID)
	if err != nil {
		return APIToken{}, fmt.Errorf("store: %w", err)
	}

	n, err := res.RowsAffected()
	if err != nil {
		return APIToken{}, fmt.Errorf("store: %w", err)
	}

	if n == 0 {
		return APIToken{}, ErrNotFound
	}

	t, err := scanToken(s.db.QueryRowContext(ctx, `SELECT `+apiColumns+` FROM api_tokens WHERE id = ?`, id))
	if err != nil {
		return APIToken{}, fmt.Errorf("store: %w", err)
	}

	return t, nil
}

// minTouchInterval limits how often a busy token writes its last-use time.
const minTouchInterval = time.Minute

// TouchAPIToken records the last use, at most once per minute per token.
func (s *Store) TouchAPIToken(ctx context.Context, id int64, now time.Time, ip string) error {
	_, err := s.w.ExecContext(ctx,
		`UPDATE api_tokens SET last_used_at = ?, last_used_ip = ?
		  WHERE id = ? AND (last_used_at IS NULL OR last_used_at <= ?)`,
		now.Unix(), ip, id, now.Add(-minTouchInterval).Unix())
	if err != nil {
		return fmt.Errorf("store: %w", err)
	}

	return nil
}

// Setting returns the JSON value of a setting, or ok=false if it is not set.
func (s *Store) Setting(ctx context.Context, key string) (value string, ok bool, err error) {
	err = s.db.QueryRowContext(ctx, `SELECT value FROM settings WHERE key = ?`, key).Scan(&value)
	if errors.Is(err, sql.ErrNoRows) {
		return "", false, nil
	}

	if err != nil {
		return "", false, fmt.Errorf("store: %w", err)
	}

	return value, true, nil
}

// SetSetting stores the JSON value of a setting.
func (s *Store) SetSetting(ctx context.Context, key, value string) error {
	_, err := s.w.ExecContext(ctx,
		`INSERT INTO settings (key, value) VALUES (?, ?)
		 ON CONFLICT (key) DO UPDATE SET value = excluded.value`, key, value)
	if err != nil {
		return fmt.Errorf("store: %w", err)
	}

	return nil
}
