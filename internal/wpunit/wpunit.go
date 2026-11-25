// SPDX-License-Identifier: AGPL-3.0-or-later

// Package wpunit runs the WordPress quick install of a site (ADR-0069):
// one oneshot unit hostashi-wp@<site>.service per site, from a fixed
// template, as the site user in the site's slice. Like deployunit, it
// writes drop-ins from typed, checked values only. The job and both
// passwords reach the run as systemd credentials (LoadCredential=), never
// as arguments or in a drop-in; the runner (`hostashi wp-run`, package
// wpcli) passes the passwords to WP-CLI on standard input. The template
// pulls in hostashi-wpcli-install.service, which puts the pinned phar in
// place first. The manager starts the unit without waiting and reports its
// state; it never runs WP-CLI or PHP itself.
package wpunit

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/maki-it/hostashi/internal/agentapi"
	"github.com/maki-it/hostashi/internal/appunit"
	"github.com/maki-it/hostashi/internal/sites"
	"github.com/maki-it/hostashi/internal/statefile"
	"github.com/maki-it/hostashi/internal/wpcli"
	"github.com/maki-it/hostashi/internal/wpinstall"
)

// Fixed places on a server.
const (
	DefaultUnitDir = "/etc/systemd/system"
	// DefaultCredDir holds job and passwords while a run needs them, root
	// only; systemd hands them to the unit.
	DefaultCredDir = "/run/hostashi-wp" //nolint:gosec // a directory, not a credential
	// DefaultLogDir holds <site>/wordpress.log, opened by systemd as root.
	DefaultLogDir = appunit.DefaultLogDir
	DefaultExe    = "/usr/local/bin/hostashi"
	// DropIn names the drop-in with the run.
	DropIn    = "60-hostashi-wp.conf"
	LogName   = "wordpress.log"
	stateFile = "state.json"
	// startGrace is how long a queued start may take before the run
	// counts as failed.
	startGrace = 5 * time.Minute
)

// Errors of the operations.
var (
	ErrBusy = errors.New("wpunit: a WordPress install of this site is running")
	// ErrNotEmpty: the document root holds files; the quick install never
	// writes over a site's files.
	ErrNotEmpty = errors.New("wpunit: the document root is not empty")
)

// Controller drives systemd (phpctl's deploy controller on a server).
type Controller interface {
	DaemonReload(ctx context.Context) error
	ResetFailed(ctx context.Context, unit string) error
	StartNoBlock(ctx context.Context, unit string) error
	Stop(ctx context.Context, unit string) error
	Show(ctx context.Context, unit string, props ...string) (map[string]string, error)
}

// Site is the account of a site.
type Site struct {
	Home     string
	UID, GID uint32
	Slice    string
}

// Manager runs the installs. Set Dir, Site and Ctl.
type Manager struct {
	Dir     string // state, root 0700
	UnitDir string
	CredDir string
	LogDir  string
	Exe     string
	// Phar is the pinned phar; wpinstall.Path("", agentapi.WPCLI) unless
	// set.
	Phar   string
	Site   func(site string) (Site, error)
	Hidden func() []string
	Ctl    Controller
	Log    *slog.Logger
	now    func() time.Time

	mu sync.Mutex
}

// Unit is the install unit of a site.
func Unit(site string) string { return "hostashi-wp@" + site + ".service" }

func or(a, b string) string {
	if a != "" {
		return a
	}

	return b
}

func (m *Manager) unitDir() string { return or(m.UnitDir, DefaultUnitDir) }
func (m *Manager) credDir() string { return or(m.CredDir, DefaultCredDir) }
func (m *Manager) logDir() string  { return or(m.LogDir, DefaultLogDir) }
func (m *Manager) exe() string     { return or(m.Exe, DefaultExe) }
func (m *Manager) phar() string    { return or(m.Phar, wpinstall.Path("", agentapi.WPCLI)) }

func (m *Manager) clock() time.Time {
	if m.now != nil {
		return m.now()
	}

	return time.Now()
}

func (m *Manager) log() *slog.Logger {
	if m.Log != nil {
		return m.Log
	}

	return slog.New(slog.DiscardHandler)
}

// LogFile is the log of a site's last run.
func (m *Manager) LogFile(site string) string { return filepath.Join(m.logDir(), site, LogName) }

func (m *Manager) load() (map[string]time.Time, error) {
	b, err := os.ReadFile(filepath.Join(m.Dir, stateFile))
	if errors.Is(err, fs.ErrNotExist) {
		return map[string]time.Time{}, nil
	}

	if err != nil {
		return nil, fmt.Errorf("wpunit: state: %w", err)
	}

	st := map[string]time.Time{}
	if err := json.Unmarshal(b, &st); err != nil {
		return nil, fmt.Errorf("wpunit: state: %w", err)
	}

	return st, nil
}

func (m *Manager) save(st map[string]time.Time) error {
	b, err := json.Marshal(st)
	if err != nil {
		return fmt.Errorf("wpunit: state: %w", err)
	}

	if err := os.MkdirAll(m.Dir, 0o700); err != nil {
		return fmt.Errorf("wpunit: %w", err)
	}

	if err := statefile.Write(m.Dir, stateFile, append(b, '\n')); err != nil {
		return fmt.Errorf("wpunit: %w", err)
	}

	return nil
}

// safePath allows plain absolute paths only: nothing systemd reads as a
// separator, quote, specifier or escape.
func safePath(p string) bool {
	return filepath.IsAbs(p) && filepath.Clean(p) == p && !strings.ContainsAny(p, " \t\n\"'\\%$;&|<>`*?[](){}")
}

// DropInContent is the run of a site.
func (m *Manager) DropInContent(site string, acct Site) ([]byte, error) {
	if err := agentapi.ValidateSiteUser(site); err != nil {
		return nil, err
	}

	cred := filepath.Join(m.credDir(), site)
	for _, p := range []string{acct.Home, m.exe(), m.phar(), cred, m.LogFile(site)} {
		if !safePath(p) {
			return nil, fmt.Errorf("wpunit: invalid path %q", p)
		}
	}

	var b strings.Builder

	w := func(s ...string) { b.WriteString(strings.Join(s, "") + "\n") }

	w("# Generated by hostashi for site ", site, " (ADR-0069). Do not edit: changes are overwritten.")
	w("[Service]")
	w("WorkingDirectory=", acct.Home)
	w("ExecStart=")
	w("ExecStart=", m.exe(), " wp-run ", m.phar())

	for _, c := range []string{wpcli.CredJob, wpcli.CredDBPass, wpcli.CredAdminPass} {
		w("LoadCredential=", c, ":", filepath.Join(cred, c))
	}

	w("StandardOutput=truncate:", m.LogFile(site))
	w("Environment=HOME=", acct.Home, " USER=", site, " LOGNAME=", site)

	return []byte(b.String()), nil
}

// Start writes the job, the credentials and the drop-ins and queues the
// unit's start (site.wordpress.install).
func (m *Manager) Start(ctx context.Context, req agentapi.WPInstallRequest) (agentapi.WPInstallStatus, error) {
	if err := req.Validate(); err != nil {
		return agentapi.WPInstallStatus{}, err
	}

	acct, err := m.Site(req.Site)
	if err != nil {
		return agentapi.WPInstallStatus{}, err
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	st, err := m.load()
	if err != nil {
		return agentapi.WPInstallStatus{}, err
	}

	unit := Unit(req.Site)

	props, err := m.Ctl.Show(ctx, unit, "ActiveState")
	if err != nil {
		return agentapi.WPInstallStatus{}, err
	}

	started, ok := st[req.Site]
	if m.phase(props["ActiveState"], started, ok) == agentapi.DeployRunning {
		return agentapi.WPInstallStatus{}, ErrBusy
	}

	if err := checkEmpty(acct.Home, req.WebDir); err != nil {
		return agentapi.WPInstallStatus{}, err
	}

	path := acct.Home + "/" + sites.WebRootName
	if req.WebDir != "" {
		path += "/" + req.WebDir
	}

	job := wpcli.Job{WPInstall: req.Install, PHP: req.PHP, Path: path, Home: acct.Home, Domain: req.Domain}
	if err := job.Validate(); err != nil {
		return agentapi.WPInstallStatus{}, err
	}

	if err := m.writeCreds(req, job); err != nil {
		return agentapi.WPInstallStatus{}, err
	}

	if err := m.writeUnit(ctx, req.Site, acct); err != nil {
		m.removeCreds(req.Site)

		return agentapi.WPInstallStatus{}, err
	}

	// The last run stays active (RemainAfterExit=) or failed: stop it,
	// so the start below runs again.
	if err := m.Ctl.Stop(ctx, unit); err != nil {
		m.removeCreds(req.Site)

		return agentapi.WPInstallStatus{}, err
	}

	_ = m.Ctl.ResetFailed(ctx, unit)

	st[req.Site] = m.clock().UTC()
	if err := m.save(st); err != nil {
		m.removeCreds(req.Site)

		return agentapi.WPInstallStatus{}, err
	}

	if err := m.Ctl.StartNoBlock(ctx, unit); err != nil {
		m.removeCreds(req.Site)

		return agentapi.WPInstallStatus{}, err
	}

	m.log().InfoContext(ctx, "wordpress install started", "site", req.Site, "locale", req.Install.Locale)

	return agentapi.WPInstallStatus{Site: req.Site, State: agentapi.DeployRunning}, nil
}

// checkEmpty refuses a document root with anything in it. A missing one
// is fine: WP-CLI creates it. Links are not followed.
func checkEmpty(home, webDir string) error {
	root, err := os.OpenRoot(home)
	if err != nil {
		return fmt.Errorf("wpunit: %w", err)
	}
	defer root.Close()

	p := sites.WebRootName
	if webDir != "" {
		p += "/" + webDir
	}

	fi, err := root.Lstat(p)
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}

	if err != nil {
		return fmt.Errorf("wpunit: %w", err)
	}

	if !fi.IsDir() {
		return ErrNotEmpty
	}

	f, err := root.Open(p)
	if err != nil {
		return fmt.Errorf("wpunit: %w", err)
	}
	defer f.Close()

	names, err := f.Readdirnames(1)
	if err != nil && !errors.Is(err, io.EOF) {
		return fmt.Errorf("wpunit: %w", err)
	}

	if len(names) > 0 {
		return ErrNotEmpty
	}

	return nil
}

func (m *Manager) writeCreds(req agentapi.WPInstallRequest, job wpcli.Job) error {
	raw, err := json.Marshal(job)
	if err != nil {
		return fmt.Errorf("wpunit: %w", err)
	}

	if err := os.MkdirAll(m.credDir(), 0o700); err != nil {
		return fmt.Errorf("wpunit: %w", err)
	}

	dir := filepath.Join(m.credDir(), req.Site)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("wpunit: %w", err)
	}

	for name, data := range map[string][]byte{
		wpcli.CredJob: raw, wpcli.CredDBPass: []byte(req.DBPassword), wpcli.CredAdminPass: []byte(req.AdminPassword),
	} {
		if err := writeFile(filepath.Join(dir, name), data, 0o600); err != nil {
			return err
		}
	}

	return nil
}

func (m *Manager) removeCreds(site string) { _ = os.RemoveAll(filepath.Join(m.credDir(), site)) }

func (m *Manager) writeUnit(ctx context.Context, site string, acct Site) error {
	content, err := m.DropInContent(site, acct)
	if err != nil {
		return err
	}

	var hidden []string
	if m.Hidden != nil {
		hidden = m.Hidden()
	}

	if err := os.MkdirAll(filepath.Join(m.logDir(), site), 0o700); err != nil {
		return fmt.Errorf("wpunit: %w", err)
	}

	dir := filepath.Join(m.unitDir(), Unit(site)+".d")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("wpunit: %w", err)
	}

	slice := appunit.SliceDropInContent(site, appunit.Site{Home: acct.Home, UID: acct.UID, Slice: acct.Slice}, hidden)
	if err := writeFile(filepath.Join(dir, appunit.SliceDropIn), slice, 0o644); err != nil {
		return err
	}

	if err := writeFile(filepath.Join(dir, DropIn), content, 0o644); err != nil {
		return err
	}

	return m.Ctl.DaemonReload(ctx)
}

// phase maps the unit's ActiveState to the run's state, as in deployunit.
func (m *Manager) phase(state string, started time.Time, ok bool) string {
	switch state {
	case "activating", "deactivating", "reloading":
		return agentapi.DeployRunning
	case "active":
		return agentapi.DeploySucceeded
	case "failed":
		return agentapi.DeployFailed
	}

	if ok && m.clock().Sub(started) < startGrace {
		return agentapi.DeployRunning
	}

	return agentapi.DeployUnknown
}

// Status reports the last run of a site (site.wordpress.status). A
// finished run has its credentials deleted.
func (m *Manager) Status(ctx context.Context, req agentapi.WPStatusRequest) (agentapi.WPInstallStatus, error) {
	if err := req.Validate(); err != nil {
		return agentapi.WPInstallStatus{}, err
	}

	if _, err := m.Site(req.Site); err != nil {
		return agentapi.WPInstallStatus{}, err
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	out := agentapi.WPInstallStatus{Site: req.Site, State: agentapi.WPNone}

	st, err := m.load()
	if err != nil {
		return out, err
	}

	started, ok := st[req.Site]
	if !ok {
		return out, nil
	}

	props, err := m.Ctl.Show(ctx, Unit(req.Site), "ActiveState", "Result")
	if err != nil {
		return out, err
	}

	switch m.phase(props["ActiveState"], started, true) {
	case agentapi.DeployRunning:
		out.State = agentapi.DeployRunning
		out.Log = tail(m.LogFile(req.Site))

		return out, nil
	case agentapi.DeploySucceeded:
		out.State = agentapi.DeploySucceeded
	case agentapi.DeployFailed:
		out.State, out.Error = agentapi.DeployFailed, failReason(props["Result"])
	default:
		out.State, out.Error = agentapi.DeployFailed, "the install did not start"
	}

	m.removeCreds(req.Site)
	out.Log = tail(m.LogFile(req.Site))

	return out, nil
}

func failReason(result string) string {
	switch result {
	case "timeout":
		return "the install took too long"
	case "exit-code":
		return "a step of the install failed, see the log"
	case "signal", "core-dump":
		return "the install was killed"
	case "oom-kill":
		return "the install ran out of memory"
	}

	return "the install failed (" + result + ")"
}

// tail is the end of the log, valid UTF-8.
func tail(name string) string {
	f, err := os.Open(name)
	if err != nil {
		return ""
	}
	defer f.Close()

	if fi, err := f.Stat(); err == nil && fi.Size() > agentapi.MaxWPLog {
		_, _ = f.Seek(-agentapi.MaxWPLog, io.SeekEnd)
	}

	b, _ := io.ReadAll(io.LimitReader(f, agentapi.MaxWPLog))

	return strings.ToValidUTF8(string(b), "")
}

func writeFile(name string, data []byte, mode os.FileMode) error {
	if old, err := os.ReadFile(name); err == nil && bytes.Equal(old, data) {
		if err := os.Chmod(name, mode); err != nil {
			return fmt.Errorf("wpunit: %w", err)
		}

		return nil
	}

	tmp := name + ".tmp"
	if err := os.WriteFile(tmp, data, mode); err != nil {
		return fmt.Errorf("wpunit: %w", err)
	}

	if err := os.Chmod(tmp, mode); err != nil {
		return fmt.Errorf("wpunit: %w", err)
	}

	if err := os.Rename(tmp, name); err != nil {
		return fmt.Errorf("wpunit: %w", err)
	}

	return nil
}

// Remove stops a deleted site's install and removes its drop-ins,
// credentials and state.
func (m *Manager) Remove(ctx context.Context, site string) error {
	if err := agentapi.ValidateSiteUser(site); err != nil {
		return err
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	if err := m.Ctl.Stop(ctx, Unit(site)); err != nil {
		m.log().WarnContext(ctx, "stop wordpress install of deleted site", "site", site, "err", err)
	}

	dir := filepath.Join(m.unitDir(), Unit(site)+".d")
	for _, f := range []string{appunit.SliceDropIn, DropIn} {
		if err := os.Remove(filepath.Join(dir, f)); err != nil && !errors.Is(err, fs.ErrNotExist) {
			return fmt.Errorf("wpunit: %w", err)
		}
	}

	_ = os.Remove(dir)

	m.removeCreds(site)

	st, err := m.load()
	if err != nil {
		return err
	}

	if _, ok := st[site]; ok {
		delete(st, site)

		if err := m.save(st); err != nil {
			return err
		}
	}

	return m.Ctl.DaemonReload(ctx)
}
