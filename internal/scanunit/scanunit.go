// SPDX-License-Identifier: AGPL-3.0-or-later

// Package scanunit runs the malware scans of the sites (ADR-0064): one
// oneshot unit hostashi-scan@<site>.service per site, from a fixed
// template, as the site user, with the site's home mounted read only, in
// hostashi-scan.slice (not the site's slice, whose memory limit is far
// below what ClamAV needs). Like deployunit, it writes the drop-in from
// typed, checked values only and starts the unit without waiting. A
// finished run's log is read by the agent, and only lines that name a
// regular file inside the site's home become findings.
//
// The agent never runs clamscan itself: systemd does, as the site user, so
// a bug in libclamav reached through a hostile file gives that user
// nothing it did not have.
package scanunit

import (
	"bufio"
	"context"
	"crypto/sha256"
	"encoding/hex"
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
	"time"

	"golang.org/x/sys/unix"

	"github.com/maki-it/hostashi/internal/agentapi"
	"github.com/maki-it/hostashi/internal/appunit"
	"github.com/maki-it/hostashi/internal/sites"
	"github.com/maki-it/hostashi/internal/statefile"
)

// Fixed places on a server.
const (
	DefaultUnitDir = "/etc/systemd/system"
	// DefaultLogDir holds <site>.log, the output of the last run, opened
	// by systemd as root (StandardOutput=truncate: in the template).
	DefaultLogDir = "/var/log/hostashi/scan"
	// DefaultQuarantineDir holds quarantined files, root only.
	DefaultQuarantineDir = "/var/lib/hostashi-agent/quarantine"
	// DefaultDatabaseDir is ClamAV's signature directory.
	DefaultDatabaseDir = "/var/lib/clamav"
	// Clamscan is the distribution's scanner.
	Clamscan = "/usr/bin/clamscan"
	// DefaultClientDir holds <site>.conf, the clamdscan configuration of
	// a site's scan while the real-time mode is on (root 0711, files
	// 0644): the scan runs as the site user, who cannot read
	// DefaultRealtimeConfig below /etc/hostashi (root:hostashi 0750).
	DefaultClientDir = "/etc/hostashi-scan"
	// DropIn names the drop-in with the run.
	DropIn    = "60-hostashi-scan.conf"
	stateFile = "state.json"
	// startGrace is how long a queued start may take before the run
	// counts as failed. Runs wait for each other in the panel, not here.
	startGrace = 5 * time.Minute
	// maxLogLine and maxLogLines bound what is read of a log.
	maxLogLine  = 64 << 10
	maxLogLines = 200_000
	// maxErrorTail is how much of the log a failed run carries.
	maxErrorTail = 2048
	// maxErrorLine bounds the error line a partial run carries.
	maxErrorLine = 512
)

// Excluded are the directories of a home that are not scanned: caches
// that the site's own tools rebuild.
var Excluded = []string{".cache"}

// Errors of the operations.
var (
	// ErrBusy: a scan of the site is running.
	ErrBusy = errors.New("scanunit: a scan of this site is running")
	// ErrChanged: the file is gone or no longer has the finding's SHA-256.
	ErrChanged = errors.New("scanunit: the file changed since the scan")
	// ErrNotRegular: the path is not a regular file.
	ErrNotRegular = errors.New("scanunit: not a regular file")
	// ErrExists: something is in the way of a restore.
	ErrExists = errors.New("scanunit: a file exists at the original path")
	// ErrNoParent: the directory of a restore is gone.
	ErrNoParent = errors.New("scanunit: the directory of the original path is gone")
	// ErrTarget: a path of a scan of a selection is not a file or folder
	// in the home.
	ErrTarget = errors.New("scanunit: not a file or folder in the site")
	// ErrUnknownItem: no such file in the quarantine.
	ErrUnknownItem = errors.New("scanunit: no such file in the quarantine")
)

// Controller starts and reads the scan units (phpctl.ScanController).
type Controller interface {
	DaemonReload(ctx context.Context) error
	ResetFailed(ctx context.Context, unit string) error
	StartNoBlock(ctx context.Context, unit string) error
	Stop(ctx context.Context, unit string) error
	Show(ctx context.Context, unit string, props ...string) (map[string]string, error)
}

// Site is what the manager needs to know of a site's account.
type Site struct {
	Home     string
	UID, GID uint32
}

// Manager runs the scans. Set Dir, Site and Ctl.
type Manager struct {
	// Dir holds the state (root 0700).
	Dir           string
	UnitDir       string // default DefaultUnitDir
	LogDir        string // default DefaultLogDir
	QuarantineDir string // default DefaultQuarantineDir
	DatabaseDir   string // default DefaultDatabaseDir
	ClientDir     string // default DefaultClientDir
	// Site returns the account of a site user.
	Site func(site string) (Site, error)
	// Hidden returns the pools the unit must hide (sites.HiddenPaths).
	Hidden func() []string
	// Clamd reports whether the real-time mode's clamd serves scans; nil
	// is never.
	Clamd func(ctx context.Context) bool
	Ctl   Controller
	Log   *slog.Logger
	// now is time.Now; tests set it.
	now func() time.Time

	mu sync.Mutex
}

// Unit is the scan unit of a site.
func Unit(site string) string { return "hostashi-scan@" + site + ".service" }

func or(a, b string) string {
	if a != "" {
		return a
	}

	return b
}

func (m *Manager) unitDir() string { return or(m.UnitDir, DefaultUnitDir) }
func (m *Manager) logDir() string  { return or(m.LogDir, DefaultLogDir) }
func (m *Manager) qDir() string    { return or(m.QuarantineDir, DefaultQuarantineDir) }
func (m *Manager) dbDir() string   { return or(m.DatabaseDir, DefaultDatabaseDir) }

// ClientConfig is the clamdscan configuration of a site's scan.
func (m *Manager) ClientConfig(site string) string {
	return filepath.Join(or(m.ClientDir, DefaultClientDir), site+".conf")
}

// ClientConfigContent is what clamdscan reads as the site user: the
// socket of the real-time mode's clamd and the folders it skips, the same
// as clamscan. clamdscan walks the folders itself and hands clamd each
// open file (--fdpass).
func ClientConfigContent(site, home string) ([]byte, error) {
	if err := agentapi.ValidateSiteUser(site); err != nil {
		return nil, err
	}

	if !safePath(home) {
		return nil, fmt.Errorf("scanunit: invalid home %q", home)
	}

	var b strings.Builder

	b.WriteString("# Generated by hostashi for site " + site + " (ADR-0064). Do not edit: changes are overwritten.\n")
	b.WriteString("LocalSocket " + RealtimeSocket + "\n")

	for _, d := range Excluded {
		b.WriteString("ExcludePath ^" + regexp.QuoteMeta(home+"/"+d) + "/\n")
	}

	return []byte(b.String()), nil
}

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

func (m *Manager) dropInDir(site string) string {
	return filepath.Join(m.unitDir(), Unit(site)+".d")
}

// LogFile is the log of a site's last run.
func (m *Manager) LogFile(site string) string {
	return filepath.Join(m.logDir(), site+".log")
}

// run is the stored last run of a site.
type run struct {
	ID      int64     `json:"id"`
	Started time.Time `json:"started"`
}

func (m *Manager) load() (map[string]run, error) {
	b, err := os.ReadFile(filepath.Join(m.Dir, stateFile))
	if errors.Is(err, os.ErrNotExist) {
		return map[string]run{}, nil
	}

	if err != nil {
		return nil, fmt.Errorf("scanunit: state: %w", err)
	}

	st := map[string]run{}
	if err := json.Unmarshal(b, &st); err != nil {
		return nil, fmt.Errorf("scanunit: state: %w", err)
	}

	return st, nil
}

func (m *Manager) save(st map[string]run) error {
	b, err := json.MarshalIndent(st, "", "  ")
	if err != nil {
		return fmt.Errorf("scanunit: state: %w", err)
	}

	if err := os.MkdirAll(m.Dir, 0o700); err != nil {
		return fmt.Errorf("scanunit: %w", err)
	}

	if err := statefile.Write(m.Dir, stateFile, append(b, '\n')); err != nil {
		return fmt.Errorf("scanunit: %w", err)
	}

	return nil
}

// safePath allows plain absolute paths only: nothing systemd would read as
// a separator, quote, specifier or escape.
func safePath(p string) bool {
	return filepath.IsAbs(p) && filepath.Clean(p) == p && !strings.ContainsAny(p, " \t\n\"'\\%$;&|<>`*?[](){}")
}

// DropInContent ties the unit to the site's home, read only, and sets the
// scan: clamscan over the home with the fixed signature directory, or with
// a clamdscan configuration (clientConf, see ClientConfigContent) clamdscan,
// which hands each open file to the running clamd of the real-time mode.
// The template holds the sandbox, the slice, the log and the exit codes.
// targets, relative to the home and checked by the caller, narrow the scan
// to those files and folders; none scans the whole home.
func DropInContent(site string, acct Site, hidden []string, dbDir, clientConf string, targets []string) ([]byte, error) {
	if err := agentapi.ValidateSiteUser(site); err != nil {
		return nil, err
	}

	if !safePath(acct.Home) || !safePath(dbDir) || (clientConf != "" && !safePath(clientConf)) {
		return nil, fmt.Errorf("scanunit: invalid home %q or database directory %q", acct.Home, dbDir)
	}

	unit, service := sites.UnitIsolation(acct.Home, hidden)

	var b strings.Builder

	w := func(s ...string) { b.WriteString(strings.Join(s, "") + "\n") }

	w("# Generated by hostashi for site ", site, " (ADR-0064). Do not edit: changes are overwritten.")
	w("[Unit]")
	b.WriteString(unit)
	w("[Service]")
	b.WriteString(service)
	w("BindReadOnlyPaths=", acct.Home)
	w("WorkingDirectory=", acct.Home)
	w("Environment=HOME=", acct.Home)

	cmd := []string{Clamscan, "--recursive", "--infected", "--no-summary", "--stdout", "--database=" + dbDir}
	for _, d := range Excluded {
		cmd = append(cmd, appunit.Quote("--exclude-dir=^"+regexp.QuoteMeta(acct.Home+"/"+d)+"$"))
	}

	if clientConf != "" {
		cmd = []string{Clamdscan, "--config-file=" + clientConf, "--fdpass", "--infected", "--no-summary", "--stdout"}
	}

	if err := agentapi.ValidateScanTargets(targets); err != nil {
		return nil, err
	}

	if len(targets) == 0 {
		cmd = append(cmd, appunit.Quote(acct.Home))
	}

	for _, t := range targets {
		cmd = append(cmd, appunit.Quote(acct.Home+"/"+t))
	}

	w("ExecStart=")
	w("ExecStart=", strings.Join(cmd, " "))

	return []byte(b.String()), nil
}

// Start starts a scan of a site (site.scan.start).
func (m *Manager) Start(ctx context.Context, req agentapi.ScanStartRequest) (agentapi.ScanStatus, error) {
	if err := req.Validate(); err != nil {
		return agentapi.ScanStatus{}, err
	}

	acct, err := m.Site(req.Site)
	if err != nil {
		return agentapi.ScanStatus{}, err
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	st, err := m.load()
	if err != nil {
		return agentapi.ScanStatus{}, err
	}

	unit := Unit(req.Site)

	props, err := m.Ctl.Show(ctx, unit, "ActiveState")
	if err != nil {
		return agentapi.ScanStatus{}, err
	}

	r, ok := st[req.Site]
	if m.phase(props["ActiveState"], r, ok) == agentapi.ScanRunning {
		return agentapi.ScanStatus{}, ErrBusy
	}

	var hidden []string
	if m.Hidden != nil {
		hidden = m.Hidden()
	}

	if err := checkTargets(acct.Home, req.Paths); err != nil {
		return agentapi.ScanStatus{}, err
	}

	clientConf := ""
	if m.Clamd != nil && m.Clamd(ctx) {
		clientConf = m.ClientConfig(req.Site)

		conf, err := ClientConfigContent(req.Site, acct.Home)
		if err != nil {
			return agentapi.ScanStatus{}, err
		}

		// Explicit modes: the agent runs with UMask=0077, and the site user
		// must reach and read the file.
		dir := filepath.Dir(clientConf)
		if err := os.MkdirAll(dir, 0o711); err != nil {
			return agentapi.ScanStatus{}, fmt.Errorf("scanunit: %w", err)
		}

		if err := os.Chmod(dir, 0o711); err != nil {
			return agentapi.ScanStatus{}, fmt.Errorf("scanunit: %w", err)
		}

		if err := writeFile(clientConf, conf); err != nil {
			return agentapi.ScanStatus{}, err
		}

		if err := os.Chmod(clientConf, 0o644); err != nil {
			return agentapi.ScanStatus{}, fmt.Errorf("scanunit: %w", err)
		}
	}

	content, err := DropInContent(req.Site, acct, hidden, m.dbDir(), clientConf, req.Paths)
	if err != nil {
		return agentapi.ScanStatus{}, err
	}

	if err := os.MkdirAll(m.logDir(), 0o700); err != nil {
		return agentapi.ScanStatus{}, fmt.Errorf("scanunit: %w", err)
	}

	dir := m.dropInDir(req.Site)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return agentapi.ScanStatus{}, fmt.Errorf("scanunit: %w", err)
	}

	if err := writeFile(filepath.Join(dir, DropIn), content); err != nil {
		return agentapi.ScanStatus{}, err
	}

	if err := m.Ctl.DaemonReload(ctx); err != nil {
		return agentapi.ScanStatus{}, err
	}

	// The last run stays active (RemainAfterExit=) or failed until the
	// next one: stop it, so the start below runs again.
	if err := m.Ctl.Stop(ctx, unit); err != nil {
		return agentapi.ScanStatus{}, err
	}

	_ = m.Ctl.ResetFailed(ctx, unit)

	st[req.Site] = run{ID: req.ID, Started: m.clock().UTC()}
	if err := m.save(st); err != nil {
		return agentapi.ScanStatus{}, err
	}

	if err := m.Ctl.StartNoBlock(ctx, unit); err != nil {
		return agentapi.ScanStatus{}, err
	}

	m.log().InfoContext(ctx, "scan started", "site", req.Site, "id", req.ID)

	return agentapi.ScanStatus{Site: req.Site, ID: req.ID, State: agentapi.ScanRunning}, nil
}

// phase maps the unit's ActiveState to the state of the stored run r, as
// in deployunit: the template has RemainAfterExit=yes.
func (m *Manager) phase(state string, r run, ok bool) string {
	switch state {
	case "activating", "deactivating", "reloading":
		return agentapi.ScanRunning
	case "active":
		return agentapi.ScanSucceeded
	case "failed":
		return agentapi.ScanFailed
	}

	if ok && m.clock().Sub(r.Started) < startGrace {
		return agentapi.ScanRunning
	}

	return agentapi.ScanUnknown
}

// clamscan's exit codes (SuccessExitStatus=1 2 in the template): 1 means
// it found something, 2 that some files could not be scanned.
const (
	exitFound  = "1"
	exitErrors = "2"
)

// checkTargets reports ErrTarget unless every path is a regular file or a
// folder inside home, reached without leaving it and not a symlink itself.
func checkTargets(home string, paths []string) error {
	if len(paths) == 0 {
		return nil
	}

	root, err := os.OpenRoot(home)
	if err != nil {
		return fmt.Errorf("scanunit: %w", err)
	}
	defer root.Close()

	for _, p := range paths {
		fi, err := root.Lstat(p)
		if err != nil || !fi.Mode().IsRegular() && !fi.IsDir() {
			return fmt.Errorf("%w: %s", ErrTarget, p)
		}
	}

	return nil
}

// Status reports the state of a run (site.scan.status). A finished run
// carries its findings.
func (m *Manager) Status(ctx context.Context, req agentapi.ScanStatusRequest) (agentapi.ScanStatus, error) {
	if err := req.Validate(); err != nil {
		return agentapi.ScanStatus{}, err
	}

	acct, err := m.Site(req.Site)
	if err != nil {
		return agentapi.ScanStatus{}, err
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	out := agentapi.ScanStatus{Site: req.Site, ID: req.ID, State: agentapi.ScanUnknown}

	st, err := m.load()
	if err != nil {
		return out, err
	}

	r, ok := st[req.Site]
	if !ok || r.ID != req.ID {
		return out, nil
	}

	props, err := m.Ctl.Show(ctx, Unit(req.Site), "ActiveState", "Result", "ExecMainStatus")
	if err != nil {
		return out, err
	}

	switch m.phase(props["ActiveState"], r, true) {
	case agentapi.ScanRunning:
		out.State = agentapi.ScanRunning

		return out, nil
	case agentapi.ScanSucceeded:
		out.State = agentapi.ScanSucceeded
	case agentapi.ScanFailed:
		out.State, out.Error = agentapi.ScanFailed, failReason(props["Result"])
		if t := tail(m.LogFile(req.Site)); t != "" {
			out.Error += ": " + t
		}

		return out, nil
	default:
		out.State, out.Error = agentapi.ScanFailed, "the scan did not start"

		return out, nil
	}

	if props["ExecMainStatus"] == exitErrors {
		out.Error = "some files could not be scanned"
		if e := firstError(m.LogFile(req.Site)); e != "" {
			out.Error += ": " + e
		}
	}

	f, err := os.Open(m.LogFile(req.Site))
	if err != nil {
		if props["ExecMainStatus"] == exitFound {
			out.State, out.Error = agentapi.ScanFailed, "the scan found files but left no log"
		}

		return out, nil
	}
	defer f.Close()

	out.Findings, out.Dropped = ParseLog(f, acct.Home)

	if props["ExecMainStatus"] == exitFound && len(out.Findings) == 0 && out.Dropped == 0 {
		out.State, out.Error = agentapi.ScanFailed, "the scan found files but its log names none"
	}

	return out, nil
}

func failReason(result string) string {
	switch result {
	case "timeout":
		return "the scan took longer than its time limit"
	case "exit-code":
		return "the scanner failed"
	case "signal", "core-dump":
		return "the scan was killed"
	case "oom-kill":
		return "the scan ran out of memory"
	}

	return "the scan failed (" + result + ")"
}

// ParseLog reads clamscan's output (--infected --stdout) and returns the
// findings below home: lines "<path>: <signature> FOUND" whose path is a
// regular file inside home, with its SHA-256 and size. The log is the
// output of a run as the site user over files the site user controls, so
// every line is untrusted: a file name with a newline can make up a line,
// which at most names another file of the same site. Lines that do not
// pass are counted in dropped; other lines (warnings) are skipped.
func ParseLog(r io.Reader, home string) (findings []agentapi.ScanFinding, dropped int) {
	root, err := os.OpenRoot(home)
	if err != nil {
		return nil, 0
	}
	defer root.Close()

	seen := map[string]bool{}
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 4096), maxLogLine)

	for n := 0; n < maxLogLines && sc.Scan(); n++ {
		line, ok := strings.CutSuffix(sc.Text(), " FOUND")
		if !ok {
			continue
		}

		i := strings.LastIndex(line, ": ")
		if i < 0 {
			dropped++

			continue
		}

		p, sig := line[:i], line[i+2:]

		rel, ok := strings.CutPrefix(p, home+"/")
		if !ok || agentapi.ValidateScanPath(rel) != nil || agentapi.ValidateSignature(sig) != nil || seen[rel] {
			dropped++

			continue
		}

		if len(findings) >= agentapi.MaxScanFindings {
			dropped++

			continue
		}

		sum, size, err := hashFile(root, rel)
		if err != nil {
			dropped++

			continue
		}

		seen[rel] = true
		findings = append(findings, agentapi.ScanFinding{Path: rel, Signature: sig, SHA256: sum, Size: size})
	}

	return findings, dropped
}

// openRegular opens a regular file below root without following a final
// symlink (os.Root keeps every step inside root).
func openRegular(root *os.Root, rel string) (*os.File, os.FileInfo, error) {
	f, err := root.OpenFile(rel, os.O_RDONLY|unix.O_NOFOLLOW|unix.O_NONBLOCK, 0)
	if err != nil {
		return nil, nil, err
	}

	fi, err := f.Stat()
	if err != nil {
		_ = f.Close()

		return nil, nil, err
	}

	if !fi.Mode().IsRegular() {
		_ = f.Close()

		return nil, nil, ErrNotRegular
	}

	return f, fi, nil
}

func hashFile(root *os.Root, rel string) (string, int64, error) {
	f, fi, err := openRegular(root, rel)
	if err != nil {
		return "", 0, err
	}
	defer f.Close()

	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", 0, err
	}

	return hex.EncodeToString(h.Sum(nil)), fi.Size(), nil
}

// tail is the end of a log, at most maxErrorTail bytes on one line.
// firstError returns the first line of a log that reports an error of
// clamscan or clamdscan ("<path>: <reason> ERROR", "ERROR: ...",
// "LibClamAV Error: ..."), cut to maxErrorLine and with control
// characters replaced; "" if there is none.
func firstError(name string) string {
	f, err := os.Open(name)
	if err != nil {
		return ""
	}
	defer f.Close()

	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 4096), maxLogLine)

	for n := 0; n < maxLogLines && sc.Scan(); n++ {
		line := sc.Text()
		if !strings.HasSuffix(line, " ERROR") && !strings.HasPrefix(line, "ERROR: ") && !strings.HasPrefix(line, "LibClamAV Error: ") {
			continue
		}

		line = strings.Map(func(r rune) rune {
			if r < 0x20 || r == 0x7f {
				return ' '
			}

			return r
		}, strings.ToValidUTF8(line, "?"))

		if len(line) > maxErrorLine {
			line = strings.ToValidUTF8(line[:maxErrorLine], "") + "..."
		}

		return line
	}

	return ""
}

func tail(name string) string {
	f, err := os.Open(name)
	if err != nil {
		return ""
	}
	defer f.Close()

	fi, err := f.Stat()
	if err != nil {
		return ""
	}

	off := max(fi.Size()-maxErrorTail, 0)

	b := make([]byte, fi.Size()-off)
	if _, err := f.ReadAt(b, off); err != nil && !errors.Is(err, io.EOF) {
		return ""
	}

	s := strings.Map(func(r rune) rune {
		if r < 0x20 || r == 0x7f {
			return ' '
		}

		return r
	}, strings.ToValidUTF8(string(b), "?"))

	return strings.TrimSpace(s)
}

// Remove deletes the unit's drop-in, the state, log and quarantine of a
// deleted site.
func (m *Manager) Remove(ctx context.Context, site string) error {
	if err := agentapi.ValidateSiteUser(site); err != nil {
		return err
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	if err := m.Ctl.Stop(ctx, Unit(site)); err != nil {
		m.log().WarnContext(ctx, "stop scan of deleted site", "site", site, "err", err)
	}

	dir := m.dropInDir(site)
	if err := os.Remove(filepath.Join(dir, DropIn)); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return fmt.Errorf("scanunit: %w", err)
	}

	_ = os.Remove(dir)
	_ = os.Remove(m.LogFile(site))
	_ = os.Remove(m.ClientConfig(site))

	if err := os.RemoveAll(filepath.Join(m.qDir(), site)); err != nil {
		return fmt.Errorf("scanunit: %w", err)
	}

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

// writeFile writes a root-owned 0644 file atomically.
func writeFile(name string, data []byte) error {
	tmp := name + ".tmp"
	_ = os.Remove(tmp)

	f, err := os.OpenFile(tmp, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o644)
	if err != nil {
		return fmt.Errorf("scanunit: %w", err)
	}

	_, err = f.Write(data)

	err = errors.Join(err, f.Sync(), f.Close())
	if err == nil {
		err = os.Rename(tmp, name)
	}

	if err != nil {
		_ = os.Remove(tmp)

		return fmt.Errorf("scanunit: %w", err)
	}

	return nil
}
