// SPDX-License-Identifier: AGPL-3.0-or-later

// Package cronjobs keeps the cron jobs of the sites as systemd units
// (ADR-0028): one timer and one service per job, and a root-owned script
// that the service runs as the site user. The agent calls it; the units
// are rendered by package cron and started through a Controller.
package cronjobs

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"math"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"unicode/utf8"

	"github.com/maki-it/hostashi/internal/agentapi"
	"github.com/maki-it/hostashi/internal/cron"
)

// Fixed places on a server.
const (
	// DefaultScriptDir holds the scripts, one directory per site, root:site
	// 0750. It is not below the agent's 0700 state directory: the site
	// users read their scripts.
	DefaultScriptDir = "/var/lib/hostashi-cron"
	DefaultUnitDir   = "/etc/systemd/system"
)

// Errors of the operations.
var (
	ErrUnknownJob = errors.New("cronjobs: no such job")
	ErrDisabled   = errors.New("cronjobs: the site is disabled")
	ErrRootSite   = errors.New("cronjobs: refusing a site user with uid or gid 0")
)

// Controller drives systemd (phpctl.CronController on a server).
type Controller interface {
	DaemonReload(ctx context.Context) error
	// EnableNow enables a timer and starts it; DisableNow stops and
	// disables it. Both are no-ops for a timer in that state already.
	EnableNow(ctx context.Context, unit string) error
	DisableNow(ctx context.Context, unit string) error
	// StartNoBlock queues a start of a service and returns.
	StartNoBlock(ctx context.Context, unit string) error
	// Show returns the named properties of a unit.
	Show(ctx context.Context, unit string, props ...string) (map[string]string, error)
	// NextElapse returns the next elapse of each named timer in Unix
	// seconds; a timer without a next elapse is missing or 0. systemctl
	// show prints NextElapseUSecRealtime as a local date whatever
	// --timestamp= says, so it is read in another way.
	NextElapse(ctx context.Context, timers ...string) (map[string]int64, error)
	// Journal returns the last messages (at most
	// agentapi.CronOutputMaxLines) that the processes of a service wrote
	// in one invocation, oldest first; cut is true when journalctl gave
	// more than could be read. start is the invocation's
	// ExecMainStartTimestampMonotonic: lines that journald stored without
	// an invocation id count from then on.
	Journal(ctx context.Context, service, invocation string, start uint64) (lines []string, cut bool, err error)
}

// Site is what Apply needs to know of a site.
type Site struct {
	Name     string
	Home     string
	UID, GID uint32
	Slice    string   // the site's slice (ADR-0015), or "" for none
	Hidden   []string // pool roots the jobs must not see (ADR-0016)
}

// Manager applies the job lists. Its zero value is not usable; set at least
// StateFile and Ctl.
type Manager struct {
	StateFile string // root 0600, the last applied lists
	ScriptDir string // default DefaultScriptDir
	UnitDir   string // default DefaultUnitDir
	Ctl       Controller
	Log       *slog.Logger

	mu sync.Mutex
}

type siteState struct {
	Jobs     []agentapi.CronJob `json:"jobs"`
	Disabled bool               `json:"disabled,omitempty"`
}

type state struct {
	Version int                  `json:"version"`
	Sites   map[string]siteState `json:"sites"`
}

func (m *Manager) scriptDir() string { return cmpOr(m.ScriptDir, DefaultScriptDir) }
func (m *Manager) unitDir() string   { return cmpOr(m.UnitDir, DefaultUnitDir) }

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

func unitName(site string, id int64) string {
	return cron.Unit{Site: site, ID: id}.Name()
}

// Apply replaces the jobs of a site: it writes scripts and units, removes
// the units of jobs that are gone, and starts the timers of the jobs that
// are not paused (none while the site is disabled).
func (m *Manager) Apply(ctx context.Context, s Site, jobs []agentapi.CronJob) error {
	if err := (agentapi.CronApplyRequest{Site: s.Name, Jobs: jobs}).Validate(); err != nil {
		return err
	}

	if s.UID == 0 || s.GID == 0 {
		return ErrRootSite
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	st, err := m.load()
	if err != nil {
		return err
	}

	type files struct{ script, service, timer []byte }

	rendered := make([]files, len(jobs))
	dir := filepath.Join(m.scriptDir(), s.Name)

	for i, j := range jobs {
		cal, err := cron.OnCalendar(j.Schedule)
		if err != nil {
			return err
		}

		u := cron.Unit{
			Site: s.Name, ID: j.ID, Home: s.Home, Timeout: j.Timeout(), OnCalendar: cal,
			Slice: s.Slice, Nice: j.Niceness(), Hidden: s.Hidden,
			Script: filepath.Join(dir, strconv.FormatInt(j.ID, 10)+".sh"),
		}

		f := &rendered[i]
		if f.script, err = cron.Script(j.Command); err != nil {
			return err
		}

		if f.service, err = u.Service(); err != nil {
			return err
		}

		if f.timer, err = u.Timer(); err != nil {
			return err
		}
	}

	keep := map[int64]bool{}
	for _, j := range jobs {
		keep[j.ID] = true
	}

	if err := m.removeStale(ctx, s.Name, keep); err != nil {
		return err
	}

	if len(jobs) > 0 {
		if err := ensureDir(m.scriptDir(), 0, 0o755); err != nil {
			return err
		}

		if err := ensureDir(dir, s.GID, 0o750); err != nil {
			return err
		}
	}

	for i, j := range jobs {
		name := unitName(s.Name, j.ID)
		f := rendered[i]

		if err := writeFile(filepath.Join(dir, strconv.FormatInt(j.ID, 10)+".sh"), f.script, s.GID, 0o640); err != nil {
			return err
		}

		if err := writeFile(filepath.Join(m.unitDir(), name+".service"), f.service, 0, 0o644); err != nil {
			return err
		}

		if err := writeFile(filepath.Join(m.unitDir(), name+".timer"), f.timer, 0, 0o644); err != nil {
			return err
		}
	}

	if err := m.Ctl.DaemonReload(ctx); err != nil {
		return err
	}

	ss := st.Sites[s.Name]
	ss.Jobs = slices.Clone(jobs)

	if len(jobs) == 0 {
		_ = os.Remove(dir)

		if !ss.Disabled {
			delete(st.Sites, s.Name)
		} else {
			st.Sites[s.Name] = ss
		}
	} else {
		st.Sites[s.Name] = ss
	}

	if err := m.save(st); err != nil {
		return err
	}

	return m.timers(ctx, s.Name, ss)
}

// timers starts the timers of active jobs and stops the others.
func (m *Manager) timers(ctx context.Context, site string, ss siteState) error {
	var errs []error

	for _, j := range ss.Jobs {
		t := unitName(site, j.ID) + ".timer"

		var err error
		if j.Paused || ss.Disabled {
			err = m.Ctl.DisableNow(ctx, t)
		} else {
			err = m.Ctl.EnableNow(ctx, t)
		}

		errs = append(errs, err)
	}

	return errors.Join(errs...)
}

// removeStale stops and deletes the units and scripts of a site's jobs
// that are not in keep.
func (m *Manager) removeStale(ctx context.Context, site string, keep map[int64]bool) error {
	entries, err := os.ReadDir(m.unitDir())
	if err != nil {
		return fmt.Errorf("cronjobs: %w", err)
	}

	prefix := cron.UnitPrefix + site + "-"
	gone := map[int64]bool{}

	for _, e := range entries {
		rest, ok := strings.CutPrefix(e.Name(), prefix)
		if !ok {
			continue
		}

		idStr, ok := strings.CutSuffix(rest, ".timer")
		if !ok {
			idStr, ok = strings.CutSuffix(rest, ".service")
		}

		// Another site's name may start with this one's: "shop" and
		// "shop-2". Only a number follows the prefix of this site's units.
		id, err := strconv.ParseInt(idStr, 10, 64)
		if !ok || err != nil || id < 1 || strconv.FormatInt(id, 10) != idStr || keep[id] {
			continue
		}

		gone[id] = true
	}

	if len(gone) == 0 {
		return nil
	}

	for id := range gone {
		name := unitName(site, id)
		if err := m.Ctl.DisableNow(ctx, name+".timer"); err != nil {
			m.log().WarnContext(ctx, "cron: stopping removed job", "unit", name, "err", err)
		}

		for _, p := range []string{
			filepath.Join(m.unitDir(), name+".timer"),
			filepath.Join(m.unitDir(), name+".service"),
			filepath.Join(m.scriptDir(), site, strconv.FormatInt(id, 10)+".sh"),
		} {
			if err := os.Remove(p); err != nil && !errors.Is(err, fs.ErrNotExist) {
				return fmt.Errorf("cronjobs: %w", err)
			}
		}
	}

	return m.Ctl.DaemonReload(ctx)
}

// SetDisabled stops all timers of a disabled site, or starts the active
// ones again. A site without jobs is only remembered.
func (m *Manager) SetDisabled(ctx context.Context, site string, disabled bool) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	st, err := m.load()
	if err != nil {
		return err
	}

	ss, had := st.Sites[site]
	if ss.Disabled == disabled {
		return nil
	}

	ss.Disabled = disabled

	if !had && !disabled {
		return nil
	}

	if len(ss.Jobs) == 0 && !disabled {
		delete(st.Sites, site)
	} else {
		st.Sites[site] = ss
	}

	if err := m.save(st); err != nil {
		return err
	}

	return m.timers(ctx, site, ss)
}

// Remove deletes all jobs of a site, for a deleted site.
func (m *Manager) Remove(ctx context.Context, site string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if err := m.removeStale(ctx, site, nil); err != nil {
		return err
	}

	if err := os.Remove(filepath.Join(m.scriptDir(), site)); err != nil && !errors.Is(err, fs.ErrNotExist) {
		m.log().WarnContext(ctx, "cron: removing script directory", "site", site, "err", err)
	}

	st, err := m.load()
	if err != nil {
		return err
	}

	delete(st.Sites, site)

	return m.save(st)
}

// Run starts one job now, paused or not, unless the site is disabled.
// A run that is still going is not started twice.
func (m *Manager) Run(ctx context.Context, site string, id int64) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	st, err := m.load()
	if err != nil {
		return err
	}

	ss := st.Sites[site]
	if !slices.ContainsFunc(ss.Jobs, func(j agentapi.CronJob) bool { return j.ID == id }) {
		return ErrUnknownJob
	}

	if ss.Disabled {
		return ErrDisabled
	}

	return m.Ctl.StartNoBlock(ctx, unitName(site, id)+".service")
}

var (
	serviceProps = []string{"ActiveState", "Result", "ExecMainStatus", "ExecMainStartTimestamp", "ExecMainExitTimestamp"}
	timerProps   = []string{"ActiveState"}
)

// Status reports the jobs of a site with their last and next run.
func (m *Manager) Status(ctx context.Context, site string) (agentapi.CronSiteStatus, error) {
	m.mu.Lock()
	st, err := m.load()
	m.mu.Unlock()

	if err != nil {
		return agentapi.CronSiteStatus{}, err
	}

	ss := st.Sites[site]
	out := agentapi.CronSiteStatus{Site: site, Disabled: ss.Disabled, Jobs: []agentapi.CronJobStatus{}}

	var next map[string]int64

	if len(ss.Jobs) > 0 {
		timers := make([]string, 0, len(ss.Jobs))
		for _, j := range ss.Jobs {
			timers = append(timers, unitName(site, j.ID)+".timer")
		}

		if next, err = m.Ctl.NextElapse(ctx, timers...); err != nil {
			return agentapi.CronSiteStatus{}, err
		}
	}

	for _, j := range ss.Jobs {
		name := unitName(site, j.ID)
		js := agentapi.CronJobStatus{ID: j.ID, Paused: j.Paused}

		svc, err := m.Ctl.Show(ctx, name+".service", serviceProps...)
		if err != nil {
			return agentapi.CronSiteStatus{}, err
		}

		tm, err := m.Ctl.Show(ctx, name+".timer", timerProps...)
		if err != nil {
			return agentapi.CronSiteStatus{}, err
		}

		js.Running = svc["ActiveState"] == "activating" || svc["ActiveState"] == "active"
		js.Result = svc["Result"]
		js.LastStart = unixTime(svc["ExecMainStartTimestamp"])
		js.LastEnd = unixTime(svc["ExecMainExitTimestamp"])

		if js.LastStart > 0 {
			if n, err := strconv.Atoi(svc["ExecMainStatus"]); err == nil {
				js.ExitStatus = &n
			}
		}

		if tm["ActiveState"] == "active" && next[name+".timer"] > 0 {
			js.Next = next[name+".timer"]
		}

		out.Jobs = append(out.Jobs, js)
	}

	return out, nil
}

// Output reads what the last run of a job wrote (ADR-0028 Addendum 1).
func (m *Manager) Output(ctx context.Context, site string, id int64) (agentapi.CronJobOutput, error) {
	m.mu.Lock()
	st, err := m.load()
	m.mu.Unlock()

	if err != nil {
		return agentapi.CronJobOutput{}, err
	}

	if !slices.ContainsFunc(st.Sites[site].Jobs, func(j agentapi.CronJob) bool { return j.ID == id }) {
		return agentapi.CronJobOutput{}, ErrUnknownJob
	}

	svc := unitName(site, id) + ".service"

	props, err := m.Ctl.Show(ctx, svc, "InvocationID", "ExecMainStartTimestampMonotonic")
	if err != nil {
		return agentapi.CronJobOutput{}, err
	}

	out := agentapi.CronJobOutput{Lines: []string{}}

	inv := props["InvocationID"]
	if inv == "" {
		return out, nil // not run since the units were written or since boot
	}

	// Without a start, only lines that carry the invocation id count.
	start, err := strconv.ParseUint(props["ExecMainStartTimestampMonotonic"], 10, 64)
	if err != nil || start == 0 {
		start = math.MaxUint64
	}

	lines, cut, err := m.Ctl.Journal(ctx, svc, inv, start)
	if err != nil {
		return agentapi.CronJobOutput{}, err
	}

	out.Ran = true
	out.Lines, out.Truncated = limitOutput(lines)
	out.Truncated = out.Truncated || cut

	return out, nil
}

// limitOutput keeps the last lines within the limits of cron.output and
// cuts each line to CronOutputMaxLineBytes at a UTF-8 boundary.
func limitOutput(lines []string) ([]string, bool) {
	cut := false

	if len(lines) > agentapi.CronOutputMaxLines {
		lines, cut = lines[len(lines)-agentapi.CronOutputMaxLines:], true
	}

	out := make([]string, 0, len(lines))
	total := 0

	for i := len(lines) - 1; i >= 0; i-- {
		l := strings.ToValidUTF8(lines[i], "\uFFFD")
		if len(l) > agentapi.CronOutputMaxLineBytes {
			l, cut = truncateUTF8(l, agentapi.CronOutputMaxLineBytes), true
		}

		if total+len(l)+1 > agentapi.CronOutputMaxBytes {
			cut = true

			break
		}

		total += len(l) + 1
		out = append(out, l)
	}

	slices.Reverse(out)

	return out, cut
}

func truncateUTF8(s string, n int) string {
	for n > 0 && !utf8.RuneStart(s[n]) {
		n--
	}

	return s[:n]
}

// unixTime reads a timestamp of systemctl show --timestamp=unix ("@1700000000"),
// 0 for none.
func unixTime(v string) int64 {
	n, err := strconv.ParseInt(strings.TrimPrefix(v, "@"), 10, 64)
	if err != nil || !strings.HasPrefix(v, "@") {
		return 0
	}

	return n
}

func (m *Manager) load() (state, error) {
	st := state{Version: 1, Sites: map[string]siteState{}}

	b, err := os.ReadFile(m.StateFile)
	if errors.Is(err, fs.ErrNotExist) {
		return st, nil
	}

	if err != nil {
		return st, fmt.Errorf("cronjobs: %w", err)
	}

	if err := json.Unmarshal(b, &st); err != nil {
		return st, fmt.Errorf("cronjobs: state: %w", err)
	}

	if st.Sites == nil {
		st.Sites = map[string]siteState{}
	}

	return st, nil
}

func (m *Manager) save(st state) error {
	b, err := json.Marshal(st)
	if err != nil {
		return fmt.Errorf("cronjobs: %w", err)
	}

	return writeFile(m.StateFile, b, 0, 0o600)
}

// ensureDir creates a root-owned directory with the given group and mode,
// or fixes an existing one. A symlink or a directory owned by anyone else
// is refused.
func ensureDir(dir string, gid uint32, mode os.FileMode) error {
	if err := os.Mkdir(dir, mode); err != nil && !errors.Is(err, fs.ErrExist) {
		return fmt.Errorf("cronjobs: %w", err)
	}

	fi, err := os.Lstat(dir)
	if err != nil {
		return fmt.Errorf("cronjobs: %w", err)
	}

	sys, ok := fi.Sys().(*syscall.Stat_t)
	if !fi.IsDir() || !ok || sys.Uid != 0 {
		return fmt.Errorf("cronjobs: %s is not a root-owned directory", dir)
	}

	if err := os.Lchown(dir, 0, int(gid)); err != nil {
		return fmt.Errorf("cronjobs: %w", err)
	}

	if err := os.Chmod(dir, mode); err != nil {
		return fmt.Errorf("cronjobs: %w", err)
	}

	return nil
}

// writeFile replaces a file atomically, root-owned with the given group.
func writeFile(name string, data []byte, gid uint32, mode os.FileMode) error {
	tmp := name + ".tmp"
	_ = os.Remove(tmp)

	f, err := os.OpenFile(tmp, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return fmt.Errorf("cronjobs: %w", err)
	}

	err = errors.Join(
		f.Chown(0, int(gid)),
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

		return fmt.Errorf("cronjobs: %w", err)
	}

	return nil
}

// Refresh renders the units of every site with jobs again, after something
// the units carry changed for all sites (a pool was added or removed,
// ADR-0016). site returns what the units need of a site; sites it does not
// know are left alone.
func (m *Manager) Refresh(ctx context.Context, site func(name string) (Site, bool)) error {
	m.mu.Lock()
	st, err := m.load()
	m.mu.Unlock()

	if err != nil {
		return err
	}

	var errs []error

	for name, ss := range st.Sites {
		s, ok := site(name)
		if !ok || len(ss.Jobs) == 0 {
			continue
		}

		if err := m.Apply(ctx, s, ss.Jobs); err != nil {
			errs = append(errs, fmt.Errorf("site %s: %w", name, err))
		}
	}

	return errors.Join(errs...)
}
