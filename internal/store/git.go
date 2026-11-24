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

// SiteGit is the Git deploy setting of a site as stored (ADR-0062). Token,
// SSHKey and WebhookSecret are sealed by the caller.
type SiteGit struct {
	SiteID int64
	URL    string
	Branch string
	// DeployDir is relative to the home.
	DeployDir    string
	Shared       []string
	Commands     []string
	Keep         int
	BuildTimeout time.Duration
	Submodules   bool
	// Auth is one of agentapi.GitAuth*.
	Auth      string
	TokenUser string
	Token     []byte
	// SSHKey is the sealed private deploy key, SSHPublic its public line.
	SSHKey    []byte
	SSHPublic string
	// HostKey is the confirmed host key ("type base64"), empty until then.
	HostKey string
	// WebhookID is empty while the webhook is off.
	WebhookID     string
	WebhookSecret []byte
	UpdatedBy     int64 // 0: unknown or deleted user
	UpdatedAt     time.Time
}

const siteGitColumns = `site_id, url, branch, deploy_dir, shared, commands, keep_releases, build_timeout, submodules,
auth, token_user, token, ssh_key, ssh_public, host_key, webhook_id, webhook_secret, updated_by, updated_at`

// SiteGit returns the Git setting of a site, or ErrNotFound.
func (s *Store) SiteGit(ctx context.Context, siteID int64) (SiteGit, error) {
	var (
		g              SiteGit
		shared, cmds   string
		timeout, upd   int64
		sub            int64
		hookID         sql.NullString
		by             sql.NullInt64
		token, key, hs []byte
	)

	err := s.db.QueryRowContext(ctx, `SELECT `+siteGitColumns+` FROM site_git WHERE site_id = ?`, siteID).Scan(
		&g.SiteID, &g.URL, &g.Branch, &g.DeployDir, &shared, &cmds, &g.Keep, &timeout, &sub,
		&g.Auth, &g.TokenUser, &token, &key, &g.SSHPublic, &g.HostKey, &hookID, &hs, &by, &upd)
	if errors.Is(err, sql.ErrNoRows) {
		return SiteGit{}, ErrNotFound
	}

	if err != nil {
		return SiteGit{}, fmt.Errorf("store: %w", err)
	}

	if err := json.Unmarshal([]byte(shared), &g.Shared); err != nil {
		return SiteGit{}, fmt.Errorf("store: shared paths of site %d: %w", siteID, err)
	}

	if err := json.Unmarshal([]byte(cmds), &g.Commands); err != nil {
		return SiteGit{}, fmt.Errorf("store: build commands of site %d: %w", siteID, err)
	}

	g.BuildTimeout = time.Duration(timeout) * time.Second
	g.Submodules = sub == 1
	g.Token, g.SSHKey, g.WebhookSecret = token, key, hs
	g.WebhookID = hookID.String
	g.UpdatedBy = by.Int64
	g.UpdatedAt = time.Unix(upd, 0).UTC()

	return g, nil
}

// SetSiteGit stores the Git setting of a site, replacing an existing one.
// Callers validate first.
func (s *Store) SetSiteGit(ctx context.Context, g SiteGit, now time.Time) error {
	shared, err := json.Marshal(nonNil(g.Shared))
	if err != nil {
		return fmt.Errorf("store: %w", err)
	}

	cmds, err := json.Marshal(nonNil(g.Commands))
	if err != nil {
		return fmt.Errorf("store: %w", err)
	}

	var by, hookID any
	if g.UpdatedBy != 0 {
		by = g.UpdatedBy
	}

	if g.WebhookID != "" {
		hookID = g.WebhookID
	}

	sub := 0
	if g.Submodules {
		sub = 1
	}

	_, err = s.w.ExecContext(ctx, `INSERT INTO site_git (`+siteGitColumns+`)
VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
ON CONFLICT (site_id) DO UPDATE SET url = excluded.url, branch = excluded.branch, deploy_dir = excluded.deploy_dir,
shared = excluded.shared, commands = excluded.commands, keep_releases = excluded.keep_releases,
build_timeout = excluded.build_timeout, submodules = excluded.submodules, auth = excluded.auth,
token_user = excluded.token_user, token = excluded.token, ssh_key = excluded.ssh_key, ssh_public = excluded.ssh_public,
host_key = excluded.host_key, webhook_id = excluded.webhook_id, webhook_secret = excluded.webhook_secret,
updated_by = excluded.updated_by, updated_at = excluded.updated_at`,
		g.SiteID, g.URL, g.Branch, g.DeployDir, string(shared), string(cmds), g.Keep, int64(g.BuildTimeout/time.Second), sub,
		g.Auth, g.TokenUser, nilIfEmpty(g.Token), nilIfEmpty(g.SSHKey), g.SSHPublic, g.HostKey, hookID, nilIfEmpty(g.WebhookSecret),
		by, now.Unix())
	if err != nil {
		return fmt.Errorf("store: %w", err)
	}

	return nil
}

// DeleteSiteGit removes the Git setting of a site; the deploy history
// stays until the site is deleted. A site without a setting is no error.
func (s *Store) DeleteSiteGit(ctx context.Context, siteID int64) error {
	if _, err := s.w.ExecContext(ctx, `DELETE FROM site_git WHERE site_id = ?`, siteID); err != nil {
		return fmt.Errorf("store: %w", err)
	}

	return nil
}

func nilIfEmpty(b []byte) any {
	if len(b) == 0 {
		return nil
	}

	return b
}
