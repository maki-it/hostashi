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

// PageCache is the page cache of a site (ADR-0046). A site without a row
// has the cache off with the defaults (DefaultPageCache).
type PageCache struct {
	SiteID    int64
	Enabled   bool
	Preset    string
	Minutes   int
	SizeMB    int
	Exclude   []string
	UpdatedAt time.Time
}

// DefaultPageCache are the settings of a site that never touched its cache.
func DefaultPageCache(siteID int64) PageCache {
	return PageCache{SiteID: siteID, Preset: "wordpress", Minutes: 10, SizeMB: 256, Exclude: []string{}}
}

const pageCacheCols = `site_id, enabled, preset, minutes, size_mb, exclude, updated_at`

func scanPageCache(r scanner) (PageCache, error) {
	var (
		c       PageCache
		exclude string
		upd     int64
	)

	if err := r.Scan(&c.SiteID, &c.Enabled, &c.Preset, &c.Minutes, &c.SizeMB, &exclude, &upd); err != nil {
		return PageCache{}, err
	}

	if err := json.Unmarshal([]byte(exclude), &c.Exclude); err != nil {
		return PageCache{}, fmt.Errorf("store: excluded paths of site %d: %w", c.SiteID, err)
	}

	if c.Exclude == nil {
		c.Exclude = []string{}
	}

	c.UpdatedAt = time.Unix(upd, 0).UTC()

	return c, nil
}

// SitePageCache returns the page cache of a site, the defaults if it has
// no row.
func (s *Store) SitePageCache(ctx context.Context, siteID int64) (PageCache, error) {
	c, err := scanPageCache(s.db.QueryRowContext(ctx, `SELECT `+pageCacheCols+` FROM site_page_cache WHERE site_id = ?`, siteID))
	if errors.Is(err, sql.ErrNoRows) {
		return DefaultPageCache(siteID), nil
	}

	if err != nil {
		return PageCache{}, fmt.Errorf("store: %w", err)
	}

	return c, nil
}

// AllPageCaches returns the page caches of every site that has a row, by
// site id.
func (s *Store) AllPageCaches(ctx context.Context) (map[int64]PageCache, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT `+pageCacheCols+` FROM site_page_cache`)
	if err != nil {
		return nil, fmt.Errorf("store: %w", err)
	}

	defer rows.Close()

	out := map[int64]PageCache{}

	for rows.Next() {
		c, err := scanPageCache(rows)
		if err != nil {
			return nil, fmt.Errorf("store: %w", err)
		}

		out[c.SiteID] = c
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("store: %w", err)
	}

	return out, nil
}

// SetSitePageCache stores the page cache of a site and returns the previous
// settings. The database checks the fixed choices again.
func (s *Store) SetSitePageCache(ctx context.Context, c PageCache, now time.Time) (PageCache, error) {
	old, err := s.SitePageCache(ctx, c.SiteID)
	if err != nil {
		return PageCache{}, err
	}

	exclude := c.Exclude
	if exclude == nil {
		exclude = []string{}
	}

	b, err := json.Marshal(exclude)
	if err != nil {
		return PageCache{}, fmt.Errorf("store: %w", err)
	}

	_, err = s.w.ExecContext(ctx, `INSERT INTO site_page_cache (`+pageCacheCols+`) VALUES (?, ?, ?, ?, ?, ?, ?)
ON CONFLICT (site_id) DO UPDATE SET enabled = excluded.enabled, preset = excluded.preset, minutes = excluded.minutes,
size_mb = excluded.size_mb, exclude = excluded.exclude, updated_at = excluded.updated_at`,
		c.SiteID, c.Enabled, c.Preset, c.Minutes, c.SizeMB, string(b), now.Unix())
	if err != nil {
		return PageCache{}, fmt.Errorf("store: %w", err)
	}

	return old, nil
}
