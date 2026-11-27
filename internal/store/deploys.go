// SPDX-License-Identifier: AGPL-3.0-or-later

package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"
)

// The states, kinds and sources of a deploy run (ADR-0062).
const (
	DeployQueued    = "queued"
	DeployRunning   = "running"
	DeploySucceeded = "succeeded"
	DeployFailed    = "failed"

	DeployKindDeploy   = "deploy"
	DeployKindRollback = "rollback"

	DeploySourcePanel   = "panel"
	DeploySourceAPI     = "api"
	DeploySourceWebhook = "webhook"
)

// MaxDeployHistory is how many runs of a site the history keeps.
const MaxDeployHistory = 100

// ErrDeployBusy: a deploy of the site is queued or running.
var ErrDeployBusy = errors.New("store: a deploy of this site is running")

// SiteDeploy is one deploy run or rollback of a site.
type SiteDeploy struct {
	ID     int64
	SiteID int64
	// Release is the release directory below .deploy/releases: the run's
	// id for a deploy, the target for a rollback.
	Release string
	Kind    string
	Source  string
	Actor   int64 // 0: the webhook, or a deleted user
	Commit  string
	Author  string
	Message string
	State   string
	Error   string
	Created time.Time
	// Started and Finished are zero until then.
	Started, Finished time.Time
}

// Active reports whether the run is queued or running.
func (d SiteDeploy) Active() bool { return d.State == DeployQueued || d.State == DeployRunning }

const siteDeployColumns = `id, site_id, release_dir, kind, source, actor, commit_sha, author, message, state, error, created_at, started_at, finished_at`

func scanSiteDeploy(sc interface{ Scan(dest ...any) error }) (SiteDeploy, error) {
	var (
		d                 SiteDeploy
		actor             sql.NullInt64
		created           int64
		started, finished sql.NullInt64
	)

	if err := sc.Scan(&d.ID, &d.SiteID, &d.Release, &d.Kind, &d.Source, &actor, &d.Commit, &d.Author, &d.Message,
		&d.State, &d.Error, &created, &started, &finished); err != nil {
		return SiteDeploy{}, err
	}

	d.Actor = actor.Int64
	d.Created = time.Unix(created, 0).UTC()

	if started.Valid {
		d.Started = time.Unix(started.Int64, 0).UTC()
	}

	if finished.Valid {
		d.Finished = time.Unix(finished.Int64, 0).UTC()
	}

	return d, nil
}

// CreateSiteDeploy adds a queued run; a deploy's release is named by its
// id. While another run of the site is queued or running it is
// ErrDeployBusy.
func (s *Store) CreateSiteDeploy(ctx context.Context, d SiteDeploy, now time.Time) (SiteDeploy, error) {
	tx, err := s.w.BeginTx(ctx, nil)
	if err != nil {
		return SiteDeploy{}, fmt.Errorf("store: %w", err)
	}
	defer tx.Rollback() //nolint:errcheck // a committed transaction ignores it

	var busy int
	if err := tx.QueryRowContext(ctx, `SELECT count(*) FROM site_deploys WHERE site_id = ? AND state IN ('queued', 'running')`,
		d.SiteID).Scan(&busy); err != nil {
		return SiteDeploy{}, fmt.Errorf("store: %w", err)
	}

	if busy > 0 {
		return SiteDeploy{}, ErrDeployBusy
	}

	var actor any
	if d.Actor != 0 {
		actor = d.Actor
	}

	release := d.Release
	if d.Kind == DeployKindDeploy {
		release = "new"
	}

	res, err := tx.ExecContext(ctx, `INSERT INTO site_deploys (site_id, release_dir, kind, source, actor, commit_sha, author, message, state, created_at)
VALUES (?, ?, ?, ?, ?, ?, ?, ?, 'queued', ?)`,
		d.SiteID, release, d.Kind, d.Source, actor, d.Commit, d.Author, d.Message, now.Unix())
	if err != nil {
		return SiteDeploy{}, fmt.Errorf("store: %w", err)
	}

	id, err := res.LastInsertId()
	if err != nil {
		return SiteDeploy{}, fmt.Errorf("store: %w", err)
	}

	if d.Kind == DeployKindDeploy {
		release = strconv.FormatInt(id, 10)

		if _, err := tx.ExecContext(ctx, `UPDATE site_deploys SET release_dir = ? WHERE id = ?`, release, id); err != nil {
			return SiteDeploy{}, fmt.Errorf("store: %w", err)
		}
	}

	// The oldest runs beyond the history's size go.
	if _, err := tx.ExecContext(ctx, `DELETE FROM site_deploys WHERE site_id = ? AND id NOT IN
(SELECT id FROM site_deploys WHERE site_id = ? ORDER BY id DESC LIMIT ?)`, d.SiteID, d.SiteID, MaxDeployHistory); err != nil {
		return SiteDeploy{}, fmt.Errorf("store: %w", err)
	}

	if err := tx.Commit(); err != nil {
		return SiteDeploy{}, fmt.Errorf("store: %w", err)
	}

	d.ID, d.Release, d.State, d.Error = id, release, DeployQueued, ""
	d.Created, d.Started, d.Finished = now.UTC().Truncate(time.Second), time.Time{}, time.Time{}

	return d, nil
}

// StartSiteDeploy marks a queued run as running.
func (s *Store) StartSiteDeploy(ctx context.Context, id int64, now time.Time) error {
	return s.execOne(ctx, `UPDATE site_deploys SET state = 'running', started_at = ? WHERE id = ? AND state = 'queued'`, now.Unix(), id)
}

// FinishSiteDeploy ends a queued or running run: state is DeploySucceeded
// or DeployFailed. Empty commit fields keep the stored ones.
func (s *Store) FinishSiteDeploy(ctx context.Context, d SiteDeploy, now time.Time) error {
	if d.State != DeploySucceeded && d.State != DeployFailed {
		return fmt.Errorf("store: a run cannot finish as %q", d.State)
	}

	return s.execOne(ctx, `UPDATE site_deploys SET state = ?, error = ?, finished_at = ?,
commit_sha = CASE WHEN ? = '' THEN commit_sha ELSE ? END,
author = CASE WHEN ? = '' THEN author ELSE ? END,
message = CASE WHEN ? = '' THEN message ELSE ? END
WHERE id = ? AND state IN ('queued', 'running')`,
		d.State, clipText(d.Error, 4096), now.Unix(), d.Commit, d.Commit, d.Author, clipText(d.Author, 256), d.Message, clipText(d.Message, 1024), d.ID)
}

func (s *Store) execOne(ctx context.Context, q string, args ...any) error {
	res, err := s.w.ExecContext(ctx, q, args...)
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

// clipText cuts s to at most n bytes without splitting a rune.
func clipText(s string, n int) string {
	if len(s) <= n {
		return s
	}

	return strings.ToValidUTF8(s[:n], "")
}

// SiteDeploys lists the runs of a site, newest first, at most limit.
func (s *Store) SiteDeploys(ctx context.Context, siteID int64, limit int) ([]SiteDeploy, error) {
	return s.queryDeploys(ctx, `SELECT `+siteDeployColumns+` FROM site_deploys WHERE site_id = ? ORDER BY id DESC LIMIT ?`, siteID, limit)
}

// ActiveSiteDeploys lists the queued and running runs of every site.
func (s *Store) ActiveSiteDeploys(ctx context.Context) ([]SiteDeploy, error) {
	return s.queryDeploys(ctx, `SELECT `+siteDeployColumns+` FROM site_deploys WHERE state IN ('queued', 'running') ORDER BY id`)
}

func (s *Store) queryDeploys(ctx context.Context, q string, args ...any) ([]SiteDeploy, error) {
	rows, err := s.db.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, fmt.Errorf("store: %w", err)
	}
	defer rows.Close()

	out := []SiteDeploy{}

	for rows.Next() {
		d, err := scanSiteDeploy(rows)
		if err != nil {
			return nil, fmt.Errorf("store: %w", err)
		}

		out = append(out, d)
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("store: %w", err)
	}

	return out, nil
}

// SiteDeploy returns one run of a site, or ErrNotFound.
func (s *Store) SiteDeploy(ctx context.Context, siteID, id int64) (SiteDeploy, error) {
	d, err := scanSiteDeploy(s.db.QueryRowContext(ctx, `SELECT `+siteDeployColumns+` FROM site_deploys WHERE site_id = ? AND id = ?`, siteID, id))
	if errors.Is(err, sql.ErrNoRows) {
		return SiteDeploy{}, ErrNotFound
	}

	if err != nil {
		return SiteDeploy{}, fmt.Errorf("store: %w", err)
	}

	return d, nil
}

// KeptReleases lists the releases of the newest n successful deploys of a
// site, newest first.
func (s *Store) KeptReleases(ctx context.Context, siteID int64, n int) ([]string, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT release_dir FROM site_deploys
WHERE site_id = ? AND kind = 'deploy' AND state = 'succeeded' ORDER BY id DESC LIMIT ?`, siteID, n)
	if err != nil {
		return nil, fmt.Errorf("store: %w", err)
	}
	defer rows.Close()

	var out []string

	for rows.Next() {
		var r string
		if err := rows.Scan(&r); err != nil {
			return nil, fmt.Errorf("store: %w", err)
		}

		out = append(out, r)
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("store: %w", err)
	}

	return out, nil
}
