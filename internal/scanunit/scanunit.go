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
	// maxLogLine bounds a line read of a log.
	maxLogLine = 64 << 10
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
	ListDir       string // default DefaultListDir
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
	// prog is the progress of the running scans, by site (under mu).
	prog map[string]*progress
	// lists counts the list walks still going; tests wait for them.
	lists sync.WaitGroup
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
// as clamscan. clamdscan walks the folders itself and streams each file's
// content to clamd (--stream), up to StreamMaxLength (100M by default, the
// same as clamscan's MaxFileSize).
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
// scan: clamscan with the fixed signature directory, or with a clamdscan
// configuration (clientConf, see ClientConfigContent) clamdscan, which
// hands each file to the running clamd of the real-time mode. Both read
// the files to scan from list (see writeList) and print a line for each,
// which is what the progress counts. The template holds the sandbox, the
// slice, the log and the exit codes.
func DropInContent(site string, acct Site, hidden []string, dbDir, clientConf, list string) ([]byte, error) {
	if err := agentapi.ValidateSiteUser(site); err != nil {
		return nil, err
	}

	if !safePath(acct.Home) || !safePath(dbDir) || (clientConf != "" && !safePath(clientConf)) || !safePath(list) {
		return nil, fmt.Errorf("scanunit: invalid home %q, database directory %q or list %q", acct.Home, dbDir, list)
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

	// --recursive: a list names a folder where a file name could not
	// stand on a line, or when the home has too many files.
	cmd := []string{Clamscan, "--recursive", "--no-summary", "--stdout", "--database=" + dbDir}
	for _, d := range Excluded {
		cmd = append(cmd, appunit.Quote("--exclude-dir=^"+regexp.QuoteMeta(acct.Home+"/"+d)+"$"))
	}

	if clientConf != "" {
		// --stream, not --fdpass: the unit opens files in its own mount
		// namespace, and AppArmor refuses clamd a descriptor whose path
		// it cannot resolve ("disconnected path", web3 2026-11-25).
		cmd = []string{Clamdscan, "--config-file=" + clientConf, "--stream", "--no-summary", "--stdout"}
	}

	cmd = append(cmd, appunit.Quote("--file-list="+list))

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
	if m.phase(req.Site, props["ActiveState"], r, ok) == agentapi.ScanRunning {
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

	content, err := DropInContent(req.Site, acct, hidden, m.dbDir(), clientConf, m.ListFile(req.Site))
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

	// The output of the last run would count as progress of this one.
	_ = os.Remove(m.LogFile(req.Site))

	st[req.Site] = run{ID: req.ID, Started: m.clock().UTC()}
	if err := m.save(st); err != nil {
		return agentapi.ScanStatus{}, err
	}

	// The list may take a while on a large home: it is written in the
	// background, and the unit starts once it is there. Until then the
	// run counts as running (see phase).
	wctx, cancel := context.WithCancel(context.WithoutCancel(ctx))
	m.setProgress(req.Site, &progress{id: req.ID, listing: true, atStart: true, cancel: cancel})

	m.lists.Add(1)

	go func() {
		defer m.lists.Done()
		defer cancel()

		m.list(wctx, req.Site, req.ID, acct, req.Paths)
	}()

	m.log().InfoContext(ctx, "scan started", "site", req.Site, "id", req.ID)

	return agentapi.ScanStatus{Site: req.Site, ID: req.ID, State: agentapi.ScanRunning, Progress: &agentapi.ScanProgress{}}, nil
}

// setProgress replaces the progress of a site's scan, ending a list walk
// of an earlier one. Callers hold m.mu.
func (m *Manager) setProgress(site string, p *progress) {
	if m.prog == nil {
		m.prog = map[string]*progress{}
	}

	if old := m.prog[site]; old != nil && old.cancel != nil {
		old.cancel()
	}

	if p == nil {
		delete(m.prog, site)

		return
	}

	m.prog[site] = p
}

// list writes the list of run id and starts its unit, unless another run
// or the removal of the site came first.
func (m *Manager) list(ctx context.Context, site string, id int64, acct Site, targets []string) {
	n, err := writeList(ctx, m.ListFile(site), acct.Home, targets, acct.GID)

	m.mu.Lock()
	defer m.mu.Unlock()

	p := m.prog[site]
	if p == nil || p.id != id || ctx.Err() != nil {
		return
	}

	p.listing = false

	if err != nil {
		m.log().ErrorContext(ctx, "scan list", "site", site, "id", id, "err", err)
		p.err = "the files of the site could not be listed"

		return
	}

	p.total = n

	if err := m.Ctl.StartNoBlock(ctx, Unit(site)); err != nil {
		m.log().ErrorContext(ctx, "start scan", "site", site, "id", id, "err", err)
		p.err = "the scan did not start"
	}
}

// WaitLists waits for the list walks still going (tests).
func (m *Manager) WaitLists() { m.lists.Wait() }

// phase maps the unit's ActiveState to the state of the stored run r, as
// in deployunit: the template has RemainAfterExit=yes. While the agent
// still writes the run's list, the run is running. Callers hold m.mu.
func (m *Manager) phase(site, state string, r run, ok bool) string {
	if p := m.prog[site]; ok && p != nil && p.id == r.ID {
		switch {
		case p.listing:
			return agentapi.ScanRunning
		case p.err != "" && state == "inactive":
			return agentapi.ScanFailed
		}
	}

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

	p := m.prog[req.Site]
	if p != nil && p.id != req.ID {
		p = nil
	}

	if p != nil && p.err != "" && props["ActiveState"] == "inactive" {
		out.State, out.Error = agentapi.ScanFailed, p.err

		return out, nil
	}

	switch m.phase(req.Site, props["ActiveState"], r, true) {
	case agentapi.ScanRunning:
		out.State = agentapi.ScanRunning

		// After a restart of the agent, the list and the log tell.
		if p == nil {
			p = &progress{id: req.ID, total: countLines(m.ListFile(req.Site)), atStart: true}
			m.setProgress(req.Site, p)
		}

		if !p.listing {
			p.advance(m.LogFile(req.Site))
		}

		out.Progress = p.report()

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
		// Files deleted between the list and the scan are no error: a
		// busy site removes cache files all the time.
		if e, gone := firstError(m.LogFile(req.Site), acct.Home); e != "" || !gone {
			out.Error = "some files could not be scanned"
			if e != "" {
				out.Error += ": " + e
			}
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

// ParseLog reads the scanner's output (--stdout, a line per file) and
// returns the findings below home: lines "<path>: <signature> FOUND" whose path is a
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

	// No bound on the lines: the log has one per file of the home, and
	// the findings are bounded by MaxScanFindings.
	for sc.Scan() {
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

// clean replaces control characters and invalid UTF-8 of a log line.
func clean(line string) string {
	return strings.Map(func(r rune) rune {
		if r < 0x20 || r == 0x7f {
			return ' '
		}

		return r
	}, strings.ToValidUTF8(line, "?"))
}

// vanished returns the file a line says is gone, as clamscan ("<path>: No
// such file or directory") and clamdscan ("ERROR: Can't access file
// <path>") report a listed file deleted before its turn.
func vanished(line string) (string, bool) {
	if p, ok := strings.CutSuffix(line, ": No such file or directory"); ok {
		return p, true
	}

	return strings.CutPrefix(line, "ERROR: Can't access file ")
}

// isError reports whether a log line is an error of clamscan or clamdscan
// ("<path>: <reason> ERROR", "ERROR: ...", "LibClamAV Error: ...",
// "<path>: Access denied").
func isError(line string) bool {
	return strings.HasSuffix(line, " ERROR") || strings.HasPrefix(line, "ERROR: ") ||
		strings.HasPrefix(line, "LibClamAV Error: ") || strings.HasSuffix(line, ": Access denied")
}

// firstError returns the first line of a log that reports an error, cut
// to maxErrorLine and with control characters replaced; "" if there is
// none. A line about a file below home that was listed but is gone now is
// no error: gone reports that there was such a line and no other error.
func firstError(name, home string) (line string, gone bool) {
	f, err := os.Open(name)
	if err != nil {
		return "", false
	}
	defer f.Close()

	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 4096), maxLogLine)

	for sc.Scan() {
		line := sc.Text()

		if p, ok := vanished(line); ok && strings.HasPrefix(p, home+"/") {
			if _, err := os.Lstat(p); errors.Is(err, fs.ErrNotExist) {
				gone = true

				continue
			}
		}

		if !isError(line) {
			continue
		}

		line = clean(line)
		if len(line) > maxErrorLine {
			line = strings.ToValidUTF8(line[:maxErrorLine], "") + "..."
		}

		return line, false
	}

	return "", gone
}

// tail is the end of a log without the lines about single files (the
// scanner's own messages, which say why it stopped), at most maxErrorTail
// bytes on one line.
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

	off := max(fi.Size()-tailWindow, 0)

	b := make([]byte, fi.Size()-off)
	if _, err := f.ReadAt(b, off); err != nil && !errors.Is(err, io.EOF) {
		return ""
	}

	lines := strings.Split(string(b), "\n")
	if off > 0 {
		lines = lines[1:] // cut in the middle
	}

	kept := lines[:0]

	for _, l := range lines {
		if !strings.HasPrefix(l, "/") {
			kept = append(kept, l)
		}
	}

	s := strings.TrimSpace(clean(strings.Join(kept, "\n")))
	if len(s) > maxErrorTail {
		s = strings.ToValidUTF8(s[len(s)-maxErrorTail:], "")
	}

	return s
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
	_ = os.Remove(m.ListFile(site))

	m.setProgress(site, nil)

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
