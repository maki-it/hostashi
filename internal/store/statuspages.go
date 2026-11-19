// SPDX-License-Identifier: AGPL-3.0-or-later

package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"
)

// ErrPageInUse means a site still names the page.
var ErrPageInUse = errors.New("store: a site uses this page")

// StatusPage is an uploaded maintenance or parking page (ADR-0027) without
// its file contents.
type StatusPage struct {
	ID        int64
	Name      string
	Kind      string
	Digest    string
	Files     []StatusPageFile // sorted by path
	Sites     []string         // sites that name the page, sorted
	Parked    []string         // parked domains that show the page, sorted
	CreatedAt time.Time
	UpdatedAt time.Time
}

// StatusPageFile is one file of a page.
type StatusPageFile struct {
	Path string
	Size int
}

// CreateStatusPage stores a new page with its files.
func (s *Store) CreateStatusPage(ctx context.Context, name, kind, digest string, files map[string][]byte, now time.Time) (int64, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return 0, fmt.Errorf("store: %w", err)
	}

	defer func() { _ = tx.Rollback() }()

	res, err := tx.ExecContext(ctx, `INSERT INTO status_pages (name, kind, digest, created_at, updated_at) VALUES (?, ?, ?, ?, ?)`,
		name, kind, digest, now.Unix(), now.Unix())
	if err != nil {
		return 0, fmt.Errorf("store: %w", err)
	}

	id, err := res.LastInsertId()
	if err != nil {
		return 0, fmt.Errorf("store: %w", err)
	}

	if err := insertPageFiles(ctx, tx, id, files); err != nil {
		return 0, err
	}

	if err := tx.Commit(); err != nil {
		return 0, fmt.Errorf("store: %w", err)
	}

	return id, nil
}

func insertPageFiles(ctx context.Context, tx *sql.Tx, id int64, files map[string][]byte) error {
	for p, b := range files {
		if b == nil {
			b = []byte{}
		}

		if _, err := tx.ExecContext(ctx, `INSERT INTO status_page_files (page_id, path, content) VALUES (?, ?, ?)`, id, p, b); err != nil {
			return fmt.Errorf("store: %w", err)
		}
	}

	return nil
}

// UpdateStatusPage renames a page (name != "") and replaces its files
// (files != nil). The kind does not change: sites rely on it.
func (s *Store) UpdateStatusPage(ctx context.Context, id int64, name, digest string, files map[string][]byte, now time.Time) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("store: %w", err)
	}

	defer func() { _ = tx.Rollback() }()

	res, err := tx.ExecContext(ctx, `UPDATE status_pages SET name = coalesce(nullif(?, ''), name), digest = coalesce(nullif(?, ''), digest), updated_at = ? WHERE id = ?`,
		name, digest, now.Unix(), id)
	if err != nil {
		return fmt.Errorf("store: %w", err)
	}

	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}

	if files != nil {
		if _, err := tx.ExecContext(ctx, `DELETE FROM status_page_files WHERE page_id = ?`, id); err != nil {
			return fmt.Errorf("store: %w", err)
		}

		if err := insertPageFiles(ctx, tx, id, files); err != nil {
			return err
		}
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("store: %w", err)
	}

	return nil
}

// DeleteStatusPage removes a page that no site names.
func (s *Store) DeleteStatusPage(ctx context.Context, id int64) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("store: %w", err)
	}

	defer func() { _ = tx.Rollback() }()

	var n int
	if err := tx.QueryRowContext(ctx, `SELECT (SELECT count(*) FROM sites WHERE maintenance_page = ?) + (SELECT count(*) FROM parked_domains WHERE page = ?)`, id, id).Scan(&n); err != nil {
		return fmt.Errorf("store: %w", err)
	}

	if n > 0 {
		return ErrPageInUse
	}

	res, err := tx.ExecContext(ctx, `DELETE FROM status_pages WHERE id = ?`, id)
	if err != nil {
		return fmt.Errorf("store: %w", err)
	}

	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("store: %w", err)
	}

	return nil
}

// StatusPages returns all uploaded pages sorted by name.
func (s *Store) StatusPages(ctx context.Context) ([]StatusPage, error) {
	return s.statusPages(ctx, `SELECT id, name, kind, digest, created_at, updated_at FROM status_pages ORDER BY name, id`)
}

// StatusPageByID returns one page or ErrNotFound.
func (s *Store) StatusPageByID(ctx context.Context, id int64) (StatusPage, error) {
	out, err := s.statusPages(ctx, `SELECT id, name, kind, digest, created_at, updated_at FROM status_pages WHERE id = ?`, id)
	if err != nil {
		return StatusPage{}, err
	}

	if len(out) == 0 {
		return StatusPage{}, ErrNotFound
	}

	return out[0], nil
}

func (s *Store) statusPages(ctx context.Context, query string, args ...any) ([]StatusPage, error) {
	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("store: %w", err)
	}

	var out []StatusPage

	for rows.Next() {
		var (
			p        StatusPage
			cre, upd int64
		)

		if err := rows.Scan(&p.ID, &p.Name, &p.Kind, &p.Digest, &cre, &upd); err != nil {
			_ = rows.Close()

			return nil, fmt.Errorf("store: %w", err)
		}

		p.CreatedAt, p.UpdatedAt = time.Unix(cre, 0).UTC(), time.Unix(upd, 0).UTC()
		out = append(out, p)
	}

	if err := rows.Err(); err != nil {
		_ = rows.Close()

		return nil, fmt.Errorf("store: %w", err)
	}

	_ = rows.Close()

	for i := range out {
		if err := s.pageDetails(ctx, &out[i]); err != nil {
			return nil, err
		}
	}

	return out, nil
}

func (s *Store) pageDetails(ctx context.Context, p *StatusPage) error {
	rows, err := s.db.QueryContext(ctx, `SELECT path, length(content) FROM status_page_files WHERE page_id = ? ORDER BY path`, p.ID)
	if err != nil {
		return fmt.Errorf("store: %w", err)
	}

	p.Files = []StatusPageFile{}

	for rows.Next() {
		var f StatusPageFile
		if err := rows.Scan(&f.Path, &f.Size); err != nil {
			_ = rows.Close()

			return fmt.Errorf("store: %w", err)
		}

		p.Files = append(p.Files, f)
	}

	if err := rows.Err(); err != nil {
		_ = rows.Close()

		return fmt.Errorf("store: %w", err)
	}

	_ = rows.Close()

	if p.Sites, err = s.names(ctx, `SELECT name FROM sites WHERE maintenance_page = ? ORDER BY name`, p.ID); err != nil {
		return err
	}

	p.Parked, err = s.names(ctx, `SELECT domain FROM parked_domains WHERE page = ? ORDER BY domain`, p.ID)

	return err
}

func (s *Store) names(ctx context.Context, query string, args ...any) ([]string, error) {
	rows, err := s.db.QueryContext(ctx, query, args...)
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

// StatusPageFiles returns the files of a page with their contents.
func (s *Store) StatusPageFiles(ctx context.Context, id int64) (map[string][]byte, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT path, content FROM status_page_files WHERE page_id = ?`, id)
	if err != nil {
		return nil, fmt.Errorf("store: %w", err)
	}

	defer rows.Close()

	out := map[string][]byte{}

	for rows.Next() {
		var (
			p string
			b []byte
		)

		if err := rows.Scan(&p, &b); err != nil {
			return nil, fmt.Errorf("store: %w", err)
		}

		out[p] = b
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("store: %w", err)
	}

	return out, nil
}

// SetSiteMaintenance stores the maintenance settings of a site. page 0 is
// the built-in page; allow must be canonical already.
func (s *Store) SetSiteMaintenance(ctx context.Context, name string, on bool, page int64, allow []string, now time.Time) error {
	v := 0
	if on {
		v = 1
	}

	var ref any
	if page != 0 {
		ref = page
	}

	allow = slices.Clone(allow)

	res, err := s.db.ExecContext(ctx, `UPDATE sites SET maintenance = ?, maintenance_page = ?, maintenance_allow = ?, updated_at = ? WHERE name = ?`,
		v, ref, strings.Join(allow, " "), now.Unix(), name)
	if err != nil {
		if strings.Contains(err.Error(), "FOREIGN KEY") {
			return ErrNotFound
		}

		return fmt.Errorf("store: %w", err)
	}

	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}

	return nil
}
