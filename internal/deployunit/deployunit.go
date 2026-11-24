// SPDX-License-Identifier: AGPL-3.0-or-later

// Package deployunit runs the Git deploys of the sites (ADR-0062): one
// oneshot unit hostashi-deploy@<site>.service per site, from a fixed
// template, as the site user in the site's slice. Like appunit, it writes
// drop-ins from typed, checked values only: the slice and the home, and
// the run itself, which is `hostashi git-sync` into a new release followed
// by the build commands in one script. Token and deploy key reach the run
// as systemd credentials, never as arguments or in the drop-in. The
// manager starts the unit without waiting, reports its state, makes a
// release live by replacing the deploy directory with a link, and deletes
// old releases. It never runs git or a build command itself.
package deployunit

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
	"path"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/maki-it/hostashi/internal/agentapi"
	"github.com/maki-it/hostashi/internal/appunit"
	"github.com/maki-it/hostashi/internal/cron"
	"github.com/maki-it/hostashi/internal/statefile"
)

// Fixed places on a server.
const (
	DefaultUnitDir = "/etc/systemd/system"
	// DefaultScriptDir holds the build scripts, one directory per site,
	// root:site 0750 with the scripts 0640, as for cron jobs.
	DefaultScriptDir = "/var/lib/hostashi-deploy"
	// DefaultRunDir holds token and key while a run needs them, root
	// only; systemd hands them to the unit (LoadCredential=).
	DefaultRunDir = "/run/hostashi-deploy"
	// DefaultLogDir holds <site>/deploy-<id>.log, opened by systemd as
	// root (StandardOutput=truncate:).
	DefaultLogDir = appunit.DefaultLogDir
	// DefaultExe is the hostashi binary with the git-sync command.
	DefaultExe = "/usr/local/bin/hostashi"
	// DefaultNodeDir and DefaultPythonDir hold the runtime lines.
	DefaultNodeDir = appunit.DefaultNodeDir
	// DropIn names the drop-in with the run.
	DropIn    = "60-hostashi-deploy.conf"
	stateFile = "state.json"
	// syncBudget is what a run may take besides the build commands: the
	// fetch and the checkout.
	syncBudget = 15 * time.Minute
	// startGrace is how long a queued start may take before the run
	// counts as failed.
	startGrace = 5 * time.Minute
	// keepLogs is how many run logs of a site stay.
	keepLogs = 30
	// maxResult bounds git-sync's result file.
	maxResult = 64 << 10
)

// Errors of the operations.
var (
	// ErrBusy: a deploy of the site is running.
	ErrBusy = errors.New("deployunit: a deploy of this site is running")
	// ErrRelease: the release is missing or not a directory.
	ErrRelease = errors.New("deployunit: the release does not exist")
	// ErrDeployDir: the deploy directory is neither a directory nor a
	// link, or its parent is missing.
	ErrDeployDir = errors.New("deployunit: the deploy directory cannot be replaced")
)

// Controller drives systemd (phpctl.DeployController on a server).
type Controller interface {
	DaemonReload(ctx context.Context) error
	ResetFailed(ctx context.Context, unit string) error
	// StartNoBlock queues the start of a unit and returns.
	StartNoBlock(ctx context.Context, unit string) error
	Stop(ctx context.Context, unit string) error
	Show(ctx context.Context, unit string, props ...string) (map[string]string, error)
}

// Site is what the manager needs to know of a site's account.
type Site struct {
	Home     string
	UID, GID uint32
	Slice    string
}

// Manager runs the deploys. Set Dir, Site and Ctl.
type Manager struct {
	// Dir holds the state (root 0700).
	Dir       string
	UnitDir   string // default DefaultUnitDir
	ScriptDir string // default DefaultScriptDir
	CredDir   string // default DefaultRunDir
	LogDir    string // default DefaultLogDir
	Exe       string // default DefaultExe
	NodeDir   string // default DefaultNodeDir
	// Site returns the account of a site user.
	Site func(site string) (Site, error)
	// Hidden returns the pools the unit must hide (sites.HiddenPaths).
	Hidden func() []string
	Ctl    Controller
	Log    *slog.Logger
	// now is time.Now; tests set it.
	now func() time.Time

	mu sync.Mutex
}

// Unit is the deploy unit of a site.
func Unit(site string) string { return "hostashi-deploy@" + site + ".service" }

func or(a, b string) string {
	if a != "" {
		return a
	}

	return b
}

func (m *Manager) unitDir() string   { return or(m.UnitDir, DefaultUnitDir) }
func (m *Manager) scriptDir() string { return or(m.ScriptDir, DefaultScriptDir) }
func (m *Manager) credDir() string   { return or(m.CredDir, DefaultRunDir) }
func (m *Manager) logDir() string    { return or(m.LogDir, DefaultLogDir) }
func (m *Manager) exe() string       { return or(m.Exe, DefaultExe) }
func (m *Manager) nodeDir() string   { return or(m.NodeDir, DefaultNodeDir) }

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

// LogFile is the log of a run.
func (m *Manager) LogFile(site string, id int64) string {
	return filepath.Join(m.logDir(), site, "deploy-"+strconv.FormatInt(id, 10)+".log")
}

// run is the stored last run of a site: its deploy id and when it was
// started.
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
		return nil, fmt.Errorf("deployunit: state: %w", err)
	}

	st := map[string]run{}
	if err := json.Unmarshal(b, &st); err != nil {
		return nil, fmt.Errorf("deployunit: state: %w", err)
	}

	return st, nil
}

func (m *Manager) save(st map[string]run) error {
	b, err := json.MarshalIndent(st, "", "  ")
	if err != nil {
		return fmt.Errorf("deployunit: state: %w", err)
	}

	if err := os.MkdirAll(m.Dir, 0o700); err != nil {
		return fmt.Errorf("deployunit: %w", err)
	}

	if err := statefile.Write(m.Dir, stateFile, append(b, '\n')); err != nil {
		return fmt.Errorf("deployunit: %w", err)
	}

	return nil
}

// paths are the places of one run.
type paths struct {
	release, cache, shared, result string
}

func runPaths(home string, id int64) paths {
	return paths{
		release: home + "/" + agentapi.DeployReleasesDir + "/" + agentapi.ReleaseName(id),
		cache:   home + "/" + agentapi.DeployCacheDir,
		shared:  home + "/" + agentapi.DeploySharedDir,
		result:  home + "/" + agentapi.DeployResultsDir + "/" + agentapi.ReleaseName(id) + ".json",
	}
}

// safePath allows plain absolute paths only: nothing systemd or the shell
// would read as a separator, quote, specifier or escape.
func safePath(p string) bool {
	return filepath.IsAbs(p) && filepath.Clean(p) == p && !strings.ContainsAny(p, " \t\n\"'\\%$;&|<>`*?[](){}")
}

// DropInContent is the run: git-sync with quoted, checked values, then the
// build script if there are commands. cred names the credential file of
// the token or key below systemd's credentials directory.
func (m *Manager) DropInContent(req agentapi.DeployStartRequest, acct Site, script string) ([]byte, error) {
	if err := req.Validate(); err != nil {
		return nil, err
	}

	if !safePath(acct.Home) || !safePath(m.exe()) || script != "" && !safePath(script) {
		return nil, fmt.Errorf("deployunit: invalid home %q, program or script", acct.Home)
	}

	p := runPaths(acct.Home, req.ID)
	q := appunit.Quote

	args := []string{
		m.exe(), "git-sync",
		q("-url=" + req.URL), q("-branch=" + req.Branch), q("-auth=" + req.Auth),
		q("-cache=" + p.cache), q("-release=" + p.release), q("-result=" + p.result),
	}

	var cred string

	switch req.Auth {
	case agentapi.GitAuthToken:
		cred = "LoadCredential=token:" + m.credFile(req.Site, "token") + "\n"

		args = append(args, "-token-file=%d/token")

		if req.TokenUser != "" {
			args = append(args, q("-token-user="+req.TokenUser))
		}
	case agentapi.GitAuthSSH:
		cred = "LoadCredential=key:" + m.credFile(req.Site, "key") + "\n"
		args = append(args, "-key-file=%d/key", q("-host-key="+strings.TrimSpace(req.HostKey)))
	}

	if len(req.Shared) > 0 {
		args = append(args, q("-shared-dir="+p.shared))

		for _, s := range req.Shared {
			args = append(args, q("-shared="+s))
		}
	}

	envPath := "/usr/local/bin:/usr/bin:/bin"

	switch {
	case req.Node != "":
		envPath = path.Join(m.nodeDir(), req.Node, "bin") + ":" + envPath
	case req.Python != "":
		envPath = acct.Home + "/" + agentapi.PythonVenvDir + "/bin:" + envPath
	}

	var b strings.Builder

	w := func(s ...string) { b.WriteString(strings.Join(s, "") + "\n") }

	w("# Generated by hostashi for site ", req.Site, ", deploy ", strconv.FormatInt(req.ID, 10), " (ADR-0062). Do not edit: changes are overwritten.")
	w("[Service]")
	w("WorkingDirectory=", acct.Home)
	// An empty ExecStart= drops the template's placeholder.
	w("ExecStart=")
	w("ExecStart=", strings.Join(args, " "))

	if script != "" {
		w("ExecStart=/bin/sh ", script)
	}

	b.WriteString(cred)
	w("TimeoutStartSec=", strconv.Itoa(int((syncBudget/time.Second))+req.TimeoutSeconds))
	w("StandardOutput=truncate:", m.LogFile(req.Site, req.ID))
	w("Environment=HOME=", acct.Home, " USER=", req.Site, " LOGNAME=", req.Site)
	w("Environment=PATH=", envPath)
	w("Environment=HOSTASHI_DEPLOY_ID=", strconv.FormatInt(req.ID, 10), " HOSTASHI_RELEASE=", p.release)

	return []byte(b.String()), nil
}

func (m *Manager) credFile(site, name string) string {
	return filepath.Join(m.credDir(), site, name)
}

// shellQuote quotes s for /bin/sh.
func shellQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

// Script runs the build commands in the release, each shown in the log
// before it runs; the first that fails ends the run.
func Script(release string, commands []string) ([]byte, error) {
	if !safePath(release) {
		return nil, fmt.Errorf("deployunit: invalid release %q", release)
	}

	var b strings.Builder

	b.WriteString("#!/bin/sh\n# Written by the Hostashi agent; changes are overwritten.\nset -e\ncd " + shellQuote(release) + "\n")

	for _, c := range commands {
		if err := cron.ValidateCommand(c); err != nil {
			return nil, err
		}

		b.WriteString("printf '\\n$ %s\\n' " + shellQuote(c) + "\n" + c + "\n")
	}

	return []byte(b.String()), nil
}

// Start writes the run's drop-ins, script and credentials and queues the
// unit's start (site.deploy.start). A running deploy of the site is
// ErrBusy.
func (m *Manager) Start(ctx context.Context, req agentapi.DeployStartRequest) (agentapi.DeployStatus, error) {
	if err := req.Validate(); err != nil {
		return agentapi.DeployStatus{}, err
	}

	acct, err := m.Site(req.Site)
	if err != nil {
		return agentapi.DeployStatus{}, err
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	st, err := m.load()
	if err != nil {
		return agentapi.DeployStatus{}, err
	}

	unit := Unit(req.Site)

	props, err := m.Ctl.Show(ctx, unit, "ActiveState")
	if err != nil {
		return agentapi.DeployStatus{}, err
	}

	r, ok := st[req.Site]
	if m.phase(props["ActiveState"], r, ok) == agentapi.DeployRunning {
		return agentapi.DeployStatus{}, ErrBusy
	}

	p := runPaths(acct.Home, req.ID)
	if _, err := os.Lstat(p.release); !errors.Is(err, fs.ErrNotExist) {
		return agentapi.DeployStatus{}, fmt.Errorf("deployunit: release %s exists already", agentapi.ReleaseName(req.ID))
	}

	if err := m.writeCreds(req); err != nil {
		return agentapi.DeployStatus{}, err
	}

	script := ""

	if len(req.Commands) > 0 {
		script, err = m.writeScript(req, acct, p.release)
		if err != nil {
			return agentapi.DeployStatus{}, err
		}
	}

	if err := m.writeUnit(ctx, req, acct, script); err != nil {
		return agentapi.DeployStatus{}, err
	}

	// The last run stays active (RemainAfterExit=) or failed until the
	// next one: stop it, so the start below runs again.
	if err := m.Ctl.Stop(ctx, unit); err != nil {
		return agentapi.DeployStatus{}, err
	}

	_ = m.Ctl.ResetFailed(ctx, unit)

	st[req.Site] = run{ID: req.ID, Started: m.clock().UTC()}
	if err := m.save(st); err != nil {
		return agentapi.DeployStatus{}, err
	}

	if err := m.Ctl.StartNoBlock(ctx, unit); err != nil {
		return agentapi.DeployStatus{}, err
	}

	m.pruneLogs(req.Site, req.ID)
	m.log().InfoContext(ctx, "deploy started", "site", req.Site, "id", req.ID, "branch", req.Branch, "commands", len(req.Commands))

	return agentapi.DeployStatus{Site: req.Site, ID: req.ID, State: agentapi.DeployRunning}, nil
}

// phase maps the unit's ActiveState to the state of the stored run r. The
// template has RemainAfterExit=yes, so a finished run stays "active"
// (succeeded) or "failed" until the next start stops it; systemd keeps
// such a unit loaded. "inactive" is a start that is still queued, or one
// that never ran once startGrace is over (DeployUnknown here).
func (m *Manager) phase(state string, r run, ok bool) string {
	switch state {
	case "activating", "deactivating", "reloading":
		return agentapi.DeployRunning
	case "active":
		return agentapi.DeploySucceeded
	case "failed":
		return agentapi.DeployFailed
	}

	if ok && m.clock().Sub(r.Started) < startGrace {
		return agentapi.DeployRunning
	}

	return agentapi.DeployUnknown
}

// writeCreds writes the token or key of the run, root only, and removes
// what the auth does not need.
func (m *Manager) writeCreds(req agentapi.DeployStartRequest) error {
	if err := ensureDir(m.credDir(), -1, 0o700); err != nil {
		return err
	}

	dir := filepath.Join(m.credDir(), req.Site)
	if err := ensureDir(dir, -1, 0o700); err != nil {
		return err
	}

	files := map[string][]byte{}

	switch req.Auth {
	case agentapi.GitAuthToken:
		files["token"] = []byte(req.Token)
	case agentapi.GitAuthSSH:
		files["key"] = []byte(req.Key)
	}

	for _, name := range []string{"token", "key"} {
		p := filepath.Join(dir, name)

		data, ok := files[name]
		if !ok {
			if err := os.Remove(p); err != nil && !errors.Is(err, fs.ErrNotExist) {
				return fmt.Errorf("deployunit: %w", err)
			}

			continue
		}

		if err := writeFile(p, data, -1, 0o600); err != nil {
			return err
		}
	}

	return nil
}

// removeCreds deletes a site's credentials once no run needs them.
func (m *Manager) removeCreds(site string) {
	_ = os.RemoveAll(filepath.Join(m.credDir(), site))
}

func (m *Manager) writeScript(req agentapi.DeployStartRequest, acct Site, release string) (string, error) {
	data, err := Script(release, req.Commands)
	if err != nil {
		return "", err
	}

	if err := ensureDir(m.scriptDir(), -1, 0o755); err != nil {
		return "", err
	}

	dir := filepath.Join(m.scriptDir(), req.Site)
	if err := ensureDir(dir, int(acct.GID), 0o750); err != nil {
		return "", err
	}

	name := filepath.Join(dir, "build.sh")
	if err := writeFile(name, data, int(acct.GID), 0o640); err != nil {
		return "", err
	}

	return name, nil
}

func (m *Manager) writeUnit(ctx context.Context, req agentapi.DeployStartRequest, acct Site, script string) error {
	content, err := m.DropInContent(req, acct, script)
	if err != nil {
		return err
	}

	var hidden []string
	if m.Hidden != nil {
		hidden = m.Hidden()
	}

	if err := os.MkdirAll(filepath.Join(m.logDir(), req.Site), 0o700); err != nil {
		return fmt.Errorf("deployunit: %w", err)
	}

	dir := m.dropInDir(req.Site)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("deployunit: %w", err)
	}

	slice := appunit.SliceDropInContent(req.Site, appunit.Site{Home: acct.Home, UID: acct.UID, Slice: acct.Slice}, hidden)

	if err := writeFile(filepath.Join(dir, appunit.SliceDropIn), slice, -1, 0o644); err != nil {
		return err
	}

	if err := writeFile(filepath.Join(dir, DropIn), content, -1, 0o644); err != nil {
		return err
	}

	return m.Ctl.DaemonReload(ctx)
}

// Status reports the state of a run (site.deploy.status). A finished run
// carries git-sync's result; its credentials are deleted then.
func (m *Manager) Status(ctx context.Context, req agentapi.DeployStatusRequest) (agentapi.DeployStatus, error) {
	if err := req.Validate(); err != nil {
		return agentapi.DeployStatus{}, err
	}

	acct, err := m.Site(req.Site)
	if err != nil {
		return agentapi.DeployStatus{}, err
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	out := agentapi.DeployStatus{Site: req.Site, ID: req.ID, State: agentapi.DeployUnknown}

	st, err := m.load()
	if err != nil {
		return out, err
	}

	r, ok := st[req.Site]
	if !ok || r.ID != req.ID {
		return out, nil
	}

	props, err := m.Ctl.Show(ctx, Unit(req.Site), "ActiveState", "Result")
	if err != nil {
		return out, err
	}

	switch m.phase(props["ActiveState"], r, true) {
	case agentapi.DeployRunning:
		out.State = agentapi.DeployRunning

		return out, nil
	case agentapi.DeploySucceeded:
		out.State = agentapi.DeploySucceeded
	case agentapi.DeployFailed:
		out.State, out.Error = agentapi.DeployFailed, failReason(props["Result"])
	default:
		out.State, out.Error = agentapi.DeployFailed, "the deploy did not start"
	}

	m.removeCreds(req.Site)
	out.Log = logTail(m.LogFile(req.Site, req.ID))

	if out.State == agentapi.DeploySucceeded {
		res, err := readResult(acct.Home, req.ID)
		if err != nil {
			out.State, out.Error = agentapi.DeployFailed, err.Error()

			return out, nil
		}

		out.Commit, out.Author, out.Message = res.Commit, res.Author, res.Message
	}

	return out, nil
}

func failReason(result string) string {
	switch result {
	case "timeout":
		return "the deploy took longer than its time limit"
	case "exit-code":
		return "a step of the deploy failed"
	case "signal", "core-dump":
		return "the deploy was killed"
	case "oom-kill":
		return "the deploy ran out of memory"
	}

	return "the deploy failed (" + result + ")"
}

// result is what the agent reads of git-sync's result file. It is the
// site user's file: every field is checked.
type result struct {
	Commit  string `json:"commit"`
	Author  string `json:"author"`
	Message string `json:"message"`
}

func readResult(home string, id int64) (result, error) {
	root, err := os.OpenRoot(home)
	if err != nil {
		return result{}, fmt.Errorf("deployunit: %w", err)
	}
	defer root.Close()

	f, err := root.Open(agentapi.DeployResultsDir + "/" + agentapi.ReleaseName(id) + ".json")
	if err != nil {
		return result{}, errors.New("git-sync left no result")
	}
	defer f.Close()

	var r result
	if err := json.NewDecoder(io.LimitReader(f, maxResult)).Decode(&r); err != nil {
		return result{}, errors.New("git-sync left an unreadable result")
	}

	if len(r.Commit) != 40 || strings.Trim(r.Commit, "0123456789abcdef") != "" {
		return result{}, errors.New("git-sync left an invalid commit")
	}

	r.Author = clip(r.Author, 200)
	r.Message = clip(r.Message, 500)

	return r, nil
}

// clip keeps valid UTF-8 without control characters, at most n bytes.
func clip(s string, n int) string {
	s = strings.Map(func(r rune) rune {
		if r < 0x20 || r == 0x7f {
			return ' '
		}

		return r
	}, strings.ToValidUTF8(s, "?"))

	if len(s) > n {
		// A rune cut in two is dropped.
		s = strings.ToValidUTF8(s[:n], "")
	}

	return s
}

// RunLog returns the log of a run (site.deploy.log).
func (m *Manager) RunLog(_ context.Context, req agentapi.DeployStatusRequest) (agentapi.DeployLog, error) {
	if err := req.Validate(); err != nil {
		return agentapi.DeployLog{}, err
	}

	if _, err := m.Site(req.Site); err != nil {
		return agentapi.DeployLog{}, err
	}

	s, cut := readTail(m.LogFile(req.Site, req.ID), agentapi.MaxDeployLog)

	return agentapi.DeployLog{Log: s, Truncated: cut}, nil
}

// readTail is the end of a file, at most n bytes, and whether it was cut;
// "" if there is none.
func readTail(name string, n int64) (string, bool) {
	f, err := os.Open(name)
	if err != nil {
		return "", false
	}
	defer f.Close()

	cut := false

	if fi, err := f.Stat(); err == nil && fi.Size() > n {
		_, _ = f.Seek(-n, io.SeekEnd)
		cut = true
	}

	b, _ := io.ReadAll(io.LimitReader(f, n))

	return strings.ToValidUTF8(string(b), "?"), cut
}

// logTail is the end of a run's log, "" if there is none.
func logTail(name string) string {
	s, _ := readTail(name, agentapi.MaxDeployLogTail)

	return s
}

// pruneLogs keeps the newest keepLogs logs of a site and the current one.
func (m *Manager) pruneLogs(site string, current int64) {
	dir := filepath.Join(m.logDir(), site)

	entries, err := os.ReadDir(dir)
	if err != nil {
		return
	}

	var ids []int64

	for _, e := range entries {
		n, ok := strings.CutPrefix(e.Name(), "deploy-")
		if n, ok2 := strings.CutSuffix(n, ".log"); ok && ok2 {
			if id, err := strconv.ParseInt(n, 10, 64); err == nil && id != current {
				ids = append(ids, id)
			}
		}
	}

	slices.Sort(ids)

	for len(ids) >= keepLogs {
		_ = os.Remove(m.LogFile(site, ids[0]))
		ids = ids[1:]
	}
}

// Remove stops a site's deploy and deletes its drop-ins, script,
// credentials and state, for the delete of the site. The releases are in
// the home and go with it; the logs go with the site's log directory.
func (m *Manager) Remove(ctx context.Context, site string) error {
	if err := agentapi.ValidateSiteUser(site); err != nil {
		return err
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	if err := m.Ctl.Stop(ctx, Unit(site)); err != nil {
		m.log().WarnContext(ctx, "stop deploy of deleted site", "site", site, "err", err)
	}

	dir := m.dropInDir(site)
	for _, f := range []string{appunit.SliceDropIn, DropIn} {
		if err := os.Remove(filepath.Join(dir, f)); err != nil && !errors.Is(err, fs.ErrNotExist) {
			return fmt.Errorf("deployunit: %w", err)
		}
	}

	_ = os.Remove(dir)

	m.removeCreds(site)

	if err := os.RemoveAll(filepath.Join(m.scriptDir(), site)); err != nil {
		return fmt.Errorf("deployunit: %w", err)
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

// running reports whether a run of the site is queued or running; m.mu is
// held.
func (m *Manager) running(ctx context.Context, site string) (bool, error) {
	props, err := m.Ctl.Show(ctx, Unit(site), "ActiveState")
	if err != nil {
		return false, err
	}

	st, err := m.load()
	if err != nil {
		return false, err
	}

	r, ok := st[site]

	return m.phase(props["ActiveState"], r, ok) == agentapi.DeployRunning, nil
}

// ensureDir makes dir the agent's directory with the group gid, -1 to
// keep the agent's own.
func ensureDir(dir string, gid int, mode os.FileMode) error {
	if err := os.Mkdir(dir, mode); err != nil && !errors.Is(err, fs.ErrExist) {
		return fmt.Errorf("deployunit: %w", err)
	}

	fi, err := os.Lstat(dir)
	if err != nil {
		return fmt.Errorf("deployunit: %w", err)
	}

	sys, ok := fi.Sys().(*syscall.Stat_t)
	if !fi.IsDir() || !ok || int(sys.Uid) != os.Geteuid() {
		return fmt.Errorf("deployunit: %s is not a directory of the agent", dir)
	}

	if err := os.Lchown(dir, -1, gid); err != nil {
		return fmt.Errorf("deployunit: %w", err)
	}

	if err := os.Chmod(dir, mode); err != nil {
		return fmt.Errorf("deployunit: %w", err)
	}

	return nil
}

// writeFile replaces a file atomically, the agent's with the group gid
// (-1: the agent's own).
func writeFile(name string, data []byte, gid int, mode os.FileMode) error {
	if old, err := os.ReadFile(name); err == nil && bytes.Equal(old, data) {
		if err := os.Chown(name, -1, gid); err != nil {
			return fmt.Errorf("deployunit: %w", err)
		}

		if err := os.Chmod(name, mode); err != nil {
			return fmt.Errorf("deployunit: %w", err)
		}

		return nil
	}

	tmp := name + ".tmp"
	_ = os.Remove(tmp)

	f, err := os.OpenFile(tmp, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return fmt.Errorf("deployunit: %w", err)
	}

	err = errors.Join(
		f.Chown(-1, gid),
		f.Chmod(mode),
		func() error { _, err := f.Write(data); return err }(),
		f.Sync(),
	)

	if cerr := f.Close(); err == nil {
		err = cerr
	}

	if err == nil {
		err = os.Rename(tmp, name)
	}

	if err != nil {
		_ = os.Remove(tmp)

		return fmt.Errorf("deployunit: %w", err)
	}

	return nil
}
