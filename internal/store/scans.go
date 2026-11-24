// SPDX-License-Identifier: AGPL-3.0-or-later

package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"
)

// The schedules, states and sources of the malware scan (ADR-0064).
const (
	ScanScheduleOff    = "off"
	ScanScheduleDaily  = "daily"
	ScanScheduleWeekly = "weekly"
	// ScanScheduleDefault applies to a site without a row.
	ScanScheduleDefault = ScanScheduleWeekly

	ScanQueued    = "queued"
	ScanRunning   = "running"
	ScanSucceeded = "succeeded"
	ScanFailed    = "failed"

	ScanSourcePanel    = "panel"
	ScanSourceAPI      = "api"
	ScanSourceSchedule = "schedule"

	FindingOpen        = "open"
	FindingIgnored     = "ignored"
	FindingQuarantined = "quarantined"
	FindingGone        = "gone"
	FindingRestored    = "restored"
)

// MaxScanHistory is how many runs of a site the history keeps.
const MaxScanHistory = 50

// ErrScanBusy: a scan of the site is queued or running.
var ErrScanBusy = errors.New("store: a scan of this site is queued or running")

// ScanRun is one malware scan of a site.
type ScanRun struct {
	ID       int64
	SiteID   int64
	Source   string
	Actor    int64 // 0: the schedule, or a deleted user
	State    string
	Error    string
	Found    int
	NewFound int
	Dropped  int
	Created  time.Time
	// Started and Finished are zero until then.
	Started, Finished time.Time
}

// Active reports whether the run is queued or running.
func (r ScanRun) Active() bool { return r.State == ScanQueued || r.State == ScanRunning }

// ScanFinding is one file ClamAV flagged.
type ScanFinding struct {
	ID           int64
	SiteID       int64
	Path         string
	Signature    string
	SHA256       string
	Size         int64
	State        string
	QuarantineID string
	LastRun      int64
	FirstSeen    time.Time
	LastSeen     time.Time
	DecidedBy    int64
	DecidedAt    time.Time
}

// SiteScanSchedule returns the schedule of a site, ScanScheduleDefault
// without a row.
func (s *Store) SiteScanSchedule(ctx context.Context, siteID int64) (string, error) {
	var v string

	err := s.db.QueryRowContext(ctx, `SELECT schedule FROM site_scan WHERE site_id = ?`, siteID).Scan(&v)
	if errors.Is(err, sql.ErrNoRows) {
		return ScanScheduleDefault, nil
	}

	if err != nil {
		return "", fmt.Errorf("store: %w", err)
	}

	return v, nil
}

// ScanSchedules returns the schedule of every site that has a row.
func (s *Store) ScanSchedules(ctx context.Context) (map[int64]string, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT site_id, schedule FROM site_scan`)
	if err != nil {
		return nil, fmt.Errorf("store: %w", err)
	}
	defer rows.Close()

	out := map[int64]string{}

	for rows.Next() {
		var (
			id int64
			v  string
		)

		if err := rows.Scan(&id, &v); err != nil {
			return nil, fmt.Errorf("store: %w", err)
		}

		out[id] = v
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("store: %w", err)
	}

	return out, nil
}

// SetSiteScanSchedule stores the schedule of a site.
func (s *Store) SetSiteScanSchedule(ctx context.Context, siteID int64, schedule string, by int64, now time.Time) error {
	switch schedule {
	case ScanScheduleOff, ScanScheduleDaily, ScanScheduleWeekly:
	default:
		return fmt.Errorf("store: unknown scan schedule %q", schedule)
	}

	var actor any
	if by != 0 {
		actor = by
	}

	if _, err := s.w.ExecContext(ctx, `INSERT INTO site_scan (site_id, schedule, updated_by, updated_at) VALUES (?, ?, ?, ?)
ON CONFLICT (site_id) DO UPDATE SET schedule = excluded.schedule, updated_by = excluded.updated_by, updated_at = excluded.updated_at`,
		siteID, schedule, actor, now.Unix()); err != nil {
		return fmt.Errorf("store: %w", err)
	}

	return nil
}

const scanRunColumns = `id, site_id, source, actor, state, error, found, new_found, dropped, created_at, started_at, finished_at`

func scanScanRun(sc interface{ Scan(dest ...any) error }) (ScanRun, error) {
	var (
		r                 ScanRun
		actor             sql.NullInt64
		created           int64
		started, finished sql.NullInt64
	)

	if err := sc.Scan(&r.ID, &r.SiteID, &r.Source, &actor, &r.State, &r.Error, &r.Found, &r.NewFound, &r.Dropped,
		&created, &started, &finished); err != nil {
		return ScanRun{}, err
	}

	r.Actor = actor.Int64
	r.Created = time.Unix(created, 0).UTC()

	if started.Valid {
		r.Started = time.Unix(started.Int64, 0).UTC()
	}

	if finished.Valid {
		r.Finished = time.Unix(finished.Int64, 0).UTC()
	}

	return r, nil
}

// CreateScanRun queues a scan of a site. While another scan of the site is
// queued or running it is ErrScanBusy.
func (s *Store) CreateScanRun(ctx context.Context, siteID int64, source string, actor int64, now time.Time) (ScanRun, error) {
	tx, err := s.w.BeginTx(ctx, nil)
	if err != nil {
		return ScanRun{}, fmt.Errorf("store: %w", err)
	}
	defer tx.Rollback() //nolint:errcheck // a committed transaction ignores it

	var busy int
	if err := tx.QueryRowContext(ctx, `SELECT count(*) FROM scan_runs WHERE site_id = ? AND state IN ('queued', 'running')`,
		siteID).Scan(&busy); err != nil {
		return ScanRun{}, fmt.Errorf("store: %w", err)
	}

	if busy > 0 {
		return ScanRun{}, ErrScanBusy
	}

	var by any
	if actor != 0 {
		by = actor
	}

	res, err := tx.ExecContext(ctx, `INSERT INTO scan_runs (site_id, source, actor, state, created_at) VALUES (?, ?, ?, 'queued', ?)`,
		siteID, source, by, now.Unix())
	if err != nil {
		return ScanRun{}, fmt.Errorf("store: %w", err)
	}

	id, err := res.LastInsertId()
	if err != nil {
		return ScanRun{}, fmt.Errorf("store: %w", err)
	}

	// The oldest runs beyond the history's size go; findings keep their
	// state, only their link to the run.
	if _, err := tx.ExecContext(ctx, `DELETE FROM scan_runs WHERE site_id = ? AND id NOT IN
(SELECT id FROM scan_runs WHERE site_id = ? ORDER BY id DESC LIMIT ?)`, siteID, siteID, MaxScanHistory); err != nil {
		return ScanRun{}, fmt.Errorf("store: %w", err)
	}

	if err := tx.Commit(); err != nil {
		return ScanRun{}, fmt.Errorf("store: %w", err)
	}

	return ScanRun{ID: id, SiteID: siteID, Source: source, Actor: actor, State: ScanQueued, Created: now.UTC().Truncate(time.Second)}, nil
}

// StartScanRun marks a queued run as running.
func (s *Store) StartScanRun(ctx context.Context, id int64, now time.Time) error {
	return s.execOne(ctx, `UPDATE scan_runs SET state = 'running', started_at = ? WHERE id = ? AND state = 'queued'`, now.Unix(), id)
}

// FailScanRun ends a queued or running run as failed.
func (s *Store) FailScanRun(ctx context.Context, id int64, msg string, now time.Time) error {
	return s.execOne(ctx, `UPDATE scan_runs SET state = 'failed', error = ?, finished_at = ? WHERE id = ? AND state IN ('queued', 'running')`,
		clipText(msg, 4096), now.Unix(), id)
}

// FoundFile is one file a finished run reports.
type FoundFile struct {
	Path, Signature, SHA256 string
	Size                    int64
}

// FinishScanRun ends a running run as succeeded and records its findings
// in one transaction: a file seen before keeps its row (and an ignored one
// stays ignored), a file that was quarantined, gone or restored and is
// there again is open again, a new file is open unless the same content
// with the same signature is ignored elsewhere in the site. Open findings
// this run did not see are gone. It returns the findings that are newly
// open.
func (s *Store) FinishScanRun(ctx context.Context, run ScanRun, files []FoundFile, now time.Time) ([]ScanFinding, error) {
	tx, err := s.w.BeginTx(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("store: %w", err)
	}
	defer tx.Rollback() //nolint:errcheck // a committed transaction ignores it

	var fresh []ScanFinding

	for _, f := range files {
		var (
			id    int64
			state string
		)

		err := tx.QueryRowContext(ctx, `SELECT id, state FROM scan_findings WHERE site_id = ? AND path = ? AND sha256 = ? AND signature = ?`,
			run.SiteID, f.Path, f.SHA256, f.Signature).Scan(&id, &state)

		switch {
		case errors.Is(err, sql.ErrNoRows):
			var ignored int
			if err := tx.QueryRowContext(ctx, `SELECT count(*) FROM scan_findings WHERE site_id = ? AND sha256 = ? AND signature = ? AND state = 'ignored'`,
				run.SiteID, f.SHA256, f.Signature).Scan(&ignored); err != nil {
				return nil, fmt.Errorf("store: %w", err)
			}

			state = FindingOpen
			if ignored > 0 {
				state = FindingIgnored
			}

			res, err := tx.ExecContext(ctx, `INSERT INTO scan_findings (site_id, path, signature, sha256, size, state, last_run, first_seen, last_seen)
VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`, run.SiteID, f.Path, f.Signature, f.SHA256, f.Size, state, run.ID, now.Unix(), now.Unix())
			if err != nil {
				return nil, fmt.Errorf("store: %w", err)
			}

			if id, err = res.LastInsertId(); err != nil {
				return nil, fmt.Errorf("store: %w", err)
			}

			if state == FindingOpen {
				fresh = append(fresh, ScanFinding{
					ID: id, SiteID: run.SiteID, Path: f.Path, Signature: f.Signature, SHA256: f.SHA256, Size: f.Size,
					State: FindingOpen, LastRun: run.ID, FirstSeen: now.UTC().Truncate(time.Second), LastSeen: now.UTC().Truncate(time.Second),
				})
			}
		case err != nil:
			return nil, fmt.Errorf("store: %w", err)
		default:
			next := state
			if state == FindingQuarantined || state == FindingGone || state == FindingRestored {
				next = FindingOpen
			}

			if _, err := tx.ExecContext(ctx, `UPDATE scan_findings SET state = ?, quarantine_id = NULL, size = ?, last_run = ?, last_seen = ? WHERE id = ?`,
				next, f.Size, run.ID, now.Unix(), id); err != nil {
				return nil, fmt.Errorf("store: %w", err)
			}

			if next == FindingOpen && state != FindingOpen {
				fresh = append(fresh, ScanFinding{
					ID: id, SiteID: run.SiteID, Path: f.Path, Signature: f.Signature, SHA256: f.SHA256, Size: f.Size,
					State: FindingOpen, LastRun: run.ID, LastSeen: now.UTC().Truncate(time.Second),
				})
			}
		}
	}

	if _, err := tx.ExecContext(ctx, `UPDATE scan_findings SET state = 'gone' WHERE site_id = ? AND state = 'open' AND (last_run IS NULL OR last_run <> ?)`,
		run.SiteID, run.ID); err != nil {
		return nil, fmt.Errorf("store: %w", err)
	}

	res, err := tx.ExecContext(ctx, `UPDATE scan_runs SET state = 'succeeded', error = ?, found = ?, new_found = ?, dropped = ?, finished_at = ?
WHERE id = ? AND state = 'running'`, clipText(run.Error, 4096), len(files), len(fresh), run.Dropped, now.Unix(), run.ID)
	if err != nil {
		return nil, fmt.Errorf("store: %w", err)
	}

	if n, _ := res.RowsAffected(); n == 0 {
		return nil, ErrNotFound
	}

	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("store: %w", err)
	}

	return fresh, nil
}

// ScanRuns lists the runs of a site, newest first, at most limit.
func (s *Store) ScanRuns(ctx context.Context, siteID int64, limit int) ([]ScanRun, error) {
	return s.queryScanRuns(ctx, `SELECT `+scanRunColumns+` FROM scan_runs WHERE site_id = ? ORDER BY id DESC LIMIT ?`, siteID, limit)
}

// ActiveScanRuns lists the queued and running runs of every site, oldest
// first.
func (s *Store) ActiveScanRuns(ctx context.Context) ([]ScanRun, error) {
	return s.queryScanRuns(ctx, `SELECT `+scanRunColumns+` FROM scan_runs WHERE state IN ('queued', 'running') ORDER BY id`)
}

// LastScanRuns returns the newest run of every site.
func (s *Store) LastScanRuns(ctx context.Context) (map[int64]ScanRun, error) {
	list, err := s.queryScanRuns(ctx, `SELECT `+scanRunColumns+` FROM scan_runs WHERE id IN (SELECT max(id) FROM scan_runs GROUP BY site_id)`)
	if err != nil {
		return nil, err
	}

	out := make(map[int64]ScanRun, len(list))
	for _, r := range list {
		out[r.SiteID] = r
	}

	return out, nil
}

func (s *Store) queryScanRuns(ctx context.Context, q string, args ...any) ([]ScanRun, error) {
	rows, err := s.db.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, fmt.Errorf("store: %w", err)
	}
	defer rows.Close()

	out := []ScanRun{}

	for rows.Next() {
		r, err := scanScanRun(rows)
		if err != nil {
			return nil, fmt.Errorf("store: %w", err)
		}

		out = append(out, r)
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("store: %w", err)
	}

	return out, nil
}

const scanFindingColumns = `id, site_id, path, signature, sha256, size, state, quarantine_id, last_run, first_seen, last_seen, decided_by, decided_at`

func scanScanFinding(sc interface{ Scan(dest ...any) error }) (ScanFinding, error) {
	var (
		f               ScanFinding
		qid             sql.NullString
		run, by, dec    sql.NullInt64
		first, lastSeen int64
	)

	if err := sc.Scan(&f.ID, &f.SiteID, &f.Path, &f.Signature, &f.SHA256, &f.Size, &f.State, &qid, &run, &first, &lastSeen, &by, &dec); err != nil {
		return ScanFinding{}, err
	}

	f.QuarantineID, f.LastRun, f.DecidedBy = qid.String, run.Int64, by.Int64
	f.FirstSeen, f.LastSeen = time.Unix(first, 0).UTC(), time.Unix(lastSeen, 0).UTC()

	if dec.Valid {
		f.DecidedAt = time.Unix(dec.Int64, 0).UTC()
	}

	return f, nil
}

// MaxScanFindingsList bounds a list of findings.
const MaxScanFindingsList = 1000

// ScanFindings lists the findings of a site: open ones first, then by
// when they were last seen.
func (s *Store) ScanFindings(ctx context.Context, siteID int64) ([]ScanFinding, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT `+scanFindingColumns+` FROM scan_findings WHERE site_id = ?
ORDER BY state <> 'open', last_seen DESC, id DESC LIMIT ?`, siteID, MaxScanFindingsList)
	if err != nil {
		return nil, fmt.Errorf("store: %w", err)
	}
	defer rows.Close()

	out := []ScanFinding{}

	for rows.Next() {
		f, err := scanScanFinding(rows)
		if err != nil {
			return nil, fmt.Errorf("store: %w", err)
		}

		out = append(out, f)
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("store: %w", err)
	}

	return out, nil
}

// ScanFinding returns one finding of a site, or ErrNotFound.
func (s *Store) ScanFinding(ctx context.Context, siteID, id int64) (ScanFinding, error) {
	f, err := scanScanFinding(s.db.QueryRowContext(ctx, `SELECT `+scanFindingColumns+` FROM scan_findings WHERE site_id = ? AND id = ?`, siteID, id))
	if errors.Is(err, sql.ErrNoRows) {
		return ScanFinding{}, ErrNotFound
	}

	if err != nil {
		return ScanFinding{}, fmt.Errorf("store: %w", err)
	}

	return f, nil
}

// OpenScanFindings counts the open findings per site.
func (s *Store) OpenScanFindings(ctx context.Context) (map[int64]int, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT site_id, count(*) FROM scan_findings WHERE state = 'open' GROUP BY site_id`)
	if err != nil {
		return nil, fmt.Errorf("store: %w", err)
	}
	defer rows.Close()

	out := map[int64]int{}

	for rows.Next() {
		var id, n int64
		if err := rows.Scan(&id, &n); err != nil {
			return nil, fmt.Errorf("store: %w", err)
		}

		out[id] = int(n)
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("store: %w", err)
	}

	return out, nil
}

// SetScanFindingState records a decision on a finding: from must be its
// current state. quarantineID is set for FindingQuarantined only.
func (s *Store) SetScanFindingState(ctx context.Context, siteID, id int64, from, to, quarantineID string, by int64, now time.Time) error {
	var qid, actor any
	if to == FindingQuarantined {
		qid = quarantineID
	}

	if by != 0 {
		actor = by
	}

	return s.execOne(ctx, `UPDATE scan_findings SET state = ?, quarantine_id = ?, decided_by = ?, decided_at = ?
WHERE site_id = ? AND id = ? AND state = ?`, to, qid, actor, now.Unix(), siteID, id, from)
}
