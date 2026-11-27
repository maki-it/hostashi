// SPDX-License-Identifier: AGPL-3.0-or-later

package store

import (
	"context"
	"fmt"
	"maps"
	"slices"
)

// NotifyRecipient is an active account with a confirmed address, with the
// operations email switches it set itself (ADR-0044). A category missing
// from Prefs has the default of the role.
type NotifyRecipient struct {
	UserID   int64
	Username string
	Role     string
	Email    string
	// Language is the account's setting: auto, en or de (ADR-0071).
	Language string
	Prefs    map[string]bool
}

// NotificationPrefs returns the switches the account set; categories it
// never set are missing.
func (s *Store) NotificationPrefs(ctx context.Context, userID int64) (map[string]bool, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT category, enabled FROM notification_prefs WHERE user_id = ?`, userID)
	if err != nil {
		return nil, fmt.Errorf("store: %w", err)
	}

	defer func() { _ = rows.Close() }()

	out := map[string]bool{}

	for rows.Next() {
		var (
			c  string
			on int
		)

		if err := rows.Scan(&c, &on); err != nil {
			return nil, fmt.Errorf("store: %w", err)
		}

		out[c] = on == 1
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("store: %w", err)
	}

	return out, nil
}

// SetNotificationPrefs stores the given switches of the account in one
// transaction; categories not in prefs keep their value.
func (s *Store) SetNotificationPrefs(ctx context.Context, userID int64, prefs map[string]bool) error {
	tx, err := s.w.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("store: %w", err)
	}

	defer func() { _ = tx.Rollback() }()

	for _, c := range slices.Sorted(maps.Keys(prefs)) {
		on := 0
		if prefs[c] {
			on = 1
		}

		res, err := tx.ExecContext(ctx,
			`INSERT INTO notification_prefs (user_id, category, enabled) SELECT id, ?, ? FROM users WHERE id = ?
			 ON CONFLICT (user_id, category) DO UPDATE SET enabled = excluded.enabled`, c, on, userID)
		if err != nil {
			return fmt.Errorf("store: %w", err)
		}

		if n, err := res.RowsAffected(); err != nil {
			return fmt.Errorf("store: %w", err)
		} else if n == 0 {
			return ErrNotFound
		}
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("store: %w", err)
	}

	return nil
}

// NotificationRecipients returns every enabled account with a confirmed
// address, sorted by user name.
func (s *Store) NotificationRecipients(ctx context.Context) ([]NotifyRecipient, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT u.id, u.username, u.role, u.email, u.language, p.category, p.enabled FROM users u
		 LEFT JOIN notification_prefs p ON p.user_id = u.id
		 WHERE u.disabled = 0 AND u.email != '' ORDER BY u.username, p.category`)
	if err != nil {
		return nil, fmt.Errorf("store: %w", err)
	}

	defer func() { _ = rows.Close() }()

	out := []NotifyRecipient{}

	for rows.Next() {
		var (
			r   NotifyRecipient
			cat *string
			on  *int64
		)

		if err := rows.Scan(&r.UserID, &r.Username, &r.Role, &r.Email, &r.Language, &cat, &on); err != nil {
			return nil, fmt.Errorf("store: %w", err)
		}

		if n := len(out); n == 0 || out[n-1].UserID != r.UserID {
			r.Prefs = map[string]bool{}
			out = append(out, r)
		}

		if cat != nil && on != nil {
			out[len(out)-1].Prefs[*cat] = *on == 1
		}
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("store: %w", err)
	}

	return out, nil
}
