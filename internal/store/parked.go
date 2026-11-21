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

// Parked is a parked domain (ADR-0027).
type Parked struct {
	ID     int64
	Domain string
	Page   int64 // 0: the built-in parking page
	TLS    bool  // HTTPS wanted
	// TLSLive: the last accepted web state serves the domain over HTTPS.
	TLSLive     bool
	TLSNotAfter time.Time // zero: no certificate
	TLSError    string
	TLSFailedAt time.Time
	// Certificate source and renewal requests as for sites (ADR-0039).
	TLSSource      string
	TLSCertSource  string
	TLSTrusted     bool
	TLSRenewAt     time.Time
	TLSRenewForced bool
	CreatedAt      time.Time
	UpdatedAt      time.Time
}

const parkedColumns = `id, domain, coalesce(page, 0), tls, tls_live, tls_not_after, tls_error, tls_failed_at,
	tls_source, tls_cert_source, tls_trusted, tls_renew_at, tls_renew_forced, created_at, updated_at`

func scanParked(r scanner) (Parked, error) {
	var (
		p                          Parked
		tls, live, trusted, forced int
		notAfter, failed, cre, upd int64
		renewAt                    int64
	)

	if err := r.Scan(&p.ID, &p.Domain, &p.Page, &tls, &live, &notAfter, &p.TLSError, &failed,
		&p.TLSSource, &p.TLSCertSource, &trusted, &renewAt, &forced, &cre, &upd); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return Parked{}, ErrNotFound
		}

		return Parked{}, fmt.Errorf("store: %w", err)
	}

	p.TLS, p.TLSLive = tls == 1, live == 1
	p.TLSNotAfter, p.TLSFailedAt = unixOrZero(notAfter), unixOrZero(failed)
	p.TLSTrusted, p.TLSRenewForced, p.TLSRenewAt = trusted == 1, forced == 1, unixOrZero(renewAt)
	p.CreatedAt, p.UpdatedAt = time.Unix(cre, 0).UTC(), time.Unix(upd, 0).UTC()

	return p, nil
}

// ParkedDomains returns all parked domains sorted by domain.
func (s *Store) ParkedDomains(ctx context.Context) ([]Parked, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT `+parkedColumns+` FROM parked_domains ORDER BY domain`)
	if err != nil {
		return nil, fmt.Errorf("store: %w", err)
	}

	defer rows.Close()

	var out []Parked

	for rows.Next() {
		p, err := scanParked(rows)
		if err != nil {
			return nil, err
		}

		out = append(out, p)
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("store: %w", err)
	}

	return out, nil
}

// ParkedDomain returns one parked domain or ErrNotFound.
func (s *Store) ParkedDomain(ctx context.Context, domain string) (Parked, error) {
	return scanParked(s.db.QueryRowContext(ctx, `SELECT `+parkedColumns+` FROM parked_domains WHERE domain = ?`, domain))
}

// PutParked parks a domain or changes its page and HTTPS setting. A domain
// of a site is refused with ErrDomainTaken; page 0 is the built-in page.
// It returns the previous state (ok false: it was not parked).
func (s *Store) PutParked(ctx context.Context, domain string, page int64, tls bool, now time.Time) (old Parked, ok bool, err error) {
	tx, err := s.w.BeginTx(ctx, nil)
	if err != nil {
		return Parked{}, false, fmt.Errorf("store: %w", err)
	}

	defer func() { _ = tx.Rollback() }()

	var n int
	if err := tx.QueryRowContext(ctx, `SELECT count(*) FROM site_domains WHERE domain = ?`, domain).Scan(&n); err != nil {
		return Parked{}, false, fmt.Errorf("store: %w", err)
	}

	if n > 0 {
		return Parked{}, false, fmt.Errorf("%w: %s", ErrDomainTaken, domain)
	}

	old, err = scanParked(tx.QueryRowContext(ctx, `SELECT `+parkedColumns+` FROM parked_domains WHERE domain = ?`, domain))
	switch {
	case err == nil:
		ok = true
	case !errors.Is(err, ErrNotFound):
		return Parked{}, false, err
	}

	var ref any
	if page != 0 {
		ref = page
	}

	v := 0
	if tls {
		v = 1
	}

	if _, err := tx.ExecContext(ctx, `INSERT INTO parked_domains (domain, page, tls, created_at, updated_at) VALUES (?, ?, ?, ?, ?)
		ON CONFLICT (domain) DO UPDATE SET page = excluded.page, tls = excluded.tls, updated_at = excluded.updated_at`,
		domain, ref, v, now.Unix(), now.Unix()); err != nil {
		if strings.Contains(err.Error(), "FOREIGN KEY") {
			return Parked{}, false, ErrNotFound
		}

		return Parked{}, false, fmt.Errorf("store: %w", err)
	}

	if err := tx.Commit(); err != nil {
		return Parked{}, false, fmt.Errorf("store: %w", err)
	}

	return old, ok, nil
}

// RestoreParked puts back the state PutParked returned, or removes the
// domain when it was not parked before.
func (s *Store) RestoreParked(ctx context.Context, domain string, old Parked, ok bool, now time.Time) error {
	if !ok {
		return s.DeleteParked(ctx, domain)
	}

	_, _, err := s.PutParked(ctx, domain, old.Page, old.TLS, now)

	return err
}

// DeleteParked removes a parked domain. A missing one is ErrNotFound.
func (s *Store) DeleteParked(ctx context.Context, domain string) error {
	res, err := s.w.ExecContext(ctx, `DELETE FROM parked_domains WHERE domain = ?`, domain)
	if err != nil {
		return fmt.Errorf("store: %w", err)
	}

	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}

	return nil
}

// SetParkedCert records the live Let's Encrypt certificate of a parked
// domain.
func (s *Store) SetParkedCert(ctx context.Context, id int64, notAfter, now time.Time) error {
	_, err := s.w.ExecContext(ctx, `UPDATE parked_domains SET tls_not_after = ?, tls_error = '', tls_failed_at = 0,
		tls_cert_source = 'letsencrypt', tls_trusted = 1, tls_renew_forced = 0, updated_at = ? WHERE id = ?`,
		notAfter.Unix(), now.Unix(), id)
	if err != nil {
		return fmt.Errorf("store: %w", err)
	}

	return nil
}

// SetParkedTLSError records a failed attempt to get a certificate.
func (s *Store) SetParkedTLSError(ctx context.Context, id int64, msg string, at time.Time) error {
	if len(msg) > 1000 {
		msg = strings.ToValidUTF8(msg[:1000], "")
	}

	if _, err := s.w.ExecContext(ctx, `UPDATE parked_domains SET tls_error = ?, tls_failed_at = ?, tls_renew_forced = 0 WHERE id = ?`,
		msg, at.Unix(), id); err != nil {
		return fmt.Errorf("store: %w", err)
	}

	return nil
}

// SetParkedTLSLive records which parked domains the agent serves over
// HTTPS now; all others are not.
func (s *Store) SetParkedTLSLive(ctx context.Context, ids []int64) error {
	b, err := json.Marshal(ids)
	if err != nil {
		return fmt.Errorf("store: %w", err)
	}

	if _, err := s.w.ExecContext(ctx, `UPDATE parked_domains SET tls_live = (id IN (SELECT value FROM json_each(?)))`, string(b)); err != nil {
		return fmt.Errorf("store: %w", err)
	}

	return nil
}

// SetParkedCustomCert records an own certificate that is now live for a
// parked domain: the source becomes custom and HTTPS is wanted.
func (s *Store) SetParkedCustomCert(ctx context.Context, id int64, notAfter time.Time, trusted bool, now time.Time) error {
	t := 0
	if trusted {
		t = 1
	}

	res, err := s.w.ExecContext(ctx,
		`UPDATE parked_domains SET tls = 1, tls_source = 'custom', tls_cert_source = 'custom', tls_not_after = ?, tls_trusted = ?,
			tls_error = '', tls_failed_at = 0, tls_renew_forced = 0, updated_at = ? WHERE id = ?`,
		notAfter.Unix(), t, now.Unix(), id)
	if err != nil {
		return fmt.Errorf("store: %w", err)
	}

	if n, _ := res.RowsAffected(); n != 1 {
		return ErrNotFound
	}

	return nil
}

// SetParkedTLSSource sets where the certificate of a parked domain should
// come from. The live certificate stays.
func (s *Store) SetParkedTLSSource(ctx context.Context, id int64, source string, now time.Time) error {
	if source != TLSLetsEncrypt && source != TLSCustom {
		return fmt.Errorf("store: unknown certificate source %q", source)
	}

	res, err := s.w.ExecContext(ctx, `UPDATE parked_domains SET tls_source = ?, tls_error = '', tls_failed_at = 0, updated_at = ? WHERE id = ?`,
		source, now.Unix(), id)
	if err != nil {
		return fmt.Errorf("store: %w", err)
	}

	if n, _ := res.RowsAffected(); n != 1 {
		return ErrNotFound
	}

	return nil
}

// ClearParkedCert forgets the certificate of a parked domain after it was
// deleted; the source goes back to Let's Encrypt.
func (s *Store) ClearParkedCert(ctx context.Context, id int64, now time.Time) error {
	res, err := s.w.ExecContext(ctx,
		`UPDATE parked_domains SET tls_not_after = 0, tls_cert_source = '', tls_trusted = 1, tls_source = 'letsencrypt',
			tls_renew_forced = 0, tls_error = '', tls_failed_at = 0, updated_at = ? WHERE id = ?`, now.Unix(), id)
	if err != nil {
		return fmt.Errorf("store: %w", err)
	}

	if n, _ := res.RowsAffected(); n != 1 {
		return ErrNotFound
	}

	return nil
}

// RequestParkedRenew asks for a new certificate of a parked domain, at
// most once per gap (ErrTooSoon).
func (s *Store) RequestParkedRenew(ctx context.Context, id int64, gap time.Duration, now time.Time) error {
	res, err := s.w.ExecContext(ctx,
		`UPDATE parked_domains SET tls_renew_at = ?, tls_renew_forced = 1, tls_error = '', tls_failed_at = 0 WHERE id = ? AND tls_renew_at <= ?`,
		now.Unix(), id, now.Add(-gap).Unix())
	if err != nil {
		return fmt.Errorf("store: %w", err)
	}

	if n, _ := res.RowsAffected(); n == 1 {
		return nil
	}

	var one int
	if err := s.db.QueryRowContext(ctx, `SELECT 1 FROM parked_domains WHERE id = ?`, id).Scan(&one); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return ErrNotFound
		}

		return fmt.Errorf("store: %w", err)
	}

	return ErrTooSoon
}
