// SPDX-License-Identifier: AGPL-3.0-or-later

package agentapi

import (
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
}
