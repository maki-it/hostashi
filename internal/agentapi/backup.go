// SPDX-License-Identifier: AGPL-3.0-or-later

package agentapi

import (
	"errors"
	"fmt"
	"net"
	"net/netip"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/maki-it/hostashi/internal/agentrpc"
)

// ErrBackupInvalid wraps every backup settings error.
var ErrBackupInvalid = errors.New("backup: invalid settings")

var (
	// An SFTP login name: letters, digits, dot, underscore and dash, as
	// Linux, NAS and hosting accounts use them (u123456-sub1, backup_web3,
	// first.last). Never a leading dash, never @, : or /.
	sftpUserRe = regexp.MustCompile(`^[A-Za-z0-9_][A-Za-z0-9._-]{0,63}$`)
	hostnameRe = regexp.MustCompile(`^([a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?\.)+[a-z]{2,63}$`)
	// A path relative to the login directory, or absolute with a leading
	// slash: segments of plain characters.
	remotePathRe = regexp.MustCompile(`^/?[A-Za-z0-9_-][A-Za-z0-9._-]{0,63}(/[A-Za-z0-9_-][A-Za-z0-9._-]{0,63}){0,7}$`)
)

// BackupTarget is the SFTP account the repository lives in: any SSH server
// with SFTP and key login, for example a Hetzner Storage Box, a NAS or
// another Linux server (ADR-0008, recommendation 2). It is never a
// free-form restic URL.
type BackupTarget struct {
	User string `json:"user"`
	Host string `json:"host"` // lowercase DNS name or IP address
	Port int    `json:"port"`
	Path string `json:"path"` // relative to the login directory, or absolute
}

// backupHost accepts a lowercase DNS name or a unicast IP address that is
// not loopback or unspecified.
func backupHost(h string) bool {
	if len(h) <= 253 && hostnameRe.MatchString(h) {
		return true
	}

	a, err := netip.ParseAddr(h)
	if err != nil || a.Zone() != "" || a.String() != h {
		return false
	}

	return !a.IsLoopback() && !a.IsUnspecified() && !a.IsMulticast() && !a.Is4In6()
}

// Validate checks every field on its own.
func (t BackupTarget) Validate() error {
	switch {
	case !sftpUserRe.MatchString(t.User):
		return fmt.Errorf("%w: user", ErrBackupInvalid)
	case !backupHost(t.Host):
		return fmt.Errorf("%w: host must be a lowercase DNS name or an IP address", ErrBackupInvalid)
	case t.Port < 1 || t.Port > 65535:
		return fmt.Errorf("%w: port", ErrBackupInvalid)
	case !remotePathRe.MatchString(t.Path) || strings.Contains(t.Path, ".."):
		return fmt.Errorf("%w: path must be relative without dot-dot segments", ErrBackupInvalid)
	}

	return nil
}

// Repository is the restic repository URL of the target. restic drops the
// first slash after the port, so an absolute path is written with two.
func (t BackupTarget) Repository() string {
	return "sftp://" + t.User + "@" + net.JoinHostPort(t.Host, strconv.Itoa(t.Port)) + "/" + t.Path
}

// KnownHostsName is how ssh names the target in known_hosts.
func (t BackupTarget) KnownHostsName() string {
	if t.Port == 22 {
		return t.Host
	}

	return "[" + t.Host + "]:" + strconv.Itoa(t.Port)
}

// BackupRetention is how many snapshots of each site and kind are kept.
type BackupRetention struct {
	Daily   int `json:"daily"`
	Weekly  int `json:"weekly"`
	Monthly int `json:"monthly"`
}

// Bounds of the adjustable values (ADR-0008, recommendation 3).
const (
	MaxBackupDaily     = 90
	MaxBackupWeekly    = 52
	MaxBackupMonthly   = 120
	MaxBackupReadParts = 120
)

// BackupSettings is the complete backup configuration of a server. The
// panel holds the desired state, the agent writes it to its config
// directory.
type BackupSettings struct {
	Version   int             `json:"version"`
	Target    *BackupTarget   `json:"target,omitempty"` // nil: backups not set up
	Retention BackupRetention `json:"retention"`
	// WindowStart and WindowEnd (hours, local time) bound the daily start;
	// the timer picks a random minute between them.
	WindowStart int `json:"windowStart"`
	WindowEnd   int `json:"windowEnd"`
	// ReadParts: the monthly check reads 1/ReadParts of all data, a
	// different part each month. 0 turns the data check off.
	ReadParts int `json:"readParts"`
}

// BackupSettingsVersion is the current format of the settings.
const BackupSettingsVersion = 1

// DefaultBackupSettings are the defaults Kim decided (2026-11-19).
func DefaultBackupSettings() BackupSettings {
	return BackupSettings{
		Version:     BackupSettingsVersion,
		Retention:   BackupRetention{Daily: 7, Weekly: 4, Monthly: 6},
		WindowStart: 1,
		WindowEnd:   5,
		ReadParts:   12,
	}
}

// Validate checks the whole configuration.
func (s BackupSettings) Validate() error {
	r := s.Retention

	switch {
	case s.Version != BackupSettingsVersion:
		return fmt.Errorf("%w: version %d", ErrBackupInvalid, s.Version)
	case r.Daily < 1 || r.Daily > MaxBackupDaily:
		return fmt.Errorf("%w: daily must be 1 to %d", ErrBackupInvalid, MaxBackupDaily)
	case r.Weekly < 0 || r.Weekly > MaxBackupWeekly:
		return fmt.Errorf("%w: weekly must be 0 to %d", ErrBackupInvalid, MaxBackupWeekly)
	case r.Monthly < 0 || r.Monthly > MaxBackupMonthly:
		return fmt.Errorf("%w: monthly must be 0 to %d", ErrBackupInvalid, MaxBackupMonthly)
	case s.WindowStart < 0 || s.WindowStart > 23 || s.WindowEnd < 1 || s.WindowEnd > 24 || s.WindowEnd <= s.WindowStart:
		return fmt.Errorf("%w: window must be hours 0 to 24 with start before end", ErrBackupInvalid)
	case s.ReadParts < 0 || s.ReadParts > MaxBackupReadParts:
		return fmt.Errorf("%w: read parts must be 0 to %d", ErrBackupInvalid, MaxBackupReadParts)
	}

	if s.Target != nil {
		return s.Target.Validate()
	}

	return nil
}

// MaxHostKey bounds the host key line of backup.hostkey.set.
const MaxHostKey = 4096

// BackupSetRequest replaces the backup settings. Changing the target's
// host or port drops the confirmed host key.
type BackupSetRequest struct {
	Settings BackupSettings `json:"settings"`
}

// Validate checks the settings.
func (r BackupSetRequest) Validate() error { return r.Settings.Validate() }

// BackupHostKeyRequest pins the target's SSH host key after the
// administrator confirmed its fingerprint. Key is one public key in
// authorized_keys form ("ssh-ed25519 AAAA...").
type BackupHostKeyRequest struct {
	Key string `json:"key"`
}

// Validate checks the size; the agent parses the key itself.
func (r BackupHostKeyRequest) Validate() error {
	if r.Key == "" || len(r.Key) > MaxHostKey {
		return fmt.Errorf("key: between 1 and %d bytes", MaxHostKey)
	}

	return nil
}

// BackupGetRequest has no parameters.
type BackupGetRequest struct{}

// Validate accepts the empty request.
func (BackupGetRequest) Validate() error { return nil }

// BackupInfo is the backup configuration without secrets.
type BackupInfo struct {
	Settings BackupSettings `json:"settings"`
	// PublicKey is the SSH key to add to the target account's
	// authorized_keys; empty until a target is set.
	PublicKey string `json:"publicKey"`
	// HostKey is the pinned host key's SHA256 fingerprint, empty if none.
	HostKey string `json:"hostKey"`
	// Ready: a target and its host key are set; the timer is on.
	Ready bool `json:"ready"`
}

// BackupRunRequest has no parameters.
type BackupRunRequest struct{}

// Validate accepts the empty request.
func (BackupRunRequest) Validate() error { return nil }

// BackupRunResponse is empty: the run continues in its own unit.
type BackupRunResponse struct{}

// BackupStatusRequest has no parameters.
type BackupStatusRequest struct{}

// Validate accepts the empty request.
func (BackupStatusRequest) Validate() error { return nil }

// BackupItemResult is the outcome of one item of the last run.
type BackupItemResult struct {
	Kind       string `json:"kind"` // files, db or panel
	Site       string `json:"site,omitempty"`
	Database   string `json:"database,omitempty"`
	SnapshotID string `json:"snapshotId,omitempty"`
	DataAdded  int64  `json:"dataAdded"`
	Files      int64  `json:"files"`
	Bytes      int64  `json:"bytes"`
	Partial    bool   `json:"partial,omitempty"`
	Error      string `json:"error,omitempty"`
}

// BackupStatus is the result of the last run.
type BackupStatus struct {
	Running       bool               `json:"running"`
	Started       time.Time          `json:"started,omitzero"`
	Finished      time.Time          `json:"finished,omitzero"`
	OK            bool               `json:"ok"`
	Error         string             `json:"error,omitempty"`
	Items         []BackupItemResult `json:"items"`
	LastPrune     time.Time          `json:"lastPrune,omitzero"`
	LastCheck     time.Time          `json:"lastCheck,omitzero"`
	LastReadCheck time.Time          `json:"lastReadCheck,omitzero"`
	MaintenanceOK bool               `json:"maintenanceOk"`
	Maintenance   string             `json:"maintenance,omitempty"`
}

// BackupSnapshotsRequest asks for the snapshots of one site, or of the
// panel when Site is empty.
type BackupSnapshotsRequest struct {
	Site string `json:"site,omitempty"`
}

// Validate checks the site name.
func (r BackupSnapshotsRequest) Validate() error {
	if r.Site == "" {
		return nil
	}

	return ValidateSiteUser(r.Site)
}

// BackupSnapshot is one snapshot as of the end of the last run.
type BackupSnapshot struct {
	ID       string    `json:"id"`
	Time     time.Time `json:"time"`
	Kind     string    `json:"kind"`
	Database string    `json:"database,omitempty"`
	// Bytes and Files are the size of the content, 0 when unknown.
	Bytes int64 `json:"bytes,omitempty"`
	Files int64 `json:"files,omitempty"`
}

// BackupSnapshotsResponse lists snapshots, newest first.
type BackupSnapshotsResponse struct {
	Snapshots []BackupSnapshot `json:"snapshots"`
}

// Backup operations (ADR-0008). The agent has no network access, so
// everything that talks to the target happens in hostashi-backup.service;
// these operations configure it, start it and read its results.
var (
	BackupSet       = agentrpc.Op[BackupSetRequest, BackupInfo]{Name: "backup.set"}
	BackupHostKey   = agentrpc.Op[BackupHostKeyRequest, BackupInfo]{Name: "backup.hostkey.set"}
	BackupGet       = agentrpc.Op[BackupGetRequest, BackupInfo]{Name: "backup.get"}
	BackupRun       = agentrpc.Op[BackupRunRequest, BackupRunResponse]{Name: "backup.run"}
	BackupStatusGet = agentrpc.Op[BackupStatusRequest, BackupStatus]{Name: "backup.status"}
	BackupSnapshots = agentrpc.Op[BackupSnapshotsRequest, BackupSnapshotsResponse]{Name: "backup.snapshots"}
)

var (
	snapshotIDRe = regexp.MustCompile(`^[0-9a-f]{64}$`)
	restoreIDRe  = regexp.MustCompile(`^[0-9a-f]{16}$`)
)

// BackupRestoreRequest restores one snapshot of a site (its files or one
// database dump) into a new directory restore-<time> in the site's home,
// owned by the site user. Nothing live is overwritten (ADR-0008,
// recommendation 7).
type BackupRestoreRequest struct {
	Site     string `json:"site"`
	Snapshot string `json:"snapshot"` // the full 64-digit id
}

// Validate checks the site name and the id's form.
func (r BackupRestoreRequest) Validate() error {
	if err := ValidateSiteUser(r.Site); err != nil {
		return err
	}

	if !snapshotIDRe.MatchString(r.Snapshot) {
		return errors.New("snapshot: must be the full id of 64 hex digits")
	}

	return nil
}

// States of a restore.
const (
	RestoreQueued  = "queued"
	RestoreRunning = "running"
	RestoreDone    = "done"
	RestoreFailed  = "failed"
)

// BackupRestore is one restore and how it went.
type BackupRestore struct {
	ID       string `json:"id"`
	Site     string `json:"site"`
	Snapshot string `json:"snapshot"`
	Kind     string `json:"kind"` // files or db
	Database string `json:"database,omitempty"`
	// Dir is the new directory, relative to the site's home.
	Dir string `json:"dir"`
	// DumpFile is the name of a database dump in Dir once the restore
	// ran: <database>.sql or, for PostgreSQL, <database>.dump. Empty for
	// files and for restores made before it was recorded.
	DumpFile string    `json:"dumpFile,omitempty"`
	State    string    `json:"state"`
	Created  time.Time `json:"created"`
	Started  time.Time `json:"started,omitzero"`
	Finished time.Time `json:"finished,omitzero"`
	Files    int64     `json:"files"`
	Bytes    int64     `json:"bytes"`
	// Skipped counts entries a site directory cannot hold, such as device
	// files; they are left out.
	Skipped int64  `json:"skipped,omitempty"`
	Error   string `json:"error,omitempty"`
}

// BackupRestoreGetRequest asks for one restore.
type BackupRestoreGetRequest struct {
	ID string `json:"id"`
}

// Validate checks the id's form.
func (r BackupRestoreGetRequest) Validate() error {
	if !restoreIDRe.MatchString(r.ID) {
		return errors.New("id: must be 16 hex digits")
	}

	return nil
}

// Restore operations. The restore itself runs in hostashi-restore@<id>,
// because the agent has no network access.
var (
	BackupRestoreStart = agentrpc.Op[BackupRestoreRequest, BackupRestore]{Name: "backup.restore"}
	BackupRestoreGet   = agentrpc.Op[BackupRestoreGetRequest, BackupRestore]{Name: "backup.restore.get"}
)
