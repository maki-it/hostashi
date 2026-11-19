// SPDX-License-Identifier: AGPL-3.0-or-later

package plan

import (
	"github.com/maki-it/hostashi/internal/cloudpanel/scan"
	"github.com/maki-it/hostashi/internal/cron"
)

// CronJobs returns the scanned cron jobs of a site that Hostashi accepts
// (ADR-0028), in crontab order and at most cron.MaxJobsPerSite. Jobs with
// @reboot or a schedule the validator does not understand stay behind.
func CronJobs(s scan.Site) []scan.CronJob {
	out := []scan.CronJob{}

	for _, j := range s.Cron {
		if len(out) == cron.MaxJobsPerSite {
			break
		}

		if _, err := cron.OnCalendar(j.Schedule); err != nil {
			continue
		}

		if cron.ValidateCommand(j.Command) != nil {
			continue
		}

		out = append(out, j)
	}

	return out
}
