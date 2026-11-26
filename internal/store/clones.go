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

// States of a clone's copy (ADR-0070).
const (
	CloneCopying = "copying"
	CloneDone    = "done"
	CloneFailed  = "failed"
)

// States of a clone's configuration step after the copy.
const (
	CloneConfigNone    = ""
	CloneConfigRunning = "running"
	CloneConfigDone    = "done"
)

// MaxCloneError bounds the stored error text.
const MaxCloneError = 512

// ErrCloneExists: the site has a clone row already.
var ErrCloneExists = errors.New("store: the site is a clone already")

// CloneDatabase is one database pair of a clone: the source's database,
// the clone's, and the agent's copy job.
type CloneDatabase struct {
	Source string `json:"source"`
	Target string `json:"target"`
	// User is the clone's database user (the source's user name changed
	// like the database name).
	User string `json:"user,omitempty"`
	Job  string `json:"job,omitempty"`
	Done bool   `json:"done,omitempty"`
}

// SiteClone is the clone row of a site.
type SiteClone struct {
	SiteID int64
	// SourceID is the site it was copied from; 0 once that site is gone.
	SourceID  int64
	Staging   bool
	State     string
	Refresh   bool
	Error     string
	HomeJob   string
	HomeDone  bool
	Databases []CloneDatabase
	// Config is the configuration step: CloneConfigNone, ...Running or
	// ...Done.
	Config     string
	StartedBy  int64 // 0: unknown or deleted user
	StartedAt  time.Time
	FinishedAt time.Time // zero while copying
}

const cloneCols = `site_id, source_id, staging, state, refresh, error, home_job, home_done, databases, config, started_by, started_at, finished_at`

func scanClone(sc interface{ Scan(dest ...any) error }) (SiteClone, error) {
	var (
		c             SiteClone
		src, by       sql.NullInt64
		staging, ref  int
		homeDone      int
		raw           string
		started, fini int64
	)

	if err := sc.Scan(&c.SiteID, &src, &staging, &c.State, &ref, &c.Error, &c.HomeJob, &homeDone, &raw, &c.Config, &by, &started, &fini); err != nil {
		return SiteClone{}, err
	}

	c.SourceID, c.StartedBy = src.Int64, by.Int64
	c.Staging, c.Refresh, c.HomeDone = staging == 1, ref == 1, homeDone == 1
	c.StartedAt = time.Unix(started, 0).UTC()

	if fini != 0 {
		c.FinishedAt = time.Unix(fini, 0).UTC()
	}

	if err := json.Unmarshal([]byte(raw), &c.Databases); err != nil {
		return SiteClone{}, fmt.Errorf("store: clone databases of site %d: %w", c.SiteID, err)
	}

	return c, nil
}

// SiteClone returns the clone row of a site, or ErrNotFound.
func (s *Store) SiteClone(ctx context.Context, siteID int64) (SiteClone, error) {
	c, err := scanClone(s.db.QueryRowContext(ctx, `SELECT `+cloneCols+` FROM site_clones WHERE site_id = ?`, siteID))
	if errors.Is(err, sql.ErrNoRows) {
		return SiteClone{}, ErrNotFound
	}

	if err != nil {
		return SiteClone{}, fmt.Errorf("store: %w", err)
	}

	return c, nil
}

// SiteClones returns every clone row, of one source when sourceID > 0.
func (s *Store) SiteClones(ctx context.Context, sourceID int64) ([]SiteClone, error) {
	q, args := `SELECT `+cloneCols+` FROM site_clones ORDER BY site_id`, []any{}
	if sourceID > 0 {
		q, args = `SELECT `+cloneCols+` FROM site_clones WHERE source_id = ? ORDER BY site_id`, []any{sourceID}
	}

	rows, err := s.db.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, fmt.Errorf("store: %w", err)
	}

	defer rows.Close()

	var out []SiteClone

	for rows.Next() {
		c, err := scanClone(rows)
		if err != nil {
			return nil, fmt.Errorf("store: %w", err)
		}

		out = append(out, c)
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("store: %w", err)
	}

	return out, nil
}

// CreateSiteClone adds the clone row of a new site in state copying.
func (s *Store) CreateSiteClone(ctx context.Context, c SiteClone, now time.Time) error {
	raw, err := cloneDBJSON(c.Databases)
	if err != nil {
		return err
	}

	_, err = s.w.ExecContext(ctx, `INSERT INTO site_clones (site_id, source_id, staging, state, refresh, databases, started_by, started_at)
		VALUES (?, ?, ?, ?, 0, ?, ?, ?)`, c.SiteID, nullID(c.SourceID), boolInt(c.Staging), CloneCopying, raw, nullID(c.StartedBy), now.Unix())
	if isUnique(err) {
		return ErrCloneExists
	}

	if err != nil {
		return fmt.Errorf("store: %w", err)
	}

	return nil
}

// UpdateSiteClone writes the progress of a clone: its state, error, jobs,
// database pairs and configuration step. finished_at is set when the state leaves copying.
func (s *Store) UpdateSiteClone(ctx context.Context, c SiteClone, now time.Time) error {
	raw, err := cloneDBJSON(c.Databases)
	if err != nil {
		return err
	}

	if len(c.Error) > MaxCloneError {
		c.Error = c.Error[:MaxCloneError]
	}

	var fini int64
	if c.State != CloneCopying {
		fini = now.Unix()
	}

	res, err := s.w.ExecContext(ctx, `UPDATE site_clones SET state = ?, refresh = ?, error = ?, home_job = ?, home_done = ?, databases = ?, config = ?, finished_at = ? WHERE site_id = ?`,
		c.State, boolInt(c.Refresh), c.Error, c.HomeJob, boolInt(c.HomeDone), raw, c.Config, fini, c.SiteID)
	if err != nil {
		return fmt.Errorf("store: %w", err)
	}

	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}

	return nil
}

// StartCloneRefresh puts a finished clone back into copying for "refresh
// from source", unless a copy runs already. It returns ErrNotFound for a
// site without a clone row and ErrCloneBusy while one copies.
func (s *Store) StartCloneRefresh(ctx context.Context, siteID, by int64, dbs []CloneDatabase, now time.Time) error {
	raw, err := cloneDBJSON(dbs)
	if err != nil {
		return err
	}

	res, err := s.w.ExecContext(ctx, `UPDATE site_clones SET state = ?, refresh = 1, error = '', home_job = '', home_done = 0, databases = ?, config = '', started_by = ?, started_at = ?, finished_at = 0
		WHERE site_id = ? AND state != ?`, CloneCopying, raw, nullID(by), now.Unix(), siteID, CloneCopying)
	if err != nil {
		return fmt.Errorf("store: %w", err)
	}

	if n, _ := res.RowsAffected(); n == 1 {
		return nil
	}

	if _, err := s.SiteClone(ctx, siteID); err != nil {
		return err
	}

	return ErrCloneBusy
}

// ErrCloneBusy: a copy into the clone runs already.
var ErrCloneBusy = errors.New("store: the clone is being copied")

func cloneDBJSON(dbs []CloneDatabase) (string, error) {
	if dbs == nil {
		dbs = []CloneDatabase{}
	}

	raw, err := json.Marshal(dbs)
	if err != nil {
		return "", fmt.Errorf("store: %w", err)
	}

	return string(raw), nil
}

// CloneSettings says which settings CopySiteSettings takes over.
type CloneSettings struct {
	// PauseCron copies the cron jobs paused (the staging default).
	PauseCron bool
}

// CopySiteSettings copies the stored settings of site src to the new site
// dst in one transaction (ADR-0070): web options, custom snippets, limits,
// PHP settings, security headers, real IP, access rules (without the
// password users), the application profile, cron jobs and the grants of
// accounts with the role user. Not copied: domains, certificates, page
// cache, Git deploy, SSH keys, databases, the object cache (the caller
// turns it on through the agent, so the stored switch never claims a
// cache that is not running) and app environment variables (those are
// sealed to their site; the caller seals them again).
func (s *Store) CopySiteSettings(ctx context.Context, src, dst int64, o CloneSettings, now time.Time) error {
	tx, err := s.w.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("store: %w", err)
	}

	defer func() { _ = tx.Rollback() }()

	t := now.Unix()
	stmts := []struct {
		q    string
		args []any
	}{
		{`UPDATE sites SET (web_dir, spa, asset_cache, asset_cache_seconds, asset_cache_exts, php_programs, hsts) =
			(SELECT web_dir, spa, asset_cache, asset_cache_seconds, asset_cache_exts, php_programs, hsts FROM sites WHERE id = ?), updated_at = ? WHERE id = ?`, []any{src, t, dst}},
		{`INSERT OR REPLACE INTO site_snippets (site_id, server, root, updated_by, updated_at) SELECT ?, server, root, NULL, ? FROM site_snippets WHERE site_id = ?`, []any{dst, t, src}},
		{`INSERT OR REPLACE INTO site_limits (site_id, preset_id, disk_bytes, disk_inodes, memory_bytes, php_workers, tasks, cpu_priority, cpu_quota_percent, applied_at, apply_error, updated_at)
			SELECT ?, preset_id, disk_bytes, disk_inodes, memory_bytes, php_workers, tasks, cpu_priority, cpu_quota_percent, 0, '', ? FROM site_limits WHERE site_id = ?`, []any{dst, t, src}},
		{`INSERT OR REPLACE INTO site_php_settings (site_id, settings, updated_by, updated_at) SELECT ?, settings, NULL, ? FROM site_php_settings WHERE site_id = ?`, []any{dst, t, src}},
		{`INSERT OR REPLACE INTO site_headers (site_id, headers, updated_by, updated_at) SELECT ?, headers, NULL, ? FROM site_headers WHERE site_id = ?`, []any{dst, t, src}},
		{`INSERT OR REPLACE INTO site_realip (site_id, realip, updated_by, updated_at) SELECT ?, realip, NULL, ? FROM site_realip WHERE site_id = ?`, []any{dst, t, src}},
		{`INSERT OR REPLACE INTO site_access (site_id, settings, updated_by, updated_at) SELECT ?, settings, NULL, ? FROM site_access WHERE site_id = ?`, []any{dst, t, src}},
		{`INSERT OR REPLACE INTO site_profiles (site_id, profile, overrides, updated_by, updated_at) SELECT ?, profile, overrides, NULL, ? FROM site_profiles WHERE site_id = ?`, []any{dst, t, src}},
		{`INSERT INTO cron_jobs (site_id, schedule, command, timeout_sec, nice, paused, created_at, updated_at)
			SELECT ?, schedule, command, timeout_sec, nice, CASE WHEN ? THEN 1 ELSE paused END, ?, ? FROM cron_jobs WHERE site_id = ? ORDER BY id`, []any{dst, boolInt(o.PauseCron), t, t, src}},
		{`INSERT OR IGNORE INTO site_grants (user_id, site_id, created_at) SELECT user_id, ?, ? FROM site_grants WHERE site_id = ?`, []any{dst, t, src}},
	}

	for _, st := range stmts {
		if _, err := tx.ExecContext(ctx, st.q, st.args...); err != nil {
			return fmt.Errorf("store: copy site settings: %w", err)
		}
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("store: %w", err)
	}

	return nil
}
