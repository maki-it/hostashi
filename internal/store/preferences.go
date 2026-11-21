// SPDX-License-Identifier: AGPL-3.0-or-later

package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
)

// Preferences is how the panel looks for an account (ADR-0037).
type Preferences struct {
	// ColorScheme is "system", "light" or "dark".
	ColorScheme string
	// Theme names the token set; only "default" exists.
	Theme string
}

// UserPreferences returns the preferences of an account, or ErrNotFound.
func (s *Store) UserPreferences(ctx context.Context, userID int64) (Preferences, error) {
	var p Preferences

	err := s.db.QueryRowContext(ctx, `SELECT color_scheme, theme FROM users WHERE id = ?`, userID).Scan(&p.ColorScheme, &p.Theme)
	if errors.Is(err, sql.ErrNoRows) {
		return Preferences{}, ErrNotFound
	}

	if err != nil {
		return Preferences{}, fmt.Errorf("store: %w", err)
	}

	return p, nil
}

// SetUserPreferences stores the preferences of an account. The schema
// refuses values outside the allowlist.
func (s *Store) SetUserPreferences(ctx context.Context, userID int64, p Preferences) error {
	res, err := s.w.ExecContext(ctx, `UPDATE users SET color_scheme = ?, theme = ? WHERE id = ?`, p.ColorScheme, p.Theme, userID)
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
