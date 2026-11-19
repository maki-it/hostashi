// SPDX-License-Identifier: AGPL-3.0-or-later

package agentapi

import (
	"errors"
	"fmt"
	"net"
	"path"
	"strconv"
	"strings"
	"time"

	"github.com/maki-it/hostashi/internal/agentrpc"
)

// ErrSourceInvalid marks a malformed CloudPanel source.
var ErrSourceInvalid = errors.New("invalid migration source")

// CloudPanelSource is the old CloudPanel server the new server reads from
// over SSH (ADR-0026). The login user is always root: the key is locked to
// a read-only SFTP server and one port forward on the old side.
type CloudPanelSource struct {
	Host string `json:"host"` // DNS name or IP address of the old server
	Port int    `json:"port"`
}

// Validate accepts a lower-case DNS name or an IP address, and a port.
// An IP address is the safer choice: the DNS names move during the
// migration.
func (s CloudPanelSource) Validate() error {
	if s.Port < 1 || s.Port > 65535 {
		return fmt.Errorf("%w: port", ErrSourceInvalid)
	}

	if ip := net.ParseIP(s.Host); ip != nil {
		if ip.IsUnspecified() || ip.IsMulticast() || ip.String() != s.Host {
			return fmt.Errorf("%w: host", ErrSourceInvalid)
		}

		return nil
	}

	if len(s.Host) > 253 || !hostnameRe.MatchString(s.Host) {
		return fmt.Errorf("%w: host must be a lowercase DNS name or an IP address", ErrSourceInvalid)
	}

	return nil
}

// Addr is host:port for dialing.
func (s CloudPanelSource) Addr() string { return net.JoinHostPort(s.Host, strconv.Itoa(s.Port)) }

// KnownHostsName is how ssh names the source in known_hosts.
func (s CloudPanelSource) KnownHostsName() string {
	if s.Port == 22 {
		return s.Host
	}

	return "[" + s.Host + "]:" + strconv.Itoa(s.Port)
}

// CloudPanelSourceInfo is the source as the agent keeps it. It never holds
// the private key.
type CloudPanelSourceInfo struct {
	Source *CloudPanelSource `json:"source"` // nil: not set up
	// PublicKey is the key to add for root on the old server, in
	// authorized_keys form; AuthorizedKeysLine adds the restrictions.
	PublicKey          string `json:"publicKey,omitempty"`
	AuthorizedKeysLine string `json:"authorizedKeysLine,omitempty"`
	// HostKey is the pinned host key's SHA256 fingerprint, empty if none.
	HostKey string `json:"hostKey"`
	Ready   bool   `json:"ready"`
	// DBUser is the read-only database user on the old server, empty until
	// its password was generated (cloudpanel.source.db).
	DBUser string `json:"dbUser,omitempty"`
}

// CloudPanelDBUser is the database user the import reads with on the old
// server. It connects through the SSH tunnel, so from 127.0.0.1.
const CloudPanelDBUser = "hostashi_import"

// CloudPanelDBGrant is what the dump user may do on the old server: read,
// nothing else (ADR-0026 Part 2).
const CloudPanelDBGrant = "SELECT, SHOW VIEW, TRIGGER, EVENT, LOCK TABLES"

// CloudPanelDBRequest generates a new password for the dump user.
type CloudPanelDBRequest struct{}

// Validate implements agentrpc.Request.
func (CloudPanelDBRequest) Validate() error { return nil }

// CloudPanelDBCredentials is shown once: the administrator runs SQL on the
// old server. The agent keeps the password root-only for the import job.
type CloudPanelDBCredentials struct {
	User     string `json:"user"`
	Password string `json:"password"`
	SQL      string `json:"sql"`
}

// CloudPanelDBSQL is the statement that creates the dump user on the old
// server. pw is generated (letters and digits only).
func CloudPanelDBSQL(pw string) string {
	acct := "'" + CloudPanelDBUser + "'@'127.0.0.1'"

	return "CREATE OR REPLACE USER " + acct + " IDENTIFIED BY '" + pw + "';\n" +
		"GRANT " + CloudPanelDBGrant + " ON *.* TO " + acct + ";"
}

// CloudPanelSourceGetRequest reads the source.
type CloudPanelSourceGetRequest struct{}

// Validate implements agentrpc.Request.
func (CloudPanelSourceGetRequest) Validate() error { return nil }

// CloudPanelSourceSetRequest sets the source. The SSH key is created on
// first use; another host or port drops the pinned host key.
type CloudPanelSourceSetRequest struct {
	Source CloudPanelSource `json:"source"`
}

// Validate implements agentrpc.Request.
func (r CloudPanelSourceSetRequest) Validate() error { return r.Source.Validate() }

// CloudPanelHostKeyRequest pins the source's host key after the
// administrator confirmed its fingerprint.
type CloudPanelHostKeyRequest struct {
	Key string `json:"key"` // one public key in authorized_keys form
}

// Validate implements agentrpc.Request.
func (r CloudPanelHostKeyRequest) Validate() error {
	if r.Key == "" || len(r.Key) > MaxHostKey {
		return fmt.Errorf("key: between 1 and %d bytes", MaxHostKey)
	}

	return nil
}

// CloudPanelSourceDeleteRequest removes the source, its SSH key and the
// pinned host key, at the end of a migration.
type CloudPanelSourceDeleteRequest struct{}

// Validate implements agentrpc.Request.
func (CloudPanelSourceDeleteRequest) Validate() error { return nil }

// Operations of the migration source (ADR-0026).
var (
	CloudPanelSourceGet    = agentrpc.Op[CloudPanelSourceGetRequest, CloudPanelSourceInfo]{Name: "cloudpanel.source.get"}
	CloudPanelSourceSet    = agentrpc.Op[CloudPanelSourceSetRequest, CloudPanelSourceInfo]{Name: "cloudpanel.source.set"}
	CloudPanelHostKey      = agentrpc.Op[CloudPanelHostKeyRequest, CloudPanelSourceInfo]{Name: "cloudpanel.source.hostkey"}
	CloudPanelSourceDelete = agentrpc.Op[CloudPanelSourceDeleteRequest, CloudPanelSourceInfo]{Name: "cloudpanel.source.delete"}
	CloudPanelSourceDB     = agentrpc.Op[CloudPanelDBRequest, CloudPanelDBCredentials]{Name: "cloudpanel.source.db"}
)

// ValidateRemoteDocRoot accepts a clean absolute path below /home/<user>/
// on the old server, where user is the old site user. The transfer reads
// only below it (ADR-0026).
func ValidateRemoteDocRoot(user, p string) error {
	home := "/home/" + user + "/"
	if len(p) > 4096 || p != path.Clean(p) || !strings.HasPrefix(p, home) || len(p) == len(home) || strings.ContainsAny(p, "\x00\n\\$") {
		return fmt.Errorf("document root %q is not a clean path below %s", p, home)
	}

	return nil
}

// CloudPanelImportRequest copies one site's files from the old server
// into the existing site Site on this server (ADR-0026 Part 3). A second
// request for the same site mirrors again: that is the final sync.
type CloudPanelImportRequest struct {
	Site       string `json:"site"`       // the site on this server
	RemoteUser string `json:"remoteUser"` // the site user on the old server
	DocRoot    string `json:"docRoot"`    // below /home/<remoteUser>/ there
	// Databases are dumped from the old server into the site's home, one
	// file each, for the import of ADR-0025.
	Databases []CloudPanelDBMap `json:"databases,omitempty"`
}

// CloudPanelDBMap names a database on the old server and the site's
// database here that its dump is meant for.
type CloudPanelDBMap struct {
	From string `json:"from"`
	To   string `json:"to"`
}

// MaxImportDatabases bounds the databases of one import.
const MaxImportDatabases = 50

// Validate checks the names and the path.
func (r CloudPanelImportRequest) Validate() error {
	if err := ValidateSiteUser(r.Site); err != nil {
		return err
	}

	if err := ValidateSiteUser(r.RemoteUser); err != nil {
		return fmt.Errorf("remote user: %w", err)
	}

	if err := ValidateRemoteDocRoot(r.RemoteUser, r.DocRoot); err != nil {
		return err
	}

	if len(r.Databases) > MaxImportDatabases {
		return fmt.Errorf("databases: at most %d", MaxImportDatabases)
	}

	seen := map[string]bool{}

	for _, d := range r.Databases {
		if err := ValidateDBName(d.From); err != nil {
			return fmt.Errorf("database on the old server: %w", err)
		}

		if err := ValidateDBName(d.To); err != nil {
			return err
		}

		if seen[d.To] {
			return fmt.Errorf("database %s: named twice", d.To)
		}

		seen[d.To] = true
	}

	return nil
}

// CloudPanelImportDBDir holds the dumps of an import, relative to the
// site's home; a dump is named <database>-<time>.sql.
const CloudPanelImportDBDir = "cloudpanel-import-db"

// CloudPanelImportDir is where an import lands, relative to the site's
// home. The web root is replaced only in a later step.
const CloudPanelImportDir = "cloudpanel-import"

// States of an import.
const (
	ImportQueued  = "queued"
	ImportRunning = "running"
	ImportDone    = "done"
	ImportFailed  = "failed"
)

// CloudPanelImport is one import run and how it went.
type CloudPanelImport struct {
	ID         string    `json:"id"`
	Site       string    `json:"site"`
	RemoteUser string    `json:"remoteUser"`
	DocRoot    string    `json:"docRoot"`
	Dir        string    `json:"dir"` // relative to the site's home
	State      string    `json:"state"`
	Created    time.Time `json:"created"`
	Started    time.Time `json:"started,omitzero"`
	Finished   time.Time `json:"finished,omitzero"`
	Files      int64     `json:"files"`
	Bytes      int64     `json:"bytes"`
	Unchanged  int64     `json:"unchanged"`
	Deleted    int64     `json:"deleted"`
	Skipped    int64     `json:"skipped"`
	// SkippedPaths lists the first skipped entries with the reason.
	SkippedPaths []string          `json:"skippedPaths,omitempty"`
	Databases    []CloudPanelDBMap `json:"databases,omitempty"`
	// Dumps are the written dump files, relative to the site's home, in
	// the order of Databases: the path for "db import".
	Dumps []string `json:"dumps,omitempty"`
	Error string   `json:"error,omitempty"`
}

// CloudPanelImportGetRequest asks for one import.
type CloudPanelImportGetRequest struct {
	ID string `json:"id"`
}

// Validate checks the id's form.
func (r CloudPanelImportGetRequest) Validate() error {
	if !ValidImportID(r.ID) {
		return errors.New("id: must be 16 hex digits")
	}

	return nil
}

// ValidImportID reports whether id can name an import and its unit.
func ValidImportID(id string) bool { return restoreIDRe.MatchString(id) }

// Import operations. The copy runs in hostashi-import@<id>.service, because
// the agent has no network access.
var (
	CloudPanelImportStart = agentrpc.Op[CloudPanelImportRequest, CloudPanelImport]{Name: "cloudpanel.import"}
	CloudPanelImportGet   = agentrpc.Op[CloudPanelImportGetRequest, CloudPanelImport]{Name: "cloudpanel.import.get"}
)
