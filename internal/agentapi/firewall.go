// SPDX-License-Identifier: AGPL-3.0-or-later

package agentapi

import (
	"errors"
	"fmt"
	"net/netip"
	"regexp"
	"slices"

	"github.com/maki-it/hostashi/internal/agentrpc"
)

// Firewall rules from the panel (ADR-0034). The panel sends the whole
// desired state; the agent renders it into the panel's part of Hostashi's
// nftables table and reloads the firewall. Every change is pending until
// the panel confirms it, and the agent rolls it back when the confirmation
// does not come within FirewallConfirmWithin.

const (
	// FirewallConfirmWithin is how long a change stays pending.
	FirewallConfirmWithin = 120 // seconds
	// MaxFirewallRules bounds the opened ports.
	MaxFirewallRules = 50
	// MaxFirewallAddresses bounds the addresses of the whole state, so a
	// request stays well below agentrpc.MaxFrame.
	MaxFirewallAddresses = 256
)

// FirewallProtocols are the protocols a rule can open.
var FirewallProtocols = []string{"tcp", "udp"}

// FirewallFixedPorts are the ports of the sites; the panel can neither
// open nor close them (ADR-0034).
var FirewallFixedPorts = []int{80, 443}

// FirewallRule opens a port or a range of ports, to everyone or only to
// Sources.
type FirewallRule struct {
	Proto   string   `json:"proto"`             // "tcp" or "udp"
	From    int      `json:"from"`              // first port
	To      int      `json:"to,omitempty"`      // last port of a range; 0 or From for one port
	Sources []string `json:"sources,omitempty"` // addresses or CIDR prefixes; empty means everyone
	Note    string   `json:"note,omitempty"`    // the admin's label, not rendered
}

// Last returns the last port of the rule.
func (r FirewallRule) Last() int {
	if r.To == 0 {
		return r.From
	}

	return r.To
}

// FirewallState is everything the panel manages. An empty allowlist leaves
// the port open to everyone, as the installer's rules do.
type FirewallState struct {
	Open       []FirewallRule `json:"open"`
	SSHAllow   []string       `json:"sshAllow"`
	PanelAllow []string       `json:"panelAllow"`
}

var noteRe = regexp.MustCompile(`^[\p{L}\p{N} ._,:/()+-]{0,64}$`)

// ParsePrefix accepts an address or a CIDR prefix and returns it as a
// masked prefix, for example 192.0.2.7 as 192.0.2.7/32. An IPv4-mapped
// IPv6 address and a zone are refused.
func ParsePrefix(s string) (netip.Prefix, error) {
	if p, err := netip.ParsePrefix(s); err == nil {
		if p.Addr().Zone() != "" || p.Addr().Is4In6() {
			return netip.Prefix{}, fmt.Errorf("address %q is not allowed", s)
		}

		return p.Masked(), nil
	}

	a, err := netip.ParseAddr(s)
	if err != nil || a.Zone() != "" || a.Is4In6() {
		return netip.Prefix{}, fmt.Errorf("%q is not an IP address or CIDR prefix", s)
	}

	return netip.PrefixFrom(a, a.BitLen()), nil
}

func validAddresses(what string, list []string) error {
	seen := map[netip.Prefix]bool{}

	for _, s := range list {
		p, err := ParsePrefix(s)
		if err != nil {
			return fmt.Errorf("%s: %w", what, err)
		}

		if p.Bits() == 0 {
			return fmt.Errorf("%s: %q allows every address; leave the list empty instead", what, s)
		}

		if seen[p] {
			return fmt.Errorf("%s: %q is listed twice", what, s)
		}

		seen[p] = true
	}

	return nil
}

// Validate checks every value that ends up in the rules file.
func (s FirewallState) Validate() error {
	if len(s.Open) > MaxFirewallRules {
		return fmt.Errorf("at most %d opened ports", MaxFirewallRules)
	}

	total := len(s.SSHAllow) + len(s.PanelAllow)

	for i, r := range s.Open {
		what := fmt.Sprintf("rule %d", i+1)

		if !slices.Contains(FirewallProtocols, r.Proto) {
			return fmt.Errorf("%s: protocol must be tcp or udp", what)
		}

		if r.From < 1 || r.From > 65535 || r.Last() < r.From || r.Last() > 65535 {
			return fmt.Errorf("%s: ports must be from 1 to 65535, the first not above the last", what)
		}

		for _, p := range FirewallFixedPorts {
			if r.From <= p && p <= r.Last() {
				return fmt.Errorf("%s: port %d is always open for the sites", what, p)
			}
		}

		if !noteRe.MatchString(r.Note) {
			return fmt.Errorf("%s: the note may have up to 64 letters, digits, spaces and . _ , : / ( ) + -", what)
		}

		if err := validAddresses(what, r.Sources); err != nil {
			return err
		}

		total += len(r.Sources)
	}

	if total > MaxFirewallAddresses {
		return fmt.Errorf("at most %d addresses in all", MaxFirewallAddresses)
	}

	if err := validAddresses("SSH allowlist", s.SSHAllow); err != nil {
		return err
	}

	return validAddresses("panel allowlist", s.PanelAllow)
}

// FirewallApplyRequest makes State the pending change.
type FirewallApplyRequest struct {
	State FirewallState `json:"state"`
}

// Validate checks the state.
func (r FirewallApplyRequest) Validate() error { return r.State.Validate() }

// FirewallPending is a change waiting for its confirmation.
type FirewallPending struct {
	ID       string `json:"id"`       // passed back to firewall.confirm
	Deadline int64  `json:"deadline"` // Unix seconds; the agent rolls back after it
}

// FirewallStatus is what the agent has live.
type FirewallStatus struct {
	// Enabled is false when the installer's firewall is off
	// (--no-firewall); then the panel's rules are not loaded either.
	Enabled bool `json:"enabled"`
	// State is the live state, the pending one while a change waits.
	State   FirewallState    `json:"state"`
	Pending *FirewallPending `json:"pending,omitempty"`
	// RolledBack is the Unix time of the last rollback, 0 if none since
	// the agent started.
	RolledBack int64 `json:"rolledBack,omitempty"`
}

// FirewallConfirmRequest confirms the pending change with the ID from
// firewall.apply.
type FirewallConfirmRequest struct {
	ID string `json:"id"`
}

var firewallIDRe = regexp.MustCompile(`^[0-9a-f]{32}$`)

// Validate checks the form of the ID.
func (r FirewallConfirmRequest) Validate() error {
	if !firewallIDRe.MatchString(r.ID) {
		return errors.New("id must be 32 hex digits")
	}

	return nil
}

// FirewallStatusRequest has no parameters.
type FirewallStatusRequest struct{}

// Validate accepts every request.
func (FirewallStatusRequest) Validate() error { return nil }

var (
	// FirewallApply loads a new state as a pending change. It fails with
	// failed_precondition while another change is pending or when the
	// firewall is off.
	FirewallApply = agentrpc.Op[FirewallApplyRequest, FirewallStatus]{Name: "firewall.apply"}
	// FirewallConfirm keeps the pending change. It fails with not_found
	// when the ID is not the pending one (rolled back or never applied).
	FirewallConfirm = agentrpc.Op[FirewallConfirmRequest, FirewallStatus]{Name: "firewall.confirm"}
	// FirewallStatusOp reports the live state.
	FirewallStatusOp = agentrpc.Op[FirewallStatusRequest, FirewallStatus]{Name: "firewall.status"}
)
