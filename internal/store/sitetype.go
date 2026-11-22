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

// SiteTypeChange is the type of a site with the settings that belong to it
// (ADR-0054 addendum 3).
type SiteTypeChange struct {
	Type string
	// PHP is the version of a php site; every other type stores "".
	PHP string
	// Account: the site has a Linux account. A type change never removes
	// one; the caller sets it when it created the account.
	Account bool
	// Redirect and Proxy are written for these types; nil leaves the
	// stored ones as they are.
	Redirect *SiteRedirect
	Proxy    *SiteProxy
	// App is the app of a node or app site. Port 0 keeps the site's port
	// or takes the lowest free one. Without an app the port is freed; the
	// other app settings stay stored.
	App *SiteApp
}

// SetSiteType changes the type of a site and its settings in one
// transaction and returns what was stored before, so a failed change can
// be undone with the result. The types' rules are the caller's; the
// table's constraints still apply.
func (s *Store) SetSiteType(ctx context.Context, name string, c SiteTypeChange, now time.Time) (SiteTypeChange, error) {
	tx, err := s.w.BeginTx(ctx, nil)
	if err != nil {
		return SiteTypeChange{}, fmt.Errorf("store: %w", err)
	}

	defer func() { _ = tx.Rollback() }()

	old, err := siteTypeOf(ctx, tx, name)
	if err != nil {
		return SiteTypeChange{}, err
	}

	var port any // NULL without an app

	if c.App != nil {
		p := c.App.Port

		switch {
		case p == 0 && old.App != nil:
			p = old.App.Port
		case p == 0:
			if p, err = freeAppPort(ctx, tx); err != nil {
				return SiteTypeChange{}, err
			}
		}

		port = p

		if _, err := tx.ExecContext(ctx, `UPDATE sites SET node_version = ?, app_workdir = ?, app_start_kind = ?, app_start = ?, app_args = ?, app_stopped = ? WHERE name = ?`,
			c.App.Node, c.App.Workdir, c.App.StartKind, c.App.Start, appArgs(c.App.Args), boolInt(c.App.Stopped), name); err != nil {
			return SiteTypeChange{}, fmt.Errorf("store: %w", err)
		}
	}

	if r := c.Redirect; r != nil {
		if _, err := tx.ExecContext(ctx, `UPDATE sites SET redirect_target = ?, redirect_status = ?, redirect_keep_path = ? WHERE name = ?`,
			r.Target, r.Status, boolInt(r.KeepPath), name); err != nil {
			return SiteTypeChange{}, fmt.Errorf("store: %w", err)
		}
	}

	if p := c.Proxy; p != nil {
		if _, err := tx.ExecContext(ctx, `UPDATE sites SET proxy_target = ?, proxy_target_host = ?, proxy_insecure = ? WHERE name = ?`,
			p.Target, boolInt(p.TargetHost), boolInt(p.Insecure), name); err != nil {
			return SiteTypeChange{}, fmt.Errorf("store: %w", err)
		}
	}

	if _, err := tx.ExecContext(ctx, `UPDATE sites SET type = ?, php = ?, account = ?, app_port = ?, updated_at = ? WHERE name = ?`,
		c.Type, c.PHP, boolInt(c.Account), port, now.Unix(), name); err != nil {
		return SiteTypeChange{}, fmt.Errorf("store: %w", err)
	}

	if err := tx.Commit(); err != nil {
		return SiteTypeChange{}, fmt.Errorf("store: %w", err)
	}

	return old, nil
}

// siteTypeOf reads the type of a site with its settings as SetSiteType
// writes them: redirect and proxy only for these types, the app only
// while the site has a port.
func siteTypeOf(ctx context.Context, tx *sql.Tx, name string) (SiteTypeChange, error) {
	var (
		c                    SiteTypeChange
		account, keepPath    int
		r                    SiteRedirect
		p                    SiteProxy
		targetHost, insecure int
		a                    SiteApp
		args                 string
		stopped              int
	)

	err := tx.QueryRowContext(ctx, `SELECT type, php, account, redirect_target, redirect_status, redirect_keep_path,
		proxy_target, proxy_target_host, proxy_insecure,
		node_version, app_workdir, app_start_kind, app_start, app_args, coalesce(app_port, 0), app_stopped FROM sites WHERE name = ?`, name).
		Scan(&c.Type, &c.PHP, &account, &r.Target, &r.Status, &keepPath, &p.Target, &targetHost, &insecure,
			&a.Node, &a.Workdir, &a.StartKind, &a.Start, &args, &a.Port, &stopped)
	if errors.Is(err, sql.ErrNoRows) {
		return SiteTypeChange{}, ErrNotFound
	}

	if err != nil {
		return SiteTypeChange{}, fmt.Errorf("store: %w", err)
	}

	c.Account = account == 1

	if c.Type == SiteTypeRedirect {
		r.KeepPath = keepPath == 1
		c.Redirect = &r
	}

	if c.Type == SiteTypeProxy {
		p.TargetHost, p.Insecure = targetHost == 1, insecure == 1
		c.Proxy = &p
	}

	if a.Port != 0 {
		a.Stopped = stopped == 1

		if err := json.Unmarshal([]byte(args), &a.Args); err != nil {
			return SiteTypeChange{}, fmt.Errorf("store: app arguments of %s: %w", name, err)
		}

		if len(a.Args) == 0 {
			a.Args = nil
		}

		c.App = &a
	}

	return c, nil
}
