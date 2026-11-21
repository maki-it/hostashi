// SPDX-License-Identifier: AGPL-3.0-or-later

package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"
)

// ObjectCache is the object cache a site should have (ADR-0045).
type ObjectCache struct {
	SiteID    int64
	Enabled   bool
	MemoryMB  int // 0: never set, the default applies
	UpdatedBy int64
	UpdatedAt time.Time
}

// ObjectCache returns the cache of a site; a site without a row gets a
// zero value (off, no size), not ErrNotFound.
func (s *Store) ObjectCache(ctx context.Context, siteID int64) (ObjectCache, error) {
	var (
		out ObjectCache
		by  sql.NullInt64
		upd int64
	)

	err := s.db.QueryRowContext(ctx, `SELECT site_id, enabled, memory_mb, updated_by, updated_at FROM site_object_cache WHERE site_id = ?`, siteID).
		Scan(&out.SiteID, &out.Enabled, &out.MemoryMB, &by, &upd)
	if errors.Is(err, sql.ErrNoRows) {
		return ObjectCache{SiteID: siteID}, nil
	}

	if err != nil {
		return ObjectCache{}, fmt.Errorf("store: %w", err)
	}

	out.UpdatedBy = by.Int64
	out.UpdatedAt = time.Unix(upd, 0).UTC()

	return out, nil
}

// SetObjectCache stores the cache of a site. by 0 records no account.
func (s *Store) SetObjectCache(ctx context.Context, siteID int64, enabled bool, memoryMB int, by int64, now time.Time) error {
	var actor any
	if by > 0 {
		actor = by
	}

	_, err := s.w.ExecContext(ctx, `INSERT INTO site_object_cache (site_id, enabled, memory_mb, updated_by, updated_at) VALUES (?, ?, ?, ?, ?)
		ON CONFLICT (site_id) DO UPDATE SET enabled = excluded.enabled, memory_mb = excluded.memory_mb,
		updated_by = excluded.updated_by, updated_at = excluded.updated_at`,
		siteID, enabled, memoryMB, actor, now.Unix())
	if err != nil {
		return fmt.Errorf("store: %w", err)
	}

	return nil
}

// DeleteObjectCache removes the cache row of a site.
func (s *Store) DeleteObjectCache(ctx context.Context, siteID int64) error {
	if _, err := s.w.ExecContext(ctx, `DELETE FROM site_object_cache WHERE site_id = ?`, siteID); err != nil {
		return fmt.Errorf("store: %w", err)
	}

	return nil
}
