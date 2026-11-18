// SPDX-License-Identifier: AGPL-3.0-or-later

// Package backup plans and runs the server's backups (ADR-0008): every
// site's home, a dump of every database and the panel's own state go into
// one restic repository per server, tagged so that each site and kind has
// its own snapshot history. restic itself is run by internal/resticctl.
package backup

import (
	"github.com/maki-it/hostashi/internal/agentapi"
	"github.com/maki-it/hostashi/internal/resticctl"
)

// Fixed locations on a server. ConfigDir holds the secrets (repository
// password, SSH key) and is never part of a backup; StatusDir holds the
// result of the last run for the agent to read.
const (
	ConfigDir = "/etc/hostashi-agent/backup"
	StatusDir = "/var/lib/hostashi-agent/backup"
	CacheDir  = "/var/cache/hostashi-backup"

	SettingsFile   = "settings.json"
	PasswordFile   = "password"
	SSHKeyFile     = "id_ed25519"
	KnownHostsFile = "known_hosts"
	StatusFile     = "status.json"
	SnapshotsFile  = "snapshots.json"
)

// The settings types live in agentapi, which the panel shares.
type (
	Settings  = agentapi.BackupSettings
	Target    = agentapi.BackupTarget
	Retention = agentapi.BackupRetention
)

// ErrInvalid wraps every settings error.
var ErrInvalid = agentapi.ErrBackupInvalid

// DefaultSettings are the defaults Kim decided (2026-11-19).
func DefaultSettings() Settings { return agentapi.DefaultBackupSettings() }

func keep(r Retention) resticctl.Keep {
	return resticctl.Keep{Daily: r.Daily, Weekly: r.Weekly, Monthly: r.Monthly}
}
