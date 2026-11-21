// SPDX-License-Identifier: AGPL-3.0-or-later

package agentapi

import (
	"errors"
	"fmt"
	"slices"
	"time"

	"github.com/maki-it/hostashi/internal/agentrpc"
)

// Service actions (ADR-0052). The agent maps each to one fixed systemctl
// verb; there is no other way to reach systemd through it.
const (
	ServiceStart   = "start"
	ServiceStop    = "stop"
	ServiceRestart = "restart"
	ServiceReload  = "reload"
)

// Service is one row of the fixed service catalog (ADR-0052). The panel
// names a service by ID (and a site for per-site services), never by unit.
type Service struct {
	ID string `json:"id"`
	// PerSite services have one unit per site; Site must be set.
	PerSite bool `json:"perSite,omitempty"`
	// Actions lists the actions the panel may ask for, in a fixed order.
	Actions []string `json:"actions"`
	// Confirm lists the actions the UI must have the admin confirm by
	// typing; the agent does not check it, the panel does.
	Confirm []string `json:"confirm,omitempty"`
	// Unit is the systemd unit for a system service; empty for per-site
	// services, whose unit the agent builds from the site's state.
	Unit string `json:"unit,omitempty"`
}

// Allows reports whether the catalog row permits the action.
func (s Service) Allows(action string) bool { return slices.Contains(s.Actions, action) }

// Services is the catalog, in the order the panel shows it. No stop for
// nginx (the panel sits behind it, ADR-0042), SSH and the firewall; the
// panel and the agent are status only. Per-site PHP and Valkey are turned
// off through their own switches, which also change the stored state.
var Services = []Service{
	{ID: "nginx", Unit: "nginx.service", Actions: []string{ServiceStart, ServiceRestart, ServiceReload}},
	{ID: "mariadb", Unit: "mariadb.service", Actions: []string{ServiceStart, ServiceStop, ServiceRestart}, Confirm: []string{ServiceStop}},
	{ID: "ssh", Unit: "ssh.service", Actions: []string{ServiceReload}},
	{ID: "fail2ban", Unit: "fail2ban.service", Actions: []string{ServiceStart, ServiceStop, ServiceRestart, ServiceReload}},
	{ID: "firewall", Unit: "hostashi-firewall.service", Actions: []string{ServiceReload}},
	{ID: "adminer", Unit: "hostashi-adminer.service", Actions: []string{ServiceStart, ServiceStop, ServiceRestart}},
	{ID: "panel", Unit: "hostashi.service", Actions: []string{}},
	{ID: "agent", Unit: "hostashi-agent.service", Actions: []string{}},
	{ID: "php", PerSite: true, Actions: []string{ServiceStart, ServiceRestart, ServiceReload}},
	{ID: "valkey", PerSite: true, Actions: []string{ServiceStart, ServiceRestart}},
}

// LookupService returns the catalog row of id.
func LookupService(id string) (Service, bool) {
	i := slices.IndexFunc(Services, func(s Service) bool { return s.ID == id })
	if i < 0 {
		return Service{}, false
	}

	return Services[i], true
}

var errServiceSite = errors.New("site: required for a per-site service and not allowed otherwise")

// ValidateServiceRef checks a service ID and the site that goes with it.
func ValidateServiceRef(id, site string) error {
	s, ok := LookupService(id)
	if !ok {
		return fmt.Errorf("service: unknown %q", truncate(id, 32))
	}

	if s.PerSite != (site != "") {
		return errServiceSite
	}

	if site != "" {
		return ValidateSiteUser(site)
	}

	return nil
}

// ServiceState is what systemd says about one unit. Every field comes from
// a fixed property and is checked by the agent; unknown values are dropped.
type ServiceState struct {
	ID   string `json:"id"`
	Site string `json:"site,omitempty"`
	Unit string `json:"unit"`
	// Installed is false when systemd does not know the unit (LoadState
	// "not-found"); the other fields are then empty.
	Installed bool   `json:"installed"`
	Load      string `json:"load,omitempty"`      // LoadState
	Active    string `json:"active,omitempty"`    // ActiveState
	Sub       string `json:"sub,omitempty"`       // SubState
	UnitFile  string `json:"unitFile,omitempty"`  // UnitFileState
	Result    string `json:"result,omitempty"`    // Result of the last run
	NRestarts uint32 `json:"nRestarts,omitempty"` // automatic restarts systemd counted
	MainPID   uint32 `json:"mainPid,omitempty"`
	// MemoryBytes is MemoryCurrent; MemoryKnown is false when systemd has
	// no number (no memory accounting, unit not running).
	MemoryBytes uint64 `json:"memoryBytes,omitempty"`
	MemoryKnown bool   `json:"memoryKnown,omitempty"`
	// ActiveSince is ActiveEnterTimestamp, StartedAt is the start of the
	// current main process (ExecMainStartTimestamp); zero for never.
	ActiveSince time.Time `json:"activeSince,omitzero"`
	StartedAt   time.Time `json:"startedAt,omitzero"`
}

// ServicesStatusRequest asks for every catalog unit (ID empty) or one
// service. For all units, the per-site services of every site with PHP or
// an object cache are included.
type ServicesStatusRequest struct {
	ID   string `json:"id,omitempty"`
	Site string `json:"site,omitempty"`
}

// Validate checks the optional service reference.
func (r ServicesStatusRequest) Validate() error {
	if r.ID == "" {
		if r.Site != "" {
			return errServiceSite
		}

		return nil
	}

	return ValidateServiceRef(r.ID, r.Site)
}

// MaxServiceStates bounds the answer: the system services plus two per
// site.
const MaxServiceStates = 16 + 2*MaxSites

// ServicesStatusResponse lists the states in catalog order, per-site
// services sorted by site.
type ServicesStatusResponse struct {
	Services []ServiceState `json:"services"`
}

// ServiceActionRequest asks for one action on one service.
type ServiceActionRequest struct {
	ID     string `json:"id"`
	Site   string `json:"site,omitempty"`
	Action string `json:"action"`
}

// Validate checks the reference and that the catalog permits the action.
func (r ServiceActionRequest) Validate() error {
	if err := ValidateServiceRef(r.ID, r.Site); err != nil {
		return err
	}

	s, _ := LookupService(r.ID)
	if !s.Allows(r.Action) {
		return fmt.Errorf("action: %q is not allowed for %s", truncate(r.Action, 16), r.ID)
	}

	return nil
}

// Service operations (ADR-0052). services.status only reads;
// services.action runs one catalog action and answers the new state.
var (
	ServicesStatus = agentrpc.Op[ServicesStatusRequest, ServicesStatusResponse]{Name: "services.status"}
	ServiceAction  = agentrpc.Op[ServiceActionRequest, ServiceState]{Name: "services.action"}
)

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}

	return s[:n] + "..."
}
