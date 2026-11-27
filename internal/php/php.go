// SPDX-License-Identifier: AGPL-3.0-or-later

// Package php runs one PHP-FPM master per site (ADR-0015, ADR-0019): a
// systemd unit hostashi-php-<version>@<site>.service on a pool file that
// this package renders. The master runs as root and drops its workers to
// the site user; nginx reaches them through a Unix socket that only the
// site user and the web server group may open (docs/04-architecture.md 4.4).
//
// The pool file does not depend on the PHP version, so switching a site to
// another version only stops one unit and starts the other.
package php

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
	"regexp"
	"slices"
	"sort"
	"sync"
	"text/template"

	"github.com/maki-it/hostashi/internal/agentapi"
	"github.com/maki-it/hostashi/internal/sites"
)

// Controller runs php-fpm and systemd. The production one is package phpctl.
type Controller interface {
	// Test runs php-fpm<version> -t on the config file conf.
	Test(ctx context.Context, version, conf string) error
	// Start enables and starts a unit.
	Start(ctx context.Context, unit string) error
	// Reload makes a running unit re-read its config, or starts it.
	Reload(ctx context.Context, unit string) error
	// Stop stops and disables a unit.
	Stop(ctx context.Context, unit string) error
}

// SliceUnits makes systemd read changed drop-ins and moves a running unit
// into its slice, which only a restart does.
type SliceUnits interface {
	DaemonReload(ctx context.Context) error
	Restart(ctx context.Context, unit string) error
}

// Pool is the desired PHP state of one site.
type Pool struct {
	User    string `json:"user"`
	Version string `json:"version"`
	Home    string `json:"home"` // the site's home directory, the open_basedir
	// Workers is pm.max_children, 0 for agentapi.DefaultPHPWorkers.
	Workers int `json:"workers,omitempty"`
	// Programs lets the site's PHP start programs (exec, system, ...).
	// Without it those functions are disabled (ADR-0033).
	Programs bool `json:"programs,omitempty"`
	// Settings are the site's PHP ini settings (ADR-0049), rendered as
	// php_value defaults; nil renders the Hostashi defaults.
	Settings *agentapi.PHPSettings `json:"settings,omitempty"`
}

// DisabledFunctions are the PHP functions that start programs. A pool
// disables them unless the site allows programs (ADR-0033).
const DisabledFunctions = "exec,passthru,shell_exec,system,proc_open,popen,pcntl_exec"

// Errors of the manager.
var (
	ErrInvalid = errors.New("php: invalid pool")
	// ErrNotInstalled means the requested PHP version is not installed.
	ErrNotInstalled = errors.New("php: version not installed")
	// ErrValidation means php-fpm rejected the pool file; nothing changed.
	ErrValidation = errors.New("php: php-fpm rejected the pool configuration")
	// ErrStartFailed means the new pool did not start; the previous state
	// of that site was restored as far as possible.
	ErrStartFailed = errors.New("php: the pool did not start, the previous state was restored")
)

// Unit is the systemd unit of a site's FPM master.
func Unit(version, user string) string {
	return "hostashi-php-" + version + "@" + user + ".service"
}

// Manager keeps the pool files and units in line with the desired state.
type Manager struct {
	// Dir holds the pool files and the state (root only). The units read
	// <Dir>/<user>.conf.
	Dir string
	// SocketDir holds one directory per site with its FPM socket. The units
	// create it (RuntimeDirectory=hostashi-php/%i).
	SocketDir string
	// LogDir holds <site>/error.log of each master (ADR-0053); empty means
	// the default LogDir. The units create the directories, but php-fpm -t
	// opens the file before the unit runs, so Ensure creates them too when
	// LogDir is set (production and tests with a real php-fpm).
	LogDir string
	// WebGroup is the web server's group, the only group that may open a
	// site's socket.
	WebGroup string
	Ctl      Controller
	// Units and UnitDir put each site's FPM unit into its slice
	// (ADR-0015) with a drop-in <UnitDir>/<unit>.d/50-hostashi-slice.conf
	// naming Slice(user). Nil Units: no drop-ins (tests).
	Units   SliceUnits
	UnitDir string
	Slice   func(user string) string
	// Hidden returns the pool roots the units hide besides /home
	// (sites.Manager.HiddenPaths); nil hides nothing more.
	Hidden func() []string
	// Installed lists the installed PHP versions; nil allows all (tests).
	Installed func() []string
	// VersionDir holds <user> with the version of each pool, for the site
	// users' wp command (ADR-0069); empty writes none (tests).
	VersionDir string
	Log        *slog.Logger

	mu sync.Mutex
}

var (
	homeRe  = regexp.MustCompile(`^/[A-Za-z0-9_./-]+$`)
	groupRe = regexp.MustCompile(`^[a-z_][a-z0-9_-]{0,31}$`)
)

func (m *Manager) logDir() string {
	if m.LogDir == "" {
		return LogDir
	}

	return m.LogDir
}

// ensureLogDir creates <LogDir>/<user>, root-owned 0750 like the unit's
// LogsDirectory. A symlink or other non-directory is refused.
func (m *Manager) ensureLogDir(user string) error {
	if m.LogDir == "" {
		return nil
	}

	if err := os.MkdirAll(m.LogDir, 0o750); err != nil {
		return fmt.Errorf("php: log directory: %w", err)
	}

	p := filepath.Join(m.LogDir, user)
	if err := os.Mkdir(p, 0o750); err != nil && !errors.Is(err, os.ErrExist) {
		return fmt.Errorf("php: log directory: %w", err)
	}

	fi, err := os.Lstat(p)
	if err != nil {
		return fmt.Errorf("php: log directory: %w", err)
	}

	if !fi.IsDir() {
		return fmt.Errorf("%w: log directory of %s is not a directory", ErrInvalid, user)
	}

	return nil
}

// Socket is the FPM socket of a site.
func (m *Manager) Socket(user string) string {
	return m.SocketDir + "/" + user + "/php-fpm.sock"
}

func (m *Manager) check(p Pool) error {
	if err := agentapi.ValidateSiteUser(p.User); err != nil {
		return fmt.Errorf("%w: %w", ErrInvalid, err)
	}

	if err := agentapi.ValidatePHPVersion(p.Version); err != nil {
		return fmt.Errorf("%w: %w", ErrInvalid, err)
	}

	if !homeRe.MatchString(p.Home) || path.Clean(p.Home) != p.Home || p.Home == "/" {
		return fmt.Errorf("%w: site %s: home %q", ErrInvalid, p.User, p.Home)
	}

	if p.Workers < 0 || p.Workers > agentapi.MaxPHPWorkers {
		return fmt.Errorf("%w: site %s: %d workers", ErrInvalid, p.User, p.Workers)
	}

	if p.Settings != nil {
		if err := p.Settings.Validate(); err != nil {
			return fmt.Errorf("%w: site %s: %w", ErrInvalid, p.User, err)
		}
	}

	if !homeRe.MatchString(m.SocketDir) || !homeRe.MatchString(m.logDir()) || !groupRe.MatchString(m.WebGroup) {
		return fmt.Errorf("%w: socket directory or web group", ErrInvalid)
	}

	if m.Installed != nil && !slices.Contains(m.Installed(), p.Version) {
		return fmt.Errorf("%w: PHP %s", ErrNotInstalled, p.Version)
	}

	return nil
}

var poolTmpl = template.Must(template.New("pool").Parse(`; Generated by hostashi for site {{.P.User}}. Do not edit: changes are overwritten.
[global]
; The unit's LogsDirectory (root, 0750); the panel shows it (ADR-0053).
; PHP's own errors arrive here too: without an error_log of its own PHP
; hands them to FPM, which writes them here and sends them to nginx.
error_log = {{.LogDir}}/{{.P.User}}/error.log
log_level = notice
; systemd runs the master in the foreground.
daemonize = no

[{{.P.User}}]
user = {{.P.User}}
group = {{.P.User}}
listen = {{.Socket}}
listen.owner = {{.P.User}}
listen.group = {{.Group}}
listen.mode = 0660
pm = ondemand
pm.max_children = {{.Workers}}
pm.process_idle_timeout = 10s
pm.max_requests = 500
chdir = /
clear_env = yes
catch_workers_output = yes
decorate_workers_output = no
security.limit_extensions = .php
; The unit has its own /tmp (PrivateTmp=yes), so this is per site.
php_admin_value[open_basedir] = {{.P.Home}}/:/tmp/
php_admin_value[upload_tmp_dir] = /tmp
php_admin_value[sys_temp_dir] = /tmp
php_admin_value[session.save_path] = /tmp
php_admin_flag[allow_url_include] = off
php_admin_flag[session.use_strict_mode] = on
php_admin_flag[session.cookie_httponly] = on
; Defaults from the panel (ADR-0049); the site's .user.ini may change them.
{{- range .Ini}}
php_value[{{.Key}}] = {{.Value}}
{{- end}}
{{- if not .P.Programs}}
; The site may not start programs (ADR-0033); the panel switches this per site.
php_admin_value[disable_functions] = {{.Disabled}}
{{- end}}
`))

// Render returns the pool file of p.
func (m *Manager) Render(p Pool) ([]byte, error) {
	var b bytes.Buffer

	workers := p.Workers
	if workers == 0 {
		workers = agentapi.DefaultPHPWorkers
	}

	if workers < 1 || workers > agentapi.MaxPHPWorkers {
		return nil, fmt.Errorf("%w: %d workers", ErrInvalid, p.Workers)
	}

	var settings agentapi.PHPSettings

	if p.Settings != nil {
		if err := p.Settings.Validate(); err != nil {
			return nil, fmt.Errorf("%w: %w", ErrInvalid, err)
		}

		settings = *p.Settings
	}

	if err := poolTmpl.Execute(&b, map[string]any{"Ini": settings.IniValues(), "LogDir": m.logDir(), "P": p, "Socket": m.Socket(p.User), "Group": m.WebGroup, "Workers": workers, "Disabled": DisabledFunctions}); err != nil {
		return nil, fmt.Errorf("php: render pool %s: %w", p.User, err)
	}

	return b.Bytes(), nil
}

func (m *Manager) log() *slog.Logger {
	if m.Log == nil {
		return slog.New(slog.DiscardHandler)
	}

	return m.Log
}

func (m *Manager) statePath() string       { return filepath.Join(m.Dir, "state.json") }
func (m *Manager) conf(user string) string { return filepath.Join(m.Dir, user+".conf") }

// loadState returns the pools that are set up, by user.
func (m *Manager) loadState() (map[string]Pool, error) {
	b, err := os.ReadFile(m.statePath())
	if errors.Is(err, os.ErrNotExist) {
		return map[string]Pool{}, nil
	}

	if err != nil {
		return nil, fmt.Errorf("php: state: %w", err)
	}

	st := map[string]Pool{}
	if err := json.Unmarshal(b, &st); err != nil {
		return nil, fmt.Errorf("php: state: %w", err)
	}

	return st, nil
}

func (m *Manager) saveState(st map[string]Pool) error {
	b, err := json.MarshalIndent(st, "", "  ")
	if err != nil {
		return fmt.Errorf("php: state: %w", err)
	}

	return writeAtomic(m.statePath(), append(b, '\n'))
}

// writeAtomic replaces name with data (mode 0600) through a synced
// temporary file in the same directory.
func writeAtomic(name string, data []byte) error { return writeAtomicMode(name, data, 0o600) }

// writeAtomicMode is writeAtomic with the file mode mode.
func writeAtomicMode(name string, data []byte, mode os.FileMode) error {
	tmp := name + ".tmp"

	f, err := os.OpenFile(tmp, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o600)
	if err != nil {
		return fmt.Errorf("php: %w", err)
	}

	// Chmod on the file: the umask does not cut it.
	if err := f.Chmod(mode); err != nil {
		_ = f.Close()

		return fmt.Errorf("php: %w", err)
	}

	if _, err := f.Write(data); err != nil {
		_ = f.Close()

		return fmt.Errorf("php: %w", err)
	}

	if err := f.Sync(); err != nil {
		_ = f.Close()

		return fmt.Errorf("php: %w", err)
	}

	if err := f.Close(); err != nil {
		return fmt.Errorf("php: %w", err)
	}

	if err := os.Rename(tmp, name); err != nil {
		return fmt.Errorf("php: %w", err)
	}

	return nil
}

// Current returns the pools that are set up, by user.
func (m *Manager) Current() (map[string]Pool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	return m.loadState()
}

// Ensure sets up every pool in pools: new ones are started, changed ones
// reloaded, a changed version switches the unit. Pools of other sites are
// not touched (Prune removes them). It stops at the first failure; pools
// handled before stay changed.
func (m *Manager) Ensure(ctx context.Context, pools []Pool) error {
	for _, p := range pools {
		if err := m.check(p); err != nil {
			return err
		}
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	if err := os.MkdirAll(m.Dir, 0o700); err != nil {
		return fmt.Errorf("php: %w", err)
	}

	st, err := m.loadState()
	if err != nil {
		return err
	}

	for _, p := range pools {
		if err := m.ensure(ctx, st, p); err != nil {
			return err
		}
	}

	return nil
}

func (m *Manager) ensure(ctx context.Context, st map[string]Pool, p Pool) error {
	// The default written out is the same pool as the default left out.
	if p.Workers == agentapi.DefaultPHPWorkers {
		p.Workers = 0
	}

	content, err := m.Render(p)
	if err != nil {
		return err
	}

	cur, had := st[p.User]

	// The file holds every field of the pool, so the same version and the
	// same file mean nothing changed (Settings is a pointer, == would not
	// see equal values).
	if had && cur.Version == p.Version {
		if old, err := os.ReadFile(m.conf(p.User)); err == nil && bytes.Equal(old, content) {
			m.writeVersion(ctx, p)

			return m.moveIntoSlice(ctx, p)
		}
	}

	// Test the new file under a name of its own; php-fpm must see the final
	// content but the running unit keeps the old file until the switch.
	next := filepath.Join(m.Dir, p.User+".next.conf")
	if err := writeAtomic(next, content); err != nil {
		return err
	}

	if err := m.ensureLogDir(p.User); err != nil {
		_ = os.Remove(next)

		return err
	}

	if err := m.Ctl.Test(ctx, p.Version, next); err != nil {
		_ = os.Remove(next)

		m.log().ErrorContext(ctx, "php-fpm rejected the pool", "site", p.User, "version", p.Version, "err", err)

		return fmt.Errorf("%w: site %s", ErrValidation, p.User)
	}

	switched := had && cur.Version != p.Version
	if switched {
		if err := m.Ctl.Stop(ctx, Unit(cur.Version, p.User)); err != nil {
			_ = os.Remove(next)

			return err
		}

		m.removeDropIn(ctx, cur.Version, p.User)
	}

	dropChanged, err := m.writeDropIn(ctx, p)
	if err != nil {
		_ = os.Remove(next)

		return err
	}

	if err := os.Rename(next, m.conf(p.User)); err != nil {
		return fmt.Errorf("php: %w", err)
	}

	st[p.User] = p
	if err := m.saveState(st); err != nil {
		return err
	}

	unit := Unit(p.Version, p.User)

	switch {
	case had && !switched && dropChanged:
		err = m.Units.Restart(ctx, unit)
	case had && !switched:
		err = m.Ctl.Reload(ctx, unit)
	default:
		err = m.Ctl.Start(ctx, unit)
	}

	if err == nil {
		m.writeVersion(ctx, p)
		m.log().InfoContext(ctx, "php pool ready", "site", p.User, "version", p.Version)

		return nil
	}

	m.log().ErrorContext(ctx, "php pool did not start", "site", p.User, "version", p.Version, "err", err)
	m.undo(ctx, st, p, cur, had)

	return fmt.Errorf("%w: site %s", ErrStartFailed, p.User)
}

// undo brings a site back to its state before a failed ensure: the old pool
// and unit, or no pool at all. Failures are only logged; the next Ensure or
// Recover tries again.
func (m *Manager) undo(ctx context.Context, st map[string]Pool, p, cur Pool, had bool) {
	undo := context.WithoutCancel(ctx)

	if p.Version != cur.Version || !had {
		if err := m.Ctl.Stop(undo, Unit(p.Version, p.User)); err != nil {
			m.log().ErrorContext(ctx, "stop failed pool", "site", p.User, "err", err)
		}
	}

	if p.Version != cur.Version || !had {
		m.removeDropIn(undo, p.Version, p.User)
	}

	if !had {
		delete(st, p.User)
		_ = os.Remove(m.conf(p.User))
		m.removeVersion(ctx, p.User)

		if err := m.saveState(st); err != nil {
			m.log().ErrorContext(ctx, "php state after failed start", "site", p.User, "err", err)
		}

		return
	}

	content, err := m.Render(cur)
	if err == nil {
		err = writeAtomic(m.conf(p.User), content)
	}

	if err == nil {
		st[p.User] = cur
		err = m.saveState(st)
	}

	if err == nil {
		_, err = m.writeDropIn(undo, cur)
	}

	if err == nil {
		m.writeVersion(undo, cur)
		err = m.Ctl.Start(undo, Unit(cur.Version, p.User))
	}

	if err != nil {
		m.log().ErrorContext(ctx, "restore previous php pool", "site", p.User, "err", err)
	}
}

// Prune stops and removes the pools of all sites not in keep.
func (m *Manager) Prune(ctx context.Context, keep map[string]bool) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	st, err := m.loadState()
	if err != nil {
		return err
	}

	users := make([]string, 0, len(st))
	for u := range st {
		if !keep[u] {
			users = append(users, u)
		}
	}

	slices.Sort(users)

	for _, u := range users {
		if err := m.Ctl.Stop(ctx, Unit(st[u].Version, u)); err != nil {
			return err
		}

		if err := os.Remove(m.conf(u)); err != nil && !errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("php: %w", err)
		}

		m.removeDropIn(ctx, st[u].Version, u)
		m.removeVersion(ctx, u)
		delete(st, u)

		if err := m.saveState(st); err != nil {
			return err
		}

		m.log().InfoContext(ctx, "php pool removed", "site", u)
	}

	return nil
}

// Recover starts the unit of every pool that is set up. systemd starts
// enabled units at boot anyway; this repairs a switch that was interrupted
// between stopping the old unit and starting the new one.
func (m *Manager) Recover(ctx context.Context) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	st, err := m.loadState()
	if err != nil {
		return err
	}

	var errs []error

	for u, p := range st {
		m.writeVersion(ctx, p)

		// A unit from before ADR-0015 gets its slice drop-in and a restart.
		changed, err := m.writeDropIn(ctx, p)
		if err != nil {
			errs = append(errs, err)

			continue
		}

		if changed {
			err = m.Units.Restart(ctx, Unit(p.Version, u))
		} else {
			err = m.Ctl.Start(ctx, Unit(p.Version, u))
		}

		if err != nil {
			errs = append(errs, err)
		}
	}

	return errors.Join(errs...)
}

// VersionDir holds one file per site, <user>, with the PHP version of its
// pool and a newline. The site users' wp command (packaging/ssh/wp,
// ADR-0069) reads it to run WP-CLI with the site's PHP. Root owns the
// directory (0711: a site user reads its own file by name but cannot list
// the sites) and the files (0644).
const VersionDir = "/etc/hostashi-php"

const (
	versionDirMode  = 0o711
	versionFileMode = 0o644
)

// writeVersion writes the version file of p. Failures are only logged: the
// pool runs without it, only the wp command misses it; the next Ensure or
// Recover tries again.
func (m *Manager) writeVersion(ctx context.Context, p Pool) {
	if m.VersionDir == "" {
		return
	}

	if err := m.ensureVersionFile(p); err != nil {
		m.log().ErrorContext(ctx, "write php version file", "site", p.User, "err", err)
	}
}

func (m *Manager) ensureVersionFile(p Pool) error {
	if err := agentapi.ValidateSiteUser(p.User); err != nil {
		return fmt.Errorf("%w: %w", ErrInvalid, err)
	}

	if err := agentapi.ValidatePHPVersion(p.Version); err != nil {
		return fmt.Errorf("%w: %w", ErrInvalid, err)
	}

	if err := os.MkdirAll(m.VersionDir, versionDirMode); err != nil {
		return fmt.Errorf("php: version directory: %w", err)
	}

	fi, err := os.Lstat(m.VersionDir)
	if err != nil {
		return fmt.Errorf("php: version directory: %w", err)
	}

	if !fi.IsDir() {
		return fmt.Errorf("%w: %s is not a directory", ErrInvalid, m.VersionDir)
	}

	// The umask may have cut the mode; an older directory may be wider.
	if fi.Mode().Perm() != versionDirMode {
		if err := os.Chmod(m.VersionDir, versionDirMode); err != nil {
			return fmt.Errorf("php: version directory: %w", err)
		}
	}

	file := filepath.Join(m.VersionDir, p.User)
	content := []byte(p.Version + "\n")

	if fi, err := os.Lstat(file); err == nil && fi.Mode().IsRegular() && fi.Mode().Perm() == versionFileMode {
		if old, err := os.ReadFile(file); err == nil && bytes.Equal(old, content) {
			return nil
		}
	}

	// The rename replaces whatever is there, a symbolic link too, without
	// following it.
	return writeAtomicMode(file, content, versionFileMode)
}

// removeVersion deletes the version file of a site whose pool is gone.
func (m *Manager) removeVersion(ctx context.Context, user string) {
	if m.VersionDir == "" || agentapi.ValidateSiteUser(user) != nil {
		return
	}

	if err := os.Remove(filepath.Join(m.VersionDir, user)); err != nil && !errors.Is(err, os.ErrNotExist) {
		m.log().ErrorContext(ctx, "remove php version file", "site", user, "err", err)
	}
}

// DropInName is the file in a unit's drop-in directory that names the
// slice and mounts the site's home (ADR-0015, ADR-0033).
const DropInName = "50-hostashi-slice.conf"

// LogDir holds <site>/error.log of each site's PHP-FPM master, created by
// the unit's LogsDirectory=hostashi/php/%i (ADR-0053).
const LogDir = "/var/log/hostashi/php"

func (m *Manager) dropInDir(version, user string) string {
	return filepath.Join(m.UnitDir, Unit(version, user)+".d")
}

// writeDropIn puts the unit of p into its site's slice, mounts the site's
// home into the unit and reports whether the drop-in changed; systemd has
// read it when it returns true.
func (m *Manager) writeDropIn(ctx context.Context, p Pool) (bool, error) {
	if m.Units == nil || m.Slice == nil {
		return false, nil
	}

	// The unit hides /home (ProtectHome=tmpfs), the other pools
	// (TemporaryFileSystem=) and makes the system read only
	// (ProtectSystem=strict): the master sees no other site's home, and
	// only the site's own is mounted back, writable for its workers
	// (ADR-0033, ADR-0016). Validate has checked that the home is a plain
	// path.
	var hidden []string
	if m.Hidden != nil {
		hidden = m.Hidden()
	}

	unit, service := sites.UnitIsolation(p.Home, hidden)
	content := []byte("# Generated by hostashi for site " + p.User + " (ADR-0015, ADR-0033). Do not edit: changes are overwritten.\n" +
		"[Unit]\n" + unit +
		"[Service]\nSlice=" + m.Slice(p.User) + "\nBindPaths=" + p.Home + "\nReadWritePaths=" + p.Home + "\n" + service)

	dir := m.dropInDir(p.Version, p.User)
	file := filepath.Join(dir, DropInName)

	if old, err := os.ReadFile(file); err == nil && bytes.Equal(old, content) {
		return false, nil
	}

	if err := os.MkdirAll(dir, 0o755); err != nil {
		return false, fmt.Errorf("php: %w", err)
	}

	if err := writeAtomic(file, content); err != nil {
		return false, err
	}

	if err := os.Chmod(file, 0o644); err != nil {
		return false, fmt.Errorf("php: %w", err)
	}

	if err := m.Units.DaemonReload(ctx); err != nil {
		return false, err
	}

	return true, nil
}

// moveIntoSlice handles an unchanged pool whose unit has no drop-in yet
// (a site from before ADR-0015): it writes it and restarts the unit.
func (m *Manager) moveIntoSlice(ctx context.Context, p Pool) error {
	changed, err := m.writeDropIn(ctx, p)
	if err != nil || !changed {
		return err
	}

	if err := m.Units.Restart(ctx, Unit(p.Version, p.User)); err != nil {
		return fmt.Errorf("%w: site %s: %w", ErrStartFailed, p.User, err)
	}

	m.log().InfoContext(ctx, "php pool moved into its slice", "site", p.User, "version", p.Version)

	return nil
}

// removeDropIn deletes the drop-in of a stopped unit. Failures are only
// logged: a stale drop-in of a stopped unit does no harm.
func (m *Manager) removeDropIn(ctx context.Context, version, user string) {
	if m.Units == nil || m.UnitDir == "" {
		return
	}

	dir := m.dropInDir(version, user)
	if err := os.Remove(filepath.Join(dir, DropInName)); err != nil && !errors.Is(err, os.ErrNotExist) {
		m.log().ErrorContext(ctx, "remove php slice drop-in", "site", user, "err", err)
	}

	if err := os.Remove(dir); err != nil && !errors.Is(err, os.ErrNotExist) {
		m.log().InfoContext(ctx, "php drop-in directory not empty, kept", "site", user, "dir", dir)
	}
}

// Refresh writes the drop-ins of all pools again and restarts the units
// whose drop-in changed, after the set of storage pools changed (ADR-0016):
// every unit has to hide the new pool.
func (m *Manager) Refresh(ctx context.Context) error {
	st, err := m.Current()
	if err != nil {
		return err
	}

	pools := make([]Pool, 0, len(st))
	for _, p := range st {
		pools = append(pools, p)
	}

	sort.Slice(pools, func(i, j int) bool { return pools[i].User < pools[j].User })

	return m.Ensure(ctx, pools)
}
