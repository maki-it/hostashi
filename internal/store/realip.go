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

// SiteRealIP is the real client address setting of a site (ADR-0059).
type SiteRealIP struct {
	SiteID    int64
	RealIP    agentapi.WebRealIP
	UpdatedBy int64 // 0: unknown (importer or deleted user)
	UpdatedAt time.Time
}

func scanRealIP(siteID int64, raw string, by sql.NullInt64, upd int64) (SiteRealIP, error) {
	out := SiteRealIP{SiteID: siteID, UpdatedBy: by.Int64, UpdatedAt: time.Unix(upd, 0).UTC()}
	if err := json.Unmarshal([]byte(raw), &out.RealIP); err != nil {
		return SiteRealIP{}, fmt.Errorf("store: real IP setting of site %d: %w", siteID, err)
	}

	return out, nil
}

// SiteRealIP returns the setting of a site; a site without one gets
// the zero value (trusts nobody), not ErrNotFound.
func (s *Store) SiteRealIP(ctx context.Context, siteID int64) (SiteRealIP, error) {
	var (
		raw string
		by  sql.NullInt64
		upd int64
	)

	err := s.db.QueryRowContext(ctx, `SELECT realip, updated_by, updated_at FROM site_realip WHERE site_id = ?`, siteID).Scan(&raw, &by, &upd)
	if errors.Is(err, sql.ErrNoRows) {
		return SiteRealIP{SiteID: siteID}, nil
	}

	if err != nil {
		return SiteRealIP{}, fmt.Errorf("store: %w", err)
	}

	return scanRealIP(siteID, raw, by, upd)
}

// AllSiteRealIP returns the setting of every site that has one, by
// site id.
func (s *Store) AllSiteRealIP(ctx context.Context) (map[int64]SiteRealIP, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT site_id, realip, updated_by, updated_at FROM site_realip`)
	if err != nil {
		return nil, fmt.Errorf("store: %w", err)
	}

	defer rows.Close()

	out := map[int64]SiteRealIP{}

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

		h, err := scanRealIP(id, raw, by, upd)
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

// SetSiteRealIP replaces the setting of a site and returns the
// previous one. The zero value removes the row. Callers validate first.
func (s *Store) SetSiteRealIP(ctx context.Context, h SiteRealIP, now time.Time) (SiteRealIP, error) {
	old, err := s.SiteRealIP(ctx, h.SiteID)
	if err != nil {
		return SiteRealIP{}, err
	}

	raw, err := json.Marshal(h.RealIP)
	if err != nil {
		return SiteRealIP{}, fmt.Errorf("store: %w", err)
	}

	if string(raw) == "{}" {
		_, err = s.w.ExecContext(ctx, `DELETE FROM site_realip WHERE site_id = ?`, h.SiteID)
	} else {
		var by any
		if h.UpdatedBy != 0 {
			by = h.UpdatedBy
		}

		_, err = s.w.ExecContext(ctx, `INSERT INTO site_realip (site_id, realip, updated_by, updated_at) VALUES (?, ?, ?, ?)
ON CONFLICT (site_id) DO UPDATE SET realip = excluded.realip, updated_by = excluded.updated_by, updated_at = excluded.updated_at`,
			h.SiteID, string(raw), by, now.Unix())
	}

	if err != nil {
		return SiteRealIP{}, fmt.Errorf("store: %w", err)
	}

	return old, nil
}
