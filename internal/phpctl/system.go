// SPDX-License-Identifier: AGPL-3.0-or-later

package phpctl

import (
	"context"
	"fmt"
	"slices"
	"strings"
)

// systemUnits are the units outside the sites whose state the agent may
// read: Hostashi's firewall (ADR-0033), for hostashi doctor. Reading the
// state changes nothing.
var systemUnits = []string{"hostashi-firewall.service"}

// UnitActive reports whether one of systemUnits is active (systemctl show
// --property=ActiveState).
func UnitActive(ctx context.Context, unit string) (bool, error) {
	if !slices.Contains(systemUnits, unit) {
		return false, fmt.Errorf("phpctl: refusing unit name %q", unit)
	}

	out, err := output(ctx, Systemctl, "show", "--property=ActiveState", "--value", unit)
	if err != nil {
		return false, err
	}

	return strings.TrimSpace(out) == "active", nil
}

// FirewallUnit loads Hostashi's nftables table, with the panel's rules
// (ADR-0034).
const FirewallUnit = "hostashi-firewall.service"

// ReloadFirewall makes the firewall unit load its files again (systemctl
// reload-or-restart: a unit that is not running is started). nft loads
// them in one transaction, so a file that does not load leaves the old
// rules and makes the call fail.
func ReloadFirewall(ctx context.Context) error {
	return run(ctx, Systemctl, "reload-or-restart", FirewallUnit)
}
