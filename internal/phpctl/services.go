// SPDX-License-Identifier: AGPL-3.0-or-later

package phpctl

import (
	"context"
	"fmt"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/maki-it/hostashi/internal/agentapi"
)

// Service management (ADR-0052). The agent maps a catalog row of
// agentapi.Services to a unit; this file checks that unit again against the
// catalog and the per-site patterns before systemctl sees it, and maps each
// action to one fixed verb.

var valkeyUnitRe = regexp.MustCompile(`^hostashi-valkey@[a-z][a-z0-9_-]{1,31}\.service$`)

// serviceUnitAllowed reports whether unit belongs to the service catalog.
func serviceUnitAllowed(unit string) bool {
	if unitRe.MatchString(unit) || valkeyUnitRe.MatchString(unit) {
		return true
	}

	return slices.ContainsFunc(agentapi.Services, func(s agentapi.Service) bool { return s.Unit != "" && s.Unit == unit })
}

// serviceVerbs maps an action to systemctl's verb. Reload uses
// "reload": a unit that is not running stays off instead of being started.
var serviceVerbs = map[string]string{
	agentapi.ServiceStart:   "start",
	agentapi.ServiceStop:    "stop",
	agentapi.ServiceRestart: "restart",
	agentapi.ServiceReload:  "reload",
}

// showProps are the properties services.status reads, in this order.
var showProps = []string{
	"Id", "LoadState", "ActiveState", "SubState", "UnitFileState", "Result",
	"NRestarts", "MainPID", "MemoryCurrent", "ActiveEnterTimestamp", "ExecMainStartTimestamp",
}

// MaxShowUnits bounds one systemctl show call.
const MaxShowUnits = 64

// Services reads and drives the units of the service catalog.
type Services struct{}

// Show returns the state of each unit, in the order given. A unit systemd
// does not know comes back with Installed false.
func (Services) Show(ctx context.Context, units []string) ([]agentapi.ServiceState, error) {
	if len(units) == 0 {
		return []agentapi.ServiceState{}, nil
	}

	if len(units) > MaxShowUnits {
		return nil, fmt.Errorf("phpctl: more than %d units", MaxShowUnits)
	}

	for _, u := range units {
		if !serviceUnitAllowed(u) {
			return nil, fmt.Errorf("phpctl: refusing unit name %q", u)
		}
	}

	args := []string{"show", "--timestamp=unix", "--property=" + strings.Join(showProps, ",")}
	args = append(args, "--")
	args = append(args, units...)

	out, err := output(ctx, Systemctl, args...)
	if err != nil {
		return nil, err
	}

	return ParseShow(out, units)
}

// Action runs one catalog action on unit. The caller has checked the
// catalog row and run any config test.
func (Services) Action(ctx context.Context, unit, action string) error {
	if !serviceUnitAllowed(unit) {
		return fmt.Errorf("phpctl: refusing unit name %q", unit)
	}

	verb, ok := serviceVerbs[action]
	if !ok {
		return fmt.Errorf("phpctl: refusing action %q", action)
	}

	return run(ctx, Systemctl, verb, "--", unit)
}

// Sshd is the OpenSSH server binary (Debian 13 and Ubuntu 26.04).
const Sshd = "/usr/sbin/sshd"

// TestSSHD runs sshd -t, which checks the server config and keys and
// changes nothing.
func (Services) TestSSHD(ctx context.Context) error {
	return run(ctx, Sshd, "-t")
}

var (
	loadStates   = []string{"loaded", "not-found", "bad-setting", "error", "merged", "stub", "masked"}
	activeStates = []string{"active", "reloading", "inactive", "failed", "activating", "deactivating", "maintenance", "refreshing"}
	fileStates   = []string{"enabled", "enabled-runtime", "linked", "linked-runtime", "alias", "masked", "masked-runtime", "static", "disabled", "indirect", "generated", "transient", "bad", ""}
	subStateRe   = regexp.MustCompile(`^[a-z][a-z-]{0,31}$`)
	resultRe     = regexp.MustCompile(`^[a-z][a-z-]{0,31}$`)
)

// parseShowTime reads a timestamp as systemctl --timestamp=unix prints
// it ("@1763737445"); empty, "n/a" and anything else is zero.
func parseShowTime(v string) time.Time {
	n, ok := strings.CutPrefix(v, "@")
	if !ok {
		return time.Time{}
	}

	sec, err := strconv.ParseInt(n, 10, 64)
	if err != nil || sec <= 0 || sec > 1<<40 {
		return time.Time{}
	}

	return time.Unix(sec, 0).UTC()
}

// ParseShow parses the output of systemctl show for several units: one
// block of key=value lines per unit, separated by an empty line, in the
// order of units. Unknown keys and odd values are dropped.
func ParseShow(out string, units []string) ([]agentapi.ServiceState, error) {
	blocks := strings.Split(strings.TrimRight(out, "\n"), "\n\n")
	if len(blocks) != len(units) {
		return nil, fmt.Errorf("phpctl: systemctl show returned %d blocks for %d units", len(blocks), len(units))
	}

	res := make([]agentapi.ServiceState, 0, len(units))

	for i, b := range blocks {
		st := agentapi.ServiceState{Unit: units[i]}

		for l := range strings.SplitSeq(b, "\n") {
			k, v, ok := strings.Cut(l, "=")
			if !ok {
				continue
			}

			switch k {
			case "Id":
				if v != units[i] {
					return nil, fmt.Errorf("phpctl: systemctl show block %d is for another unit", i)
				}
			case "LoadState":
				if slices.Contains(loadStates, v) {
					st.Load = v
				}
			case "ActiveState":
				if slices.Contains(activeStates, v) {
					st.Active = v
				}
			case "SubState":
				if subStateRe.MatchString(v) {
					st.Sub = v
				}
			case "UnitFileState":
				if slices.Contains(fileStates, v) {
					st.UnitFile = v
				}
			case "Result":
				if resultRe.MatchString(v) {
					st.Result = v
				}
			case "NRestarts":
				if n, err := strconv.ParseUint(v, 10, 32); err == nil {
					st.NRestarts = uint32(n)
				}
			case "MainPID":
				if n, err := strconv.ParseUint(v, 10, 32); err == nil {
					st.MainPID = uint32(n)
				}
			case "MemoryCurrent":
				// "[not set]" or 2^64-1 mean no number.
				if n, err := strconv.ParseUint(v, 10, 64); err == nil && n != ^uint64(0) {
					st.MemoryBytes, st.MemoryKnown = n, true
				}
			case "ActiveEnterTimestamp":
				st.ActiveSince = parseShowTime(v)
			case "ExecMainStartTimestamp":
				st.StartedAt = parseShowTime(v)
			}
		}

		st.Installed = st.Load != "" && st.Load != "not-found"
		if !st.Installed {
			st = agentapi.ServiceState{Unit: units[i]}
		}

		res = append(res, st)
	}

	return res, nil
}
