// SPDX-License-Identifier: AGPL-3.0-or-later

package store

import (
	"context"
	"fmt"
)

// SealedEnvVar is an app environment variable as stored: its value
// sealed by the caller (ADR-0054).
type SealedEnvVar struct {
	Name  string
	Value []byte
}

// AppEnv returns the variables of a site's app, sorted by name.
func (s *Store) AppEnv(ctx context.Context, siteID int64) ([]SealedEnvVar, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT name, value FROM site_app_env WHERE site_id = ? ORDER BY name`, siteID)
	if err != nil {
		return nil, fmt.Errorf("store: %w", err)
	}

	defer rows.Close()

	out := []SealedEnvVar{}

	for rows.Next() {
		var v SealedEnvVar
		if err := rows.Scan(&v.Name, &v.Value); err != nil {
			return nil, fmt.Errorf("store: %w", err)
		}

		out = append(out, v)
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("store: %w", err)
	}

	return out, nil
}

// SetAppEnv replaces the variables of a site's app in one transaction.
func (s *Store) SetAppEnv(ctx context.Context, siteID int64, env []SealedEnvVar) error {
	tx, err := s.w.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("store: %w", err)
	}

	defer func() { _ = tx.Rollback() }()

	if _, err := tx.ExecContext(ctx, `DELETE FROM site_app_env WHERE site_id = ?`, siteID); err != nil {
		return fmt.Errorf("store: %w", err)
	}

	for _, v := range env {
		if _, err := tx.ExecContext(ctx, `INSERT INTO site_app_env (site_id, name, value) VALUES (?, ?, ?)`, siteID, v.Name, v.Value); err != nil {
			return fmt.Errorf("store: %w", err)
		}
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("store: %w", err)
	}

	return nil
}
