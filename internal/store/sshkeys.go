// SPDX-License-Identifier: AGPL-3.0-or-later

package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"
)

// ErrSSHKeyExists means the site already has this public key.
var ErrSSHKeyExists = errors.New("store: the site already has this key")

// SSHKey is one public key of a site (ADR-0032).
type SSHKey struct {
	ID          int64
	SiteID      int64
	Label       string
	Access      string // "sftp" or "shell"
	Public      string // canonical authorized_keys form, no options or comment
	Fingerprint string // SHA256:...
	CreatedAt   time.Time
	UpdatedAt   time.Time
}

const sshKeyColumns = `id, site_id, label, access, public_key, fingerprint, created_at, updated_at`

func scanSSHKey(r scanner) (SSHKey, error) {
	var (
		k        SSHKey
		cre, upd int64
	)

	if err := r.Scan(&k.ID, &k.SiteID, &k.Label, &k.Access, &k.Public, &k.Fingerprint, &cre, &upd); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return SSHKey{}, ErrNotFound
		}

		return SSHKey{}, fmt.Errorf("store: %w", err)
	}

	k.CreatedAt, k.UpdatedAt = time.Unix(cre, 0).UTC(), time.Unix(upd, 0).UTC()

	return k, nil
}

// SSHKeys returns the keys of a site by id.
func (s *Store) SSHKeys(ctx context.Context, siteID int64) ([]SSHKey, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT `+sshKeyColumns+` FROM ssh_keys WHERE site_id = ? ORDER BY id`, siteID)
	if err != nil {
		return nil, fmt.Errorf("store: %w", err)
	}

	defer rows.Close()

	out := []SSHKey{}

	for rows.Next() {
		k, err := scanSSHKey(rows)
		if err != nil {
			return nil, err
		}

		out = append(out, k)
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("store: %w", err)
	}

	return out, nil
}

// SSHKey returns one key of a site or ErrNotFound.
func (s *Store) SSHKey(ctx context.Context, siteID, id int64) (SSHKey, error) {
	return scanSSHKey(s.db.QueryRowContext(ctx, `SELECT `+sshKeyColumns+` FROM ssh_keys WHERE site_id = ? AND id = ?`, siteID, id))
}

// InsertSSHKey adds a key to a site that has fewer than maxKeys keys.
func (s *Store) InsertSSHKey(ctx context.Context, k SSHKey, maxKeys int, now time.Time) (SSHKey, error) {
	tx, err := s.w.BeginTx(ctx, nil)
	if err != nil {
		return SSHKey{}, fmt.Errorf("store: %w", err)
	}

	defer func() { _ = tx.Rollback() }()

	var n int
	if err := tx.QueryRowContext(ctx, `SELECT count(*) FROM ssh_keys WHERE site_id = ?`, k.SiteID).Scan(&n); err != nil {
		return SSHKey{}, fmt.Errorf("store: %w", err)
	}

	if n >= maxKeys {
		return SSHKey{}, ErrTooMany
	}

	res, err := tx.ExecContext(ctx,
		`INSERT INTO ssh_keys (site_id, label, access, public_key, fingerprint, created_at, updated_at) VALUES (?, ?, ?, ?, ?, ?, ?)`,
		k.SiteID, k.Label, k.Access, k.Public, k.Fingerprint, now.Unix(), now.Unix())
	if isUnique(err) {
		return SSHKey{}, ErrSSHKeyExists
	}

	if err != nil {
		return SSHKey{}, fmt.Errorf("store: %w", err)
	}

	id, err := res.LastInsertId()
	if err != nil {
		return SSHKey{}, fmt.Errorf("store: %w", err)
	}

	out, err := scanSSHKey(tx.QueryRowContext(ctx, `SELECT `+sshKeyColumns+` FROM ssh_keys WHERE id = ?`, id))
	if err != nil {
		return SSHKey{}, err
	}

	if err := tx.Commit(); err != nil {
		return SSHKey{}, fmt.Errorf("store: %w", err)
	}

	return out, nil
}

// UpdateSSHKey changes a key's label and access and returns the key as it
// was. The public key itself never changes; a new key is a new entry.
func (s *Store) UpdateSSHKey(ctx context.Context, k SSHKey, now time.Time) (SSHKey, error) {
	tx, err := s.w.BeginTx(ctx, nil)
	if err != nil {
		return SSHKey{}, fmt.Errorf("store: %w", err)
	}

	defer func() { _ = tx.Rollback() }()

	old, err := scanSSHKey(tx.QueryRowContext(ctx, `SELECT `+sshKeyColumns+` FROM ssh_keys WHERE site_id = ? AND id = ?`, k.SiteID, k.ID))
	if err != nil {
		return SSHKey{}, err
	}

	if _, err := tx.ExecContext(ctx, `UPDATE ssh_keys SET label = ?, access = ?, updated_at = ? WHERE id = ?`,
		k.Label, k.Access, now.Unix(), k.ID); err != nil {
		return SSHKey{}, fmt.Errorf("store: %w", err)
	}

	if err := tx.Commit(); err != nil {
		return SSHKey{}, fmt.Errorf("store: %w", err)
	}

	return old, nil
}

// DeleteSSHKey removes a key and returns it.
func (s *Store) DeleteSSHKey(ctx context.Context, siteID, id int64) (SSHKey, error) {
	tx, err := s.w.BeginTx(ctx, nil)
	if err != nil {
		return SSHKey{}, fmt.Errorf("store: %w", err)
	}

	defer func() { _ = tx.Rollback() }()

	old, err := scanSSHKey(tx.QueryRowContext(ctx, `SELECT `+sshKeyColumns+` FROM ssh_keys WHERE site_id = ? AND id = ?`, siteID, id))
	if err != nil {
		return SSHKey{}, err
	}

	if _, err := tx.ExecContext(ctx, `DELETE FROM ssh_keys WHERE id = ?`, id); err != nil {
		return SSHKey{}, fmt.Errorf("store: %w", err)
	}

	if err := tx.Commit(); err != nil {
		return SSHKey{}, fmt.Errorf("store: %w", err)
	}

	return old, nil
}

// RestoreSSHKey puts a key back exactly as it was, with its id: after a
// change the agent refused.
func (s *Store) RestoreSSHKey(ctx context.Context, k SSHKey) error {
	_, err := s.w.ExecContext(ctx,
		`INSERT INTO ssh_keys (id, site_id, label, access, public_key, fingerprint, created_at, updated_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?)
		 ON CONFLICT (id) DO UPDATE SET label = excluded.label, access = excluded.access, updated_at = excluded.updated_at`,
		k.ID, k.SiteID, k.Label, k.Access, k.Public, k.Fingerprint, k.CreatedAt.Unix(), k.UpdatedAt.Unix())
	if err != nil {
		return fmt.Errorf("store: %w", err)
	}

	return nil
}
