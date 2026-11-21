// SPDX-License-Identifier: AGPL-3.0-or-later

package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/maki-it/hostashi/internal/agentapi"
)

// SitePHPSettings are the PHP ini settings of a site (ADR-0049).
type SitePHPSettings struct {
	SiteID    int64
	Settings  agentapi.PHPSettings
	UpdatedBy int64 // 0: unknown (importer or deleted user)
	UpdatedAt time.Time
}

func scanPHPSettings(siteID int64, raw string, by sql.NullInt64, upd int64) (SitePHPSettings, error) {
	out := SitePHPSettings{SiteID: siteID, UpdatedBy: by.Int64, UpdatedAt: time.Unix(upd, 0).UTC()}
	if err := json.Unmarshal([]byte(raw), &out.Settings); err != nil {
		return SitePHPSettings{}, fmt.Errorf("store: php settings of site %d: %w", siteID, err)
	}

	return out, nil
}

// SitePHPSettings returns the settings of a site; a site without any gets
// the zero value (all defaults), not ErrNotFound.
func (s *Store) SitePHPSettings(ctx context.Context, siteID int64) (SitePHPSettings, error) {
	var (
		raw string
		by  sql.NullInt64
		upd int64
	)

	err := s.db.QueryRowContext(ctx, `SELECT settings, updated_by, updated_at FROM site_php_settings WHERE site_id = ?`, siteID).Scan(&raw, &by, &upd)
	if errors.Is(err, sql.ErrNoRows) {
		return SitePHPSettings{SiteID: siteID}, nil
	}

	if err != nil {
		return SitePHPSettings{}, fmt.Errorf("store: %w", err)
	}

	return scanPHPSettings(siteID, raw, by, upd)
}

// AllSitePHPSettings returns the settings of every site that has any, by
// site id.
func (s *Store) AllSitePHPSettings(ctx context.Context) (map[int64]SitePHPSettings, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT site_id, settings, updated_by, updated_at FROM site_php_settings`)
	if err != nil {
		return nil, fmt.Errorf("store: %w", err)
	}

	defer rows.Close()

	out := map[int64]SitePHPSettings{}

	for rows.Next() {
		var (
			id  int64
			raw string
			by  sql.NullInt64
			upd int64
		)

		if err := rows.Scan(&id, &raw, &by, &upd); err != nil {
			return nil, fmt.Errorf("store: %w", err)
		}

		ps, err := scanPHPSettings(id, raw, by, upd)
		if err != nil {
			return nil, err
		}

		out[id] = ps
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("store: %w", err)
	}

	return out, nil
}

// SetSitePHPSettings replaces the settings of a site and returns the
// previous ones. All defaults remove the row. Callers validate first.
func (s *Store) SetSitePHPSettings(ctx context.Context, ps SitePHPSettings, now time.Time) (SitePHPSettings, error) {
	old, err := s.SitePHPSettings(ctx, ps.SiteID)
	if err != nil {
		return SitePHPSettings{}, err
	}

	raw, err := json.Marshal(ps.Settings)
	if err != nil {
		return SitePHPSettings{}, fmt.Errorf("store: %w", err)
	}

	if string(raw) == "{}" {
		_, err = s.w.ExecContext(ctx, `DELETE FROM site_php_settings WHERE site_id = ?`, ps.SiteID)
	} else {
		var by any
		if ps.UpdatedBy != 0 {
			by = ps.UpdatedBy
		}

		_, err = s.w.ExecContext(ctx, `INSERT INTO site_php_settings (site_id, settings, updated_by, updated_at) VALUES (?, ?, ?, ?)
ON CONFLICT (site_id) DO UPDATE SET settings = excluded.settings, updated_by = excluded.updated_by, updated_at = excluded.updated_at`,
			ps.SiteID, string(raw), by, now.Unix())
	}

	if err != nil {
		return SitePHPSettings{}, fmt.Errorf("store: %w", err)
	}

	return old, nil
}
