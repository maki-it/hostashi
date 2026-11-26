// SPDX-License-Identifier: AGPL-3.0-or-later

package agentapi

import (
	"errors"
	"net/netip"

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

// Fail2banJail is the state of one jail.
type Fail2banJail struct {
	Name string `json:"name"`
	// Running is false when fail2ban does not know the jail, for example
	// after a broken local change to its configuration.
	Running         bool          `json:"running"`
	CurrentlyFailed int           `json:"currentlyFailed"`
	TotalFailed     int           `json:"totalFailed"`
	CurrentlyBanned int           `json:"currentlyBanned"`
	TotalBanned     int           `json:"totalBanned"`
	Bans            []Fail2banBan `json:"bans"`
}

// Fail2banStatus is the state of the Hostashi jails.
type Fail2banStatus struct {
	Jails []Fail2banJail `json:"jails"`
}

// Fail2banStatusRequest has no parameters.
type Fail2banStatusRequest struct{}

// Validate accepts every request.
func (Fail2banStatusRequest) Validate() error { return nil }

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
