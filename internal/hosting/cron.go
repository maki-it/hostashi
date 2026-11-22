// SPDX-License-Identifier: AGPL-3.0-or-later

package hosting

import (
	"context"
	"errors"
	"fmt"
	"strconv"

	"github.com/maki-it/hostashi/internal/agentapi"
	"github.com/maki-it/hostashi/internal/cron"
	"github.com/maki-it/hostashi/internal/store"
)

// Errors of the cron jobs.
var (
	ErrNoCron        = errors.New("hosting: cron jobs are not available")
	ErrCronNotFound  = errors.New("hosting: no such cron job")
	ErrCronLimit     = fmt.Errorf("hosting: a site has at most %d cron jobs", cron.MaxJobsPerSite)
	errCronNotActive = fmt.Errorf("%w: the site is not active", ErrState)
)

// CronAgent is the part of the agent the cron jobs use (ADR-0028).
type CronAgent interface {
	CronApply(ctx context.Context, req agentapi.CronApplyRequest) error
	CronRun(ctx context.Context, site string, id int64) error
	CronStatus(ctx context.Context, site string) (agentapi.CronSiteStatus, error)
}

// CronApply calls cron.apply.
func (a RPCAgent) CronApply(ctx context.Context, req agentapi.CronApplyRequest) error {
	_, err := agentapi.CronApply.Call(ctx, a.C, req)

	return err
}

// CronRun calls cron.run.
func (a RPCAgent) CronRun(ctx context.Context, site string, id int64) error {
	_, err := agentapi.CronRun.Call(ctx, a.C, agentapi.CronRunRequest{Site: site, ID: id})

	return err
}

// CronStatus calls cron.status.
func (a RPCAgent) CronStatus(ctx context.Context, site string) (agentapi.CronSiteStatus, error) {
	return agentapi.CronStatus.Call(ctx, a.C, agentapi.CronStatusRequest{Site: site})
}

// EnableCron sets the agent for cron jobs. Without it they answer ErrNoCron.
func (s *Service) EnableCron(a CronAgent) { s.cron = a }

// CronInput is what an admin sets on a job.
type CronInput struct {
	Schedule   string
	Command    string
	TimeoutSec int64 // 0: the default
	Nice       *int  // nil: the default
	Paused     bool
}

func (in CronInput) check() error {
	j := agentapi.CronJob{ID: 1, Schedule: in.Schedule, Command: in.Command, TimeoutSec: in.TimeoutSec, Nice: in.Nice}
	if err := j.Validate(); err != nil {
		return fmt.Errorf("%w: %w", ErrInvalid, err)
	}

	return nil
}

func agentJobs(jobs []store.CronJob) []agentapi.CronJob {
	out := make([]agentapi.CronJob, len(jobs))
	for i, j := range jobs {
		out[i] = agentapi.CronJob{
			ID: j.ID, Schedule: j.Schedule, Command: j.Command, TimeoutSec: j.TimeoutSec, Nice: j.Nice, Paused: j.Paused,
		}
	}

	return out
}

// cronSite returns an active site for a cron change.
func (s *Service) cronSite(ctx context.Context, name string) (store.Site, error) {
	if s.cron == nil {
		return store.Site{}, ErrNoCron
	}

	site, err := s.Get(ctx, name)
	if err != nil {
		return store.Site{}, err
	}

	if site.State != store.SiteActive {
		return store.Site{}, errCronNotActive
	}

	if err := needAccount(site); err != nil {
		return store.Site{}, err
	}

	return site, nil
}

// applyCron sends the complete job list of a site.
func (s *Service) applyCron(ctx context.Context, site store.Site) error {
	jobs, err := s.st.CronJobs(ctx, site.ID)
	if err != nil {
		return err
	}

	return s.cron.CronApply(ctx, agentapi.CronApplyRequest{Site: site.Name, Jobs: agentJobs(jobs)})
}

// CronJobs lists the jobs of a site.
func (s *Service) CronJobs(ctx context.Context, site string) ([]store.CronJob, error) {
	st, err := s.Get(ctx, site)
	if err != nil {
		return nil, err
	}

	return s.st.CronJobs(ctx, st.ID)
}

// CronJob returns one job of a site.
func (s *Service) CronJob(ctx context.Context, site string, id int64) (store.CronJob, error) {
	st, err := s.Get(ctx, site)
	if err != nil {
		return store.CronJob{}, err
	}

	j, err := s.st.CronJob(ctx, st.ID, id)
	if errors.Is(err, store.ErrNotFound) {
		return store.CronJob{}, ErrCronNotFound
	}

	return j, err
}

// cronFields describes a job for a diff, without its command.
func cronFields(j store.CronJob) map[string]string {
	d := cronAudit(j)
	d["timeoutSec"] = strconv.FormatInt(j.TimeoutSec, 10)
	d["nice"] = "default"

	if j.Nice != nil {
		d["nice"] = strconv.Itoa(*j.Nice)
	}

	return d
}

func cronAudit(j store.CronJob) map[string]string {
	// The command is left out: it may carry a token or password.
	return map[string]string{"id": strconv.FormatInt(j.ID, 10), "schedule": j.Schedule, "paused": strconv.FormatBool(j.Paused)}
}

// CreateCronJob adds a job to a site and sends the site's jobs to the agent.
func (s *Service) CreateCronJob(ctx context.Context, actor int64, site string, in CronInput) (store.CronJob, error) {
	if err := in.check(); err != nil {
		return store.CronJob{}, err
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	st, err := s.cronSite(ctx, site)
	if err != nil {
		return store.CronJob{}, err
	}

	j, err := s.st.InsertCronJob(ctx, store.CronJob{
		SiteID: st.ID, Schedule: in.Schedule, Command: in.Command, TimeoutSec: in.TimeoutSec, Nice: in.Nice, Paused: in.Paused,
	}, cron.MaxJobsPerSite, s.now())
	if errors.Is(err, store.ErrTooMany) {
		return store.CronJob{}, ErrCronLimit
	}

	if err != nil {
		return store.CronJob{}, err
	}

	if err := s.applyCron(ctx, st); err != nil {
		if _, rerr := s.st.DeleteCronJob(context.WithoutCancel(ctx), st.ID, j.ID); rerr != nil {
			s.log.ErrorContext(ctx, "remove cron job after failed apply", "site", site, "err", rerr)
		}

		return store.CronJob{}, err
	}

	s.audit(ctx, actor, "cron.create", site, cronAudit(j))

	return j, nil
}

// UpdateCronJob changes a job, pausing or resuming it too.
func (s *Service) UpdateCronJob(ctx context.Context, actor int64, site string, id int64, in CronInput) (store.CronJob, error) {
	if err := in.check(); err != nil {
		return store.CronJob{}, err
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	st, err := s.cronSite(ctx, site)
	if err != nil {
		return store.CronJob{}, err
	}

	old, err := s.st.UpdateCronJob(ctx, store.CronJob{
		ID: id, SiteID: st.ID, Schedule: in.Schedule, Command: in.Command, TimeoutSec: in.TimeoutSec, Nice: in.Nice, Paused: in.Paused,
	}, s.now())
	if errors.Is(err, store.ErrNotFound) {
		return store.CronJob{}, ErrCronNotFound
	}

	if err != nil {
		return store.CronJob{}, err
	}

	if err := s.applyCron(ctx, st); err != nil {
		if rerr := s.st.RestoreCronJob(context.WithoutCancel(ctx), old); rerr != nil {
			s.log.ErrorContext(ctx, "restore cron job after failed apply", "site", site, "err", rerr)
		}

		return store.CronJob{}, err
	}

	j, err := s.st.CronJob(ctx, st.ID, id)
	if err != nil {
		return store.CronJob{}, err
	}

	was, now := cronFields(old), cronFields(j)
	if old.Command != j.Command {
		// The commands themselves stay out of the log; only the change is noted.
		was["command"], now["command"] = "(not logged)", "(changed, not logged)"
	}

	s.auditChange(ctx, actor, "cron.update", site, cronAudit(j), was, now)

	return j, nil
}

// DeleteCronJob removes a job.
func (s *Service) DeleteCronJob(ctx context.Context, actor int64, site string, id int64) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	st, err := s.cronSite(ctx, site)
	if err != nil {
		return err
	}

	old, err := s.st.DeleteCronJob(ctx, st.ID, id)
	if errors.Is(err, store.ErrNotFound) {
		return ErrCronNotFound
	}

	if err != nil {
		return err
	}

	if err := s.applyCron(ctx, st); err != nil {
		if rerr := s.st.RestoreCronJob(context.WithoutCancel(ctx), old); rerr != nil {
			s.log.ErrorContext(ctx, "restore cron job after failed apply", "site", site, "err", rerr)
		}

		return err
	}

	s.audit(ctx, actor, "cron.delete", site, cronAudit(old))

	return nil
}

// RunCronJob starts a job now, paused or not.
func (s *Service) RunCronJob(ctx context.Context, actor int64, site string, id int64) error {
	st, err := s.cronSite(ctx, site)
	if err != nil {
		return err
	}

	j, err := s.st.CronJob(ctx, st.ID, id)
	if errors.Is(err, store.ErrNotFound) {
		return ErrCronNotFound
	}

	if err != nil {
		return err
	}

	if err := s.cron.CronRun(ctx, site, id); err != nil {
		return err
	}

	s.audit(ctx, actor, "cron.run", site, cronAudit(j))

	return nil
}

// CronStatus reads the last and next runs of a site's jobs from the agent.
func (s *Service) CronStatus(ctx context.Context, site string) (agentapi.CronSiteStatus, error) {
	if _, err := s.cronSite(ctx, site); err != nil {
		return agentapi.CronSiteStatus{}, err
	}

	return s.cron.CronStatus(ctx, site)
}

// SyncCron sends the jobs of every active site again, for example after
// the panel started. It goes on after a failure and returns all errors.
func (s *Service) SyncCron(ctx context.Context) error {
	if s.cron == nil {
		return nil
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	sites, err := s.st.Sites(ctx)
	if err != nil {
		return err
	}

	var errs []error

	for _, site := range sites {
		if site.State == store.SiteActive && site.Account {
			if err := s.applyCron(ctx, site); err != nil {
				errs = append(errs, fmt.Errorf("site %s: %w", site.Name, err))
			}
		}
	}

	return errors.Join(errs...)
}
