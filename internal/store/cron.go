// SPDX-License-Identifier: AGPL-3.0-or-later

package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"
)

// ErrTooMany means a site has the most cron jobs it may have.
var ErrTooMany = errors.New("store: too many entries")

// CronJob is one cron job of a site (ADR-0028).
type CronJob struct {
	ID         int64
	SiteID     int64
	Schedule   string
	Command    string
	TimeoutSec int64 // 0: the default
	Nice       *int  // nil: the default
	Paused     bool
	CreatedAt  time.Time
	UpdatedAt  time.Time
}

const cronColumns = `id, site_id, schedule, command, timeout_sec, nice, paused, created_at, updated_at`

func scanCronJob(r scanner) (CronJob, error) {
	var (
		j        CronJob
		nice     sql.NullInt64
		paused   int
		cre, upd int64
	)

	if err := r.Scan(&j.ID, &j.SiteID, &j.Schedule, &j.Command, &j.TimeoutSec, &nice, &paused, &cre, &upd); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return CronJob{}, ErrNotFound
		}

		return CronJob{}, fmt.Errorf("store: %w", err)
	}

	if nice.Valid {
		n := int(nice.Int64)
		j.Nice = &n
	}

	j.Paused = paused == 1
	j.CreatedAt, j.UpdatedAt = time.Unix(cre, 0).UTC(), time.Unix(upd, 0).UTC()

	return j, nil
}

func niceArg(n *int) any {
	if n == nil {
		return nil
	}

	return *n
}

func boolInt(b bool) int {
	if b {
		return 1
	}

	return 0
}

// CronJobs returns the cron jobs of a site by id.
func (s *Store) CronJobs(ctx context.Context, siteID int64) ([]CronJob, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT `+cronColumns+` FROM cron_jobs WHERE site_id = ? ORDER BY id`, siteID)
	if err != nil {
		return nil, fmt.Errorf("store: %w", err)
	}

	defer rows.Close()

	out := []CronJob{}

	for rows.Next() {
		j, err := scanCronJob(rows)
		if err != nil {
			return nil, err
		}

		out = append(out, j)
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("store: %w", err)
	}

	return out, nil
}

// CronJob returns one job of a site or ErrNotFound.
func (s *Store) CronJob(ctx context.Context, siteID, id int64) (CronJob, error) {
	return scanCronJob(s.db.QueryRowContext(ctx, `SELECT `+cronColumns+` FROM cron_jobs WHERE site_id = ? AND id = ?`, siteID, id))
}

// InsertCronJob adds a job to a site that has fewer than max jobs.
func (s *Store) InsertCronJob(ctx context.Context, j CronJob, maxJobs int, now time.Time) (CronJob, error) {
	tx, err := s.w.BeginTx(ctx, nil)
	if err != nil {
		return CronJob{}, fmt.Errorf("store: %w", err)
	}

	defer func() { _ = tx.Rollback() }()

	var n int
	if err := tx.QueryRowContext(ctx, `SELECT count(*) FROM cron_jobs WHERE site_id = ?`, j.SiteID).Scan(&n); err != nil {
		return CronJob{}, fmt.Errorf("store: %w", err)
	}

	if n >= maxJobs {
		return CronJob{}, ErrTooMany
	}

	res, err := tx.ExecContext(ctx,
		`INSERT INTO cron_jobs (site_id, schedule, command, timeout_sec, nice, paused, created_at, updated_at) VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
		j.SiteID, j.Schedule, j.Command, j.TimeoutSec, niceArg(j.Nice), boolInt(j.Paused), now.Unix(), now.Unix())
	if err != nil {
		return CronJob{}, fmt.Errorf("store: %w", err)
	}

	id, err := res.LastInsertId()
	if err != nil {
		return CronJob{}, fmt.Errorf("store: %w", err)
	}

	out, err := scanCronJob(tx.QueryRowContext(ctx, `SELECT `+cronColumns+` FROM cron_jobs WHERE id = ?`, id))
	if err != nil {
		return CronJob{}, err
	}

	if err := tx.Commit(); err != nil {
		return CronJob{}, fmt.Errorf("store: %w", err)
	}

	return out, nil
}

// UpdateCronJob changes a job's schedule, command, timeout, niceness and
// paused flag, and returns the job as it was.
func (s *Store) UpdateCronJob(ctx context.Context, j CronJob, now time.Time) (CronJob, error) {
	tx, err := s.w.BeginTx(ctx, nil)
	if err != nil {
		return CronJob{}, fmt.Errorf("store: %w", err)
	}

	defer func() { _ = tx.Rollback() }()

	old, err := scanCronJob(tx.QueryRowContext(ctx, `SELECT `+cronColumns+` FROM cron_jobs WHERE site_id = ? AND id = ?`, j.SiteID, j.ID))
	if err != nil {
		return CronJob{}, err
	}

	if _, err := tx.ExecContext(ctx,
		`UPDATE cron_jobs SET schedule = ?, command = ?, timeout_sec = ?, nice = ?, paused = ?, updated_at = ? WHERE id = ?`,
		j.Schedule, j.Command, j.TimeoutSec, niceArg(j.Nice), boolInt(j.Paused), now.Unix(), j.ID); err != nil {
		return CronJob{}, fmt.Errorf("store: %w", err)
	}

	if err := tx.Commit(); err != nil {
		return CronJob{}, fmt.Errorf("store: %w", err)
	}

	return old, nil
}

// DeleteCronJob removes a job and returns it.
func (s *Store) DeleteCronJob(ctx context.Context, siteID, id int64) (CronJob, error) {
	tx, err := s.w.BeginTx(ctx, nil)
	if err != nil {
		return CronJob{}, fmt.Errorf("store: %w", err)
	}

	defer func() { _ = tx.Rollback() }()

	old, err := scanCronJob(tx.QueryRowContext(ctx, `SELECT `+cronColumns+` FROM cron_jobs WHERE site_id = ? AND id = ?`, siteID, id))
	if err != nil {
		return CronJob{}, err
	}

	if _, err := tx.ExecContext(ctx, `DELETE FROM cron_jobs WHERE id = ?`, id); err != nil {
		return CronJob{}, fmt.Errorf("store: %w", err)
	}

	if err := tx.Commit(); err != nil {
		return CronJob{}, fmt.Errorf("store: %w", err)
	}

	return old, nil
}

// RestoreCronJob puts a job back exactly as it was, with its id: after a
// change the agent refused.
func (s *Store) RestoreCronJob(ctx context.Context, j CronJob) error {
	_, err := s.w.ExecContext(ctx,
		`INSERT INTO cron_jobs (id, site_id, schedule, command, timeout_sec, nice, paused, created_at, updated_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)
		 ON CONFLICT (id) DO UPDATE SET schedule = excluded.schedule, command = excluded.command,
		   timeout_sec = excluded.timeout_sec, nice = excluded.nice, paused = excluded.paused, updated_at = excluded.updated_at`,
		j.ID, j.SiteID, j.Schedule, j.Command, j.TimeoutSec, niceArg(j.Nice), boolInt(j.Paused), j.CreatedAt.Unix(), j.UpdatedAt.Unix())
	if err != nil {
		return fmt.Errorf("store: %w", err)
	}

	return nil
}
