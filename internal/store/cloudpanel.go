// SPDX-License-Identifier: AGPL-3.0-or-later

package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"
)

// CloudPanelReport is an uploaded scan report. Sealed is the encrypted JSON.
type CloudPanelReport struct {
	ID        string
	Sites     int
	Sealed    []byte
	CreatedBy *int64
	CreatedAt time.Time
}

// CreateCloudPanelReport stores a sealed report.
func (s *Store) CreateCloudPanelReport(ctx context.Context, r CloudPanelReport) error {
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO cloudpanel_reports (id, sites, sealed, created_by, created_at) VALUES (?, ?, ?, ?, ?)`,
		r.ID, r.Sites, r.Sealed, r.CreatedBy, r.CreatedAt.Unix())
	if err != nil {
		return fmt.Errorf("store: %w", err)
	}

	return nil
}

// CloudPanelReportByID returns one report. A missing one is ErrNotFound.
func (s *Store) CloudPanelReportByID(ctx context.Context, id string) (CloudPanelReport, error) {
	var (
		r       CloudPanelReport
		created int64
	)

	err := s.db.QueryRowContext(ctx,
		`SELECT id, sites, sealed, created_by, created_at FROM cloudpanel_reports WHERE id = ?`, id).
		Scan(&r.ID, &r.Sites, &r.Sealed, &r.CreatedBy, &created)
	if errors.Is(err, sql.ErrNoRows) {
		return CloudPanelReport{}, ErrNotFound
	}

	if err != nil {
		return CloudPanelReport{}, fmt.Errorf("store: %w", err)
	}

	r.CreatedAt = time.Unix(created, 0).UTC()

	return r, nil
}

// CloudPanelReports lists the reports, newest first, without their content.
func (s *Store) CloudPanelReports(ctx context.Context) ([]CloudPanelReport, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT id, sites, created_by, created_at FROM cloudpanel_reports ORDER BY created_at DESC, id`)
	if err != nil {
		return nil, fmt.Errorf("store: %w", err)
	}

	defer func() { _ = rows.Close() }()

	out := []CloudPanelReport{}

	for rows.Next() {
		var (
			r       CloudPanelReport
			created int64
		)

		if err := rows.Scan(&r.ID, &r.Sites, &r.CreatedBy, &created); err != nil {
			return nil, fmt.Errorf("store: %w", err)
		}

		r.CreatedAt = time.Unix(created, 0).UTC()
		out = append(out, r)
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("store: %w", err)
	}

	return out, nil
}

// DeleteCloudPanelReport removes a report. A missing one is ErrNotFound.
func (s *Store) DeleteCloudPanelReport(ctx context.Context, id string) error {
	res, err := s.db.ExecContext(ctx, `DELETE FROM cloudpanel_reports WHERE id = ?`, id)
	if err != nil {
		return fmt.Errorf("store: %w", err)
	}

	if n, err := res.RowsAffected(); err != nil {
		return fmt.Errorf("store: %w", err)
	} else if n == 0 {
		return ErrNotFound
	}

	return nil
}
