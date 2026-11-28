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

// ErrDNSAccountExists is returned for a label that another account has.
var ErrDNSAccountExists = errors.New("store: a DNS account with this label exists")

// DNSAccount is a DNS provider login for DNS-01 certificates (ADR-0076).
// The credentials stay sealed; only the caller with the server key opens
// them.
type DNSAccount struct {
	ID                int64
	Label             string
	Provider          string
	SealedCredentials []byte
	Zones             []string
	LastError         string
	CheckedAt         time.Time
	CreatedAt         time.Time
	UpdatedAt         time.Time
}

// NewDNSAccount is an account to add. Seal is called inside the
// transaction with the new row's id and returns the sealed credentials,
// so the purpose string can name the id.
type NewDNSAccount struct {
	Label    string
	Provider string
	Zones    []string
	Seal     func(id int64) ([]byte, error)
}

// DNSAccountUpdate changes an account. A nil Seal keeps the credentials.
type DNSAccountUpdate struct {
	Label string
	Zones []string
	Seal  func(id int64) ([]byte, error)
}

const dnsAccountCols = `id, label, provider, credentials, zones, last_error, checked_at, created_at, updated_at`

func scanDNSAccount(r rowScanner) (DNSAccount, error) {
	var (
		a                           DNSAccount
		zones                       string
		checked, created, updatedAt int64
	)

	if err := r.Scan(&a.ID, &a.Label, &a.Provider, &a.SealedCredentials, &zones, &a.LastError, &checked, &created, &updatedAt); err != nil {
		return DNSAccount{}, err
	}

	if err := json.Unmarshal([]byte(zones), &a.Zones); err != nil {
		return DNSAccount{}, fmt.Errorf("zones of DNS account %d: %w", a.ID, err)
	}

	if a.Zones == nil {
		a.Zones = []string{}
	}

	if checked != 0 {
		a.CheckedAt = time.Unix(checked, 0).UTC()
	}

	a.CreatedAt = time.Unix(created, 0).UTC()
	a.UpdatedAt = time.Unix(updatedAt, 0).UTC()

	return a, nil
}

func zonesJSON(z []string) (string, error) {
	if z == nil {
		z = []string{}
	}

	b, err := json.Marshal(z)
	if err != nil {
		return "", fmt.Errorf("store: %w", err)
	}

	return string(b), nil
}

func isUniqueLabel(err error) bool {
	return err != nil && strings.Contains(err.Error(), "UNIQUE constraint failed: dns_accounts.label")
}

// DNSAccounts lists all accounts by label.
func (s *Store) DNSAccounts(ctx context.Context) ([]DNSAccount, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT `+dnsAccountCols+` FROM dns_accounts ORDER BY label`)
	if err != nil {
		return nil, fmt.Errorf("store: %w", err)
	}

	defer rows.Close()

	out := []DNSAccount{}

	for rows.Next() {
		a, err := scanDNSAccount(rows)
		if err != nil {
			return nil, fmt.Errorf("store: %w", err)
		}

		out = append(out, a)
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("store: %w", err)
	}

	return out, nil
}

// DNSAccount returns one account or ErrNotFound.
func (s *Store) DNSAccount(ctx context.Context, id int64) (DNSAccount, error) {
	a, err := scanDNSAccount(s.db.QueryRowContext(ctx, `SELECT `+dnsAccountCols+` FROM dns_accounts WHERE id = ?`, id))
	if errors.Is(err, sql.ErrNoRows) {
		return DNSAccount{}, ErrNotFound
	}

	if err != nil {
		return DNSAccount{}, fmt.Errorf("store: %w", err)
	}

	return a, nil
}

// CreateDNSAccount adds an account.
func (s *Store) CreateDNSAccount(ctx context.Context, n NewDNSAccount, now time.Time) (DNSAccount, error) {
	if n.Seal == nil {
		return DNSAccount{}, errors.New("store: a DNS account needs sealed credentials")
	}

	zones, err := zonesJSON(n.Zones)
	if err != nil {
		return DNSAccount{}, err
	}

	tx, err := s.w.BeginTx(ctx, nil)
	if err != nil {
		return DNSAccount{}, fmt.Errorf("store: %w", err)
	}

	defer func() { _ = tx.Rollback() }()

	// The id is needed for the seal's purpose string: take it first with
	// empty credentials and replace them below.
	res, err := tx.ExecContext(ctx, `
		INSERT INTO dns_accounts (label, provider, credentials, zones, created_at, updated_at)
		VALUES (?, ?, x'', ?, ?, ?)`, n.Label, n.Provider, zones, now.Unix(), now.Unix())
	if isUniqueLabel(err) {
		return DNSAccount{}, ErrDNSAccountExists
	}

	if err != nil {
		return DNSAccount{}, fmt.Errorf("store: %w", err)
	}

	id, err := res.LastInsertId()
	if err != nil {
		return DNSAccount{}, fmt.Errorf("store: %w", err)
	}

	sealed, err := n.Seal(id)
	if err != nil {
		return DNSAccount{}, err
	}

	if _, err := tx.ExecContext(ctx, `UPDATE dns_accounts SET credentials = ? WHERE id = ?`, sealed, id); err != nil {
		return DNSAccount{}, fmt.Errorf("store: %w", err)
	}

	a, err := scanDNSAccount(tx.QueryRowContext(ctx, `SELECT `+dnsAccountCols+` FROM dns_accounts WHERE id = ?`, id))
	if err != nil {
		return DNSAccount{}, fmt.Errorf("store: %w", err)
	}

	if err := tx.Commit(); err != nil {
		return DNSAccount{}, fmt.Errorf("store: %w", err)
	}

	return a, nil
}

// UpdateDNSAccount changes label, zones and, with Seal, the credentials.
// New credentials clear the last check, which was about the old ones.
func (s *Store) UpdateDNSAccount(ctx context.Context, id int64, u DNSAccountUpdate, now time.Time) (DNSAccount, error) {
	zones, err := zonesJSON(u.Zones)
	if err != nil {
		return DNSAccount{}, err
	}

	tx, err := s.w.BeginTx(ctx, nil)
	if err != nil {
		return DNSAccount{}, fmt.Errorf("store: %w", err)
	}

	defer func() { _ = tx.Rollback() }()

	res, err := tx.ExecContext(ctx, `UPDATE dns_accounts SET label = ?, zones = ?, updated_at = ? WHERE id = ?`,
		u.Label, zones, now.Unix(), id)
	if isUniqueLabel(err) {
		return DNSAccount{}, ErrDNSAccountExists
	}

	if err := oneRow(res, err); err != nil {
		return DNSAccount{}, err
	}

	if u.Seal != nil {
		sealed, err := u.Seal(id)
		if err != nil {
			return DNSAccount{}, err
		}

		if _, err := tx.ExecContext(ctx, `UPDATE dns_accounts SET credentials = ?, last_error = '', checked_at = 0 WHERE id = ?`,
			sealed, id); err != nil {
			return DNSAccount{}, fmt.Errorf("store: %w", err)
		}
	}

	a, err := scanDNSAccount(tx.QueryRowContext(ctx, `SELECT `+dnsAccountCols+` FROM dns_accounts WHERE id = ?`, id))
	if err != nil {
		return DNSAccount{}, fmt.Errorf("store: %w", err)
	}

	if err := tx.Commit(); err != nil {
		return DNSAccount{}, fmt.Errorf("store: %w", err)
	}

	return a, nil
}

// SetDNSAccountCheck records an access check; lastError "" is a pass.
func (s *Store) SetDNSAccountCheck(ctx context.Context, id int64, lastError string, at time.Time) error {
	res, err := s.w.ExecContext(ctx, `UPDATE dns_accounts SET last_error = ?, checked_at = ? WHERE id = ?`, lastError, at.Unix(), id)

	return oneRow(res, err)
}

// DeleteDNSAccount removes an account.
func (s *Store) DeleteDNSAccount(ctx context.Context, id int64) error {
	res, err := s.w.ExecContext(ctx, `DELETE FROM dns_accounts WHERE id = ?`, id)

	return oneRow(res, err)
}
