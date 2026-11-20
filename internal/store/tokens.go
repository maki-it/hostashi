// SPDX-License-Identifier: AGPL-3.0-or-later

package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
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
	// Sites limits the token to these sites, sorted; nil means every site
	// the owner reaches. An empty, non-nil list reaches no site.
	Sites []string
}

// MaxTokenSites keeps a token's site list small.
const MaxTokenSites = 100

func unixPtr(v sql.NullInt64) *time.Time {
	if !v.Valid {
		return nil
	}

	t := time.Unix(v.Int64, 0).UTC()

	return &t
}

// CreateAPIToken stores a new token. It fails with ErrTooManyTokens when the
// user has MaxActiveTokens unrevoked tokens, and with ErrNotFound for an
// unknown site in t.Sites.
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

	if t.Sites != nil {
		t.Sites = slices.Clone(t.Sites)
		slices.Sort(t.Sites)
		t.Sites = slices.Compact(t.Sites)
	}

	if len(t.Sites) > MaxTokenSites {
		return APIToken{}, fmt.Errorf("store: at most %d sites per token", MaxTokenSites)
	}

	res, err := tx.ExecContext(ctx,
		`INSERT INTO api_tokens (user_id, name, prefix, token_hash, scopes, created_at, expires_at, site_limited)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
		t.UserID, t.Name, t.Prefix, tokenHash, string(scopes), t.CreatedAt.Unix(), exp, boolInt(t.Sites != nil))
	if err != nil {
		return APIToken{}, fmt.Errorf("store: %w", err)
	}

	id, err := res.LastInsertId()
	if err != nil {
		return APIToken{}, fmt.Errorf("store: %w", err)
	}

	for _, name := range t.Sites {
		res, err := tx.ExecContext(ctx,
			`INSERT INTO api_token_sites (token_id, site_id) SELECT ?, id FROM sites WHERE name = ?`, id, name)
		if err != nil {
			return APIToken{}, fmt.Errorf("store: %w", err)
		}

		if n, _ := res.RowsAffected(); n == 0 {
			return APIToken{}, fmt.Errorf("%w: site %q", ErrNotFound, name)
		}
	}

	if err := tx.Commit(); err != nil {
		return APIToken{}, fmt.Errorf("store: %w", err)
	}

	t.ID = id

	return t, nil
}

// apiColumns ends with the site list as a JSON array, or NULL for a token
// without one.
const apiColumns = `id, user_id, name, prefix, scopes, created_at, expires_at, last_used_at, last_used_ip, revoked_at,
	CASE WHEN site_limited = 1 THEN (SELECT json_group_array(s.name) FROM api_token_sites ts
	  JOIN sites s ON s.id = ts.site_id WHERE ts.token_id = api_tokens.id) END`

type rowScanner interface{ Scan(dest ...any) error }

func scanToken(r rowScanner) (APIToken, error) {
	var (
		t                    APIToken
		scopes               string
		created              int64
		exp, lastUsed, revok sql.NullInt64
		sites                sql.NullString
	)

	if err := r.Scan(&t.ID, &t.UserID, &t.Name, &t.Prefix, &scopes, &created, &exp, &lastUsed, &t.LastUsedIP, &revok, &sites); err != nil {
		return APIToken{}, err //nolint:wrapcheck // callers wrap with context
	}

	if err := json.Unmarshal([]byte(scopes), &t.Scopes); err != nil {
		return APIToken{}, fmt.Errorf("stored scopes: %w", err)
	}

	if sites.Valid {
		t.Sites = []string{}
		if err := json.Unmarshal([]byte(sites.String), &t.Sites); err != nil {
			return APIToken{}, fmt.Errorf("stored token sites: %w", err)
		}

		slices.Sort(t.Sites)
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
