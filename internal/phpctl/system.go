// SPDX-License-Identifier: AGPL-3.0-or-later

package phpctl

import (
	"context"
	"fmt"
	"slices"
	"strings"

	"github.com/maki-it/hostashi/internal/agentapi"
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

// InstallUnits starts and reads hostashi-php-install@<version>.service,
// the fixed unit that installs more PHP versions (ADR-0036). The version
// must be one of agentapi.PHPVersions; the script behind the unit checks it
// again.
type InstallUnits struct{}

func installUnit(version string) (string, error) {
	if err := agentapi.ValidatePHPVersion(version); err != nil {
		return "", err
	}

	return "hostashi-php-install@" + version + ".service", nil
}

// Start queues the unit (systemctl start --no-block): apt may run for
// minutes, the agent does not wait for it.
func (InstallUnits) Start(ctx context.Context, version string) error {
	unit, err := installUnit(version)
	if err != nil {
		return err
	}

	return run(ctx, Systemctl, "start", "--no-block", unit)
}

// Active reports whether the unit is running or about to.
func (InstallUnits) Active(ctx context.Context, version string) (bool, error) {
	unit, err := installUnit(version)
	if err != nil {
		return false, err
	}

	out, err := output(ctx, Systemctl, "show", "--property=ActiveState", "--value", unit)
	if err != nil {
		return false, err
	}

	switch strings.TrimSpace(out) {
	case "active", "activating", "reloading":
		return true, nil
	}

	return false, nil
}

// ValkeyDefaultUnit is the Valkey package's own service, which the
// installer masks (ADR-0045).
const ValkeyDefaultUnit = "valkey-server.service"

// ValkeyDefaultState returns ActiveState and UnitFileState of the
// package's Valkey service, for hostashi doctor. Reading changes nothing.
func ValkeyDefaultState(ctx context.Context) (active, file string, err error) {
	out, err := output(ctx, Systemctl, "show", "--property=ActiveState", "--property=UnitFileState", ValkeyDefaultUnit)
	if err != nil {
		return "", "", err
	}

	for l := range strings.SplitSeq(out, "\n") {
		k, v, _ := strings.Cut(l, "=")
		switch k {
		case "ActiveState":
			active = v
		case "UnitFileState":
			file = v
		}
	}

	return active, file, nil
}
