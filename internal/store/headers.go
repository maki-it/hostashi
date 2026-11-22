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

// SiteHeaders are the security headers of a site (ADR-0056).
type SiteHeaders struct {
	SiteID    int64
	Headers   agentapi.WebHeaders
	UpdatedBy int64 // 0: unknown (importer or deleted user)
	UpdatedAt time.Time
}

func scanHeaders(siteID int64, raw string, by sql.NullInt64, upd int64) (SiteHeaders, error) {
	out := SiteHeaders{SiteID: siteID, UpdatedBy: by.Int64, UpdatedAt: time.Unix(upd, 0).UTC()}
	if err := json.Unmarshal([]byte(raw), &out.Headers); err != nil {
		return SiteHeaders{}, fmt.Errorf("store: headers of site %d: %w", siteID, err)
	}

	return out, nil
}

// SiteHeaders returns the headers of a site; a site without any gets
// the zero value (none), not ErrNotFound.
func (s *Store) SiteHeaders(ctx context.Context, siteID int64) (SiteHeaders, error) {
	var (
		raw string
		by  sql.NullInt64
		upd int64
	)

	err := s.db.QueryRowContext(ctx, `SELECT headers, updated_by, updated_at FROM site_headers WHERE site_id = ?`, siteID).Scan(&raw, &by, &upd)
	if errors.Is(err, sql.ErrNoRows) {
		return SiteHeaders{SiteID: siteID}, nil
	}

	if err != nil {
		return SiteHeaders{}, fmt.Errorf("store: %w", err)
	}

	return scanHeaders(siteID, raw, by, upd)
}

// AllSiteHeaders returns the headers of every site that has any, by
// site id.
func (s *Store) AllSiteHeaders(ctx context.Context) (map[int64]SiteHeaders, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT site_id, headers, updated_by, updated_at FROM site_headers`)
	if err != nil {
		return nil, fmt.Errorf("store: %w", err)
	}

	defer rows.Close()

	out := map[int64]SiteHeaders{}

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

		h, err := scanHeaders(id, raw, by, upd)
		if err != nil {
			return nil, err
		}

		out[id] = h
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("store: %w", err)
	}

	return out, nil
}

// SetSiteHeaders replaces the headers of a site and returns the
// previous ones. None remove the row. Callers validate first.
func (s *Store) SetSiteHeaders(ctx context.Context, h SiteHeaders, now time.Time) (SiteHeaders, error) {
	old, err := s.SiteHeaders(ctx, h.SiteID)
	if err != nil {
		return SiteHeaders{}, err
	}

	raw, err := json.Marshal(h.Headers)
	if err != nil {
		return SiteHeaders{}, fmt.Errorf("store: %w", err)
	}

	if string(raw) == "{}" {
		_, err = s.w.ExecContext(ctx, `DELETE FROM site_headers WHERE site_id = ?`, h.SiteID)
	} else {
		var by any
		if h.UpdatedBy != 0 {
			by = h.UpdatedBy
		}

		_, err = s.w.ExecContext(ctx, `INSERT INTO site_headers (site_id, headers, updated_by, updated_at) VALUES (?, ?, ?, ?)
ON CONFLICT (site_id) DO UPDATE SET headers = excluded.headers, updated_by = excluded.updated_by, updated_at = excluded.updated_at`,
			h.SiteID, string(raw), by, now.Unix())
	}

	if err != nil {
		return SiteHeaders{}, fmt.Errorf("store: %w", err)
	}

	return old, nil
}
