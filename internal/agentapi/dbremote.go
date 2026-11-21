// SPDX-License-Identifier: AGPL-3.0-or-later

package agentapi

import (
	"errors"
	"fmt"
	"net/netip"
	"regexp"

	"github.com/maki-it/hostashi/internal/agentrpc"
)

// Import and export of a database on an external server (ADR-0041
// addendum 1). The panel creates a temporary user on that server; the
// agent records the job and starts hostashi-dbjob@<id>.service, which may
// reach only the server's addresses and runs the client as the site user.

// Limits of a remote job request.
const (
	MaxRemoteHostLen  = 253
	MaxRemoteAddrs    = 8
	MaxRemoteCAPEMLen = 64 << 10
)

var (
	remoteHostRe     = regexp.MustCompile(`^[a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?(\.[a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?)*$`)
	remoteJobUserRe  = regexp.MustCompile(`^hostashi_job_[0-9a-f]{16}$`)
	remoteJobPassRe  = regexp.MustCompile(`^[A-Za-z0-9]{32}$`)
	errRemoteJobKind = errors.New("kind: must be import or export")
)

// DBRemoteJobRequest starts an import or export of a database on an
// external server. The password belongs to a temporary user the panel
// created for this job with privileges on Database only.
type DBRemoteJobRequest struct {
	Kind     string `json:"kind"` // "import" or "export"
	Site     string `json:"site"`
	Database string `json:"database"`
	Path     string `json:"path"`

	Host string `json:"host"`
	Port int    `json:"port"`
	// Addrs are the addresses the panel resolved Host to; the job may
	// connect to these only and needs no DNS.
	Addrs []string `json:"addrs"`
	TLS   string   `json:"tls"` // "verify", "verify-ca" or "none"
	CAPEM string   `json:"caPem,omitempty"`

	User     string `json:"user"`
	Password string `json:"password"`
}

// Validate checks the request.
func (r DBRemoteJobRequest) Validate() error {
	if r.Kind != "import" && r.Kind != "export" {
		return errRemoteJobKind
	}

	if err := ValidateSiteUser(r.Site); err != nil {
		return err
	}

	if err := (DBDumpRequest{Database: r.Database, Path: r.Path}).Validate(); err != nil {
		return err
	}

	if r.Password == "" {
		return errors.New("password: must be 32 letters and digits")
	}

	if err := validateRemoteHost(r.Host); err != nil {
		return err
	}

	if r.Port < 1 || r.Port > 65535 {
		return errors.New("port: must be 1 to 65535")
	}

	if _, err := r.Addresses(); err != nil {
		return err
	}

	switch r.TLS {
	case "verify", "none":
		if r.CAPEM != "" {
			return errors.New("caPem: only for TLS mode verify-ca")
		}
	case "verify-ca":
		if r.CAPEM == "" || len(r.CAPEM) > MaxRemoteCAPEMLen {
			return fmt.Errorf("caPem: must have 1 to %d bytes", MaxRemoteCAPEMLen)
		}
	default:
		return errors.New("tls: must be verify, verify-ca or none")
	}

	return ValidateRemoteJobUser(r.User, r.Password)
}

// ValidateRemoteJobUser checks the temporary user of a remote job and its
// password. The names start with hostashi, which site users cannot take.
func ValidateRemoteJobUser(user, password string) error {
	if !remoteJobUserRe.MatchString(user) {
		return fmt.Errorf("user: must match %s", remoteJobUserRe)
	}

	if password != "" && !remoteJobPassRe.MatchString(password) {
		return errors.New("password: must be 32 letters and digits")
	}

	return nil
}

// Addresses parses Addrs: 1 to MaxRemoteAddrs unicast addresses, no
// loopback (the local server has its socket) and no duplicates. When Host
// is an IP address, it must be the only address.
func (r DBRemoteJobRequest) Addresses() ([]netip.Addr, error) {
	if len(r.Addrs) == 0 || len(r.Addrs) > MaxRemoteAddrs {
		return nil, fmt.Errorf("addrs: must have 1 to %d addresses", MaxRemoteAddrs)
	}

	seen := map[netip.Addr]bool{}
	out := make([]netip.Addr, 0, len(r.Addrs))

	for _, s := range r.Addrs {
		a, err := netip.ParseAddr(s)
		if err != nil || a.Zone() != "" || a.String() != s {
			return nil, fmt.Errorf("addrs: %q is not an IP address in canonical form", s)
		}

		if !a.IsGlobalUnicast() || a.Is4In6() {
			return nil, fmt.Errorf("addrs: %s is not a unicast address", s)
		}

		if seen[a] {
			return nil, fmt.Errorf("addrs: %s twice", s)
		}

		seen[a] = true
		out = append(out, a)
	}

	if ip, err := netip.ParseAddr(r.Host); err == nil && (len(out) != 1 || out[0] != ip) {
		return nil, errors.New("addrs: must be the host's own address")
	}

	return out, nil
}

func validateRemoteHost(h string) error {
	if h == "" || len(h) > MaxRemoteHostLen {
		return fmt.Errorf("host: must have 1 to %d characters", MaxRemoteHostLen)
	}

	if a, err := netip.ParseAddr(h); err == nil {
		if a.Zone() != "" || a.String() != h {
			return errors.New("host: IP address must be in canonical form")
		}

		return nil
	}

	if !remoteHostRe.MatchString(h) {
		return errors.New("host: must be a lower-case DNS name or an IP address")
	}

	return nil
}

// Ops of remote jobs. Their state is read with db.remote.job, because the
// agent keeps them in records, not in memory like local jobs.
var (
	DBRemoteJobStart = agentrpc.Op[DBRemoteJobRequest, DBJob]{Name: "db.remote.start"}
	DBRemoteJobGet   = agentrpc.Op[DBJobRequest, DBJob]{Name: "db.remote.job"}
)
