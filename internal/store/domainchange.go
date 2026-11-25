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

// ErrDomainPending: the domain is part of another site's pending domain
// change (ADR-0068).
var ErrDomainPending = fmt.Errorf("%w (reserved by a pending domain change)", ErrDomainTaken)

// DomainChange is a domain change of a site that waits for its
// certificate (ADR-0068).
type DomainChange struct {
	Site string
	DomainSet
	// RequestedBy is the user who asked, 0 for a deleted user.
	RequestedBy int64
	RequestedAt time.Time
	// Error and FailedAt describe the last failed attempt (none: empty).
	Error    string
	FailedAt time.Time
}

// pendingElsewhere refuses d when a pending change of a site other than
// siteID lists it.
func pendingElsewhere(ctx context.Context, tx *sql.Tx, siteID int64, d string) error {
	var n int

	err := tx.QueryRowContext(ctx, `SELECT count(*) FROM site_domain_changes c
		WHERE c.site_id != ? AND (
			EXISTS (SELECT 1 FROM json_each(c.domains) WHERE value = ?) OR
			EXISTS (SELECT 1 FROM json_each(c.redirect_domains) WHERE value = ?))`, siteID, d, d).Scan(&n)
	if err != nil {
		return fmt.Errorf("store: %w", err)
	}

	if n > 0 {
		return fmt.Errorf("%w: %s", ErrDomainPending, d)
	}

	return nil
}

// DomainPending reports whether a pending domain change lists d. Parked
// domains use it, so a reserved name cannot be parked.
func (s *Store) DomainPending(ctx context.Context, d string) (bool, error) {
	var n int

	err := s.db.QueryRowContext(ctx, `SELECT count(*) FROM site_domain_changes c WHERE
		EXISTS (SELECT 1 FROM json_each(c.domains) WHERE value = ?) OR
		EXISTS (SELECT 1 FROM json_each(c.redirect_domains) WHERE value = ?)`, d, d).Scan(&n)
	if err != nil {
		return false, fmt.Errorf("store: %w", err)
	}

	return n > 0, nil
}

// SetDomainChange stores or replaces the pending domain change of a site.
// Every name must be free: not a domain or pending name of another site,
// not parked, and not the panel's (checked by the caller).
func (s *Store) SetDomainChange(ctx context.Context, name string, set DomainSet, actor int64, now time.Time) error {
	tx, err := s.w.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("store: %w", err)
	}

	defer func() { _ = tx.Rollback() }()

	var id int64
	if err := tx.QueryRowContext(ctx, `SELECT id FROM sites WHERE name = ?`, name).Scan(&id); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return ErrNotFound
		}

		return fmt.Errorf("store: %w", err)
	}

	for _, d := range set.All() {
		var owner int64

		err := tx.QueryRowContext(ctx, `SELECT site_id FROM site_domains WHERE domain = ?`, d).Scan(&owner)

		switch {
		case err == nil && owner != id:
			return fmt.Errorf("%w: %s", ErrDomainTaken, d)
		case err != nil && !errors.Is(err, sql.ErrNoRows):
			return fmt.Errorf("store: %w", err)
		}

		if err := pendingElsewhere(ctx, tx, id, d); err != nil {
			return err
		}
	}

	domains, err := json.Marshal(nonNilStrings(set.Domains))
	if err != nil {
		return fmt.Errorf("store: %w", err)
	}

	redirect, err := json.Marshal(nonNilStrings(set.Redirect))
	if err != nil {
		return fmt.Errorf("store: %w", err)
	}

	var by any
	if actor > 0 {
		by = actor
	}

	if _, err := tx.ExecContext(ctx, `INSERT INTO site_domain_changes (site_id, domains, redirect_domains, requested_by, requested_at, error, failed_at)
		VALUES (?, ?, ?, ?, ?, '', 0)
		ON CONFLICT (site_id) DO UPDATE SET domains = excluded.domains, redirect_domains = excluded.redirect_domains,
			requested_by = excluded.requested_by, requested_at = excluded.requested_at, error = '', failed_at = 0`,
		id, string(domains), string(redirect), by, now.Unix()); err != nil {
		return fmt.Errorf("store: %w", err)
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("store: %w", err)
	}

	return nil
}

func nonNilStrings(v []string) []string {
	if v == nil {
		return []string{}
	}

	return v
}

const domainChangeColumns = `s.name, c.domains, c.redirect_domains, coalesce(c.requested_by, 0), c.requested_at, c.error, c.failed_at`

func scanDomainChange(r scanner) (DomainChange, error) {
	var (
		c                 DomainChange
		domains, redirect string
		requested, failed int64
	)

	if err := r.Scan(&c.Site, &domains, &redirect, &c.RequestedBy, &requested, &c.Error, &failed); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return DomainChange{}, ErrNotFound
		}

		return DomainChange{}, fmt.Errorf("store: %w", err)
	}

	if err := json.Unmarshal([]byte(domains), &c.Domains); err != nil {
		return DomainChange{}, fmt.Errorf("store: domain change of %s: %w", c.Site, err)
	}

	if err := json.Unmarshal([]byte(redirect), &c.Redirect); err != nil {
		return DomainChange{}, fmt.Errorf("store: domain change of %s: %w", c.Site, err)
	}

	c.Redirect = nonNilStrings(c.Redirect)
	c.RequestedAt, c.FailedAt = time.Unix(requested, 0).UTC(), unixOrZero(failed)

	return c, nil
}

// DomainChangeOf returns the pending domain change of a site or
// ErrNotFound.
func (s *Store) DomainChangeOf(ctx context.Context, name string) (DomainChange, error) {
	return scanDomainChange(s.db.QueryRowContext(ctx, `SELECT `+domainChangeColumns+`
		FROM site_domain_changes c JOIN sites s ON s.id = c.site_id WHERE s.name = ?`, name))
}

// DomainChanges returns every pending domain change, by site name.
func (s *Store) DomainChanges(ctx context.Context) ([]DomainChange, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT `+domainChangeColumns+`
		FROM site_domain_changes c JOIN sites s ON s.id = c.site_id ORDER BY s.name`)
	if err != nil {
		return nil, fmt.Errorf("store: %w", err)
	}

	defer rows.Close()

	out := []DomainChange{}

	for rows.Next() {
		c, err := scanDomainChange(rows)
		if err != nil {
			return nil, err
		}

		out = append(out, c)
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("store: %w", err)
	}

	return out, nil
}

// SetDomainChangeError records a failed attempt of a pending change. A
// change that is gone is not an error.
func (s *Store) SetDomainChangeError(ctx context.Context, name, msg string, at time.Time) error {
	if len(msg) > 4096 {
		msg = msg[:4096]
	}

	_, err := s.w.ExecContext(ctx, `UPDATE site_domain_changes SET error = ?, failed_at = ?
		WHERE site_id = (SELECT id FROM sites WHERE name = ?)`, msg, at.Unix(), name)
	if err != nil {
		return fmt.Errorf("store: %w", err)
	}

	return nil
}

// DeleteDomainChange removes the pending change of a site; it reports
// whether there was one.
func (s *Store) DeleteDomainChange(ctx context.Context, name string) (bool, error) {
	res, err := s.w.ExecContext(ctx, `DELETE FROM site_domain_changes WHERE site_id = (SELECT id FROM sites WHERE name = ?)`, name)
	if err != nil {
		return false, fmt.Errorf("store: %w", err)
	}

	n, err := res.RowsAffected()
	if err != nil {
		return false, fmt.Errorf("store: %w", err)
	}

	return n > 0, nil
}

// FinishDomainChange stores the domains of a pending change and the
// certificate obtained for them, and removes the change, in one
// transaction. It fails with ErrNotFound when the change is gone or no
// longer asks for set (it was replaced or canceled meanwhile). It
// returns the previous domains.
func (s *Store) FinishDomainChange(ctx context.Context, name string, set DomainSet, certDomains []string, notAfter, now time.Time) (DomainSet, error) {
	tx, err := s.w.BeginTx(ctx, nil)
	if err != nil {
		return DomainSet{}, fmt.Errorf("store: %w", err)
	}

	defer func() { _ = tx.Rollback() }()

	c, err := scanDomainChange(tx.QueryRowContext(ctx, `SELECT `+domainChangeColumns+`
		FROM site_domain_changes c JOIN sites s ON s.id = c.site_id WHERE s.name = ?`, name))
	if err != nil {
		return DomainSet{}, err
	}

	if !sameStrings(c.Domains, set.Domains) || !sameStrings(c.Redirect, set.Redirect) {
		return DomainSet{}, ErrNotFound
	}

	var id int64
	if err := tx.QueryRowContext(ctx, `SELECT id FROM sites WHERE name = ?`, name).Scan(&id); err != nil {
		return DomainSet{}, fmt.Errorf("store: %w", err)
	}

	old, err := domainsOf(ctx, tx, id)
	if err != nil {
		return DomainSet{}, err
	}

	if _, err := tx.ExecContext(ctx, `DELETE FROM site_domain_changes WHERE site_id = ?`, id); err != nil {
		return DomainSet{}, fmt.Errorf("store: %w", err)
	}

	if _, err := tx.ExecContext(ctx, `DELETE FROM site_domains WHERE site_id = ?`, id); err != nil {
		return DomainSet{}, fmt.Errorf("store: %w", err)
	}

	if err := insertDomains(ctx, tx, id, set.Domains, set.Redirect); err != nil {
		return DomainSet{}, err
	}

	if _, err := tx.ExecContext(ctx, `UPDATE sites SET tls_domains = ?, tls_not_after = ?, tls_error = '', tls_failed_at = 0,
		tls_cert_source = ?, tls_trusted = 1, tls_renew_forced = 0, updated_at = ? WHERE id = ?`,
		strings.Join(certDomains, " "), notAfter.Unix(), TLSLetsEncrypt, now.Unix(), id); err != nil {
		return DomainSet{}, fmt.Errorf("store: %w", err)
	}

	if err := tx.Commit(); err != nil {
		return DomainSet{}, fmt.Errorf("store: %w", err)
	}

	return old, nil
}

func sameStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}

	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}

	return true
}
