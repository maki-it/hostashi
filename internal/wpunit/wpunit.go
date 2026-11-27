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
	"regexp"
	"strings"
	"sync"
	"syscall"
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
	DefaultExe    = "/usr/bin/hostashi"
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

	st, err := m.start(ctx, req.Site, func(acct Site) (map[string][]byte, error) {
		if !req.Reinstall {
			if err := checkEmpty(acct.Home, req.WebDir); err != nil {
				return nil, err
			}
		}

		job := wpcli.Job{
			WPInstall: req.Install, PHP: req.PHP, Path: docRoot(acct.Home, req.WebDir), Home: acct.Home, Domain: req.Domain,
			Clear: req.Reinstall,
		}
		if err := job.Validate(); err != nil {
			return nil, err
		}

		raw, err := json.Marshal(job)
		if err != nil {
			return nil, fmt.Errorf("wpunit: %w", err)
		}

		return map[string][]byte{wpcli.CredJob: raw, wpcli.CredDBPass: []byte(req.DBPassword), wpcli.CredAdminPass: []byte(req.AdminPassword)}, nil
	})
	if err == nil {
		m.log().InfoContext(ctx, "wordpress install started", "site", req.Site, "locale", req.Install.Locale)
	}

	return st, err
}

// StartClone queues the configuration of a cloned WordPress site
// (site.wordpress.clone, ADR-0070) in the same unit. The database
// passwords go into the dbpass credential as a JSON object.
func (m *Manager) StartClone(ctx context.Context, req agentapi.WPCloneRequest) (agentapi.WPInstallStatus, error) {
	if err := req.Validate(); err != nil {
		return agentapi.WPInstallStatus{}, err
	}

	st, err := m.start(ctx, req.Site, func(acct Site) (map[string][]byte, error) {
		job := wpcli.Job{Clone: &wpcli.CloneJob{
			PHP: req.PHP, Path: docRoot(acct.Home, req.WebDir), Home: acct.Home, URL: req.URL,
			From: req.From, Databases: req.Databases, Staging: req.Staging,
		}}
		if err := job.Clone.Validate(); err != nil {
			return nil, err
		}

		raw, err := json.Marshal(job)
		if err != nil {
			return nil, fmt.Errorf("wpunit: %w", err)
		}

		pw, err := json.Marshal(req.DBPasswords)
		if err != nil {
			return nil, fmt.Errorf("wpunit: %w", err)
		}

		// The unit loads every credential; the admin password is unused.
		return map[string][]byte{wpcli.CredJob: raw, wpcli.CredDBPass: pw, wpcli.CredAdminPass: nil}, nil
	})
	if err == nil {
		m.log().InfoContext(ctx, "wordpress clone configuration started", "site", req.Site)
	}

	return st, err
}

// StartDotEnv queues the .env step of a cloned Laravel or Symfony site
// (site.dotenv.clone, ADR-0070) in the same unit.
func (m *Manager) StartDotEnv(ctx context.Context, req agentapi.DotEnvCloneRequest) (agentapi.WPInstallStatus, error) {
	if err := req.Validate(); err != nil {
		return agentapi.WPInstallStatus{}, err
	}

	st, err := m.start(ctx, req.Site, func(acct Site) (map[string][]byte, error) {
		job := wpcli.Job{DotEnv: &wpcli.DotEnvJob{
			Home: acct.Home, Dir: wpcli.ProjectDir(req.WebDir), Kind: req.Kind, URL: req.URL, Databases: req.Databases,
		}}

		raw, err := json.Marshal(job)
		if err != nil {
			return nil, fmt.Errorf("wpunit: %w", err)
		}

		pw, err := json.Marshal(req.DBPasswords)
		if err != nil {
			return nil, fmt.Errorf("wpunit: %w", err)
		}

		return map[string][]byte{wpcli.CredJob: raw, wpcli.CredDBPass: pw, wpcli.CredAdminPass: nil}, nil
	})
	if err == nil {
		m.log().InfoContext(ctx, "clone .env step started", "site", req.Site, "kind", req.Kind)
	}

	return st, err
}

func docRoot(home, webDir string) string {
	path := home + "/" + sites.WebRootName
	if webDir != "" {
		path += "/" + webDir
	}

	return path
}

// start runs a job in the site's unit unless one runs: creds returns the
// credential files after its own checks.
func (m *Manager) start(ctx context.Context, site string, creds func(Site) (map[string][]byte, error)) (agentapi.WPInstallStatus, error) {
	acct, err := m.Site(site)
	if err != nil {
		return agentapi.WPInstallStatus{}, err
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	st, err := m.load()
	if err != nil {
		return agentapi.WPInstallStatus{}, err
	}

	unit := Unit(site)

	props, err := m.Ctl.Show(ctx, unit, "ActiveState")
	if err != nil {
		return agentapi.WPInstallStatus{}, err
	}

	started, ok := st[site]
	if m.phase(props["ActiveState"], started, ok) == agentapi.DeployRunning {
		return agentapi.WPInstallStatus{}, ErrBusy
	}

	files, err := creds(acct)
	if err != nil {
		return agentapi.WPInstallStatus{}, err
	}

	if err := m.writeCreds(site, files); err != nil {
		return agentapi.WPInstallStatus{}, err
	}

	if err := m.writeUnit(ctx, site, acct); err != nil {
		m.removeCreds(site)

		return agentapi.WPInstallStatus{}, err
	}

	// The last run stays active (RemainAfterExit=) or failed: stop it,
	// so the start below runs again.
	if err := m.Ctl.Stop(ctx, unit); err != nil {
		m.removeCreds(site)

		return agentapi.WPInstallStatus{}, err
	}

	_ = m.Ctl.ResetFailed(ctx, unit)

	st[site] = m.clock().UTC()
	if err := m.save(st); err != nil {
		m.removeCreds(site)

		return agentapi.WPInstallStatus{}, err
	}

	if err := m.Ctl.StartNoBlock(ctx, unit); err != nil {
		m.removeCreds(site)

		return agentapi.WPInstallStatus{}, err
	}

	return agentapi.WPInstallStatus{Site: site, State: agentapi.DeployRunning}, nil
}

// maxConfig bounds the wp-config.php that inspect reads.
const maxConfig = 256 << 10

// dbNameRe finds DB_NAME in wp-config.php as WP-CLI writes it.
var dbNameRe = regexp.MustCompile(`define\(\s*['"]DB_NAME['"]\s*,\s*['"]([A-Za-z0-9_]{1,64})['"]\s*\)`)

// inspect looks at the site's document root: whether it holds WordPress,
// whether it is empty, and the database its wp-config.php names. Links
// are never followed; anything it cannot read counts as not there.
func inspect(home, webDir string) (installed, empty bool, db string) {
	empty = checkEmpty(home, webDir) == nil
	if empty {
		return false, true, ""
	}

	root, err := os.OpenRoot(home)
	if err != nil {
		return false, false, ""
	}
	defer root.Close()

	dir := sites.WebRootName
	if webDir != "" {
		dir += "/" + webDir
	}

	regular := func(name string) bool {
		fi, err := root.Lstat(dir + "/" + name)

		return err == nil && fi.Mode().IsRegular()
	}

	installed = regular("wp-config.php") || regular("wp-includes/version.php")
	if !regular("wp-config.php") {
		return installed, false, ""
	}

	f, err := root.OpenFile(dir+"/wp-config.php", os.O_RDONLY|syscall.O_NOFOLLOW|syscall.O_NONBLOCK, 0)
	if err != nil {
		return installed, false, ""
	}
	defer f.Close()

	b, err := io.ReadAll(io.LimitReader(f, maxConfig))
	if err != nil {
		return installed, false, ""
	}

	if m := dbNameRe.FindSubmatch(b); m != nil && agentapi.ValidateDBName(string(m[1])) == nil {
		db = string(m[1])
	}

	return installed, false, db
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

func (m *Manager) writeCreds(site string, files map[string][]byte) error {
	if err := os.MkdirAll(m.credDir(), 0o700); err != nil {
		return fmt.Errorf("wpunit: %w", err)
	}

	dir := filepath.Join(m.credDir(), site)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("wpunit: %w", err)
	}

	for name, data := range files {
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

	acct, err := m.Site(req.Site)
	if err != nil {
		return agentapi.WPInstallStatus{}, err
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	out := agentapi.WPInstallStatus{Site: req.Site, State: agentapi.WPNone}
	out.Installed, out.Empty, out.Database = inspect(acct.Home, req.WebDir)

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

	phase := m.phase(props["ActiveState"], started, true)
	if phase == agentapi.DeployRunning {
		out.State = agentapi.DeployRunning
		out.Log = tail(m.LogFile(req.Site))

		return out, nil
	}

	m.removeCreds(req.Site)

	// WordPress was installed but is gone, for example deleted in the
	// file manager: the site counts as never installed again. A failed
	// run keeps its error and log; the panel offers the install below it.
	if phase == agentapi.DeploySucceeded && !out.Installed {
		return out, nil
	}

	switch phase {
	case agentapi.DeploySucceeded:
		out.State = agentapi.DeploySucceeded
	case agentapi.DeployFailed:
		out.State, out.Error = agentapi.DeployFailed, failReason(props["Result"])
	default:
		out.State, out.Error = agentapi.DeployFailed, "the run did not start"
	}

	out.Log = tail(m.LogFile(req.Site))

	return out, nil
}

func failReason(result string) string {
	switch result {
	case "timeout":
		return "the run took too long"
	case "exit-code":
		return "a step failed, see the log"
	case "signal", "core-dump":
		return "the run was killed"
	case "oom-kill":
		return "the run ran out of memory"
	}

	return "the run failed (" + result + ")"
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
