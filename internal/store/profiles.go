// SPDX-License-Identifier: AGPL-3.0-or-later

package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/maki-it/hostashi/internal/profile"
)

// SiteProfile is the application profile of a site and the hardening
// values it changed (ADR-0069). Profile is empty for a site that follows
// the default of its type.
type SiteProfile struct {
	SiteID    int64
	Profile   string
	Overrides profile.Overrides
	UpdatedBy int64 // 0: unknown (importer or deleted user)
	UpdatedAt time.Time
}

func scanSiteProfile(siteID int64, name, raw string, by sql.NullInt64, upd int64) (SiteProfile, error) {
	out := SiteProfile{SiteID: siteID, Profile: name, UpdatedBy: by.Int64, UpdatedAt: time.Unix(upd, 0).UTC()}
	if err := json.Unmarshal([]byte(raw), &out.Overrides); err != nil {
		return SiteProfile{}, fmt.Errorf("store: profile of site %d: %w", siteID, err)
	}

	return out, nil
}

// SiteProfile returns the profile of a site; a site without a row gets an
// empty Profile, not ErrNotFound.
func (s *Store) SiteProfile(ctx context.Context, siteID int64) (SiteProfile, error) {
	var (
		name, raw string
		by        sql.NullInt64
		upd       int64
	)

	err := s.db.QueryRowContext(ctx, `SELECT profile, overrides, updated_by, updated_at FROM site_profiles WHERE site_id = ?`, siteID).Scan(&name, &raw, &by, &upd)
	if errors.Is(err, sql.ErrNoRows) {
		return SiteProfile{SiteID: siteID}, nil
	}

	if err != nil {
		return SiteProfile{}, fmt.Errorf("store: %w", err)
	}

	return scanSiteProfile(siteID, name, raw, by, upd)
}

// AllSiteProfiles returns the profiles of every site that has a row, by
// site id.
func (s *Store) AllSiteProfiles(ctx context.Context) (map[int64]SiteProfile, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT site_id, profile, overrides, updated_by, updated_at FROM site_profiles`)
	if err != nil {
		return nil, fmt.Errorf("store: %w", err)
	}

	defer rows.Close()

	out := map[int64]SiteProfile{}

	for rows.Next() {
		var (
			id        int64
			name, raw string
			by        sql.NullInt64
			upd       int64
		)

		if err := rows.Scan(&id, &name, &raw, &by, &upd); err != nil {
			return nil, fmt.Errorf("store: %w", err)
		}

		p, err := scanSiteProfile(id, name, raw, by, upd)
		if err != nil {
			return nil, err
		}

		out[id] = p
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("store: %w", err)
	}

	return out, nil
}

// SetSiteProfile replaces the profile of a site and returns the previous
// one. An empty Profile without overrides removes the row. Callers
// validate first.
func (s *Store) SetSiteProfile(ctx context.Context, p SiteProfile, now time.Time) (SiteProfile, error) {
	old, err := s.SiteProfile(ctx, p.SiteID)
	if err != nil {
		return SiteProfile{}, err
	}

	if p.Profile == "" {
		if !p.Overrides.IsZero() {
			return SiteProfile{}, errors.New("store: overrides need a profile")
		}

		if _, err := s.w.ExecContext(ctx, `DELETE FROM site_profiles WHERE site_id = ?`, p.SiteID); err != nil {
			return SiteProfile{}, fmt.Errorf("store: %w", err)
		}

		return old, nil
	}

	raw, err := json.Marshal(p.Overrides)
	if err != nil {
		return SiteProfile{}, fmt.Errorf("store: %w", err)
	}

	var by any
	if p.UpdatedBy != 0 {
		by = p.UpdatedBy
	}

	if _, err := s.w.ExecContext(ctx, `INSERT INTO site_profiles (site_id, profile, overrides, updated_by, updated_at) VALUES (?, ?, ?, ?, ?)
ON CONFLICT (site_id) DO UPDATE SET profile = excluded.profile, overrides = excluded.overrides, updated_by = excluded.updated_by, updated_at = excluded.updated_at`,
		p.SiteID, p.Profile, string(raw), by, now.Unix()); err != nil {
		return SiteProfile{}, fmt.Errorf("store: %w", err)
	}

	return old, nil
}
