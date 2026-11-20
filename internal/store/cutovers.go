// SPDX-License-Identifier: AGPL-3.0-or-later

package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"
)

// Cut-over states (ADR-0031).
const (
	CutoverWaiting  = "waiting"
	CutoverSwitched = "switched"
	CutoverFailed   = "failed"
	CutoverCanceled = "canceled"
)

// Cutover is the DNS cut-over of one site. Answers is JSON, written and read
// by the caller. Zero times are unset.
type Cutover struct {
	Site      string
	State     string
	StartedBy *int64
	StartedAt time.Time
	Deadline  time.Time
	CheckedAt time.Time
	DoneAt    time.Time
	Answers   []byte
	Error     string
}

// ErrCutoverRunning is returned when a cut-over of the site still waits.
var ErrCutoverRunning = errors.New("store: a cut-over of this site is still waiting")

const cutoverCols = `s.name, c.state, c.started_by, c.started_at, c.deadline, c.checked_at, c.done_at, c.answers, c.error`

// StartCutover stores a new waiting cut-over for c.Site, replacing a finished
// one. A missing site is ErrNotFound, a waiting one ErrCutoverRunning.
func (s *Store) StartCutover(ctx context.Context, c Cutover) error {
	var id int64

	err := s.db.QueryRowContext(ctx, `SELECT id FROM sites WHERE name = ?`, c.Site).Scan(&id)
	if errors.Is(err, sql.ErrNoRows) {
		return ErrNotFound
	}

	if err != nil {
		return fmt.Errorf("store: %w", err)
	}

	res, err := s.w.ExecContext(ctx,
		`INSERT INTO site_cutovers (site_id, state, started_by, started_at, deadline) VALUES (?, 'waiting', ?, ?, ?)
		 ON CONFLICT (site_id) DO UPDATE SET state = 'waiting', started_by = excluded.started_by,
		   started_at = excluded.started_at, deadline = excluded.deadline, checked_at = 0, done_at = 0, answers = '[]', error = ''
		 WHERE site_cutovers.state != 'waiting'`,
		id, c.StartedBy, c.StartedAt.Unix(), c.Deadline.Unix())
	if err != nil {
		return fmt.Errorf("store: %w", err)
	}

	if n, err := res.RowsAffected(); err != nil {
		return fmt.Errorf("store: %w", err)
	} else if n == 0 {
		return ErrCutoverRunning
	}

	return nil
}

// CutoverBySite returns the cut-over of a site. None is ErrNotFound.
func (s *Store) CutoverBySite(ctx context.Context, site string) (Cutover, error) {
	row := s.db.QueryRowContext(ctx,
		`SELECT `+cutoverCols+` FROM site_cutovers c JOIN sites s ON s.id = c.site_id WHERE s.name = ?`, site)

	c, err := scanCutover(row)
	if errors.Is(err, sql.ErrNoRows) {
		return Cutover{}, ErrNotFound
	}

	return c, err
}

// WaitingCutovers lists the cut-overs that still wait, by site name.
func (s *Store) WaitingCutovers(ctx context.Context) ([]Cutover, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT `+cutoverCols+` FROM site_cutovers c JOIN sites s ON s.id = c.site_id WHERE c.state = 'waiting' ORDER BY s.name`)
	if err != nil {
		return nil, fmt.Errorf("store: %w", err)
	}

	defer func() { _ = rows.Close() }()

	var out []Cutover

	for rows.Next() {
		c, err := scanCutover(rows)
		if err != nil {
			return nil, err
		}

		out = append(out, c)
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("store: %w", err)
	}

	return out, nil
}

// UpdateCutover writes state, checked and done times, answers and error of a
// cut-over that still waits. It reports false when the cut-over no longer
// waits (canceled meanwhile) or does not exist.
func (s *Store) UpdateCutover(ctx context.Context, c Cutover) (bool, error) {
	answers := c.Answers
	if answers == nil {
		answers = []byte("[]")
	}

	res, err := s.w.ExecContext(ctx,
		`UPDATE site_cutovers SET state = ?, checked_at = ?, done_at = ?, answers = ?, error = ?
		 WHERE state = 'waiting' AND site_id = (SELECT id FROM sites WHERE name = ?)`,
		c.State, unixOrNull(c.CheckedAt), unixOrNull(c.DoneAt), string(answers), c.Error, c.Site)
	if err != nil {
		return false, fmt.Errorf("store: %w", err)
	}

	n, err := res.RowsAffected()
	if err != nil {
		return false, fmt.Errorf("store: %w", err)
	}

	return n == 1, nil
}

func scanCutover(r rowScanner) (Cutover, error) {
	var (
		c                                Cutover
		by                               sql.NullInt64
		started, deadline, checked, done int64
		answers                          string
	)

	err := r.Scan(&c.Site, &c.State, &by, &started, &deadline, &checked, &done, &answers, &c.Error)
	if errors.Is(err, sql.ErrNoRows) {
		return Cutover{}, err
	}

	if err != nil {
		return Cutover{}, fmt.Errorf("store: %w", err)
	}

	if by.Valid {
		c.StartedBy = &by.Int64
	}

	c.StartedAt, c.Deadline = time.Unix(started, 0).UTC(), time.Unix(deadline, 0).UTC()
	c.CheckedAt, c.DoneAt = unixOrZero(checked), unixOrZero(done)
	c.Answers = []byte(answers)

	return c, nil
}

// unixOrNull stores a zero time as 0.
func unixOrNull(t time.Time) int64 {
	if t.IsZero() {
		return 0
	}

	return t.Unix()
}
