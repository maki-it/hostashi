// SPDX-License-Identifier: AGPL-3.0-or-later

package agentapi

import (
	"errors"
	"fmt"
	"net/netip"
	"slices"
	"strings"

	"github.com/maki-it/hostashi/internal/agentrpc"
)

// fail2ban's bans in the panel (ADR-0078). The agent reads the Hostashi
// jails and lifts the ban of one address; it never bans and never changes
// a jail's settings.

// Fail2banJails are the jails the installer sets up (ADR-0021), in the
// order the panel shows them. No other jail name reaches fail2ban-client.
var Fail2banJails = []string{"sshd", "hostashi-panel", "hostashi-site-auth", "recidive"}

// Fail2banBan is one banned address.
type Fail2banBan struct {
	Address string `json:"address"`
	// Since is the Unix time of the ban.
	Since int64 `json:"since"`
	// Until is the Unix time the ban ends; 0 for a ban without end.
	Until int64 `json:"until,omitempty"`
}

// Fail2banJail is the state of one jail: its counters. The addresses it
// blocks come merged per address in Fail2banStatus.Blocked.
type Fail2banJail struct {
	Name string `json:"name"`
	// Running is false when fail2ban does not know the jail, for example
	// after a broken local change to its configuration.
	Running         bool `json:"running"`
	CurrentlyFailed int  `json:"currentlyFailed"`
	TotalFailed     int  `json:"totalFailed"`
	CurrentlyBanned int  `json:"currentlyBanned"`
	TotalBanned     int  `json:"totalBanned"`
}

// Fail2banBlocked is one blocked address with every jail that blocks it,
// since its first ban and until its last ban ends.
type Fail2banBlocked struct {
	Address string   `json:"address"`
	Jails   []string `json:"jails"`
	Since   int64    `json:"since"`
	// Until is 0 when one of the bans has no end.
	Until int64 `json:"until,omitempty"`
}

// Fail2banStatus is the state of the Hostashi jails and one page of the
// blocked addresses that match the request, newest first.
type Fail2banStatus struct {
	Jails   []Fail2banJail    `json:"jails"`
	Blocked []Fail2banBlocked `json:"blocked"`
	// Total counts every matching address, not only this page.
	Total int `json:"total"`
	// Truncated is true when fail2ban listed more addresses than the
	// agent reads (f2bctl's output limit); the list then misses some.
	Truncated bool `json:"truncated,omitempty"`
}

// Page sizes of fail2ban.status. One page with the longest addresses
// blocked in every jail stays far below agentrpc.MaxFrame (tested).
const (
	Fail2banPageDefault = 50
	Fail2banPageMax     = 100
	// Fail2banQueryMax is the longest address filter.
	Fail2banQueryMax = 64
	// fail2banOffsetMax keeps the offset in a sane range.
	fail2banOffsetMax = 10_000_000
)

// Fail2banStatusRequest filters and pages the blocked addresses. The
// filters work in the agent, so a server with thousands of blocked
// addresses still answers with one small page (ADR-0078).
type Fail2banStatusRequest struct {
	// Jail keeps the addresses one jail blocks; empty is every jail.
	Jail string `json:"jail,omitempty"`
	// Address keeps the addresses that contain it, equal it, or, written
	// as a range such as 203.0.113.0/24, lie in it.
	Address string `json:"address,omitempty"`
	Offset  int    `json:"offset,omitempty"`
	// Limit is the page size; 0 is Fail2banPageDefault.
	Limit int `json:"limit,omitempty"`
}

// ErrFail2banFilter is returned for a filter fail2ban.status does not take.
var ErrFail2banFilter = errors.New("invalid filter")

// Validate checks the filter and the page.
func (r Fail2banStatusRequest) Validate() error {
	if r.Jail != "" && !slices.Contains(Fail2banJails, r.Jail) {
		return fmt.Errorf("%w: jail must be one of %s", ErrFail2banFilter, strings.Join(Fail2banJails, ", "))
	}

	if len(r.Address) > Fail2banQueryMax || strings.Trim(r.Address, "0123456789abcdefABCDEF.:/") != "" {
		return fmt.Errorf("%w: address may only hold an IP address, a part of one or a range", ErrFail2banFilter)
	}

	if strings.Contains(r.Address, "/") {
		if _, err := netip.ParsePrefix(r.Address); err != nil {
			return fmt.Errorf("%w: %q is not a range such as 203.0.113.0/24", ErrFail2banFilter, r.Address)
		}
	}

	if r.Offset < 0 || r.Offset > fail2banOffsetMax || r.Limit < 0 || r.Limit > Fail2banPageMax {
		return fmt.Errorf("%w: page out of range (at most %d per page)", ErrFail2banFilter, Fail2banPageMax)
	}

	return nil
}

// Fail2banUnbanRequest lifts the ban of Address in every Hostashi jail
// (ADR-0078: one action, so a recidive ban does not stay behind).
type Fail2banUnbanRequest struct {
	Address string `json:"address"`
}

// ErrFail2banAddress is returned for an address that is not a plain IPv4
// or IPv6 address in canonical form.
var ErrFail2banAddress = errors.New("address must be an IPv4 or IPv6 address in canonical form")

// Validate allows only a canonical address, so nothing else reaches the
// command line.
func (r Fail2banUnbanRequest) Validate() error {
	a, err := netip.ParseAddr(r.Address)
	if err != nil || a.Zone() != "" || a.Unmap().String() != r.Address {
		return ErrFail2banAddress
	}

	return nil
}

// Fail2banUnbanResponse names the jails that had banned the address; empty
// when none had.
type Fail2banUnbanResponse struct {
	Jails []string `json:"jails"`
}

var (
	// Fail2banStatusOp reports the Hostashi jails. It fails with
	// failed_precondition when fail2ban does not answer.
	Fail2banStatusOp = agentrpc.Op[Fail2banStatusRequest, Fail2banStatus]{Name: "fail2ban.status"}
	// Fail2banUnban lifts the ban of one address in every Hostashi jail.
	Fail2banUnban = agentrpc.Op[Fail2banUnbanRequest, Fail2banUnbanResponse]{Name: "fail2ban.unban"}
)
