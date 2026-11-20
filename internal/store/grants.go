// SPDX-License-Identifier: AGPL-3.0-or-later

package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"slices"
	"time"
)

// ErrNotGrantee is returned when site grants are set for a user whose role
// is not "user" (ADR-0035): admins and managers reach every site anyway.
var ErrNotGrantee = errors.New("store: only users with the role user hold site grants")

// MaxGrantsPerUser keeps one request and one list small.
const MaxGrantsPerUser = 500

// SiteGrants returns the names of the sites granted to the user, sorted.
func (s *Store) SiteGrants(ctx context.Context, userID int64) ([]string, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT s.name FROM site_grants g JOIN sites s ON s.id = g.site_id
		  WHERE g.user_id = ? ORDER BY s.name`, userID)
	if err != nil {
		return nil, fmt.Errorf("store: %w", err)
	}

	defer func() { _ = rows.Close() }()

	out := []string{}

	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			return nil, fmt.Errorf("store: %w", err)
		}

		out = append(out, name)
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("store: %w", err)
	}

	return out, nil
}

// HasSiteGrant reports whether the site is granted to the user. An unknown
// site is not granted.
func (s *Store) HasSiteGrant(ctx context.Context, userID int64, site string) (bool, error) {
	var n int

	err := s.db.QueryRowContext(ctx,
		`SELECT count(*) FROM site_grants g JOIN sites s ON s.id = g.site_id
		  WHERE g.user_id = ? AND s.name = ?`, userID, site).Scan(&n)
	if err != nil {
		return false, fmt.Errorf("store: %w", err)
	}

	return n > 0, nil
}

// SetSiteGrants replaces the user's grants with the named sites, in one
// transaction. It fails with ErrNotFound for an unknown user or site, and
// with ErrNotGrantee if the user's role is not "user". It returns the sites
// that were added and removed, for the audit log.
func (s *Store) SetSiteGrants(ctx context.Context, userID int64, sites []string, now time.Time) (added, removed []string, err error) {
	want := slices.Clone(sites)
	slices.Sort(want)
	want = slices.Compact(want)

	if len(want) > MaxGrantsPerUser {
		return nil, nil, fmt.Errorf("store: at most %d site grants per user", MaxGrantsPerUser)
	}

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, nil, fmt.Errorf("store: %w", err)
	}

	defer func() { _ = tx.Rollback() }()

	var role string

	err = tx.QueryRowContext(ctx, `SELECT role FROM users WHERE id = ?`, userID).Scan(&role)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil, ErrNotFound
	}

	if err != nil {
		return nil, nil, fmt.Errorf("store: %w", err)
	}

	if role != "user" {
		return nil, nil, ErrNotGrantee
	}

	ids := make([]int64, 0, len(want))

	for _, name := range want {
		var id int64

		err := tx.QueryRowContext(ctx, `SELECT id FROM sites WHERE name = ?`, name).Scan(&id)
		if errors.Is(err, sql.ErrNoRows) {
			return nil, nil, fmt.Errorf("%w: site %q", ErrNotFound, name)
		}

		if err != nil {
			return nil, nil, fmt.Errorf("store: %w", err)
		}

		ids = append(ids, id)
	}

	have, err := grantNames(ctx, tx, userID)
	if err != nil {
		return nil, nil, err
	}

	if _, err := tx.ExecContext(ctx, `DELETE FROM site_grants WHERE user_id = ?`, userID); err != nil {
		return nil, nil, fmt.Errorf("store: %w", err)
	}

	for _, id := range ids {
		if _, err := tx.ExecContext(ctx,
			`INSERT INTO site_grants (user_id, site_id, created_at) VALUES (?, ?, ?)`, userID, id, now.Unix()); err != nil {
			return nil, nil, fmt.Errorf("store: %w", err)
		}
	}

	if err := tx.Commit(); err != nil {
		return nil, nil, fmt.Errorf("store: %w", err)
	}

	added, removed = []string{}, []string{}

	for _, n := range want {
		if !slices.Contains(have, n) {
			added = append(added, n)
		}
	}

	for _, n := range have {
		if !slices.Contains(want, n) {
			removed = append(removed, n)
		}
	}

	return added, removed, nil
}

func grantNames(ctx context.Context, tx *sql.Tx, userID int64) ([]string, error) {
	rows, err := tx.QueryContext(ctx,
		`SELECT s.name FROM site_grants g JOIN sites s ON s.id = g.site_id WHERE g.user_id = ?`, userID)
	if err != nil {
		return nil, fmt.Errorf("store: %w", err)
	}

	defer func() { _ = rows.Close() }()

	var out []string

	for rows.Next() {
		var n string
		if err := rows.Scan(&n); err != nil {
			return nil, fmt.Errorf("store: %w", err)
		}

		out = append(out, n)
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("store: %w", err)
	}

	return out, nil
}
