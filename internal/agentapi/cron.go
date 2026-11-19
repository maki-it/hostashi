// SPDX-License-Identifier: AGPL-3.0-or-later

package agentapi

import (
	"fmt"
	"time"

	"github.com/maki-it/hostashi/internal/cron"
)

// CronJob is one cron job of a site (ADR-0028). The panel database holds
// the desired state; every change sends a site's complete list.
type CronJob struct {
	ID       int64  `json:"id"`
	Schedule string `json:"schedule"`
	Command  string `json:"command"`
	// TimeoutSec is the longest a run may take, in seconds; 0 means
	// cron.DefaultTimeout.
	TimeoutSec int64 `json:"timeout_sec,omitempty"`
	Paused     bool  `json:"paused,omitempty"`
	// Nice is the job's niceness, 0 to 19; nil means DefaultCronNice.
	Nice *int `json:"nice,omitempty"`
}

// DefaultCronNice is the niceness of a cron job that sets none (ADR-0015).
const DefaultCronNice = 10

// Niceness returns the job's niceness, with the default for nil.
func (j CronJob) Niceness() int {
	if j.Nice == nil {
		return DefaultCronNice
	}

	return *j.Nice
}

// Timeout returns the job's timeout, with the default for 0.
func (j CronJob) Timeout() time.Duration {
	if j.TimeoutSec == 0 {
		return cron.DefaultTimeout
	}

	return time.Duration(j.TimeoutSec) * time.Second
}

// Validate checks the id, schedule, command, timeout and niceness.
func (j CronJob) Validate() error {
	if j.ID < 1 {
		return fmt.Errorf("cron job: id must be positive, not %d", j.ID)
	}

	if _, err := cron.OnCalendar(j.Schedule); err != nil {
		return fmt.Errorf("cron job %d: %w", j.ID, err)
	}

	if err := cron.ValidateCommand(j.Command); err != nil {
		return fmt.Errorf("cron job %d: %w", j.ID, err)
	}

	if j.TimeoutSec < 0 || j.TimeoutSec > int64(cron.MaxTimeout/time.Second) {
		return fmt.Errorf("cron job %d: timeout out of range", j.ID)
	}

	if err := cron.ValidateTimeout(j.Timeout()); err != nil {
		return fmt.Errorf("cron job %d: %w", j.ID, err)
	}

	if n := j.Niceness(); n < 0 || n > 19 {
		return fmt.Errorf("cron job %d: nice must be 0 to 19", j.ID)
	}

	return nil
}

// CronApplyRequest replaces all cron jobs of a site.
type CronApplyRequest struct {
	Site string    `json:"site"`
	Jobs []CronJob `json:"jobs"`
}

// Validate checks the site, every job, the job limit and unique ids.
func (r CronApplyRequest) Validate() error {
	if err := ValidateSiteUser(r.Site); err != nil {
		return err
	}

	if len(r.Jobs) > cron.MaxJobsPerSite {
		return fmt.Errorf("cron: at most %d jobs per site", cron.MaxJobsPerSite)
	}

	seen := map[int64]bool{}

	for _, j := range r.Jobs {
		if err := j.Validate(); err != nil {
			return err
		}

		if seen[j.ID] {
			return fmt.Errorf("cron: job id %d twice", j.ID)
		}

		seen[j.ID] = true
	}

	return nil
}
