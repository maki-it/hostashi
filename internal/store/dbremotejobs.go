// SPDX-License-Identifier: AGPL-3.0-or-later

package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"
)

// DBRemoteJob is an import or export on an external server whose temporary
// user still exists there (ADR-0041 addendum 1).
type DBRemoteJob struct {
	JobID     string
	Database  string
	ServerID  int64
	User      string
	CreatedAt time.Time
}

// AddDBRemoteJob records a job and its temporary user.
func (s *Store) AddDBRemoteJob(ctx context.Context, j DBRemoteJob) error {
	_, err := s.w.ExecContext(ctx,
		`INSERT INTO db_remote_jobs (job_id, database, server_id, db_user, created_at) VALUES (?, ?, ?, ?, ?)`,
		j.JobID, j.Database, j.ServerID, j.User, j.CreatedAt.Unix())
	if err != nil {
		return fmt.Errorf("store: %w", err)
	}

	return nil
}

// DBRemoteJobs returns the recorded jobs, oldest first.
func (s *Store) DBRemoteJobs(ctx context.Context) ([]DBRemoteJob, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT job_id, database, server_id, db_user, created_at FROM db_remote_jobs ORDER BY created_at, job_id`)
	if err != nil {
		return nil, fmt.Errorf("store: %w", err)
	}

	defer rows.Close()

	out := []DBRemoteJob{}

	for rows.Next() {
		var (
			j       DBRemoteJob
			created int64
		)

		if err := rows.Scan(&j.JobID, &j.Database, &j.ServerID, &j.User, &created); err != nil {
			return nil, fmt.Errorf("store: %w", err)
		}

		j.CreatedAt = time.Unix(created, 0).UTC()
		out = append(out, j)
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("store: %w", err)
	}

	return out, nil
}

// DeleteDBRemoteJob forgets a job after its user was dropped. A missing
// row is not an error.
func (s *Store) DeleteDBRemoteJob(ctx context.Context, jobID string) error {
	if _, err := s.w.ExecContext(ctx, `DELETE FROM db_remote_jobs WHERE job_id = ?`, jobID); err != nil {
		return fmt.Errorf("store: %w", err)
	}

	return nil
}

// DBServerByID returns one server or ErrNotFound.
func (s *Store) DBServerByID(ctx context.Context, id int64) (DBServer, error) {
	d, err := scanDBServer(s.db.QueryRowContext(ctx, `SELECT `+dbServerCols+` FROM db_servers s WHERE s.id = ?`, id))
	if errors.Is(err, sql.ErrNoRows) {
		return DBServer{}, ErrNotFound
	}

	if err != nil {
		return DBServer{}, fmt.Errorf("store: %w", err)
	}

	return d, nil
}
