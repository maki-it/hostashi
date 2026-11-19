// SPDX-License-Identifier: AGPL-3.0-or-later

package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
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
	ID      int64
	Name    string
	Pool    string
	State   string
	PHP     string // PHP version, empty for no PHP
	Domains []string
	TLS     bool // HTTPS wanted
	// HSTS sends Strict-Transport-Security while the site is on HTTPS.
	HSTS bool
	// Disabled: the web server answers with 503 and PHP is stopped; the
	// account, files, databases and certificate stay.
	Disabled bool
	// Maintenance serves MaintenancePage (0: the built-in page) with 503 to
	// every address outside MaintenanceAllow (ADR-0027).
	Maintenance      bool
	MaintenancePage  int64
	MaintenanceAllow []string
	// TLSLive: the last accepted web state serves the site over HTTPS.
	TLSLive bool
	// TLSDomains and TLSNotAfter describe the live certificate (none: empty).
	TLSDomains  []string
	TLSNotAfter time.Time
	// TLSError is the reason of the last failed attempt at TLSFailedAt.
	TLSError    string
	TLSFailedAt time.Time
	CreatedAt   time.Time
	UpdatedAt   time.Time
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

	return Site{ID: id, Name: name, Pool: pool, State: SiteCreating, PHP: php, Domains: domains, TLSDomains: []string{}, CreatedAt: t, UpdatedAt: t}, nil
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

		// A parked domain that becomes part of a site is no longer parked.
		if _, err := tx.ExecContext(ctx, `DELETE FROM parked_domains WHERE domain = ?`, d); err != nil {
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
		`SELECT `+siteColumns+` FROM sites ORDER BY name`)
	if err != nil {
		return nil, fmt.Errorf("store: %w", err)
	}

	var out []Site

	for rows.Next() {
		st, err := scanSite(rows)
		if err != nil {
			_ = rows.Close()

			return nil, err
		}

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
	st, err := scanSite(s.db.QueryRowContext(ctx, `SELECT `+siteColumns+` FROM sites WHERE name = ?`, name))
	if errors.Is(err, sql.ErrNoRows) {
		return Site{}, ErrNotFound
	}

	if err != nil {
		return Site{}, err
	}

	if st.Domains, err = domainsOf(ctx, s.db, st.ID); err != nil {
		return Site{}, err
	}

	return st, nil
}

type scanner interface{ Scan(dest ...any) error }

const siteColumns = `id, name, pool, state, php, tls, hsts, disabled, tls_live, tls_domains, tls_not_after, tls_error, tls_failed_at,
	maintenance, coalesce(maintenance_page, 0), maintenance_allow, created_at, updated_at`

func scanSite(r scanner) (Site, error) {
	var (
		st                         Site
		tls, hsts, disabled, live  int
		maint                      int
		tlsDomains, allow          string
		notAfter, failed, cre, upd int64
	)

	err := r.Scan(&st.ID, &st.Name, &st.Pool, &st.State, &st.PHP, &tls, &hsts, &disabled, &live, &tlsDomains, &notAfter, &st.TLSError, &failed,
		&maint, &st.MaintenancePage, &allow, &cre, &upd)
	if errors.Is(err, sql.ErrNoRows) {
		return Site{}, err
	}

	if err != nil {
		return Site{}, fmt.Errorf("store: %w", err)
	}

	st.TLS, st.HSTS, st.Disabled, st.TLSLive = tls == 1, hsts == 1, disabled == 1, live == 1
	st.Maintenance, st.MaintenanceAllow = maint == 1, strings.Fields(allow)
	st.TLSDomains = strings.Fields(tlsDomains)
	st.TLSNotAfter, st.TLSFailedAt = unixOrZero(notAfter), unixOrZero(failed)
	st.CreatedAt, st.UpdatedAt = time.Unix(cre, 0).UTC(), time.Unix(upd, 0).UTC()

	return st, nil
}

func unixOrZero(v int64) time.Time {
	if v == 0 {
		return time.Time{}
	}

	return time.Unix(v, 0).UTC()
}

// SetSiteTLS sets whether a site wants HTTPS and returns the previous value.
func (s *Store) SetSiteTLS(ctx context.Context, name string, on bool, now time.Time) (bool, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return false, fmt.Errorf("store: %w", err)
	}

	defer func() { _ = tx.Rollback() }()

	var old int
	if err := tx.QueryRowContext(ctx, `SELECT tls FROM sites WHERE name = ?`, name).Scan(&old); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return false, ErrNotFound
		}

		return false, fmt.Errorf("store: %w", err)
	}

	v := 0
	if on {
		v = 1
	}

	// A new request starts without the old failure, so it is tried at once.
	if _, err := tx.ExecContext(ctx, `UPDATE sites SET tls = ?, tls_error = '', tls_failed_at = 0, updated_at = ? WHERE name = ?`,
		v, now.Unix(), name); err != nil {
		return false, fmt.Errorf("store: %w", err)
	}

	if err := tx.Commit(); err != nil {
		return false, fmt.Errorf("store: %w", err)
	}

	return old == 1, nil
}

// SetSiteCert records a newly installed certificate and clears the error.
func (s *Store) SetSiteCert(ctx context.Context, name string, domains []string, notAfter, now time.Time) error {
	res, err := s.db.ExecContext(ctx,
		`UPDATE sites SET tls_domains = ?, tls_not_after = ?, tls_error = '', tls_failed_at = 0, updated_at = ? WHERE name = ?`,
		strings.Join(domains, " "), notAfter.Unix(), now.Unix(), name)
	if err != nil {
		return fmt.Errorf("store: %w", err)
	}

	if n, _ := res.RowsAffected(); n != 1 {
		return ErrNotFound
	}

	return nil
}

// SetSiteHSTS sets whether a site sends HSTS and returns the previous value.
func (s *Store) SetSiteHSTS(ctx context.Context, name string, on bool, now time.Time) (bool, error) {
	return s.setSiteFlag(ctx, `SELECT hsts FROM sites WHERE name = ?`, `UPDATE sites SET hsts = ?, updated_at = ? WHERE name = ?`, name, on, now)
}

// SetSiteDisabled sets whether a site is disabled and returns the previous
// value.
func (s *Store) SetSiteDisabled(ctx context.Context, name string, on bool, now time.Time) (bool, error) {
	return s.setSiteFlag(ctx, `SELECT disabled FROM sites WHERE name = ?`, `UPDATE sites SET disabled = ?, updated_at = ? WHERE name = ?`, name, on, now)
}

// setSiteFlag sets one 0/1 column of a site and returns its previous value.
// The statements are constants of the callers, never built from input.
func (s *Store) setSiteFlag(ctx context.Context, get, set, name string, on bool, now time.Time) (bool, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return false, fmt.Errorf("store: %w", err)
	}

	defer func() { _ = tx.Rollback() }()

	var old int
	if err := tx.QueryRowContext(ctx, get, name).Scan(&old); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return false, ErrNotFound
		}

		return false, fmt.Errorf("store: %w", err)
	}

	v := 0
	if on {
		v = 1
	}

	if _, err := tx.ExecContext(ctx, set, v, now.Unix(), name); err != nil {
		return false, fmt.Errorf("store: %w", err)
	}

	if err := tx.Commit(); err != nil {
		return false, fmt.Errorf("store: %w", err)
	}

	return old == 1, nil
}

// SetTLSLive records which sites the agent now serves over HTTPS; all
// others are not.
func (s *Store) SetTLSLive(ctx context.Context, names []string) error {
	b, err := json.Marshal(names)
	if err != nil {
		return fmt.Errorf("store: %w", err)
	}

	if _, err := s.db.ExecContext(ctx,
		`UPDATE sites SET tls_live = (name IN (SELECT value FROM json_each(?)))`, string(b)); err != nil {
		return fmt.Errorf("store: %w", err)
	}

	return nil
}

// SetSiteTLSError records a failed attempt to get a certificate.
func (s *Store) SetSiteTLSError(ctx context.Context, name, msg string, at time.Time) error {
	if len(msg) > 1000 {
		msg = strings.ToValidUTF8(msg[:1000], "")
	}

	if _, err := s.db.ExecContext(ctx, `UPDATE sites SET tls_error = ?, tls_failed_at = ? WHERE name = ?`, msg, at.Unix(), name); err != nil {
		return fmt.Errorf("store: %w", err)
	}

	return nil
}

// DeleteSite removes a site and its domains. A missing site is not an error.
func (s *Store) DeleteSite(ctx context.Context, name string) error {
	if _, err := s.db.ExecContext(ctx, `DELETE FROM sites WHERE name = ?`, name); err != nil {
		return fmt.Errorf("store: %w", err)
	}

	return nil
}
