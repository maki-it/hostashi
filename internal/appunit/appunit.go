// SPDX-License-Identifier: AGPL-3.0-or-later

// Package appunit runs the apps of Node.js sites (ADR-0054): one systemd
// unit hostashi-node@<site>.service per site, from a fixed template, as
// the site user in the site's slice. This package writes two drop-ins for
// the unit, both from typed, checked values only: the slice and the home
// (as for the PHP units), and the app's working directory, command line
// and environment. It keeps the firewall's list of app ports and their
// users, so only nginx and the site user reach an app's port. It starts,
// restarts and stops the unit through a Controller and never runs node
// itself.
package appunit

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync"

	"github.com/maki-it/hostashi/internal/agentapi"
	"github.com/maki-it/hostashi/internal/firewall"
	"github.com/maki-it/hostashi/internal/sites"
	"github.com/maki-it/hostashi/internal/statefile"
)

// Fixed places on a server.
const (
	DefaultUnitDir = "/etc/systemd/system"
	// DefaultNodeDir holds the Node.js lines (internal/nodeinstall).
	DefaultNodeDir = "/opt/hostashi/node"
	// DefaultLogDir holds <site>/app.log, which systemd (as root) opens
	// for the unit's output (StandardOutput=append: in the template).
	DefaultLogDir = "/var/log/hostashi/app"
	// SliceDropIn names the drop-in with the slice and the home, the same
	// name the PHP units use; AppDropIn the one with the app.
	SliceDropIn = "50-hostashi-slice.conf"
	AppDropIn   = "60-hostashi-app.conf"
	stateFile   = "state.json"
)

// Errors of the operations.
var (
	// ErrNoApp: the site has no app.
	ErrNoApp = errors.New("appunit: the site has no app")
	// ErrNodeMissing: the app's Node.js line is not installed.
	ErrNodeMissing = errors.New("appunit: this Node.js version is not installed")
	// ErrStopped: the app is kept stopped (no start command, stopped in
	// the panel or the site is disabled).
	ErrStopped = errors.New("appunit: the app is stopped; start it in the panel")
	// ErrPortTaken: another site's app has the port.
	ErrPortTaken = errors.New("appunit: the port belongs to another site's app")
)

// Controller drives systemd (phpctl.AppController on a server).
type Controller interface {
	DaemonReload(ctx context.Context) error
	// Start enables a unit and starts it; Stop stops and disables it.
	Start(ctx context.Context, unit string) error
	Stop(ctx context.Context, unit string) error
	Restart(ctx context.Context, unit string) error
	ResetFailed(ctx context.Context, unit string) error
	Show(ctx context.Context, unit string, props ...string) (map[string]string, error)
}

// Site is what the manager needs to know of a site's account.
type Site struct {
	Home  string
	UID   uint32
	Slice string
}

// Manager keeps the apps. Set Dir, Site, Ctl and Firewall.
type Manager struct {
	// Dir holds the state (root 0700).
	Dir     string
	UnitDir string // default DefaultUnitDir
	NodeDir string // default DefaultNodeDir
	LogDir  string // default DefaultLogDir
	// Site returns the account of a site user.
	Site func(site string) (Site, error)
	// Hidden returns the pools the unit must hide (sites.HiddenPaths).
	Hidden func() []string
	Ctl    Controller
	// Firewall hands the app ports and their users to the firewall
	// (firewall.Manager.SetApps). firewall.ErrDisabled is logged, not an
	// error: without Hostashi's firewall nothing filters the ports.
	Firewall func(ctx context.Context, owners []firewall.AppOwner) error
	Log      *slog.Logger

	mu sync.Mutex
}

// Unit is the systemd unit of a site's app.
func Unit(site string) string { return "hostashi-node@" + site + ".service" }

func (m *Manager) unitDir() string { return cmpOr(m.UnitDir, DefaultUnitDir) }
func (m *Manager) nodeDir() string { return cmpOr(m.NodeDir, DefaultNodeDir) }
func (m *Manager) logDir() string  { return cmpOr(m.LogDir, DefaultLogDir) }

func cmpOr(a, b string) string {
	if a != "" {
		return a
	}

	return b
}

func (m *Manager) log() *slog.Logger {
	if m.Log != nil {
		return m.Log
	}

	return slog.New(slog.DiscardHandler)
}

func (m *Manager) dropInDir(site string) string {
	return filepath.Join(m.unitDir(), Unit(site)+".d")
}

// entry is the stored app of a site with the uid it was set up for.
type entry struct {
	App agentapi.AppSpec `json:"app"`
	UID uint32           `json:"uid"`
}

func (m *Manager) load() (map[string]entry, error) {
	b, err := os.ReadFile(filepath.Join(m.Dir, stateFile))
	if errors.Is(err, os.ErrNotExist) {
		return map[string]entry{}, nil
	}

	if err != nil {
		return nil, fmt.Errorf("appunit: state: %w", err)
	}

	st := map[string]entry{}
	if err := json.Unmarshal(b, &st); err != nil {
		return nil, fmt.Errorf("appunit: state: %w", err)
	}

	return st, nil
}

func (m *Manager) save(st map[string]entry) error {
	b, err := json.MarshalIndent(st, "", "  ")
	if err != nil {
		return fmt.Errorf("appunit: state: %w", err)
	}

	if err := os.MkdirAll(m.Dir, 0o700); err != nil {
		return fmt.Errorf("appunit: %w", err)
	}

	return statefile.Write(m.Dir, stateFile, append(b, '\n'))
}

// ExecStart is the command line of an app: node or npm of the app's line.
// The fields are checked by AppSpec.Validate: only letters, digits, '.',
// '_', '-', ':' and '/', so nothing systemd would expand or split.
func ExecStart(nodeDir string, a agentapi.AppSpec) (string, error) {
	if err := a.Validate(); err != nil {
		return "", err
	}

	bin := path.Join(nodeDir, a.Node, "bin")

	switch a.StartKind {
	case agentapi.AppStartScript:
		return bin + "/node " + a.Start, nil
	case agentapi.AppStartNPM:
		return bin + "/npm run " + a.Start, nil
	}

	return "", errors.New("appunit: the app has no start command")
}

// SliceDropInContent puts the unit into the site's slice and mounts the
// site's home back into the otherwise hidden /home, writable: the same
// isolation as the PHP units (ADR-0015, ADR-0033).
func SliceDropInContent(site string, acct Site, hidden []string) []byte {
	unit, service := sites.UnitIsolation(acct.Home, hidden)

	return []byte("# Generated by hostashi for site " + site + " (ADR-0015, ADR-0033, ADR-0054). Do not edit: changes are overwritten.\n" +
		"[Unit]\n" + unit +
		"[Service]\nSlice=" + acct.Slice + "\nBindPaths=" + acct.Home + "\nReadWritePaths=" + acct.Home + "\n" + service)
}

// AppDropInContent sets the app's working directory, command line and
// environment.
func AppDropInContent(nodeDir string, a agentapi.AppSpec, acct Site) ([]byte, error) {
	if !filepath.IsAbs(acct.Home) || filepath.Clean(acct.Home) != acct.Home || strings.ContainsAny(acct.Home, " \t\n\"'\\%$") {
		return nil, fmt.Errorf("appunit: invalid home %q", acct.Home)
	}

	var b strings.Builder

	w := func(s ...string) { b.WriteString(strings.Join(s, "") + "\n") }

	w("# Generated by hostashi for site ", a.Site, " (ADR-0054). Do not edit: changes are overwritten.")
	w("[Service]")
	w("WorkingDirectory=", acct.Home, "/", a.Workdir)
	// An empty ExecStart= drops the template's placeholder; a second
	// command would make the unit fail to load.
	w("ExecStart=")

	if a.StartKind != "" {
		exec, err := ExecStart(nodeDir, a)
		if err != nil {
			return nil, err
		}

		w("ExecStart=", exec)
	} else {
		// Never started (Stopped is set); systemd still wants a command.
		w("ExecStart=/bin/false")
	}

	w("Environment=NODE_ENV=production HOST=127.0.0.1 PORT=", strconv.Itoa(a.Port))
	w("Environment=HOME=", acct.Home, " USER=", a.Site, " LOGNAME=", a.Site)
	w("Environment=PATH=", path.Join(nodeDir, a.Node, "bin"), ":/usr/local/bin:/usr/bin:/bin")

	return []byte(b.String()), nil
}

// installed reports whether a line's node binary is there.
func (m *Manager) installed(major string) bool {
	st, err := os.Stat(filepath.Join(m.nodeDir(), major, "bin", "node"))

	return err == nil && st.Mode().IsRegular()
}

// owners lists the firewall entries of st.
func owners(st map[string]entry) []firewall.AppOwner {
	out := make([]firewall.AppOwner, 0, len(st))
	for _, e := range st {
		out = append(out, firewall.AppOwner{Port: e.App.Port, UID: e.UID})
	}

	slices.SortFunc(out, func(a, b firewall.AppOwner) int { return a.Port - b.Port })

	return out
}

func (m *Manager) setFirewall(ctx context.Context, st map[string]entry) error {
	if m.Firewall == nil {
		return nil
	}

	err := m.Firewall(ctx, owners(st))
	if errors.Is(err, firewall.ErrDisabled) {
		m.log().WarnContext(ctx, "Hostashi's firewall is off: other users on this server can reach the apps' ports")

		return nil
	}

	return err
}

// Apply sets up or changes the app of a site and starts, restarts or stops
// it as the spec says. A running app restarts only when its unit changed.
func (m *Manager) Apply(ctx context.Context, a agentapi.AppSpec) (agentapi.AppStatus, error) {
	if err := a.Validate(); err != nil {
		return agentapi.AppStatus{}, err
	}

	if !m.installed(a.Node) {
		return agentapi.AppStatus{}, fmt.Errorf("%w: Node.js %s", ErrNodeMissing, a.Node)
	}

	acct, err := m.Site(a.Site)
	if err != nil {
		return agentapi.AppStatus{}, err
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	st, err := m.load()
	if err != nil {
		return agentapi.AppStatus{}, err
	}

	for s, e := range st {
		if s != a.Site && e.App.Port == a.Port {
			return agentapi.AppStatus{}, fmt.Errorf("%w: %d (%s)", ErrPortTaken, a.Port, s)
		}
	}

	prev, had := st[a.Site]

	changed, err := m.writeUnit(ctx, a, acct)
	if err != nil {
		return agentapi.AppStatus{}, err
	}

	st[a.Site] = entry{App: a, UID: acct.UID}

	if err := m.save(st); err != nil {
		return agentapi.AppStatus{}, err
	}

	// The filter knows the port before the app listens on it.
	if err := m.setFirewall(ctx, st); err != nil {
		if had {
			st[a.Site] = prev
		} else {
			delete(st, a.Site)
		}

		_ = m.save(st)

		return agentapi.AppStatus{}, err
	}

	unit := Unit(a.Site)

	switch {
	case a.Stopped:
		err = m.Ctl.Stop(ctx, unit)
	case changed || prev.App.Stopped:
		_ = m.Ctl.ResetFailed(ctx, unit)

		if err = m.Ctl.Restart(ctx, unit); err == nil {
			err = m.Ctl.Start(ctx, unit) // enable: it comes back after a reboot
		}
	default:
		err = m.Ctl.Start(ctx, unit)
	}

	if err != nil {
		m.log().ErrorContext(ctx, "app unit", "site", a.Site, "err", err)

		return agentapi.AppStatus{}, fmt.Errorf("appunit: site %s: %w", a.Site, err)
	}

	m.log().InfoContext(ctx, "app applied", "site", a.Site, "node", a.Node, "port", a.Port, "stopped", a.Stopped)

	return m.status(ctx, a.Site), nil
}

// writeUnit writes the log directory and both drop-ins and reports
// whether a drop-in changed; systemd has read them when it returns.
func (m *Manager) writeUnit(ctx context.Context, a agentapi.AppSpec, acct Site) (bool, error) {
	var hidden []string
	if m.Hidden != nil {
		hidden = m.Hidden()
	}

	app, err := AppDropInContent(m.nodeDir(), a, acct)
	if err != nil {
		return false, err
	}

	// systemd opens the log as root before it starts the app; the site
	// user can neither open nor change it (like the PHP error log).
	if err := os.MkdirAll(filepath.Join(m.logDir(), a.Site), 0o750); err != nil {
		return false, fmt.Errorf("appunit: %w", err)
	}

	dir := m.dropInDir(a.Site)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return false, fmt.Errorf("appunit: %w", err)
	}

	c1, err := writeIfChanged(filepath.Join(dir, SliceDropIn), SliceDropInContent(a.Site, acct, hidden))
	if err != nil {
		return false, err
	}

	c2, err := writeIfChanged(filepath.Join(dir, AppDropIn), app)
	if err != nil {
		return false, err
	}

	if c1 || c2 {
		if err := m.Ctl.DaemonReload(ctx); err != nil {
			return false, err
		}
	}

	return c1 || c2, nil
}

// Remove stops a site's app and deletes its drop-ins and state, for a type
// change or the delete of the site. A site without one is not an error.
// The log stays until the site is deleted (sites removes its logs).
func (m *Manager) Remove(ctx context.Context, site string) error {
	if err := agentapi.ValidateSiteUser(site); err != nil {
		return err
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	st, err := m.load()
	if err != nil {
		return err
	}

	if _, ok := st[site]; !ok {
		return nil
	}

	if err := m.Ctl.Stop(ctx, Unit(site)); err != nil {
		return err
	}

	dir := m.dropInDir(site)
	for _, f := range []string{SliceDropIn, AppDropIn} {
		if err := os.Remove(filepath.Join(dir, f)); err != nil && !errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("appunit: %w", err)
		}
	}

	if err := os.Remove(dir); err != nil && !errors.Is(err, os.ErrNotExist) {
		m.log().InfoContext(ctx, "app drop-in directory not empty, kept", "site", site, "dir", dir)
	}

	if err := m.Ctl.DaemonReload(ctx); err != nil {
		m.log().ErrorContext(ctx, "daemon-reload after removing an app", "site", site, "err", err)
	}

	delete(st, site)

	if err := m.save(st); err != nil {
		return err
	}

	if err := m.setFirewall(ctx, st); err != nil {
		m.log().ErrorContext(ctx, "firewall after removing an app", "site", site, "err", err)
	}

	m.log().InfoContext(ctx, "app removed", "site", site)

	return nil
}

// Action starts, stops or restarts a site's app now. An app that is kept
// stopped cannot be started here: the panel changes that with Apply.
func (m *Manager) Action(ctx context.Context, site, action string) (agentapi.AppStatus, error) {
	if err := (agentapi.AppActionRequest{Site: site, Action: action}).Validate(); err != nil {
		return agentapi.AppStatus{}, err
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	st, err := m.load()
	if err != nil {
		return agentapi.AppStatus{}, err
	}

	e, ok := st[site]

	switch {
	case !ok:
		return agentapi.AppStatus{}, ErrNoApp
	case e.App.Stopped && action != agentapi.AppActionStop:
		return agentapi.AppStatus{}, ErrStopped
	}

	unit := Unit(site)

	switch action {
	case agentapi.AppActionStop:
		err = m.Ctl.Stop(ctx, unit)
	case agentapi.AppActionStart:
		_ = m.Ctl.ResetFailed(ctx, unit)
		err = m.Ctl.Start(ctx, unit)
	default:
		_ = m.Ctl.ResetFailed(ctx, unit)
		err = m.Ctl.Restart(ctx, unit)
	}

	if err != nil {
		return agentapi.AppStatus{}, fmt.Errorf("appunit: site %s: %w", site, err)
	}

	m.log().InfoContext(ctx, "app "+action, "site", site)

	return m.status(ctx, site), nil
}

// Status reports a site's app; a site without one has State "".
func (m *Manager) Status(ctx context.Context, site string) (agentapi.AppStatus, error) {
	if err := agentapi.ValidateSiteUser(site); err != nil {
		return agentapi.AppStatus{}, err
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	st, err := m.load()
	if err != nil {
		return agentapi.AppStatus{}, err
	}

	if _, ok := st[site]; !ok {
		return agentapi.AppStatus{Site: site}, nil
	}

	return m.status(ctx, site), nil
}

func (m *Manager) status(ctx context.Context, site string) agentapi.AppStatus {
	out := agentapi.AppStatus{Site: site}

	props, err := m.Ctl.Show(ctx, Unit(site), "ActiveState", "SubState", "NRestarts", "MemoryCurrent")
	if err != nil {
		m.log().ErrorContext(ctx, "app state", "site", site, "err", err)

		return out
	}

	out.State, out.SubState = props["ActiveState"], props["SubState"]
	out.Restarts, _ = strconv.Atoi(props["NRestarts"])

	if n, err := strconv.ParseUint(props["MemoryCurrent"], 10, 64); err == nil {
		out.UsedBytes = n
	}

	return out
}

// Recover writes the drop-ins and the firewall's list again and starts
// every app that is not kept stopped. systemd starts enabled units at boot
// anyway; this repairs lost drop-ins and a changed uid or home.
func (m *Manager) Recover(ctx context.Context) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	st, err := m.load()
	if err != nil {
		return err
	}

	names := make([]string, 0, len(st))
	for s := range st {
		names = append(names, s)
	}

	slices.Sort(names)

	var errs []error

	for _, s := range names {
		e := st[s]

		acct, err := m.Site(s)
		if err != nil {
			errs = append(errs, fmt.Errorf("appunit: site %s: %w", s, err))

			continue
		}

		e.UID = acct.UID
		st[s] = e

		if _, err := m.writeUnit(ctx, e.App, acct); err != nil {
			errs = append(errs, err)
		}
	}

	if err := m.save(st); err != nil {
		return err
	}

	if err := m.setFirewall(ctx, st); err != nil {
		return errors.Join(append(errs, err)...)
	}

	for _, s := range names {
		if st[s].App.Stopped || !m.installed(st[s].App.Node) {
			continue
		}

		if err := m.Ctl.Start(ctx, Unit(s)); err != nil {
			errs = append(errs, err)
		}
	}

	return errors.Join(errs...)
}

// writeIfChanged replaces name with data (mode 0644) unless it already
// holds exactly that, and reports whether it wrote.
func writeIfChanged(name string, data []byte) (bool, error) {
	if old, err := os.ReadFile(name); err == nil && bytes.Equal(old, data) {
		return false, nil
	}

	tmp := name + ".tmp"
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		return false, fmt.Errorf("appunit: %w", err)
	}

	if err := os.Chmod(tmp, 0o644); err != nil {
		return false, fmt.Errorf("appunit: %w", err)
	}

	if err := os.Rename(tmp, name); err != nil {
		return false, fmt.Errorf("appunit: %w", err)
	}

	return true, nil
}
