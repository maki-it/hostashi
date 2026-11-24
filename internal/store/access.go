// SPDX-License-Identifier: AGPL-3.0-or-later

package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"
)

// AccessSettings are a site's IP rule and password switch (ADR-0066).
// Paths and Bypass are kept while Password is off, so switching it on
// again restores them. The zero value lets everybody in.
type AccessSettings struct {
	IPMode   string   `json:"ipMode,omitempty"`
	IPs      []string `json:"ips,omitempty"`
	Password bool     `json:"password,omitempty"`
	Paths    []string `json:"paths,omitempty"`
	Bypass   []string `json:"bypass,omitempty"`
}

// SiteAccess is the stored access setting of a site.
type SiteAccess struct {
	SiteID    int64
	Settings  AccessSettings
	UpdatedBy int64 // 0: unknown (deleted user)
	UpdatedAt time.Time
}

// AuthUser is one basic auth user of a site. Hash is the bcrypt hash; the
// password itself is never stored.
type AuthUser struct {
	SiteID    int64
	Name      string
	Hash      string
	CreatedAt time.Time
	UpdatedAt time.Time
	UpdatedBy int64
}

// ErrAuthUserExists is a user name that the site already has.
var ErrAuthUserExists = errors.New("store: user exists")

func scanAccess(siteID int64, raw string, by sql.NullInt64, upd int64) (SiteAccess, error) {
	out := SiteAccess{SiteID: siteID, UpdatedBy: by.Int64, UpdatedAt: time.Unix(upd, 0).UTC()}
	if err := json.Unmarshal([]byte(raw), &out.Settings); err != nil {
		return SiteAccess{}, fmt.Errorf("store: access setting of site %d: %w", siteID, err)
	}

	return out, nil
}

// SiteAccess returns the setting of a site; a site without one gets the
// zero value, not ErrNotFound.
func (s *Store) SiteAccess(ctx context.Context, siteID int64) (SiteAccess, error) {
	var (
		raw string
		by  sql.NullInt64
		upd int64
	)

	err := s.db.QueryRowContext(ctx, `SELECT settings, updated_by, updated_at FROM site_access WHERE site_id = ?`, siteID).Scan(&raw, &by, &upd)
	if errors.Is(err, sql.ErrNoRows) {
		return SiteAccess{SiteID: siteID}, nil
	}

	if err != nil {
		return SiteAccess{}, fmt.Errorf("store: %w", err)
	}

	return scanAccess(siteID, raw, by, upd)
}

// AllSiteAccess returns the setting of every site that has one, by site id.
func (s *Store) AllSiteAccess(ctx context.Context) (map[int64]SiteAccess, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT site_id, settings, updated_by, updated_at FROM site_access`)
	if err != nil {
		return nil, fmt.Errorf("store: %w", err)
	}

	defer rows.Close()

	out := map[int64]SiteAccess{}

	for rows.Next() {
		var (
			id  int64
			raw string
			by  sql.NullInt64
			upd int64
		)

		if err := rows.Scan(&id, &raw, &by, &upd); err != nil {
			return nil, fmt.Errorf("store: %w", err)
		}

		a, err := scanAccess(id, raw, by, upd)
		if err != nil {
			return nil, err
		}

		out[id] = a
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("store: %w", err)
	}

	return out, nil
}

// SetSiteAccess replaces the setting of a site and returns the previous
// one. An empty setting removes the row. Callers validate first.
func (s *Store) SetSiteAccess(ctx context.Context, a SiteAccess, now time.Time) (SiteAccess, error) {
	old, err := s.SiteAccess(ctx, a.SiteID)
	if err != nil {
		return SiteAccess{}, err
	}

	raw, err := json.Marshal(a.Settings)
	if err != nil {
		return SiteAccess{}, fmt.Errorf("store: %w", err)
	}

	if string(raw) == "{}" {
		_, err = s.w.ExecContext(ctx, `DELETE FROM site_access WHERE site_id = ?`, a.SiteID)
	} else {
		_, err = s.w.ExecContext(ctx, `INSERT INTO site_access (site_id, settings, updated_by, updated_at) VALUES (?, ?, ?, ?)
ON CONFLICT (site_id) DO UPDATE SET settings = excluded.settings, updated_by = excluded.updated_by, updated_at = excluded.updated_at`,
			a.SiteID, string(raw), nullID(a.UpdatedBy), now.Unix())
	}

	if err != nil {
		return SiteAccess{}, fmt.Errorf("store: %w", err)
	}

	return old, nil
}

func nullID(id int64) any {
	if id == 0 {
		return nil
	}

	return id
}

const authUserCols = `site_id, name, hash, created_at, updated_at, updated_by`

func scanAuthUser(sc interface{ Scan(...any) error }) (AuthUser, error) {
	var (
		u        AuthUser
		cre, upd int64
		by       sql.NullInt64
	)

	if err := sc.Scan(&u.SiteID, &u.Name, &u.Hash, &cre, &upd, &by); err != nil {
		return AuthUser{}, err
	}

	u.CreatedAt, u.UpdatedAt, u.UpdatedBy = time.Unix(cre, 0).UTC(), time.Unix(upd, 0).UTC(), by.Int64

	return u, nil
}

// AuthUsers returns the basic auth users of a site by name.
func (s *Store) AuthUsers(ctx context.Context, siteID int64) ([]AuthUser, error) {
	all, err := s.queryAuthUsers(ctx, `SELECT `+authUserCols+` FROM site_auth_users WHERE site_id = ? ORDER BY name`, siteID)
	if err != nil {
		return nil, err
	}

	return all[siteID], nil
}

// AllAuthUsers returns the basic auth users of every site, by site id and
// name.
func (s *Store) AllAuthUsers(ctx context.Context) (map[int64][]AuthUser, error) {
	return s.queryAuthUsers(ctx, `SELECT `+authUserCols+` FROM site_auth_users ORDER BY site_id, name`)
}

func (s *Store) queryAuthUsers(ctx context.Context, q string, args ...any) (map[int64][]AuthUser, error) {
	rows, err := s.db.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, fmt.Errorf("store: %w", err)
	}

	defer rows.Close()

	out := map[int64][]AuthUser{}

	for rows.Next() {
		u, err := scanAuthUser(rows)
		if err != nil {
			return nil, fmt.Errorf("store: %w", err)
		}

		out[u.SiteID] = append(out[u.SiteID], u)
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("store: %w", err)
	}

	return out, nil
}

// AddAuthUser adds a user; ErrAuthUserExists if the name is taken.
func (s *Store) AddAuthUser(ctx context.Context, u AuthUser, now time.Time) error {
	res, err := s.w.ExecContext(ctx, `INSERT INTO site_auth_users (`+authUserCols+`) VALUES (?, ?, ?, ?, ?, ?) ON CONFLICT DO NOTHING`,
		u.SiteID, u.Name, u.Hash, now.Unix(), now.Unix(), nullID(u.UpdatedBy))
	if err != nil {
		return fmt.Errorf("store: %w", err)
	}

	if n, _ := res.RowsAffected(); n == 0 {
		return ErrAuthUserExists
	}

	return nil
}

// SetAuthUserHash replaces a user's hash and returns the user as it was;
// ErrNotFound if there is no such user.
func (s *Store) SetAuthUserHash(ctx context.Context, siteID int64, name, hash string, by int64, now time.Time) (AuthUser, error) {
	old, err := scanAuthUser(s.db.QueryRowContext(ctx, `SELECT `+authUserCols+` FROM site_auth_users WHERE site_id = ? AND name = ?`, siteID, name))
	if errors.Is(err, sql.ErrNoRows) {
		return AuthUser{}, ErrNotFound
	} else if err != nil {
		return AuthUser{}, fmt.Errorf("store: %w", err)
	}

	if _, err := s.w.ExecContext(ctx, `UPDATE site_auth_users SET hash = ?, updated_at = ?, updated_by = ? WHERE site_id = ? AND name = ?`,
		hash, now.Unix(), nullID(by), siteID, name); err != nil {
		return AuthUser{}, fmt.Errorf("store: %w", err)
	}

	return old, nil
}

// RestoreAuthUser puts a user back as it was, after a failed change.
func (s *Store) RestoreAuthUser(ctx context.Context, u AuthUser) error {
	_, err := s.w.ExecContext(ctx, `INSERT INTO site_auth_users (`+authUserCols+`) VALUES (?, ?, ?, ?, ?, ?)
ON CONFLICT (site_id, name) DO UPDATE SET hash = excluded.hash, updated_at = excluded.updated_at, updated_by = excluded.updated_by`,
		u.SiteID, u.Name, u.Hash, u.CreatedAt.Unix(), u.UpdatedAt.Unix(), nullID(u.UpdatedBy))
	if err != nil {
		return fmt.Errorf("store: %w", err)
	}

	return nil
}

// DeleteAuthUser removes a user and returns it; ErrNotFound if there is
// no such user.
func (s *Store) DeleteAuthUser(ctx context.Context, siteID int64, name string) (AuthUser, error) {
	old, err := scanAuthUser(s.db.QueryRowContext(ctx, `SELECT `+authUserCols+` FROM site_auth_users WHERE site_id = ? AND name = ?`, siteID, name))
	if errors.Is(err, sql.ErrNoRows) {
		return AuthUser{}, ErrNotFound
	} else if err != nil {
		return AuthUser{}, fmt.Errorf("store: %w", err)
	}

	if _, err := s.w.ExecContext(ctx, `DELETE FROM site_auth_users WHERE site_id = ? AND name = ?`, siteID, name); err != nil {
		return AuthUser{}, fmt.Errorf("store: %w", err)
	}

	return old, nil
}
