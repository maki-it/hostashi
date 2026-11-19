// SPDX-License-Identifier: AGPL-3.0-or-later

// Package backup plans and runs the server's backups (ADR-0008): every
// site's home, a dump of every database and the panel's own state go into
// one restic repository per server, tagged so that each site and kind has
// its own snapshot history. restic itself is run by internal/resticctl.
package backup

import (
	"errors"
	"fmt"
	"regexp"
	"strconv"
	"strings"

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
)

// ErrInvalid wraps every settings error.
var ErrInvalid = errors.New("backup: invalid settings")

var (
	// Storage Box main and sub-accounts look like u123456 and u123456-sub1.
	sftpUserRe = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,31}$`)
	hostnameRe = regexp.MustCompile(`^([a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?\.)+[a-z]{2,63}$`)
	// A path below the login directory: segments of plain characters.
	remotePathRe = regexp.MustCompile(`^[A-Za-z0-9_-][A-Za-z0-9._-]{0,63}(/[A-Za-z0-9_-][A-Za-z0-9._-]{0,63}){0,7}$`)
)

// Target is the SFTP account the repository lives in, shaped for a Hetzner
// Storage Box (ADR-0008, recommendation 2). It is never a free-form restic
// URL.
type Target struct {
	User string `json:"user"`
	Host string `json:"host"`
	Port int    `json:"port"`
	Path string `json:"path"` // relative to the login directory
}

// Validate checks every field on its own.
func (t Target) Validate() error {
	switch {
	case !sftpUserRe.MatchString(t.User):
		return fmt.Errorf("%w: user", ErrInvalid)
	case len(t.Host) > 253 || !hostnameRe.MatchString(t.Host):
		return fmt.Errorf("%w: host must be a lowercase DNS name", ErrInvalid)
	case t.Port < 1 || t.Port > 65535:
		return fmt.Errorf("%w: port", ErrInvalid)
	case !remotePathRe.MatchString(t.Path) || strings.Contains(t.Path, ".."):
		return fmt.Errorf("%w: path must be relative without dot-dot segments", ErrInvalid)
	}

	return nil
}

// Repository is the restic repository URL of the target.
func (t Target) Repository() string {
	return "sftp://" + t.User + "@" + t.Host + ":" + strconv.Itoa(t.Port) + "/" + t.Path
}

// Retention is how many snapshots of each site and kind are kept.
type Retention struct {
	Daily   int `json:"daily"`
	Weekly  int `json:"weekly"`
	Monthly int `json:"monthly"`
}

// Bounds of the adjustable values (ADR-0008, recommendation 3).
const (
	MaxDaily     = 90
	MaxWeekly    = 52
	MaxMonthly   = 120
	MaxReadParts = 120
)

// Settings is the complete backup configuration of a server. The panel
// holds the desired state, the agent writes it to ConfigDir.
type Settings struct {
	Version   int       `json:"version"`
	Target    *Target   `json:"target,omitempty"` // nil: backups not set up
	Retention Retention `json:"retention"`
	// WindowStart and WindowEnd (hours, local time) bound the daily start;
	// the timer picks a random minute between them.
	WindowStart int `json:"windowStart"`
	WindowEnd   int `json:"windowEnd"`
	// ReadParts: the monthly check reads 1/ReadParts of all data, a
	// different part each month. 0 turns the data check off.
	ReadParts int `json:"readParts"`
}

// SettingsVersion is the current format of the settings file.
const SettingsVersion = 1

// DefaultSettings are the defaults Kim decided (2026-11-19).
func DefaultSettings() Settings {
	return Settings{
		Version:     SettingsVersion,
		Retention:   Retention{Daily: 7, Weekly: 4, Monthly: 6},
		WindowStart: 1,
		WindowEnd:   5,
		ReadParts:   12,
	}
}

// Validate checks the whole configuration.
func (s Settings) Validate() error {
	r := s.Retention

	switch {
	case s.Version != SettingsVersion:
		return fmt.Errorf("%w: version %d", ErrInvalid, s.Version)
	case r.Daily < 1 || r.Daily > MaxDaily:
		return fmt.Errorf("%w: daily must be 1 to %d", ErrInvalid, MaxDaily)
	case r.Weekly < 0 || r.Weekly > MaxWeekly:
		return fmt.Errorf("%w: weekly must be 0 to %d", ErrInvalid, MaxWeekly)
	case r.Monthly < 0 || r.Monthly > MaxMonthly:
		return fmt.Errorf("%w: monthly must be 0 to %d", ErrInvalid, MaxMonthly)
	case s.WindowStart < 0 || s.WindowStart > 23 || s.WindowEnd < 1 || s.WindowEnd > 24 || s.WindowEnd <= s.WindowStart:
		return fmt.Errorf("%w: window must be hours 0 to 24 with start before end", ErrInvalid)
	case s.ReadParts < 0 || s.ReadParts > MaxReadParts:
		return fmt.Errorf("%w: read parts must be 0 to %d", ErrInvalid, MaxReadParts)
	}

	if s.Target != nil {
		return s.Target.Validate()
	}

	return nil
}

func (r Retention) keep() resticctl.Keep {
	return resticctl.Keep{Daily: r.Daily, Weekly: r.Weekly, Monthly: r.Monthly}
}
