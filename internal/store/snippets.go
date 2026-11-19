// SPDX-License-Identifier: AGPL-3.0-or-later

package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"
)

// SiteSnippets are the custom nginx snippets of a site (ADR-0029).
type SiteSnippets struct {
	SiteID    int64
	Server    string
	Root      string
	UpdatedBy int64 // 0: unknown (importer or deleted user)
	UpdatedAt time.Time
}

// Empty reports whether no snippet is set.
func (s SiteSnippets) Empty() bool { return s.Server == "" && s.Root == "" }

// SiteSnippets returns the snippets of a site; a site without any gets an
// empty value, not ErrNotFound.
func (s *Store) SiteSnippets(ctx context.Context, siteID int64) (SiteSnippets, error) {
	var (
		out SiteSnippets
		by  sql.NullInt64
		upd int64
	)

	err := s.db.QueryRowContext(ctx, `SELECT site_id, server, root, updated_by, updated_at FROM site_snippets WHERE site_id = ?`, siteID).
		Scan(&out.SiteID, &out.Server, &out.Root, &by, &upd)
	if errors.Is(err, sql.ErrNoRows) {
		return SiteSnippets{SiteID: siteID}, nil
	}

	if err != nil {
		return SiteSnippets{}, fmt.Errorf("store: %w", err)
	}

	out.UpdatedBy = by.Int64
	out.UpdatedAt = time.Unix(upd, 0).UTC()

	return out, nil
}

// AllSiteSnippets returns the snippets of every site that has any, by site id.
func (s *Store) AllSiteSnippets(ctx context.Context) (map[int64]SiteSnippets, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT site_id, server, root, updated_by, updated_at FROM site_snippets`)
	if err != nil {
		return nil, fmt.Errorf("store: %w", err)
	}

	defer rows.Close()

	out := map[int64]SiteSnippets{}

	for rows.Next() {
		var (
			sn  SiteSnippets
			by  sql.NullInt64
			upd int64
		)

		if err := rows.Scan(&sn.SiteID, &sn.Server, &sn.Root, &by, &upd); err != nil {
			return nil, fmt.Errorf("store: %w", err)
		}

		sn.UpdatedBy = by.Int64
		sn.UpdatedAt = time.Unix(upd, 0).UTC()
		out[sn.SiteID] = sn
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("store: %w", err)
	}

	return out, nil
}

// SetSiteSnippets replaces the snippets of a site and returns the previous
// ones. Empty snippets remove the row. by is 0 when no user saved them.
func (s *Store) SetSiteSnippets(ctx context.Context, sn SiteSnippets, now time.Time) (SiteSnippets, error) {
	old, err := s.SiteSnippets(ctx, sn.SiteID)
	if err != nil {
		return SiteSnippets{}, err
	}

	if sn.Empty() {
		_, err = s.db.ExecContext(ctx, `DELETE FROM site_snippets WHERE site_id = ?`, sn.SiteID)
	} else {
		var by any
		if sn.UpdatedBy != 0 {
			by = sn.UpdatedBy
		}

		_, err = s.db.ExecContext(ctx, `INSERT INTO site_snippets (site_id, server, root, updated_by, updated_at) VALUES (?, ?, ?, ?, ?)
ON CONFLICT (site_id) DO UPDATE SET server = excluded.server, root = excluded.root, updated_by = excluded.updated_by, updated_at = excluded.updated_at`,
			sn.SiteID, sn.Server, sn.Root, by, now.Unix())
	}

	if err != nil {
		return SiteSnippets{}, fmt.Errorf("store: %w", err)
	}

	return old, nil
}
