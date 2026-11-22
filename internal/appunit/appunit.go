// SPDX-License-Identifier: AGPL-3.0-or-later

// Package appunit runs the apps of Node.js and app sites (ADR-0054): one
// systemd unit hostashi-node@<site>.service or hostashi-app@<site>.service
// per site, from a fixed template, as the site user in the site's slice. This package writes two drop-ins for
// the unit, both from typed, checked values only: the slice and the home
// (as for the PHP units), and the app's working directory, command line
// and environment. It keeps the firewall's list of app ports and their
// users, so only nginx and the site user reach an app's port. It starts,
// restarts and stops the unit through a Controller and never runs node or
// the app itself.
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
	// DefaultPythonDir holds the Python lines (internal/pythoninstall).
	DefaultPythonDir = "/opt/hostashi/python"
	// DefaultLogDir holds <site>/app.log, which systemd (as root) opens
	// for the unit's output (StandardOutput=append: in the template).
	DefaultLogDir = "/var/log/hostashi/app"
	// SliceDropIn names the drop-in with the slice and the home, the same
	// name the PHP units use; AppDropIn the one with the app.
	SliceDropIn = "50-hostashi-slice.conf"
	AppDropIn   = "60-hostashi-app.conf"
	// EnvFile holds the app's own environment variables next to the
	// drop-ins, root-only (0600): systemd reads it as root, and unlike
	// Environment= its values do not show in the unit's properties, which
	// every local user can read. Without the .conf suffix systemd does not
	// take it for a drop-in.
	EnvFile   = "hostashi-app.env"
	stateFile = "state.json"
)

// Errors of the operations.
var (
	// ErrNoApp: the site has no app.
	ErrNoApp = errors.New("appunit: the site has no app")
	// ErrNodeMissing: the app's Node.js line is not installed.
	ErrNodeMissing = errors.New("appunit: this Node.js version is not installed")
	// ErrPythonMissing: the app's Python line is not installed.
	ErrPythonMissing = errors.New("appunit: this Python version is not installed")
	// ErrVenv: the Python environment could not be made.
	ErrVenv = errors.New("appunit: the Python environment could not be created")
	// ErrStopped: the app is kept stopped (no start command, stopped in
	// the panel or the site is disabled).
	ErrStopped = errors.New("appunit: the app is stopped; start it in the panel")
	// ErrPortTaken: another site's app has the port.
	ErrPortTaken = errors.New("appunit: the port belongs to another site's app")
	// ErrProgram: the program file of a binary app is missing, not a
	// regular file, not executable or not the site user's.
	ErrProgram = errors.New("appunit: the program file cannot be started")
)

// Controller drives systemd (phpctl.AppController on a server).
type Controller interface {
	DaemonReload(ctx context.Context) error
	// Start enables a unit and starts it; Stop stops and disables it.
	Start(ctx context.Context, unit string) error
	Stop(ctx context.Context, unit string) error
	Restart(ctx context.Context, unit string) error
	ResetFailed(ctx context.Context, unit string) error
	// Run starts a oneshot unit and waits until it has finished.
	Run(ctx context.Context, unit string) error
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
	// PythonDir holds the Python lines; default DefaultPythonDir.
	PythonDir string
	LogDir    string // default DefaultLogDir
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

// Unit is the systemd unit of a site's Node.js app.
func Unit(site string) string { return "hostashi-node@" + site + ".service" }

// BinaryUnit is the systemd unit of a site's binary app (ADR-0054
// addendum 2).
func BinaryUnit(site string) string { return "hostashi-app@" + site + ".service" }

// PythonUnit is the systemd unit of a site's Python app (ADR-0061).
func PythonUnit(site string) string { return "hostashi-python@" + site + ".service" }

// VenvUnit is the oneshot unit that makes a site's Python environment.
func VenvUnit(site string) string { return "hostashi-python-venv@" + site + ".service" }

// UnitOf is the unit that runs a.
func UnitOf(a agentapi.AppSpec) string {
	if a.IsPython() {
		return PythonUnit(a.Site)
	}

	if a.Binary() {
		return BinaryUnit(a.Site)
	}

	return Unit(a.Site)
}

func (m *Manager) unitDir() string { return cmpOr(m.UnitDir, DefaultUnitDir) }
func (m *Manager) nodeDir() string { return cmpOr(m.NodeDir, DefaultNodeDir) }

func (m *Manager) pythonDir() string { return cmpOr(m.PythonDir, DefaultPythonDir) }
func (m *Manager) logDir() string    { return cmpOr(m.LogDir, DefaultLogDir) }

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

func (m *Manager) dropInDir(unit string) string {
	return filepath.Join(m.unitDir(), unit+".d")
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

// ExecStart is the command line of an app: node or npm of the app's line,
// or the program file below home with its arguments. The fields but the
// arguments are checked by AppSpec.Validate: only letters, digits, '.',
// '_', '-', ':' and '/', so nothing systemd would expand or split. Every
// argument is quoted with Quote.
func ExecStart(nodeDir, home string, a agentapi.AppSpec) (string, error) {
	if err := a.Validate(); err != nil {
		return "", err
	}

	if a.IsPython() {
		return pythonExecStart(home, a)
	}

	if a.StartKind == agentapi.AppStartBinary {
		var b strings.Builder

		b.WriteString(home + "/" + a.Start)

		for _, arg := range a.Args {
			b.WriteString(" " + Quote(arg))
		}

		return b.String(), nil
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

// pythonExecStart is the command line of a Python app: the environment's
// python with gunicorn, uvicorn or the .py file, the address Hostashi
// sets, and the extra arguments, each quoted. The address comes first, so
// the arguments cannot hide it; ValidatePythonArgs refuses a second one.
func pythonExecStart(home string, a agentapi.AppSpec) (string, error) {
	var b strings.Builder

	b.WriteString(home + "/" + agentapi.PythonVenvDir + "/bin/python")

	port := strconv.Itoa(a.Port)

	switch a.StartKind {
	case agentapi.AppStartWSGI:
		b.WriteString(" -m gunicorn --bind 127.0.0.1:" + port)
	case agentapi.AppStartASGI:
		b.WriteString(" -m uvicorn --host 127.0.0.1 --port " + port)
	case agentapi.AppStartScript:
		b.WriteString(" " + a.Start)
	default:
		return "", errors.New("appunit: the app has no start command")
	}

	for _, arg := range a.Args {
		b.WriteString(" " + Quote(arg))
	}

	if a.StartKind != agentapi.AppStartScript {
		b.WriteString(" " + a.Start)
	}

	return b.String(), nil
}

// Quote makes s one word of a systemd command line, taken literally: in
// double quotes, with '\\' and '"' escaped by a backslash, '%' (a unit
// specifier) doubled and '$' (a variable) doubled. Tabs stay as they are;
// AppSpec.Validate refuses NUL, newline and carriage return.
func Quote(s string) string {
	b := make([]byte, 0, len(s)+2)
	b = append(b, '"')

	for i := range len(s) {
		switch c := s[i]; c {
		case '\\', '"':
			b = append(b, '\\', c)
		case '%', '$':
			b = append(b, c, c)
		default:
			b = append(b, c)
		}
	}

	return string(append(b, '"'))
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
// environment; envFile, if not empty, is the file with the app's own
// variables.
func AppDropInContent(nodeDir string, a agentapi.AppSpec, acct Site, envFile string) ([]byte, error) {
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
		exec, err := ExecStart(nodeDir, acct.Home, a)
		if err != nil {
			return nil, err
		}

		w("ExecStart=", exec)
	} else {
		// Never started (Stopped is set); systemd still wants a command.
		w("ExecStart=/bin/false")
	}

	switch {
	case a.IsPython():
		venv := acct.Home + "/" + agentapi.PythonVenvDir

		w("Environment=HOST=127.0.0.1 PORT=", strconv.Itoa(a.Port), " PYTHONUNBUFFERED=1")
		w("Environment=HOME=", acct.Home, " USER=", a.Site, " LOGNAME=", a.Site)
		w("Environment=VIRTUAL_ENV=", venv)
		w("Environment=PATH=", venv, "/bin:/usr/local/bin:/usr/bin:/bin")
	case a.Binary():
		w("Environment=HOST=127.0.0.1 PORT=", strconv.Itoa(a.Port))
		w("Environment=HOME=", acct.Home, " USER=", a.Site, " LOGNAME=", a.Site)
		w("Environment=PATH=/usr/local/bin:/usr/bin:/bin")
	default:
		w("Environment=NODE_ENV=production HOST=127.0.0.1 PORT=", strconv.Itoa(a.Port))
		w("Environment=HOME=", acct.Home, " USER=", a.Site, " LOGNAME=", a.Site)
		w("Environment=PATH=", path.Join(nodeDir, a.Node, "bin"), ":/usr/local/bin:/usr/bin:/bin")
	}

	// The variables come after, so they may replace NODE_ENV; the names
	// Hostashi needs (PORT, HOST, ...) are refused by ValidateAppEnv.
	if envFile != "" {
		if !filepath.IsAbs(envFile) || strings.ContainsAny(envFile, " \t\n\"'\\%$") {
			return nil, fmt.Errorf("appunit: invalid environment file %q", envFile)
		}

		w("EnvironmentFile=", envFile)
	}

	return []byte(b.String()), nil
}

// EnvFileContent is the app's variables in systemd's environment file
// syntax: one NAME="value" per line, with '\\', '"', '`' and '$' escaped
// by a backslash, so systemd takes every value literally. Names are
// checked by ValidateAppEnv; values hold no NUL, newline or carriage
// return.
func EnvFileContent(site string, env []agentapi.AppEnvVar) []byte {
	var b strings.Builder

	b.WriteString("# Generated by hostashi for site " + site + " (ADR-0054). Do not edit: changes are overwritten.\n")

	for _, v := range env {
		b.WriteString(v.Name + "=\"")

		val := make([]byte, 0, len(v.Value)+8)

		for i := range len(v.Value) {
			c := v.Value[i]
			if c == '\\' || c == '"' || c == '`' || c == '$' {
				val = append(val, '\\')
			}

			val = append(val, c)
		}

		b.WriteString(string(val))

		b.WriteString("\"\n")
	}

	return []byte(b.String())
}

// Users lists the sites whose app runs on a Node.js line, sorted.
func (m *Manager) Users(major string) ([]string, error) {
	return m.users(func(a agentapi.AppSpec) bool { return a.Node == major })
}

// PythonUsers lists the sites whose app runs on a Python line, sorted.
func (m *Manager) PythonUsers(line string) ([]string, error) {
	return m.users(func(a agentapi.AppSpec) bool { return a.Python == line })
}

func (m *Manager) users(on func(agentapi.AppSpec) bool) ([]string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	st, err := m.load()
	if err != nil {
		return nil, err
	}

	out := []string{}

	for site, e := range st {
		if on(e.App) {
			out = append(out, site)
		}
	}

	slices.Sort(out)

	return out, nil
}

// RestartLine restarts the running apps on a Node.js line, after it was
// installed again with a newer release. Stopped apps stay stopped. A
// failure is logged and the next app is restarted all the same.
func (m *Manager) RestartLine(ctx context.Context, major string) {
	m.restartLine(ctx, "node", major, func(a agentapi.AppSpec) bool { return !a.Binary() && a.Node == major })
}

// RestartPythonLine restarts the running apps on a Python line after a
// newer build of it was installed, like RestartLine. Their environments
// link the line's interpreter by path and keep working (ADR-0061).
func (m *Manager) RestartPythonLine(ctx context.Context, line string) {
	m.restartLine(ctx, "python", line, func(a agentapi.AppSpec) bool { return a.Python == line })
}

func (m *Manager) restartLine(ctx context.Context, runtime, major string, on func(agentapi.AppSpec) bool) {
	m.mu.Lock()
	defer m.mu.Unlock()

	st, err := m.load()
	if err != nil {
		m.log().ErrorContext(ctx, "restart apps", "runtime", runtime, "line", major, "err", err)

		return
	}

	sites := make([]string, 0, len(st))

	for site, e := range st {
		if on(e.App) && !e.App.Stopped && e.App.StartKind != "" {
			sites = append(sites, site)
		}
	}

	slices.Sort(sites)

	for _, site := range sites {
		unit := UnitOf(st[site].App)

		_ = m.Ctl.ResetFailed(ctx, unit)

		if err := m.Ctl.Restart(ctx, unit); err != nil {
			m.log().ErrorContext(ctx, "restart app on the new release", "runtime", runtime, "site", site, "err", err)

			continue
		}

		m.log().InfoContext(ctx, "app restarted on the new release", "runtime", runtime, "site", site, "line", major)
	}
}

// installed reports whether a line's node binary is there.
func (m *Manager) installed(major string) bool {
	st, err := os.Stat(filepath.Join(m.nodeDir(), major, "bin", "node"))

	return err == nil && st.Mode().IsRegular()
}

// pythonInstalled reports whether a Python line's interpreter is there.
func (m *Manager) pythonInstalled(line string) bool {
	st, err := os.Stat(filepath.Join(m.pythonDir(), line, "bin", "python3"))

	return err == nil && st.Mode().IsRegular()
}

// runtimeMissing reports a Node.js or Python line that a is set up for
// but that is not installed.
func (m *Manager) runtimeMissing(a agentapi.AppSpec) error {
	switch {
	case a.IsPython():
		if !m.pythonInstalled(a.Python) {
			return fmt.Errorf("%w: Python %s", ErrPythonMissing, a.Python)
		}
	case !a.Binary():
		if !m.installed(a.Node) {
			return fmt.Errorf("%w: Node.js %s", ErrNodeMissing, a.Node)
		}
	}

	return nil
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

	if err := m.runtimeMissing(a); err != nil {
		return agentapi.AppStatus{}, err
	}

	acct, err := m.Site(a.Site)
	if err != nil {
		return agentapi.AppStatus{}, err
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	// A Python site gets its environment with its first app (ADR-0061); an
	// existing one is kept, packages and all.
	if a.IsPython() && !venvThere(acct) {
		if err := m.makeVenv(ctx, a.Site, a.Python, acct, false); err != nil {
			return agentapi.AppStatus{}, err
		}
	}

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

	// A site that changes between Node.js, a binary and Python leaves the
	// other unit behind.
	if had && UnitOf(prev.App) != UnitOf(a) {
		if err := m.removeUnit(ctx, prev.App); err != nil {
			return agentapi.AppStatus{}, err
		}
	}

	changed, err := m.writeUnit(ctx, a, acct, true)
	if err != nil {
		return agentapi.AppStatus{}, err
	}

	// The state keeps no values: the environment file is their only copy
	// on this server.
	kept := a
	kept.Env = nil
	st[a.Site] = entry{App: kept, UID: acct.UID}

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

	unit := UnitOf(a)
	problem := m.problem(a, acct)

	switch {
	case a.Stopped || problem != "":
		// A binary app whose program is not there yet, or a Python app
		// without its app server, waits for a start in the panel.
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

	m.log().InfoContext(ctx, "app applied", "site", a.Site, "node", a.Node, "python", a.Python, "port", a.Port, "stopped", a.Stopped, "problem", problem)

	out := m.status(ctx, a)
	out.Problem = problem

	return out, nil
}

// problem says why a that should run cannot start, "" if nothing is wrong.
func (m *Manager) problem(a agentapi.AppSpec, acct Site) string {
	if a.IsPython() {
		return m.pythonProblem(a, acct)
	}

	return m.program(a, acct)
}

// program checks the program file of a binary app that should run and
// returns what is wrong with it, "" if nothing is (or a is no binary app
// that should run). The file must lie below the home without a symbolic
// link at its end, be a regular file with an execute bit and belong to
// the site user. The check is for the panel's message: the site user can
// change the file at any time, and the app runs as the site user anyway.
func (m *Manager) program(a agentapi.AppSpec, acct Site) string {
	if !a.Binary() || a.Stopped || a.StartKind != agentapi.AppStartBinary {
		return ""
	}

	root, err := os.OpenRoot(acct.Home)
	if err != nil {
		return "the home cannot be opened"
	}
	defer root.Close()

	fi, err := root.Lstat(a.Start)

	switch {
	case errors.Is(err, os.ErrNotExist):
		return "the program file " + a.Start + " does not exist; upload it, then start the app"
	case err != nil:
		return "the program file " + a.Start + " cannot be read"
	case fi.Mode()&os.ModeSymlink != 0:
		return "the program file " + a.Start + " is a symbolic link; name the file itself"
	case !fi.Mode().IsRegular():
		return "the program file " + a.Start + " is not a regular file"
	case fi.Mode().Perm()&0o100 == 0:
		return "the program file " + a.Start + " is not executable; set its execute bit (chmod u+x)"
	case fileUID(fi) != acct.UID:
		return "the program file " + a.Start + " does not belong to the site user"
	}

	return ""
}

// removeUnit stops the unit of a and deletes its drop-ins.
func (m *Manager) removeUnit(ctx context.Context, a agentapi.AppSpec) error {
	unit := UnitOf(a)

	if err := m.Ctl.Stop(ctx, unit); err != nil {
		return err
	}

	dir := m.dropInDir(unit)
	for _, f := range []string{SliceDropIn, AppDropIn, EnvFile} {
		if err := os.Remove(filepath.Join(dir, f)); err != nil && !errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("appunit: %w", err)
		}
	}

	if err := os.Remove(dir); err != nil && !errors.Is(err, os.ErrNotExist) {
		m.log().InfoContext(ctx, "app drop-in directory not empty, kept", "site", a.Site, "dir", dir)
	}

	if a.IsPython() {
		return m.removeVenvUnit(a.Site)
	}

	return nil
}

// writeUnit writes the log directory, both drop-ins and, with setEnv, the
// environment file (removed when a has no variables), and reports whether
// any of them changed; systemd has read them when it returns. Without
// setEnv the environment file stays as it is (Recover: the state keeps no
// values).
func (m *Manager) writeUnit(ctx context.Context, a agentapi.AppSpec, acct Site, setEnv bool) (bool, error) {
	var hidden []string
	if m.Hidden != nil {
		hidden = m.Hidden()
	}

	dir := m.dropInDir(UnitOf(a))
	envPath := filepath.Join(dir, EnvFile)

	var (
		envChanged bool
		withEnv    bool
	)

	if err := os.MkdirAll(dir, 0o755); err != nil {
		return false, fmt.Errorf("appunit: %w", err)
	}

	switch {
	case setEnv && len(a.Env) > 0:
		c, err := writeSecretIfChanged(envPath, EnvFileContent(a.Site, a.Env))
		if err != nil {
			return false, err
		}

		envChanged, withEnv = c, true
	case setEnv:
		err := os.Remove(envPath)
		if err != nil && !errors.Is(err, os.ErrNotExist) {
			return false, fmt.Errorf("appunit: %w", err)
		}

		envChanged = err == nil
	default:
		_, err := os.Lstat(envPath)
		withEnv = err == nil
	}

	envFile := ""
	if withEnv {
		envFile = envPath
	}

	app, err := AppDropInContent(m.nodeDir(), a, acct, envFile)
	if err != nil {
		return false, err
	}

	// systemd opens the log as root before it starts the app; the site
	// user can neither open nor change it (like the PHP error log).
	logs := filepath.Join(m.logDir(), a.Site)
	if err := os.MkdirAll(logs, 0o700); err != nil {
		return false, fmt.Errorf("appunit: %w", err)
	}

	if err := os.Chmod(logs, 0o700); err != nil {
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

	// systemd reads the environment file when the unit starts: a changed
	// file needs a restart, not a reload.
	return c1 || c2 || envChanged, nil
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

	e, ok := st[site]
	if !ok {
		return nil
	}

	if err := m.removeUnit(ctx, e.App); err != nil {
		return err
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

	unit := UnitOf(e.App)

	if action != agentapi.AppActionStop {
		acct, err := m.Site(site)
		if err != nil {
			return agentapi.AppStatus{}, err
		}

		if p := m.problem(e.App, acct); p != "" {
			return agentapi.AppStatus{}, fmt.Errorf("%w: %s", ErrProgram, p)
		}
	}

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

	return m.status(ctx, e.App), nil
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

	e, ok := st[site]
	if !ok {
		return agentapi.AppStatus{Site: site}, nil
	}

	out := m.status(ctx, e.App)

	if acct, err := m.Site(site); err == nil {
		out.Problem = m.problem(e.App, acct)
	}

	return out, nil
}

func (m *Manager) status(ctx context.Context, a agentapi.AppSpec) agentapi.AppStatus {
	out := agentapi.AppStatus{Site: a.Site}

	props, err := m.Ctl.Show(ctx, UnitOf(a), "ActiveState", "SubState", "NRestarts", "MemoryCurrent")
	if err != nil {
		m.log().ErrorContext(ctx, "app state", "site", a.Site, "err", err)

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

		if _, err := m.writeUnit(ctx, e.App, acct, false); err != nil {
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
		a := st[s].App
		if a.Stopped || m.runtimeMissing(a) != nil {
			continue
		}

		if acct, err := m.Site(s); err == nil && m.problem(a, acct) != "" {
			continue
		}

		if err := m.Ctl.Start(ctx, UnitOf(a)); err != nil {
			errs = append(errs, err)
		}
	}

	return errors.Join(errs...)
}

// writeSecretIfChanged replaces name with data, root-only (mode 0600),
// unless it already holds exactly that, and reports whether it wrote. A
// symbolic link or other non-regular file at name is replaced, never
// followed.
func writeSecretIfChanged(name string, data []byte) (bool, error) {
	if fi, err := os.Lstat(name); err == nil && fi.Mode().IsRegular() && fi.Mode().Perm() == 0o600 {
		if old, err := os.ReadFile(name); err == nil && bytes.Equal(old, data) {
			return false, nil
		}
	}

	tmp := name + ".tmp"
	_ = os.Remove(tmp)

	f, err := os.OpenFile(tmp, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return false, fmt.Errorf("appunit: %w", err)
	}

	if _, err := f.Write(data); err != nil {
		_ = f.Close()
		_ = os.Remove(tmp)

		return false, fmt.Errorf("appunit: %w", err)
	}

	if err := f.Close(); err != nil {
		_ = os.Remove(tmp)

		return false, fmt.Errorf("appunit: %w", err)
	}

	if err := os.Rename(tmp, name); err != nil {
		_ = os.Remove(tmp)

		return false, fmt.Errorf("appunit: %w", err)
	}

	return true, nil
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
