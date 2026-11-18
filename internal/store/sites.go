// SPDX-License-Identifier: AGPL-3.0-or-later

package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"
)

// Site states (see migration 0006).
const (
	SiteCreating = "creating"
	SiteActive   = "active"
	SiteDeleting = "deleting"
)

// Errors of the site tables.
var (
	ErrSiteExists  = errors.New("store: a site with this name exists")
	ErrDomainTaken = errors.New("store: domain belongs to another site")
)

// Site is a stored site with its domains, the primary one first.
type Site struct {
	ID        int64
	Name      string
	Pool      string
	State     string
	PHP       string // PHP version, empty for no PHP
	Domains   []string
	CreatedAt time.Time
	UpdatedAt time.Time
}

// CreateSite stores a new site in state creating together with its domains.
func (s *Store) CreateSite(ctx context.Context, name, pool, php string, domains []string, now time.Time) (Site, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return Site{}, fmt.Errorf("store: %w", err)
	}

	defer func() { _ = tx.Rollback() }()

	var n int
	if err := tx.QueryRowContext(ctx, `SELECT count(*) FROM sites WHERE name = ?`, name).Scan(&n); err != nil {
		return Site{}, fmt.Errorf("store: %w", err)
	}

	if n > 0 {
		return Site{}, ErrSiteExists
	}

	res, err := tx.ExecContext(ctx,
		`INSERT INTO sites (name, pool, state, php, created_at, updated_at) VALUES (?, ?, ?, ?, ?, ?)`,
		name, pool, SiteCreating, php, now.Unix(), now.Unix())
	if err != nil {
		return Site{}, fmt.Errorf("store: %w", err)
	}

	id, err := res.LastInsertId()
	if err != nil {
		return Site{}, fmt.Errorf("store: %w", err)
	}

	if err := insertDomains(ctx, tx, id, domains); err != nil {
		return Site{}, err
	}

	if err := tx.Commit(); err != nil {
		return Site{}, fmt.Errorf("store: %w", err)
	}

	t := time.Unix(now.Unix(), 0).UTC()

	return Site{ID: id, Name: name, Pool: pool, State: SiteCreating, PHP: php, Domains: domains, CreatedAt: t, UpdatedAt: t}, nil
}

func insertDomains(ctx context.Context, tx *sql.Tx, siteID int64, domains []string) error {
	for i, d := range domains {
		var owner int64

		err := tx.QueryRowContext(ctx, `SELECT site_id FROM site_domains WHERE domain = ?`, d).Scan(&owner)

		switch {
		case err == nil && owner != siteID:
			return fmt.Errorf("%w: %s", ErrDomainTaken, d)
		case err != nil && !errors.Is(err, sql.ErrNoRows):
			return fmt.Errorf("store: %w", err)
		}

		if _, err := tx.ExecContext(ctx,
			`INSERT INTO site_domains (domain, site_id, position) VALUES (?, ?, ?)`, d, siteID, i); err != nil {
			return fmt.Errorf("store: %w", err)
		}
	}

	return nil
}

// SetSiteState changes the state of a site.
func (s *Store) SetSiteState(ctx context.Context, name, state string, now time.Time) error {
	res, err := s.db.ExecContext(ctx,
		`UPDATE sites SET state = ?, updated_at = ? WHERE name = ?`, state, now.Unix(), name)
	if err != nil {
		return fmt.Errorf("store: %w", err)
	}

	if n, _ := res.RowsAffected(); n != 1 {
		return ErrNotFound
	}

	return nil
}

// SetSitePHP sets the PHP version of a site and returns the previous one.
func (s *Store) SetSitePHP(ctx context.Context, name, php string, now time.Time) (string, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return "", fmt.Errorf("store: %w", err)
	}

	defer func() { _ = tx.Rollback() }()

	var old string
	if err := tx.QueryRowContext(ctx, `SELECT php FROM sites WHERE name = ?`, name).Scan(&old); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return "", ErrNotFound
		}

		return "", fmt.Errorf("store: %w", err)
	}

	if _, err := tx.ExecContext(ctx, `UPDATE sites SET php = ?, updated_at = ? WHERE name = ?`, php, now.Unix(), name); err != nil {
		return "", fmt.Errorf("store: %w", err)
	}

	if err := tx.Commit(); err != nil {
		return "", fmt.Errorf("store: %w", err)
	}

	return old, nil
}

// ReplaceSiteDomains sets the domains of a site and returns the previous ones.
func (s *Store) ReplaceSiteDomains(ctx context.Context, name string, domains []string, now time.Time) ([]string, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("store: %w", err)
	}

	defer func() { _ = tx.Rollback() }()

	var id int64
	if err := tx.QueryRowContext(ctx, `SELECT id FROM sites WHERE name = ?`, name).Scan(&id); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrNotFound
		}

		return nil, fmt.Errorf("store: %w", err)
	}

	old, err := domainsOf(ctx, tx, id)
	if err != nil {
		return nil, err
	}

	if _, err := tx.ExecContext(ctx, `DELETE FROM site_domains WHERE site_id = ?`, id); err != nil {
		return nil, fmt.Errorf("store: %w", err)
	}

	if err := insertDomains(ctx, tx, id, domains); err != nil {
		return nil, err
	}

	if _, err := tx.ExecContext(ctx, `UPDATE sites SET updated_at = ? WHERE id = ?`, now.Unix(), id); err != nil {
		return nil, fmt.Errorf("store: %w", err)
	}

	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("store: %w", err)
	}

	return old, nil
}

type queryer interface {
	QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error)
}

func domainsOf(ctx context.Context, q queryer, siteID int64) ([]string, error) {
	rows, err := q.QueryContext(ctx, `SELECT domain FROM site_domains WHERE site_id = ? ORDER BY position`, siteID)
	if err != nil {
		return nil, fmt.Errorf("store: %w", err)
	}

	defer rows.Close()

	out := []string{}

	for rows.Next() {
		var d string
		if err := rows.Scan(&d); err != nil {
			return nil, fmt.Errorf("store: %w", err)
		}

		out = append(out, d)
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("store: %w", err)
	}

	return out, nil
}

// Sites returns all sites sorted by name, with their domains.
func (s *Store) Sites(ctx context.Context) ([]Site, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT id, name, pool, state, php, created_at, updated_at FROM sites ORDER BY name`)
	if err != nil {
		return nil, fmt.Errorf("store: %w", err)
	}

	var out []Site

	for rows.Next() {
		var (
			st       Site
			cre, upd int64
		)

		if err := rows.Scan(&st.ID, &st.Name, &st.Pool, &st.State, &st.PHP, &cre, &upd); err != nil {
			_ = rows.Close()

			return nil, fmt.Errorf("store: %w", err)
		}

		st.CreatedAt, st.UpdatedAt = time.Unix(cre, 0).UTC(), time.Unix(upd, 0).UTC()
		out = append(out, st)
	}

	if err := rows.Err(); err != nil {
		_ = rows.Close()

		return nil, fmt.Errorf("store: %w", err)
	}

	_ = rows.Close()

	for i := range out {
		if out[i].Domains, err = domainsOf(ctx, s.db, out[i].ID); err != nil {
			return nil, err
		}
	}

	return out, nil
}

// SiteByName returns one site or ErrNotFound.
func (s *Store) SiteByName(ctx context.Context, name string) (Site, error) {
	var (
		st       Site
		cre, upd int64
	)

	err := s.db.QueryRowContext(ctx,
		`SELECT id, name, pool, state, php, created_at, updated_at FROM sites WHERE name = ?`, name).
		Scan(&st.ID, &st.Name, &st.Pool, &st.State, &st.PHP, &cre, &upd)
	if errors.Is(err, sql.ErrNoRows) {
		return Site{}, ErrNotFound
	}

	if err != nil {
		return Site{}, fmt.Errorf("store: %w", err)
	}

	st.CreatedAt, st.UpdatedAt = time.Unix(cre, 0).UTC(), time.Unix(upd, 0).UTC()

	if st.Domains, err = domainsOf(ctx, s.db, st.ID); err != nil {
		return Site{}, err
	}

	return st, nil
}

// DeleteSite removes a site and its domains. A missing site is not an error.
func (s *Store) DeleteSite(ctx context.Context, name string) error {
	if _, err := s.db.ExecContext(ctx, `DELETE FROM sites WHERE name = ?`, name); err != nil {
		return fmt.Errorf("store: %w", err)
	}

	return nil
}
