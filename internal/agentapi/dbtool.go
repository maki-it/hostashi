// SPDX-License-Identifier: AGPL-3.0-or-later

package agentapi

import (
	"errors"
	"fmt"
	"regexp"
	"slices"
	"time"

	"github.com/maki-it/hostashi/internal/agentrpc"
)

// Web database tool (ADR-0038). The agent creates a temporary MariaDB user
// for one database and hands its login to Adminer through a one-time ticket
// file; the browser only ever sees the ticket id.
const (
	// DBToolUser is the system user of Adminer's PHP-FPM pool. It owns the
	// ticket directory and every ticket in it.
	DBToolUser = "hostashi-adminer"
	// DBToolTicketDir holds the tickets, one file per ticket id.
	DBToolTicketDir = "/run/hostashi-adminer/tickets"
	// DBToolTicketTTL is how long an unused ticket stays valid. The agent
	// removes it and drops its user afterwards.
	DBToolTicketTTL = 60 * time.Second
	// DBToolUserPrefix starts every temporary user. "hostashi" is reserved
	// for site users (ValidateDBUser), so no other account can match it.
	DBToolUserPrefix = "hostashi_tool_"
	// DBToolMaxConnections bounds the connections of one temporary user.
	DBToolMaxConnections = 2
	// DBToolSessionDir holds one file per open session, named by its
	// temporary user, with the session's end in Unix seconds; 0 means the
	// session was ended. The agent writes it, the tool only reads it, so an
	// extended or ended session reaches a tab that is already open.
	DBToolSessionDir = "/run/hostashi-adminer/sessions"
	// DBToolExtendStep is how much one extension adds.
	DBToolExtendStep = 30 * time.Minute
	// DBToolMaxRemaining bounds how far ahead a session may end, however
	// often it is extended.
	DBToolMaxRemaining = 2 * time.Hour
)

// DBToolExtendRequest moves the end of an open session to Expires.
// Remote is set for a session on an external server: the panel holds its
// user, the agent only tells the tool.
type DBToolExtendRequest struct {
	User    string    `json:"user"`
	Expires time.Time `json:"expires"`
	Remote  bool      `json:"remote,omitempty"`
}

// Validate checks the request.
func (r DBToolExtendRequest) Validate() error {
	now := time.Now()

	if !ValidDBToolUser(r.User) {
		return errors.New("user: not a session of the database tool")
	}

	if !r.Expires.After(now) || r.Expires.Sub(now) > DBToolMaxRemaining+time.Minute {
		return fmt.Errorf("expires: must be within %s", DBToolMaxRemaining)
	}

	return nil
}

// DBToolEndRequest ends an open session now.
type DBToolEndRequest struct {
	User   string `json:"user"`
	Remote bool   `json:"remote,omitempty"`
}

// Validate checks the request.
func (r DBToolEndRequest) Validate() error {
	if !ValidDBToolUser(r.User) {
		return errors.New("user: not a session of the database tool")
	}

	return nil
}

// DBToolSessionInfo is an open local session as the agent knows it.
type DBToolSessionInfo struct {
	User     string    `json:"user"`
	Database string    `json:"database"`
	Expires  time.Time `json:"expires"`
}

// DBToolSessionsRequest asks for the open local sessions.
type DBToolSessionsRequest struct{}

// Validate checks the request.
func (DBToolSessionsRequest) Validate() error { return nil }

// DBToolSessionsResponse lists the open local sessions.
type DBToolSessionsResponse struct {
	Sessions []DBToolSessionInfo `json:"sessions"`
}

var (
	// DBToolExtend moves the end of a session.
	DBToolExtend = agentrpc.Op[DBToolExtendRequest, DBToolSessionInfo]{Name: "db.tool.extend"}
	// DBToolEnd ends a session: a local user is dropped with its
	// connections, and the tool refuses the session from then on.
	DBToolEnd = agentrpc.Op[DBToolEndRequest, struct{}]{Name: "db.tool.end"}
	// DBToolSessions lists the open local sessions.
	DBToolSessions = agentrpc.Op[DBToolSessionsRequest, DBToolSessionsResponse]{Name: "db.tool.sessions"}
)

// DBToolDurations are the session lengths a user can choose, in minutes.
var DBToolDurations = []int{30, 120}

var (
	dbToolTicketRe = regexp.MustCompile(`^[0-9a-f]{32}$`)
	dbToolUserRe   = regexp.MustCompile(`^hostashi_tool_[0-9a-f]{16}$`)
)

// ValidDBToolTicket reports whether id has the form of a ticket id.
func ValidDBToolTicket(id string) bool { return dbToolTicketRe.MatchString(id) }

// ValidDBToolUser reports whether name is a temporary user of the tool.
func ValidDBToolUser(name string) bool { return dbToolUserRe.MatchString(name) }

// DBToolOpenRequest asks for a session on one database.
type DBToolOpenRequest struct {
	Database string `json:"database"`
	Minutes  int    `json:"minutes"`
}

// Validate checks the request.
func (r DBToolOpenRequest) Validate() error {
	if err := ValidateDBName(r.Database); err != nil {
		return err
	}

	if !slices.Contains(DBToolDurations, r.Minutes) {
		return fmt.Errorf("minutes: must be one of %v", DBToolDurations)
	}

	return nil
}

// DBToolOpenResponse names the ticket. Expires is the end of the session;
// the ticket itself is valid for DBToolTicketTTL.
type DBToolOpenResponse struct {
	Ticket  string    `json:"ticket"`
	User    string    `json:"user"`
	Expires time.Time `json:"expires"`
}

// DBToolOpen creates the temporary user and the ticket.
var DBToolOpen = agentrpc.Op[DBToolOpenRequest, DBToolOpenResponse]{Name: "db.tool.open"}

// DBToolTicket is the content of a ticket file, read by Adminer's wrapper.
type DBToolTicket struct {
	Database string    `json:"database"`
	User     string    `json:"user"`
	Password string    `json:"password"`
	Expires  time.Time `json:"expires"`
	// TicketExpires is when an unused ticket stops being valid.
	TicketExpires time.Time `json:"ticketExpires"`

	// An external server (ADR-0041 addendum 1); empty for the local socket.
	Host   string `json:"host,omitempty"`
	Port   int    `json:"port,omitempty"`
	TLS    string `json:"tls,omitempty"`    // "verify", "verify-ca" or "none"
	CAFile string `json:"caFile,omitempty"` // for "verify-ca"
}

// External servers in the web database tool (ADR-0041 addendum 1). The
// tool's pool reaches only the addresses of the configured external
// servers, through a drop-in the agent writes; a private /etc/hosts maps
// the servers' names, so the pool needs no DNS.
const (
	// DBToolServerDir holds the hosts file and the servers' own CAs,
	// readable by the tool.
	DBToolServerDir = "/etc/hostashi-adminer/servers"
	// DBToolUnit is the tool's PHP-FPM unit.
	DBToolUnit = "hostashi-adminer.service"
	// MaxDBToolServers bounds the external servers in the drop-in.
	MaxDBToolServers = 32
)

// DBToolServer is an external server the tool may reach.
type DBToolServer struct {
	ID    int64    `json:"id"`
	Host  string   `json:"host"`
	Addrs []string `json:"addrs"`
	CAPEM string   `json:"caPem,omitempty"` // the server's own CA, for verify-ca
}

// DBToolServersRequest is the full list of external servers.
type DBToolServersRequest struct {
	Servers []DBToolServer `json:"servers"`
}

// DBToolServersResponse says whether the pool was restarted, which ends
// the open sessions.
type DBToolServersResponse struct {
	Restarted bool `json:"restarted"`
}

// Validate checks the list.
func (r DBToolServersRequest) Validate() error {
	if len(r.Servers) > MaxDBToolServers {
		return fmt.Errorf("servers: at most %d", MaxDBToolServers)
	}

	ids := map[int64]bool{}

	for _, srv := range r.Servers {
		if srv.ID < 1 || ids[srv.ID] {
			return fmt.Errorf("servers: id %d", srv.ID)
		}

		ids[srv.ID] = true

		if err := ValidateRemoteHost(srv.Host); err != nil {
			return err
		}

		if _, err := ParseRemoteAddrs(srv.Host, srv.Addrs); err != nil {
			return err
		}

		if len(srv.CAPEM) > MaxRemoteCAPEMLen {
			return fmt.Errorf("caPem: at most %d bytes", MaxRemoteCAPEMLen)
		}
	}

	return nil
}

// DBToolCAFile is where the tool finds the CA of server id.
func DBToolCAFile(id int64) string {
	return fmt.Sprintf("%s/ca-%d.pem", DBToolServerDir, id)
}

// DBToolServers writes the drop-in and the hosts file and restarts the pool
// when they changed.
var DBToolServers = agentrpc.Op[DBToolServersRequest, DBToolServersResponse]{Name: "db.tool.servers"}

// DBToolRemoteRequest writes a ticket for a database on an external server.
// The panel created the temporary user there.
type DBToolRemoteRequest struct {
	Database string `json:"database"`
	Minutes  int    `json:"minutes"`
	ServerID int64  `json:"serverId"`
	Host     string `json:"host"`
	Port     int    `json:"port"`
	TLS      string `json:"tls"`
	User     string `json:"user"`
	Password string `json:"password"`
}

// Validate checks the request.
func (r DBToolRemoteRequest) Validate() error {
	if err := (DBToolOpenRequest{Database: r.Database, Minutes: r.Minutes}).Validate(); err != nil {
		return err
	}

	if r.ServerID < 1 {
		return errors.New("serverId: must be set")
	}

	if err := ValidateRemoteHost(r.Host); err != nil {
		return err
	}

	if r.Port < 1 || r.Port > 65535 {
		return errors.New("port: must be 1 to 65535")
	}

	if r.TLS != "verify" && r.TLS != "verify-ca" && r.TLS != "none" {
		return errors.New("tls: must be verify, verify-ca or none")
	}

	if !ValidDBToolUser(r.User) {
		return fmt.Errorf("user: must match %s", dbToolUserRe)
	}

	if !ValidRemotePassword(r.Password) {
		return errors.New("password: must be 32 letters and digits")
	}

	return nil
}

// DBToolRemote writes the ticket of an external database.
var DBToolRemote = agentrpc.Op[DBToolRemoteRequest, DBToolOpenResponse]{Name: "db.tool.remote"}
