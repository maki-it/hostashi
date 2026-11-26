// SPDX-License-Identifier: AGPL-3.0-or-later

package agent

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/maki-it/hostashi/internal/agentapi"
	"github.com/maki-it/hostashi/internal/agentrpc"
	"github.com/maki-it/hostashi/internal/sysconf"
	"github.com/maki-it/hostashi/internal/updates"
)

// SystemCtl reaches timedated, hostnamed and logind (sysconf.Client in
// production, ADR-0060).
type SystemCtl interface {
	State(ctx context.Context) (sysconf.State, error)
	Timezones(ctx context.Context) ([]string, error)
	SetTimezone(ctx context.Context, zone string) error
	SetHostname(ctx context.Context, name string) error
	Reboot(ctx context.Context) error
}

// RebootDelay is the time between the answer to system.reboot and the
// call to logind, so the answer reaches the panel and the browser.
const RebootDelay = 2 * time.Second

// Files the system operations read or write, relative to systemOps.root.
const (
	etcHosts           = "/etc/hosts"
	cloudDir           = "/etc/cloud"
	cloudHostnameCfg   = "/etc/cloud/cloud.cfg.d/90-hostashi-hostname.cfg"
	procUptime         = "/proc/uptime"
	rebootRequired     = "/run/reboot-required"
	rebootRequiredPkgs = "/run/reboot-required.pkgs"
	debianHostsAddress = "127.0.1.1"
)

// cloudHostnameContent keeps cloud-init from setting the hostname and
// /etc/hosts back to the cloud's server name at boot.
const cloudHostnameContent = `# Written by Hostashi (ADR-0060): the hostname is set in the panel.
preserve_hostname: true
manage_etc_hosts: false
`

// systemOps serves the operations of ADR-0060.
type systemOps struct {
	ctl  SystemCtl
	root string // "" in production; a temporary directory in tests
	log  *slog.Logger
	now  func() time.Time
	// after schedules the reboot; time.AfterFunc in production.
	after func(time.Duration, func())

	mu        sync.Mutex // orders hostname and time zone changes
	rebooting bool
}

var (
	errNoSystem    = &agentrpc.Error{Code: agentrpc.CodePrecondition, Message: "server settings are not available on this server"}
	errNoBus       = &agentrpc.Error{Code: agentrpc.CodePrecondition, Message: "the D-Bus system bus is not running; install and start dbus"}
	errZoneUnknown = &agentrpc.Error{Code: agentrpc.CodeInvalidArgument, Message: "timezone: the server's time zone database does not know this zone"}
	errRebooting   = &agentrpc.Error{Code: agentrpc.CodeConflict, Message: "the server is already rebooting"}
	errSystemCall  = &agentrpc.Error{Code: agentrpc.CodeInternal, Message: "systemd did not complete the change, see the agent's log"}
)

func registerSystem(s *agentrpc.Server, o *systemOps) {
	agentrpc.Handle(s, agentapi.SystemInfoOp, func(ctx context.Context, _ agentrpc.Caller, _ agentapi.SystemInfoRequest) (agentapi.SystemInfo, error) {
		if o.ctl == nil {
			return agentapi.SystemInfo{}, errNoSystem
		}

		return o.info(ctx)
	})

	agentrpc.Handle(s, agentapi.SystemTimezonesOp, func(ctx context.Context, _ agentrpc.Caller, _ agentapi.SystemTimezonesRequest) (agentapi.SystemTimezones, error) {
		if o.ctl == nil {
			return agentapi.SystemTimezones{}, errNoSystem
		}

		zones, err := o.ctl.Timezones(ctx)
		if err != nil {
			return agentapi.SystemTimezones{}, o.fail(ctx, "list time zones", err)
		}

		return agentapi.SystemTimezones{Zones: zones}, nil
	})

	agentrpc.Handle(s, agentapi.SystemTimezoneSet, func(ctx context.Context, _ agentrpc.Caller, req agentapi.SystemTimezoneRequest) (agentapi.SystemInfo, error) {
		if o.ctl == nil {
			return agentapi.SystemInfo{}, errNoSystem
		}

		return o.setTimezone(ctx, req.Zone)
	})

	agentrpc.Handle(s, agentapi.SystemHostnameSet, func(ctx context.Context, _ agentrpc.Caller, req agentapi.SystemHostnameRequest) (agentapi.SystemInfo, error) {
		if o.ctl == nil {
			return agentapi.SystemInfo{}, errNoSystem
		}

		return o.setHostname(ctx, req.Name)
	})

	agentrpc.Handle(s, agentapi.SystemReboot, func(ctx context.Context, _ agentrpc.Caller, _ agentapi.SystemRebootRequest) (agentapi.SystemRebootResponse, error) {
		if o.ctl == nil {
			return agentapi.SystemRebootResponse{}, errNoSystem
		}

		return o.reboot(ctx)
	})
}

func (o *systemOps) path(p string) string { return filepath.Join(o.root, p) }

// fail logs err and maps it to an answer without the bus's details.
func (o *systemOps) fail(ctx context.Context, what string, err error) error {
	o.log.ErrorContext(ctx, "system: "+what, "err", err)

	if errors.Is(err, sysconf.ErrNoBus) {
		return errNoBus
	}

	return errSystemCall
}

func (o *systemOps) info(ctx context.Context) (agentapi.SystemInfo, error) {
	st, err := o.ctl.State(ctx)
	if err != nil {
		return agentapi.SystemInfo{}, o.fail(ctx, "read state", err)
	}

	loc, err := time.LoadLocation(st.Timezone)
	if err != nil || st.Timezone == "" {
		loc = time.UTC
	}

	info := agentapi.SystemInfo{
		Hostname:        st.StaticHostname,
		Transient:       st.Hostname,
		Timezone:        st.Timezone,
		Time:            o.now().In(loc).Format(time.RFC3339),
		NTPSynchronized: st.NTPSynchronized,
	}

	if info.Hostname == "" {
		// No /etc/hostname: hostnamed reports the transient one only.
		info.Hostname = st.Hostname
	}

	info.UptimeSeconds = o.uptime()

	if _, err := os.Stat(o.path(rebootRequired)); err == nil {
		info.RebootRequired = true
		info.RebootPackages = o.rebootPackages()
	}

	if st, err := os.Stat(o.path(cloudDir)); err == nil && st.IsDir() {
		info.CloudInit = true
	}

	info.Updates = o.updates(ctx)

	return info, nil
}

// updates reads the hostashi package's update state; nil when it is not
// installed or the files cannot be read (the doctor reports that).
func (o *systemOps) updates(ctx context.Context) *agentapi.SystemUpdates {
	st, err := updates.Read(updates.Paths{
		DpkgStatus: o.path("/var/lib/dpkg/status"),
		Sources:    o.path("/etc/apt/sources.list.d/hostashi.sources"),
		Lists:      o.path("/var/lib/apt/lists"),
		Unattended: o.path("/etc/apt/apt.conf.d/52hostashi-unattended-upgrades"),
	})
	if err != nil {
		o.log.WarnContext(ctx, "system: read the update state", "err", err)
	}

	if st.Installed == "" {
		return nil
	}

	u := &agentapi.SystemUpdates{Installed: st.Installed, Available: st.Available, Update: st.Update, Auto: st.Auto}
	if !st.ListedAt.IsZero() {
		u.ListedAt = st.ListedAt.UTC().Format(time.RFC3339)
	}

	return u
}

func (o *systemOps) uptime() float64 {
	b, err := os.ReadFile(o.path(procUptime))
	if err != nil {
		return 0
	}

	f, _, _ := strings.Cut(string(b), " ")

	v, err := strconv.ParseFloat(strings.TrimSpace(f), 64)
	if err != nil || v < 0 {
		return 0
	}

	return v
}

// rebootPackages reads the package names apt's hooks wrote, deduplicated
// and capped.
func (o *systemOps) rebootPackages() []string {
	f, err := os.Open(o.path(rebootRequiredPkgs))
	if err != nil {
		return nil
	}
	defer f.Close()

	var out []string

	sc := bufio.NewScanner(f)
	for sc.Scan() && len(out) < agentapi.MaxRebootPackages {
		p := strings.TrimSpace(sc.Text())
		if p == "" || len(p) > 128 || slices.Contains(out, p) {
			continue
		}

		out = append(out, p)
	}

	return out
}

func (o *systemOps) setTimezone(ctx context.Context, zone string) (agentapi.SystemInfo, error) {
	o.mu.Lock()
	defer o.mu.Unlock()

	zones, err := o.ctl.Timezones(ctx)
	if err != nil {
		return agentapi.SystemInfo{}, o.fail(ctx, "list time zones", err)
	}

	if !slices.Contains(zones, zone) {
		return agentapi.SystemInfo{}, errZoneUnknown
	}

	if err := o.ctl.SetTimezone(ctx, zone); err != nil {
		return agentapi.SystemInfo{}, o.fail(ctx, "set time zone", err)
	}

	o.log.InfoContext(ctx, "system: time zone set", "zone", zone)

	return o.info(ctx)
}

func (o *systemOps) setHostname(ctx context.Context, name string) (agentapi.SystemInfo, error) {
	o.mu.Lock()
	defer o.mu.Unlock()

	if err := o.ctl.SetHostname(ctx, name); err != nil {
		return agentapi.SystemInfo{}, o.fail(ctx, "set hostname", err)
	}

	o.log.InfoContext(ctx, "system: hostname set", "hostname", name)

	if err := o.writeHosts(name); err != nil {
		o.log.ErrorContext(ctx, "system: update /etc/hosts", "err", err)

		return agentapi.SystemInfo{}, &agentrpc.Error{Code: agentrpc.CodeInternal, Message: "the hostname is set, but /etc/hosts could not be updated, see the agent's log"}
	}

	if err := o.writeCloudInit(); err != nil {
		o.log.ErrorContext(ctx, "system: write cloud-init configuration", "err", err)

		return agentapi.SystemInfo{}, &agentrpc.Error{Code: agentrpc.CodeInternal, Message: "the hostname is set, but cloud-init may set it back at the next boot, see the agent's log"}
	}

	return o.info(ctx)
}

// writeHosts points Debian's 127.0.1.1 line at name.
func (o *systemOps) writeHosts(name string) error {
	p := o.path(etcHosts)

	old, err := os.ReadFile(p)
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return err
	}

	mode := fs.FileMode(0o644)
	if st, err := os.Stat(p); err == nil {
		mode = st.Mode().Perm()
	}

	next := updateHosts(old, name)
	if bytes.Equal(old, next) {
		return nil
	}

	err = writeFileAtomic(p, next, mode)
	if errors.Is(err, syscall.EBUSY) {
		// /etc/hosts is a bind mount (containers): a rename over it
		// fails, so the file is rewritten in place.
		return writeInPlace(p, next)
	}

	return err
}

// writeInPlace rewrites an existing file without replacing it.
func writeInPlace(name string, data []byte) error {
	f, err := os.OpenFile(name, os.O_WRONLY|os.O_TRUNC, 0)
	if err != nil {
		return fmt.Errorf("write %s: %w", name, err)
	}

	if _, err := f.Write(data); err != nil {
		_ = f.Close()

		return fmt.Errorf("write %s: %w", name, err)
	}

	if err := f.Sync(); err != nil {
		_ = f.Close()

		return fmt.Errorf("write %s: %w", name, err)
	}

	if err := f.Close(); err != nil {
		return fmt.Errorf("write %s: %w", name, err)
	}

	return nil
}

// writeCloudInit stops cloud-init from undoing the change, if it is
// installed.
func (o *systemOps) writeCloudInit() error {
	st, err := os.Stat(o.path(cloudDir))
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}

	if err != nil {
		return err
	}

	if !st.IsDir() {
		return nil
	}

	p := o.path(cloudHostnameCfg)
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		return err
	}

	if old, err := os.ReadFile(p); err == nil && string(old) == cloudHostnameContent {
		return nil
	}

	return writeFileAtomic(p, []byte(cloudHostnameContent), 0o644)
}

// updateHosts replaces the names of every 127.0.1.1 line with name (and
// its short form for a fully qualified name). Without such a line it adds
// one after the first 127.0.0.1 line, or at the end. Other lines stay as
// they are, comments included.
func updateHosts(old []byte, name string) []byte {
	entry := debianHostsAddress + "\t" + name
	if short := agentapi.ShortHostname(name); short != name {
		entry += " " + short
	}

	text := string(old)
	trailing := strings.HasSuffix(text, "\n") || text == ""
	lines := strings.Split(strings.TrimSuffix(text, "\n"), "\n")

	if text == "" {
		lines = nil
	}

	found, loopback := false, -1

	for i, l := range lines {
		fields := strings.Fields(l)
		if len(fields) == 0 || strings.HasPrefix(fields[0], "#") {
			continue
		}

		switch fields[0] {
		case debianHostsAddress:
			lines[i] = entry
			found = true
		case "127.0.0.1":
			if loopback < 0 {
				loopback = i
			}
		}
	}

	if !found {
		if loopback >= 0 {
			lines = slices.Insert(lines, loopback+1, entry)
		} else {
			lines = append(lines, entry)
		}
	}

	out := strings.Join(lines, "\n")
	if trailing {
		out += "\n"
	}

	return []byte(out)
}

func (o *systemOps) reboot(ctx context.Context) (agentapi.SystemRebootResponse, error) {
	o.mu.Lock()
	defer o.mu.Unlock()

	if o.rebooting {
		return agentapi.SystemRebootResponse{}, errRebooting
	}

	o.rebooting = true

	o.log.WarnContext(ctx, "system: reboot requested", "delay", RebootDelay)

	// The call outlives the request: keep its values, not its deadline.
	later := context.WithoutCancel(ctx)

	o.after(RebootDelay, func() {
		ctx, cancel := context.WithTimeout(later, 30*time.Second)
		defer cancel()

		if err := o.ctl.Reboot(ctx); err != nil {
			o.log.ErrorContext(ctx, "system: reboot", "err", err)

			o.mu.Lock()
			o.rebooting = false
			o.mu.Unlock()
		}
	})

	return agentapi.SystemRebootResponse{DelaySeconds: int(RebootDelay / time.Second)}, nil
}

// renameFile is os.Rename; tests replace it.
var renameFile = os.Rename

// writeFileAtomic replaces name through a synced temporary file in the
// same directory.
func writeFileAtomic(name string, data []byte, mode fs.FileMode) error {
	tmp, err := os.CreateTemp(filepath.Dir(name), "."+filepath.Base(name)+".*")
	if err != nil {
		return fmt.Errorf("write %s: %w", name, err)
	}

	defer func() { _ = os.Remove(tmp.Name()) }()

	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()

		return fmt.Errorf("write %s: %w", name, err)
	}

	if err := tmp.Chmod(mode); err != nil {
		_ = tmp.Close()

		return fmt.Errorf("write %s: %w", name, err)
	}

	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()

		return fmt.Errorf("write %s: %w", name, err)
	}

	if err := tmp.Close(); err != nil {
		return fmt.Errorf("write %s: %w", name, err)
	}

	if err := renameFile(tmp.Name(), name); err != nil {
		return fmt.Errorf("write %s: %w", name, err)
	}

	return nil
}
