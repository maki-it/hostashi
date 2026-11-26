// SPDX-License-Identifier: AGPL-3.0-or-later

package agentapi

import (
	"errors"
	"fmt"
	"regexp"
	"strings"

	"github.com/maki-it/hostashi/internal/agentrpc"
)

// Server settings (ADR-0060): time zone, hostname and reboot. The agent
// reaches systemd-timedated, systemd-hostnamed and systemd-logind over
// D-Bus through internal/sysconf; nothing here names a method or a path.

// MaxHostnameLen is the kernel's limit for a hostname (HOST_NAME_MAX).
const MaxHostnameLen = 64

// MaxRebootPackages bounds the packages listed as waiting for a reboot.
const MaxRebootPackages = 64

var hostLabelRe = regexp.MustCompile(`^[a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?$`)

var (
	errHostnameEmpty = errors.New("hostname: required")
	errTimezoneEmpty = errors.New("timezone: required")
)

// ValidateHostname checks a hostname: lowercase RFC 1123 labels of 1 to 63
// characters, separated by dots, at most 64 characters in total, no
// trailing dot and not all digits in the last label.
func ValidateHostname(name string) error {
	if name == "" {
		return errHostnameEmpty
	}

	if len(name) > MaxHostnameLen {
		return fmt.Errorf("hostname: longer than %d characters", MaxHostnameLen)
	}

	labels := strings.Split(name, ".")
	for _, l := range labels {
		if !hostLabelRe.MatchString(l) {
			return fmt.Errorf("hostname: %q is not a valid name (lowercase letters, digits and hyphens, separated by dots)", truncate(name, 64))
		}
	}

	if strings.Trim(labels[len(labels)-1], "0123456789") == "" {
		return fmt.Errorf("hostname: %q looks like an address", truncate(name, 64))
	}

	return nil
}

// ShortHostname is the first label of name.
func ShortHostname(name string) string {
	short, _, _ := strings.Cut(name, ".")

	return short
}

// ValidateTimezone checks the syntax of a zone name with the rule of the
// PHP settings (timezoneRe). The agent checks again that the server's
// tzdata knows it.
func ValidateTimezone(zone string) error {
	if zone == "" {
		return errTimezoneEmpty
	}

	if len(zone) > maxTimezoneLen || !timezoneRe.MatchString(zone) || zone == "Local" {
		return fmt.Errorf("timezone: %q is not a zone name such as Europe/Berlin", truncate(zone, 64))
	}

	return nil
}

// SystemInfoRequest has no parameters.
type SystemInfoRequest struct{}

// Validate accepts the empty request.
func (SystemInfoRequest) Validate() error { return nil }

// SystemInfo is the server's current settings.
type SystemInfo struct {
	// Hostname is the static hostname (/etc/hostname), Transient the one
	// the kernel uses now; they differ until the change is complete.
	Hostname  string `json:"hostname"`
	Transient string `json:"transientHostname"`
	Timezone  string `json:"timezone"`
	// Time is the server's clock in its zone, RFC 3339.
	Time          string  `json:"time"`
	UptimeSeconds float64 `json:"uptimeSeconds"`
	// NTPSynchronized reports whether the clock is synchronized.
	NTPSynchronized bool `json:"ntpSynchronized"`
	// RebootRequired reports /run/reboot-required; RebootPackages are the
	// packages in /run/reboot-required.pkgs.
	RebootRequired bool     `json:"rebootRequired"`
	RebootPackages []string `json:"rebootPackages,omitempty"`
	// CloudInit reports that cloud-init is installed; the agent then keeps
	// it from setting the hostname back at boot.
	CloudInit bool `json:"cloudInit"`
	// Updates is the state of the hostashi package (ADR-0050); absent
	// when the server was not set up from the package.
	Updates *SystemUpdates `json:"updates,omitempty"`
}

// SystemUpdates says which hostashi package is installed, which one apt
// last saw in the Hostashi repository, and whether unattended-upgrades
// installs new ones by itself (--auto-update).
type SystemUpdates struct {
	Installed string `json:"installed"`
	// Available is empty while apt has no list of the repository.
	Available string `json:"available,omitempty"`
	Update    bool   `json:"update"`
	Auto      bool   `json:"auto"`
	// ListedAt is when apt last fetched the list, RFC 3339 UTC.
	ListedAt string `json:"listedAt,omitempty"`
}

// SystemTimezonesRequest has no parameters.
type SystemTimezonesRequest struct{}

// Validate accepts the empty request.
func (SystemTimezonesRequest) Validate() error { return nil }

// SystemTimezones lists the zones the server's tzdata knows, sorted.
type SystemTimezones struct {
	Zones []string `json:"zones"`
}

// SystemTimezoneRequest sets the time zone.
type SystemTimezoneRequest struct {
	Zone string `json:"zone"`
}

// Validate checks the zone name.
func (r SystemTimezoneRequest) Validate() error { return ValidateTimezone(r.Zone) }

// SystemHostnameRequest sets the static and the transient hostname.
type SystemHostnameRequest struct {
	Name string `json:"name"`
}

// Validate checks the hostname.
func (r SystemHostnameRequest) Validate() error { return ValidateHostname(r.Name) }

// SystemRebootRequest has no parameters: the panel checks the confirmation.
type SystemRebootRequest struct{}

// Validate accepts the empty request.
func (SystemRebootRequest) Validate() error { return nil }

// SystemAutoUpdateRequest turns the automatic installation of new Hostashi
// versions on or off (ADR-0050).
type SystemAutoUpdateRequest struct {
	On bool `json:"on"`
}

// Validate accepts both values.
func (SystemAutoUpdateRequest) Validate() error { return nil }

// SystemRebootResponse says when the agent asks systemd to reboot.
type SystemRebootResponse struct {
	DelaySeconds int `json:"delaySeconds"`
}

// The operations of ADR-0060.
var (
	SystemInfoOp      = agentrpc.Op[SystemInfoRequest, SystemInfo]{Name: "system.info"}
	SystemTimezonesOp = agentrpc.Op[SystemTimezonesRequest, SystemTimezones]{Name: "system.timezones"}
	SystemTimezoneSet = agentrpc.Op[SystemTimezoneRequest, SystemInfo]{Name: "system.timezone.set"}
	SystemHostnameSet = agentrpc.Op[SystemHostnameRequest, SystemInfo]{Name: "system.hostname.set"}
	SystemReboot      = agentrpc.Op[SystemRebootRequest, SystemRebootResponse]{Name: "system.reboot"}
	// SystemAutoUpdateSet is ADR-0050's --auto-update as a switch.
	SystemAutoUpdateSet = agentrpc.Op[SystemAutoUpdateRequest, SystemInfo]{Name: "system.autoupdate.set"}
)
