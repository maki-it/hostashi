// SPDX-License-Identifier: AGPL-3.0-or-later

package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"
)

// Errors of the database tables.
var (
	ErrDatabaseExists     = errors.New("store: a database with this name exists")
	ErrDatabaseUserExists = errors.New("store: a database user with this name exists")
)

// Database is a stored MariaDB database with its users, sorted by name.
type Database struct {
	ID        int64
	Name      string
	Site      string
	Users     []string
	CreatedAt time.Time
}

// CreateDatabase records a database for site. A missing site is ErrNotFound.
func (s *Store) CreateDatabase(ctx context.Context, site, name string, now time.Time) (Database, error) {
	tx, err := s.w.BeginTx(ctx, nil)
	if err != nil {
		return Database{}, fmt.Errorf("store: %w", err)
	}

	defer func() { _ = tx.Rollback() }()

	var siteID int64

	err = tx.QueryRowContext(ctx, `SELECT id FROM sites WHERE name = ?`, site).Scan(&siteID)
	if errors.Is(err, sql.ErrNoRows) {
		return Database{}, ErrNotFound
	}

	if err != nil {
		return Database{}, fmt.Errorf("store: %w", err)
	}

	var n int
	if err := tx.QueryRowContext(ctx, `SELECT count(*) FROM databases WHERE name = ?`, name).Scan(&n); err != nil {
		return Database{}, fmt.Errorf("store: %w", err)
	}

	if n > 0 {
		return Database{}, ErrDatabaseExists
	}

	res, err := tx.ExecContext(ctx, `INSERT INTO databases (name, site_id, created_at) VALUES (?, ?, ?)`, name, siteID, now.Unix())
	if err != nil {
		return Database{}, fmt.Errorf("store: %w", err)
	}

	id, err := res.LastInsertId()
	if err != nil {
		return Database{}, fmt.Errorf("store: %w", err)
	}

	if err := tx.Commit(); err != nil {
		return Database{}, fmt.Errorf("store: %w", err)
	}

	return Database{ID: id, Name: name, Site: site, Users: []string{}, CreatedAt: time.Unix(now.Unix(), 0).UTC()}, nil
}

// DeleteDatabase removes a database and its users. A missing one is not an error.
func (s *Store) DeleteDatabase(ctx context.Context, name string) error {
	if _, err := s.w.ExecContext(ctx, `DELETE FROM databases WHERE name = ?`, name); err != nil {
		return fmt.Errorf("store: %w", err)
	}

	return nil
}

// Databases lists databases sorted by name; site "" means all sites.
func (s *Store) Databases(ctx context.Context, site string) ([]Database, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT d.id, d.name, s.name, d.created_at FROM databases d JOIN sites s ON s.id = d.site_id
		WHERE ? = '' OR s.name = ? ORDER BY d.name`, site, site)
	if err != nil {
		return nil, fmt.Errorf("store: %w", err)
	}

	out := []Database{}

	for rows.Next() {
		var (
			d       Database
			created int64
		)

		if err := rows.Scan(&d.ID, &d.Name, &d.Site, &created); err != nil {
			_ = rows.Close()

			return nil, fmt.Errorf("store: %w", err)
		}

		d.CreatedAt = time.Unix(created, 0).UTC()
		out = append(out, d)
	}

	if err := rows.Close(); err != nil {
		return nil, fmt.Errorf("store: %w", err)
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("store: %w", err)
	}

	for i := range out {
		if out[i].Users, err = s.databaseUsers(ctx, out[i].ID); err != nil {
			return nil, err
		}
	}

	return out, nil
}

// DatabaseByName returns one database or ErrNotFound.
func (s *Store) DatabaseByName(ctx context.Context, name string) (Database, error) {
	var (
		d       Database
		created int64
	)

	err := s.db.QueryRowContext(ctx, `
		SELECT d.id, d.name, s.name, d.created_at FROM databases d JOIN sites s ON s.id = d.site_id
		WHERE d.name = ?`, name).Scan(&d.ID, &d.Name, &d.Site, &created)
	if errors.Is(err, sql.ErrNoRows) {
		return Database{}, ErrNotFound
	}

	if err != nil {
		return Database{}, fmt.Errorf("store: %w", err)
	}

	d.CreatedAt = time.Unix(created, 0).UTC()

	if d.Users, err = s.databaseUsers(ctx, d.ID); err != nil {
		return Database{}, err
	}

	return d, nil
}

func (s *Store) databaseUsers(ctx context.Context, dbID int64) ([]string, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT name FROM database_users WHERE database_id = ? ORDER BY name`, dbID)
	if err != nil {
		return nil, fmt.Errorf("store: %w", err)
	}

	defer rows.Close()

	out := []string{}

	for rows.Next() {
		var u string
		if err := rows.Scan(&u); err != nil {
			return nil, fmt.Errorf("store: %w", err)
		}

		out = append(out, u)
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("store: %w", err)
	}

	return out, nil
}

// CreateDatabaseUser records a user of database. A missing database is
// ErrNotFound.
func (s *Store) CreateDatabaseUser(ctx context.Context, database, user string, now time.Time) error {
	tx, err := s.w.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("store: %w", err)
	}

	defer func() { _ = tx.Rollback() }()

	var dbID int64

	err = tx.QueryRowContext(ctx, `SELECT id FROM databases WHERE name = ?`, database).Scan(&dbID)
	if errors.Is(err, sql.ErrNoRows) {
		return ErrNotFound
	}

	if err != nil {
		return fmt.Errorf("store: %w", err)
	}

	var n int
	if err := tx.QueryRowContext(ctx, `SELECT count(*) FROM database_users WHERE name = ?`, user).Scan(&n); err != nil {
		return fmt.Errorf("store: %w", err)
	}

	if n > 0 {
		return ErrDatabaseUserExists
	}

	if _, err := tx.ExecContext(ctx, `INSERT INTO database_users (name, database_id, created_at) VALUES (?, ?, ?)`,
		user, dbID, now.Unix()); err != nil {
		return fmt.Errorf("store: %w", err)
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("store: %w", err)
	}

	return nil
}

// DatabaseOfUser returns the database name of a user or ErrNotFound.
func (s *Store) DatabaseOfUser(ctx context.Context, user string) (string, error) {
	var name string

	err := s.db.QueryRowContext(ctx, `
		SELECT d.name FROM database_users u JOIN databases d ON d.id = u.database_id WHERE u.name = ?`, user).Scan(&name)
	if errors.Is(err, sql.ErrNoRows) {
		return "", ErrNotFound
	}

	if err != nil {
		return "", fmt.Errorf("store: %w", err)
	}

	return name, nil
}

// DeleteDatabaseUser removes a user. A missing one is not an error.
func (s *Store) DeleteDatabaseUser(ctx context.Context, user string) error {
	if _, err := s.w.ExecContext(ctx, `DELETE FROM database_users WHERE name = ?`, user); err != nil {
		return fmt.Errorf("store: %w", err)
	}

	return nil
}
