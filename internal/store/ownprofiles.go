// SPDX-License-Identifier: AGPL-3.0-or-later

package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/maki-it/hostashi/internal/profile"
)

// Errors of the own profiles.
var (
	ErrProfileExists = errors.New("store: a profile with this name exists")
	ErrProfileInUse  = errors.New("store: sites use this profile")
)

// OwnProfile is an application profile an admin made (ADR-0069). Callers
// check it with profile.Check before they store it.
type OwnProfile struct {
	Profile   profile.Profile
	CreatedBy int64 // 0: unknown or deleted user
	UpdatedBy int64
	CreatedAt time.Time
	UpdatedAt time.Time
}

const ownProfileSelect = `SELECT name, data, created_by, updated_by, created_at, updated_at FROM profiles`

func scanOwnProfile(r scanner) (OwnProfile, error) {
	var (
		name, raw string
		cby, uby  sql.NullInt64
		cre, upd  int64
	)

	if err := r.Scan(&name, &raw, &cby, &uby, &cre, &upd); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return OwnProfile{}, ErrNotFound
		}

		return OwnProfile{}, fmt.Errorf("store: %w", err)
	}

	out := OwnProfile{CreatedBy: cby.Int64, UpdatedBy: uby.Int64, CreatedAt: time.Unix(cre, 0).UTC(), UpdatedAt: time.Unix(upd, 0).UTC()}
	if err := json.Unmarshal([]byte(raw), &out.Profile); err != nil {
		return OwnProfile{}, fmt.Errorf("store: profile %q: %w", name, err)
	}

	out.Profile.Name, out.Profile.Builtin = name, false

	return out, nil
}

// OwnProfiles returns the own profiles by name.
func (s *Store) OwnProfiles(ctx context.Context) ([]OwnProfile, error) {
	rows, err := s.db.QueryContext(ctx, ownProfileSelect+` ORDER BY name`)
	if err != nil {
		return nil, fmt.Errorf("store: %w", err)
	}

	defer rows.Close()

	out := []OwnProfile{}

	for rows.Next() {
		p, err := scanOwnProfile(rows)
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

// OwnProfile returns an own profile by name or ErrNotFound.
func (s *Store) OwnProfile(ctx context.Context, name string) (OwnProfile, error) {
	return scanOwnProfile(s.db.QueryRowContext(ctx, ownProfileSelect+` WHERE name = ?`, name))
}

func profileData(p profile.Profile) (string, error) {
	p.Builtin = false

	raw, err := json.Marshal(p)
	if err != nil {
		return "", fmt.Errorf("store: %w", err)
	}

	return string(raw), nil
}

// CreateOwnProfile adds an own profile; ErrProfileExists when the name is
// taken.
func (s *Store) CreateOwnProfile(ctx context.Context, p profile.Profile, by int64, now time.Time) (OwnProfile, error) {
	raw, err := profileData(p)
	if err != nil {
		return OwnProfile{}, err
	}

	_, err = s.w.ExecContext(ctx, `INSERT INTO profiles (name, data, created_by, updated_by, created_at, updated_at) VALUES (?, ?, ?, ?, ?, ?)`,
		p.Name, raw, nullID(by), nullID(by), now.Unix(), now.Unix())
	if isUnique(err) {
		return OwnProfile{}, ErrProfileExists
	}

	if err != nil {
		return OwnProfile{}, fmt.Errorf("store: %w", err)
	}

	return s.OwnProfile(ctx, p.Name)
}

// UpdateOwnProfile replaces the values of an own profile and returns it as
// it was. The name stays.
func (s *Store) UpdateOwnProfile(ctx context.Context, p profile.Profile, by int64, now time.Time) (OwnProfile, error) {
	raw, err := profileData(p)
	if err != nil {
		return OwnProfile{}, err
	}

	tx, err := s.w.BeginTx(ctx, nil)
	if err != nil {
		return OwnProfile{}, fmt.Errorf("store: %w", err)
	}

	defer func() { _ = tx.Rollback() }()

	old, err := scanOwnProfile(tx.QueryRowContext(ctx, ownProfileSelect+` WHERE name = ?`, p.Name))
	if err != nil {
		return OwnProfile{}, err
	}

	if _, err := tx.ExecContext(ctx, `UPDATE profiles SET data = ?, updated_by = ?, updated_at = ? WHERE name = ?`, raw, nullID(by), now.Unix(), p.Name); err != nil {
		return OwnProfile{}, fmt.Errorf("store: %w", err)
	}

	if err := tx.Commit(); err != nil {
		return OwnProfile{}, fmt.Errorf("store: %w", err)
	}

	return old, nil
}

// RestoreOwnProfile puts back a profile that UpdateOwnProfile returned.
func (s *Store) RestoreOwnProfile(ctx context.Context, old OwnProfile) error {
	raw, err := profileData(old.Profile)
	if err != nil {
		return err
	}

	if _, err := s.w.ExecContext(ctx, `UPDATE profiles SET data = ?, updated_by = ?, updated_at = ? WHERE name = ?`,
		raw, nullID(old.UpdatedBy), old.UpdatedAt.Unix(), old.Profile.Name); err != nil {
		return fmt.Errorf("store: %w", err)
	}

	return nil
}

// DeleteOwnProfile removes an own profile that no site uses and returns
// it; ErrProfileInUse while a site has it.
func (s *Store) DeleteOwnProfile(ctx context.Context, name string) (OwnProfile, error) {
	tx, err := s.w.BeginTx(ctx, nil)
	if err != nil {
		return OwnProfile{}, fmt.Errorf("store: %w", err)
	}

	defer func() { _ = tx.Rollback() }()

	old, err := scanOwnProfile(tx.QueryRowContext(ctx, ownProfileSelect+` WHERE name = ?`, name))
	if err != nil {
		return OwnProfile{}, err
	}

	var n int
	if err := tx.QueryRowContext(ctx, `SELECT count(*) FROM site_profiles WHERE profile = ?`, name).Scan(&n); err != nil {
		return OwnProfile{}, fmt.Errorf("store: %w", err)
	}

	if n > 0 {
		return OwnProfile{}, ErrProfileInUse
	}

	if _, err := tx.ExecContext(ctx, `DELETE FROM profiles WHERE name = ?`, name); err != nil {
		return OwnProfile{}, fmt.Errorf("store: %w", err)
	}

	if err := tx.Commit(); err != nil {
		return OwnProfile{}, fmt.Errorf("store: %w", err)
	}

	return old, nil
}

// ProfileSites returns the names of the sites that have a profile stored,
// by name.
func (s *Store) ProfileSites(ctx context.Context, name string) ([]string, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT s.name FROM site_profiles p JOIN sites s ON s.id = p.site_id WHERE p.profile = ? ORDER BY s.name`, name)
	if err != nil {
		return nil, fmt.Errorf("store: %w", err)
	}

	defer rows.Close()

	out := []string{}

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
