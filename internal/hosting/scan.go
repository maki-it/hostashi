// SPDX-License-Identifier: AGPL-3.0-or-later

package hosting

import (
	"context"
	"errors"
	"fmt"
	"hash/fnv"
	"strconv"
	"strings"
	"time"

	"github.com/maki-it/hostashi/internal/agentapi"
	"github.com/maki-it/hostashi/internal/agentrpc"
	"github.com/maki-it/hostashi/internal/store"
)

// Malware scans of the sites (ADR-0064). ClamAV is installed on the
// server by the agent's install unit when an admin switches the scan on. A
// scan is a row in scan_runs and a run of the site's scan unit: the panel
// queues rows, RunScans starts one at a time, follows it until the agent
// reports its end and stores the findings. Sites are scanned on their
// schedule at night, spread by a hash of their name.

// Errors of the malware scan.
var (
	// ErrNoScans: this panel runs without an agent for scans.
	ErrNoScans = errors.New("hosting: the malware scan is not available")
	// ErrScanEngineOff: ClamAV is not installed.
	ErrScanEngineOff = errors.New("hosting: switch the malware scan on first")
	// ErrScanEngineBusy: ClamAV or its real-time mode is being installed
	// or removed.
	ErrScanEngineBusy = errors.New("hosting: ClamAV is being installed or removed")
	// ErrScanBusy: a scan of the site is queued or running.
	ErrScanBusy = store.ErrScanBusy
	// ErrFindingNotFound: the site has no finding with this id.
	ErrFindingNotFound = errors.New("hosting: no such finding")
	// ErrFindingState: the finding is not in a state for this action.
	ErrFindingState = errors.New("hosting: the finding is not in a state for this")
	// ErrScanSchedule: an unknown schedule.
	ErrScanSchedule = errors.New("hosting: the schedule must be off, daily or weekly")
)

// ScanAgent is the part of the agent scans use.
type ScanAgent interface {
	ScanEngineStatus(ctx context.Context) (agentapi.ScanEngineStatus, error)
	ScanEngineSet(ctx context.Context, req agentapi.ScanEngineSetRequest) (agentapi.ScanEngineStatus, error)
	ScanRealtimePoll(ctx context.Context, req agentapi.ScanRealtimePollRequest) (agentapi.ScanRealtimeFindings, error)
	ScanStart(ctx context.Context, req agentapi.ScanStartRequest) (agentapi.ScanStatus, error)
	ScanStatus(ctx context.Context, req agentapi.ScanStatusRequest) (agentapi.ScanStatus, error)
	ScanQuarantine(ctx context.Context, req agentapi.ScanQuarantineRequest) (agentapi.QuarantineItem, error)
	ScanRestore(ctx context.Context, req agentapi.ScanRestoreRequest) (agentapi.ScanRestored, error)
}

// ScanEngineStatus calls scan.engine.status.
func (a RPCAgent) ScanEngineStatus(ctx context.Context) (agentapi.ScanEngineStatus, error) {
	return agentapi.ScanEngineStatusOp.Call(ctx, a.C, agentapi.ScanEngineStatusRequest{})
}

// ScanEngineSet calls scan.engine.set.
func (a RPCAgent) ScanEngineSet(ctx context.Context, req agentapi.ScanEngineSetRequest) (agentapi.ScanEngineStatus, error) {
	return agentapi.ScanEngineSet.Call(ctx, a.C, req)
}

// ScanRealtimePoll calls scan.realtime.poll.
func (a RPCAgent) ScanRealtimePoll(ctx context.Context, req agentapi.ScanRealtimePollRequest) (agentapi.ScanRealtimeFindings, error) {
	return agentapi.ScanRealtimePoll.Call(ctx, a.C, req)
}

// ScanStart calls site.scan.start.
func (a RPCAgent) ScanStart(ctx context.Context, req agentapi.ScanStartRequest) (agentapi.ScanStatus, error) {
	return agentapi.ScanStart.Call(ctx, a.C, req)
}

// ScanStatus calls site.scan.status.
func (a RPCAgent) ScanStatus(ctx context.Context, req agentapi.ScanStatusRequest) (agentapi.ScanStatus, error) {
	return agentapi.ScanStatusOp.Call(ctx, a.C, req)
}

// ScanQuarantine calls site.scan.quarantine.
func (a RPCAgent) ScanQuarantine(ctx context.Context, req agentapi.ScanQuarantineRequest) (agentapi.QuarantineItem, error) {
	return agentapi.ScanQuarantine.Call(ctx, a.C, req)
}

// ScanRestore calls site.scan.restore.
func (a RPCAgent) ScanRestore(ctx context.Context, req agentapi.ScanRestoreRequest) (agentapi.ScanRestored, error) {
	return agentapi.ScanRestore.Call(ctx, a.C, req)
}

// EnableScans sets the agent for malware scans. Without it the scan
// routes answer ErrNoScans.
func (s *Service) EnableScans(a ScanAgent) {
	s.scans = a

	if s.scanKick == nil {
		s.scanKick = make(chan struct{}, 1)
	}
}

// ObserveMalware calls fn with the site, the run and its newly open
// findings after a scan found something new. Set it before RunScans.
func (s *Service) ObserveMalware(fn func(ctx context.Context, site string, run store.ScanRun, found []store.ScanFinding)) {
	s.onMalware = fn
}

// Timing of RunScans.
const (
	scanPoll = 10 * time.Second
	// scanQueuedGrace is how long a queued row may wait for the agent
	// once nothing else runs.
	scanQueuedGrace = 10 * time.Minute
	// scanNightStart and scanNightLength are where the schedule puts the
	// scans, in the server's time zone.
	scanNightStart  = time.Hour
	scanNightLength = 4 * time.Hour
	// scanWindow is how long after its slot a scheduled scan still starts:
	// a panel that was down at the slot catches up within it, later it
	// waits for the next slot.
	scanWindow = 2 * time.Hour
)

// ScanEngine reports ClamAV on the server.
func (s *Service) ScanEngine(ctx context.Context) (agentapi.ScanEngineStatus, error) {
	if s.scans == nil {
		return agentapi.ScanEngineStatus{}, ErrNoScans
	}

	return s.scans.ScanEngineStatus(ctx)
}

// SetScanEngine installs or removes ClamAV. Removing it keeps the history
// and the quarantine.
func (s *Service) SetScanEngine(ctx context.Context, actor int64, on bool) (agentapi.ScanEngineStatus, error) {
	if s.scans == nil {
		return agentapi.ScanEngineStatus{}, ErrNoScans
	}

	st, err := s.scans.ScanEngineSet(ctx, agentapi.ScanEngineSetRequest{On: on})
	if err != nil {
		return st, scanAgentError(err)
	}

	s.audit(ctx, actor, "malware_scan.engine", "", map[string]string{"on": strconv.FormatBool(on)})

	return st, nil
}

// SetScanRealtime switches the real-time mode (clamd and clamonacc) on or
// off. ClamAV must be on.
func (s *Service) SetScanRealtime(ctx context.Context, actor int64, on bool) (agentapi.ScanEngineStatus, error) {
	if s.scans == nil {
		return agentapi.ScanEngineStatus{}, ErrNoScans
	}

	st, err := s.scans.ScanEngineSet(ctx, agentapi.ScanEngineSetRequest{Realtime: &on})
	if err != nil {
		return st, scanAgentError(err)
	}

	s.audit(ctx, actor, "malware_scan.realtime", "", map[string]string{"on": strconv.FormatBool(on)})

	return st, nil
}

// scanAgentError turns the agent's refusals of a switch into the
// service's errors.
func scanAgentError(err error) error {
	var re *agentrpc.Error
	if errors.As(err, &re) {
		switch re.Code {
		case agentrpc.CodeConflict:
			return ErrScanEngineBusy
		case agentrpc.CodePrecondition:
			return ErrScanEngineOff
		}
	}

	return err
}

// SiteScan is the malware scan of one site.
type SiteScan struct {
	Schedule string
	Runs     []store.ScanRun
	Findings []store.ScanFinding
	// NextScheduled is the next slot of the schedule, zero when off.
	NextScheduled time.Time
	// Progress is how far the running runs are, by run id.
	Progress map[int64]agentapi.ScanProgress
}

func (s *Service) scanSite(ctx context.Context, name string) (store.Site, error) {
	site, err := s.Get(ctx, name)
	if err != nil {
		return store.Site{}, err
	}

	if !site.Account {
		return store.Site{}, ErrSiteType
	}

	return site, nil
}

// SiteScan returns the schedule, the newest runs and the findings of a
// site.
func (s *Service) SiteScan(ctx context.Context, name string) (SiteScan, error) {
	site, err := s.scanSite(ctx, name)
	if err != nil {
		return SiteScan{}, err
	}

	sched, err := s.st.SiteScanSchedule(ctx, site.ID)
	if err != nil {
		return SiteScan{}, err
	}

	runs, err := s.st.ScanRuns(ctx, site.ID, store.MaxScanHistory)
	if err != nil {
		return SiteScan{}, err
	}

	found, err := s.st.ScanFindings(ctx, site.ID)
	if err != nil {
		return SiteScan{}, err
	}

	out := SiteScan{Schedule: sched, Runs: runs, Findings: found, Progress: map[int64]agentapi.ScanProgress{}}

	for _, r := range runs {
		if r.State != store.ScanRunning {
			continue
		}

		if p, ok := s.scanProgress.Load(r.ID); ok {
			out.Progress[r.ID] = p.(agentapi.ScanProgress)
		} else {
			out.Progress[r.ID] = agentapi.ScanProgress{}
		}
	}
	if sched != store.ScanScheduleOff {
		out.NextScheduled = nextScanSlot(site.Name, sched, s.now())
	}

	return out, nil
}

// SetScanSchedule changes the schedule of a site.
func (s *Service) SetScanSchedule(ctx context.Context, actor int64, name, schedule string) error {
	switch schedule {
	case store.ScanScheduleOff, store.ScanScheduleDaily, store.ScanScheduleWeekly:
	default:
		return ErrScanSchedule
	}

	site, err := s.scanSite(ctx, name)
	if err != nil {
		return err
	}

	before, err := s.st.SiteScanSchedule(ctx, site.ID)
	if err != nil {
		return err
	}

	if err := s.st.SetSiteScanSchedule(ctx, site.ID, schedule, actor, s.now()); err != nil {
		return err
	}

	s.auditChange(ctx, actor, "site.malware_scan.schedule", site.Name, nil, map[string]string{"schedule": before}, map[string]string{"schedule": schedule})

	return nil
}

// StartScan queues a scan of a site; RunScans starts it when no other
// scan runs. paths, relative to the home, narrow it to those files and
// folders (the file manager's selection); none scans the whole home. An
// invalid list matches ErrInvalid; the server checks at the start that
// each path is there.
func (s *Service) StartScan(ctx context.Context, actor int64, name, source string, paths []string) (store.ScanRun, error) {
	if s.scans == nil {
		return store.ScanRun{}, ErrNoScans
	}

	if err := agentapi.ValidateScanTargets(paths); err != nil {
		return store.ScanRun{}, fmt.Errorf("%w: %w", ErrInvalid, err)
	}

	site, err := s.scanSite(ctx, name)
	if err != nil {
		return store.ScanRun{}, err
	}

	if site.State != store.SiteActive {
		return store.ScanRun{}, ErrState
	}

	eng, err := s.scans.ScanEngineStatus(ctx)
	if err != nil {
		return store.ScanRun{}, err
	}

	if eng.State != agentapi.ScanEngineOn {
		return store.ScanRun{}, ErrScanEngineOff
	}

	run, err := s.st.CreateScanRun(ctx, site.ID, source, actor, paths, s.now())
	if err != nil {
		return store.ScanRun{}, err
	}

	if source != store.ScanSourceSchedule {
		detail := map[string]string{"id": strconv.FormatInt(run.ID, 10), "source": source}
		if len(paths) > 0 {
			detail["paths"] = strings.Join(paths, ", ")
		}

		s.audit(ctx, actor, "site.malware_scan", site.Name, detail)
	}

	s.kickScans()

	return run, nil
}

func (s *Service) kickScans() {
	select {
	case s.scanKick <- struct{}{}:
	default:
	}
}

// RunScans starts and follows the scans until ctx ends, and queues the
// scheduled ones.
func (s *Service) RunScans(ctx context.Context) {
	if s.scans == nil {
		return
	}

	t := time.NewTicker(scanPoll)
	defer t.Stop()

	for {
		s.pollScans(ctx)

		select {
		case <-ctx.Done():
			return
		case <-t.C:
		case <-s.scanKick:
		}
	}
}

// pollScans follows the running scan, starts the next queued one and
// queues what the schedule says is due.
func (s *Service) pollScans(ctx context.Context) {
	all, err := s.st.Sites(ctx)
	if err != nil {
		s.log.ErrorContext(ctx, "list sites for scans", "err", err)

		return
	}

	sites := make(map[int64]store.Site, len(all))
	for _, site := range all {
		sites[site.ID] = site
	}

	eng, err := s.scans.ScanEngineStatus(ctx)
	if err != nil {
		s.log.WarnContext(ctx, "scan engine status", "err", err)

		return
	}

	if eng.State == agentapi.ScanEngineOn {
		s.queueScheduledScans(ctx, all)
	}

	if eng.State == agentapi.ScanEngineOn && eng.Realtime == agentapi.ScanEngineOn {
		s.pollRealtime(ctx, all)
	}

	active, err := s.st.ActiveScanRuns(ctx)
	if err != nil {
		s.log.ErrorContext(ctx, "list active scans", "err", err)

		return
	}

	running := false

	for _, r := range active {
		site, ok := sites[r.SiteID]
		if !ok {
			s.failScan(ctx, r, "the site is gone")

			continue
		}

		if r.State == store.ScanRunning {
			if s.pollScan(ctx, site, r) {
				running = true
			}
		}
	}

	if running {
		return
	}

	for _, r := range active {
		if r.State != store.ScanQueued {
			continue
		}

		site, ok := sites[r.SiteID]
		if !ok {
			continue
		}

		if eng.State != agentapi.ScanEngineOn {
			s.failScan(ctx, r, "ClamAV is not installed")

			continue
		}

		if site.State != store.SiteActive {
			s.failScan(ctx, r, "the site is not active")

			continue
		}

		if _, err := s.scans.ScanStart(ctx, agentapi.ScanStartRequest{Site: site.Name, ID: r.ID, Paths: r.Paths}); err != nil {
			if isCode(err, agentrpc.CodeConflict) && s.now().Sub(r.Created) < scanQueuedGrace {
				return
			}

			s.failScan(ctx, r, err.Error())

			continue
		}

		if err := s.st.StartScanRun(ctx, r.ID, s.now()); err != nil {
			s.log.ErrorContext(ctx, "mark scan running", "id", r.ID, "err", err)
		}

		return
	}
}

// pollScan asks the agent about a running scan and stores its end. It
// reports whether the scan is still running.
func (s *Service) pollScan(ctx context.Context, site store.Site, r store.ScanRun) bool {
	st, err := s.scans.ScanStatus(ctx, agentapi.ScanStatusRequest{Site: site.Name, ID: r.ID})
	if err != nil {
		s.log.WarnContext(ctx, "scan status", "site", site.Name, "id", r.ID, "err", err)

		return true
	}

	if st.State == agentapi.ScanRunning {
		if st.Progress != nil {
			s.scanProgress.Store(r.ID, *st.Progress)
		}

		return true
	}

	s.scanProgress.Delete(r.ID)

	switch st.State {
	case agentapi.ScanUnknown:
		s.failScan(ctx, r, "the server lost track of this scan")

		return false
	case agentapi.ScanFailed:
		s.failScan(ctx, r, st.Error)

		return false
	}

	files := make([]store.FoundFile, 0, len(st.Findings))
	for _, f := range st.Findings {
		files = append(files, store.FoundFile{Path: f.Path, Signature: f.Signature, SHA256: f.SHA256, Size: f.Size})
	}

	r.Error, r.Dropped = st.Error, st.Dropped

	fresh, err := s.st.FinishScanRun(ctx, r, files, s.now())
	if err != nil {
		s.log.ErrorContext(ctx, "store scan result", "site", site.Name, "id", r.ID, "err", err)

		return false
	}

	s.log.InfoContext(ctx, "scan finished", "site", site.Name, "id", r.ID, "found", len(files), "new", len(fresh))

	if len(fresh) > 0 && s.onMalware != nil {
		r.State, r.Found, r.NewFound = store.ScanSucceeded, len(files), len(fresh)
		s.onMalware(context.WithoutCancel(ctx), site.Name, r, fresh)
	}

	return false
}

// realtimeMailEvery is how often the real-time mode emails a site's new
// findings at most; findings in between wait for the next email.
const realtimeMailEvery = time.Hour

// pollRealtime tells the agent which sites the real-time mode watches and
// stores what it found since the last poll. Only RunScans calls it.
func (s *Service) pollRealtime(ctx context.Context, all []store.Site) {
	names := make([]string, 0, len(all))
	byName := make(map[string]store.Site, len(all))

	for _, site := range all {
		if site.Account && site.State == store.SiteActive {
			names = append(names, site.Name)
			byName[site.Name] = site
		}
	}

	if len(names) > agentapi.MaxRealtimeSites {
		names = names[:agentapi.MaxRealtimeSites]
	}

	res, err := s.scans.ScanRealtimePoll(ctx, agentapi.ScanRealtimePollRequest{Sites: names, Cursor: s.rtCursor})
	if err != nil {
		s.log.WarnContext(ctx, "real-time scan poll", "err", err)

		return
	}

	s.rtCursor = res.Cursor

	files := map[string][]store.FoundFile{}
	for _, f := range res.Findings {
		files[f.Site] = append(files[f.Site], store.FoundFile{Path: f.Path, Signature: f.Signature, SHA256: f.SHA256, Size: f.Size})
	}

	now := s.now()

	if s.rtPending == nil {
		s.rtPending = map[string]*realtimeMail{}
	}

	for name, fs := range files {
		site, ok := byName[name]
		if !ok {
			continue
		}

		run, fresh, err := s.st.RecordRealtimeFindings(ctx, site.ID, fs, now)
		if err != nil {
			s.log.ErrorContext(ctx, "store real-time findings", "site", name, "err", err)

			continue
		}

		s.log.InfoContext(ctx, "real-time scan found files", "site", name, "found", len(fs), "new", len(fresh))

		if len(fresh) > 0 {
			p := s.rtPending[name]
			if p == nil {
				p = &realtimeMail{}
				s.rtPending[name] = p
			}

			p.run = run
			p.found = append(p.found, fresh...)
		}
	}

	for name, p := range s.rtPending {
		if len(p.found) == 0 || now.Sub(p.sent) < realtimeMailEvery {
			continue
		}

		if s.onMalware != nil {
			r := p.run
			r.NewFound = len(p.found)
			s.onMalware(context.WithoutCancel(ctx), name, r, p.found)
		}

		p.sent, p.found = now, nil
	}
}

// realtimeMail holds a site's new real-time findings until they are
// emailed.
type realtimeMail struct {
	run   store.ScanRun
	found []store.ScanFinding
	sent  time.Time
}

func (s *Service) failScan(ctx context.Context, r store.ScanRun, msg string) {
	if err := s.st.FailScanRun(ctx, r.ID, msg, s.now()); err != nil && !errors.Is(err, store.ErrNotFound) {
		s.log.ErrorContext(ctx, "fail scan", "id", r.ID, "err", err)
	}
}

// queueScheduledScans queues a scan for every site whose slot has come and
// that has no run since.
func (s *Service) queueScheduledScans(ctx context.Context, sites []store.Site) {
	scheds, err := s.st.ScanSchedules(ctx)
	if err != nil {
		s.log.ErrorContext(ctx, "scan schedules", "err", err)

		return
	}

	last, err := s.st.LastScanRuns(ctx)
	if err != nil {
		s.log.ErrorContext(ctx, "last scans", "err", err)

		return
	}

	now := s.now()

	for _, site := range sites {
		if !site.Account || site.State != store.SiteActive {
			continue
		}

		sched, ok := scheds[site.ID]
		if !ok {
			sched = store.ScanScheduleDefault
		}

		if sched == store.ScanScheduleOff {
			continue
		}

		slot := lastScanSlot(site.Name, sched, now)
		if now.Sub(slot) >= scanWindow {
			continue
		}

		if r, ok := last[site.ID]; ok && !r.Created.Before(slot) {
			continue
		}

		if _, err := s.st.CreateScanRun(ctx, site.ID, store.ScanSourceSchedule, 0, nil, now); err != nil && !errors.Is(err, store.ErrScanBusy) {
			s.log.ErrorContext(ctx, "queue scheduled scan", "site", site.Name, "err", err)
		}
	}
}

// scanOffset spreads the sites over the night and, weekly, over the week.
func scanOffset(site string) (minutes, weekday int) {
	h := fnv.New32a()
	_, _ = h.Write([]byte(site))
	v := h.Sum32()

	return int(v % uint32(scanNightLength/time.Minute)), int((v / 1000) % 7)
}

// lastScanSlot is the newest slot of the schedule at or before now, in the
// server's time zone.
func lastScanSlot(site, sched string, now time.Time) time.Time {
	m, wd := scanOffset(site)
	local := now.In(time.Local)
	day := time.Date(local.Year(), local.Month(), local.Day(), 0, 0, 0, 0, time.Local)
	slot := day.Add(scanNightStart + time.Duration(m)*time.Minute)

	if sched == store.ScanScheduleWeekly {
		slot = slot.AddDate(0, 0, wd-int(local.Weekday()))
		if slot.After(local) {
			slot = slot.AddDate(0, 0, -7)
		}

		return slot
	}

	if slot.After(local) {
		slot = slot.AddDate(0, 0, -1)
	}

	return slot
}

// nextScanSlot is the first slot after now.
func nextScanSlot(site, sched string, now time.Time) time.Time {
	last := lastScanSlot(site, sched, now)
	if sched == store.ScanScheduleWeekly {
		return last.AddDate(0, 0, 7)
	}

	return last.AddDate(0, 0, 1)
}

// scanFinding returns a finding of a site.
func (s *Service) scanFinding(ctx context.Context, name string, id int64) (store.Site, store.ScanFinding, error) {
	site, err := s.scanSite(ctx, name)
	if err != nil {
		return store.Site{}, store.ScanFinding{}, err
	}

	f, err := s.st.ScanFinding(ctx, site.ID, id)
	if errors.Is(err, store.ErrNotFound) {
		return store.Site{}, store.ScanFinding{}, ErrFindingNotFound
	}

	return site, f, err
}

// QuarantineFinding moves an open finding's file into the quarantine. The
// agent refuses when the file changed since the scan; the finding is then
// gone.
func (s *Service) QuarantineFinding(ctx context.Context, actor int64, name string, id int64) (store.ScanFinding, error) {
	if s.scans == nil {
		return store.ScanFinding{}, ErrNoScans
	}

	site, f, err := s.scanFinding(ctx, name, id)
	if err != nil {
		return store.ScanFinding{}, err
	}

	if f.State != store.FindingOpen && f.State != store.FindingIgnored {
		return store.ScanFinding{}, ErrFindingState
	}

	it, err := s.scans.ScanQuarantine(ctx, agentapi.ScanQuarantineRequest{Site: site.Name, Path: f.Path, SHA256: f.SHA256})
	if err != nil {
		if isCode(err, agentrpc.CodePrecondition) {
			_ = s.st.SetScanFindingState(ctx, site.ID, f.ID, f.State, store.FindingGone, "", actor, s.now())
		}

		return store.ScanFinding{}, err
	}

	if err := s.st.SetScanFindingState(ctx, site.ID, f.ID, f.State, store.FindingQuarantined, it.ID, actor, s.now()); err != nil {
		return store.ScanFinding{}, err
	}

	s.audit(ctx, actor, "site.malware_scan.quarantine", site.Name, map[string]string{"path": f.Path, "signature": f.Signature, "sha256": f.SHA256})

	return s.st.ScanFinding(ctx, site.ID, f.ID)
}

// RestoreFinding puts a quarantined file back.
func (s *Service) RestoreFinding(ctx context.Context, actor int64, name string, id int64) (store.ScanFinding, error) {
	if s.scans == nil {
		return store.ScanFinding{}, ErrNoScans
	}

	site, f, err := s.scanFinding(ctx, name, id)
	if err != nil {
		return store.ScanFinding{}, err
	}

	if f.State != store.FindingQuarantined {
		return store.ScanFinding{}, ErrFindingState
	}

	if _, err := s.scans.ScanRestore(ctx, agentapi.ScanRestoreRequest{Site: site.Name, ID: f.QuarantineID}); err != nil {
		return store.ScanFinding{}, err
	}

	if err := s.st.SetScanFindingState(ctx, site.ID, f.ID, f.State, store.FindingRestored, "", actor, s.now()); err != nil {
		return store.ScanFinding{}, err
	}

	s.audit(ctx, actor, "site.malware_scan.restore", site.Name, map[string]string{"path": f.Path, "signature": f.Signature, "sha256": f.SHA256})

	return s.st.ScanFinding(ctx, site.ID, f.ID)
}

// IgnoreFinding marks an open finding as a false positive (ignore true),
// or reopens an ignored one.
func (s *Service) IgnoreFinding(ctx context.Context, actor int64, name string, id int64, ignore bool) (store.ScanFinding, error) {
	site, f, err := s.scanFinding(ctx, name, id)
	if err != nil {
		return store.ScanFinding{}, err
	}

	from, to, action := store.FindingOpen, store.FindingIgnored, "site.malware_scan.ignore"
	if !ignore {
		from, to, action = store.FindingIgnored, store.FindingOpen, "site.malware_scan.reopen"
	}

	if f.State != from {
		return store.ScanFinding{}, ErrFindingState
	}

	if err := s.st.SetScanFindingState(ctx, site.ID, f.ID, from, to, "", actor, s.now()); err != nil {
		return store.ScanFinding{}, err
	}

	s.audit(ctx, actor, action, site.Name, map[string]string{"path": f.Path, "signature": f.Signature, "sha256": f.SHA256})

	return s.st.ScanFinding(ctx, site.ID, f.ID)
}
