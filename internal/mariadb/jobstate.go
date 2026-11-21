// SPDX-License-Identifier: AGPL-3.0-or-later

package mariadb

import (
	"encoding/json"
	"errors"
	"os"
	"regexp"

	"github.com/maki-it/hostashi/internal/agentapi"
	"github.com/maki-it/hostashi/internal/statefile"
)

// JobsFile keeps import and export jobs across agent restarts, so a job
// that a restart cut off is reported as failed instead of being unknown.
const JobsFile = "database-jobs.json"

// ErrTextInterrupted is the error of a job that was running when the
// agent stopped. The client program died with the agent; an export file
// it was writing is incomplete.
const ErrTextInterrupted = "interrupted by an agent restart"

const maxJobsFileSize = 1 << 20

var jobIDRe = regexp.MustCompile(`^[0-9a-f]{16}$`)

type jobsState struct {
	Version int              `json:"version"`
	Jobs    []agentapi.DBJob `json:"jobs"`
}

// loadJobs reads the kept jobs. A missing or broken file only starts an
// empty list: the jobs are a report, not state the agent acts on.
func (m *Manager) loadJobs() map[string]*agentapi.DBJob {
	jobs := map[string]*agentapi.DBJob{}

	raw, err := statefile.Read(m.reg.dir, JobsFile, m.owner, maxJobsFileSize)

	switch {
	case errors.Is(err, os.ErrNotExist):
		return jobs
	case err != nil:
		m.log.Warn("mariadb: read kept jobs", "err", err)

		return jobs
	}

	var st jobsState
	if err := json.Unmarshal(raw, &st); err != nil || st.Version != stateVersion {
		m.log.Warn("mariadb: kept jobs are invalid, starting without them", "err", err)

		return jobs
	}

	now := m.now().UTC()

	for i := range st.Jobs {
		j := st.Jobs[i]
		if !jobIDRe.MatchString(j.ID) || jobs[j.ID] != nil {
			continue
		}

		if j.State == agentapi.JobRunning {
			j.State = agentapi.JobFailed
			j.Error = ErrTextInterrupted
			j.Finished = now
		}

		jobs[j.ID] = &j
	}

	m.jobs = jobs
	m.pruneJobs()

	return m.jobs
}

// saveJobs writes all jobs; the caller holds m.mu. A failed write is
// logged and the job goes on.
func (m *Manager) saveJobs() {
	st := jobsState{Version: stateVersion, Jobs: make([]agentapi.DBJob, 0, len(m.jobs))}
	for _, j := range m.jobs {
		st.Jobs = append(st.Jobs, *j)
	}

	raw, err := json.Marshal(st)
	if err == nil {
		err = statefile.Write(m.reg.dir, JobsFile, append(raw, '\n'))
	}

	if err != nil {
		m.log.Error("mariadb: keep jobs", "err", err)
	}
}
