// SPDX-License-Identifier: AGPL-3.0-or-later

package sites

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"sort"
	"sync"
	"time"

	"github.com/maki-it/hostashi/internal/agentapi"
)

// Errors of copy jobs.
var (
	ErrCopyBusy     = errors.New("sites: a copy into or out of this site is running")
	ErrCopyNotFound = errors.New("sites: unknown copy job")
)

// Limits of copy jobs.
const (
	// MaxCopyDuration ends a copy that runs longer.
	MaxCopyDuration = 6 * time.Hour
	keepCopyJobs    = 50
)

// copyJobs keeps the copy jobs of this agent run in memory. A job lost by
// a restart is unknown to the panel, which counts it as failed.
type copyJobs struct {
	mu   sync.Mutex
	jobs map[string]*copyJob
	busy map[string]bool
}

type copyJob struct {
	job  agentapi.SiteHomeCopyJob
	walk *copyWalk
	done chan struct{}
}

func (j *copyJob) snapshot() agentapi.SiteHomeCopyJob {
	out := j.job
	if j.walk != nil {
		j.walk.fill(&out)
	}

	return out
}

// StartCopy checks the request and starts copying the home of src into
// the home of dst in the background (ADR-0070). Only one copy may touch a
// site at a time. ctx bounds the job; the agent passes its run context.
func (m *Manager) StartCopy(ctx context.Context, src, dst string, replace bool) (agentapi.SiteHomeCopyJob, error) {
	req := agentapi.SiteHomeCopyRequest{Source: src, Target: dst, Replace: replace}
	if err := req.Validate(); err != nil {
		return agentapi.SiteHomeCopyJob{}, err
	}

	m.mu.Lock()
	_, okSrc := m.reg.Site(src)
	_, okDst := m.reg.Site(dst)
	m.mu.Unlock()

	if !okSrc || !okDst {
		return agentapi.SiteHomeCopyJob{}, ErrNotFound
	}

	c := &m.copies

	c.mu.Lock()
	defer c.mu.Unlock()

	if c.busy[src] || c.busy[dst] {
		return agentapi.SiteHomeCopyJob{}, ErrCopyBusy
	}

	if c.jobs == nil {
		c.jobs, c.busy = map[string]*copyJob{}, map[string]bool{}
	}

	c.prune()

	b := make([]byte, 8)
	_, _ = rand.Read(b)

	j := &copyJob{
		job:  agentapi.SiteHomeCopyJob{ID: hex.EncodeToString(b), Source: src, Target: dst, Replace: replace, State: agentapi.JobRunning, Started: time.Now().UTC()},
		done: make(chan struct{}),
	}

	c.jobs[j.job.ID] = j
	c.busy[src], c.busy[dst] = true, true

	go m.runCopy(context.WithoutCancel(ctx), j)

	return j.job, nil
}

func (m *Manager) runCopy(ctx context.Context, j *copyJob) {
	defer close(j.done)

	ctx, cancel := context.WithTimeout(ctx, MaxCopyDuration)
	defer cancel()

	// The walk is created inside CopyHome; hand it to the job as early as
	// possible so the counters show while it runs.
	walk, err := m.copyHomeTracked(ctx, j)

	c := &m.copies

	c.mu.Lock()
	defer c.mu.Unlock()

	delete(c.busy, j.job.Source)
	delete(c.busy, j.job.Target)

	j.walk = walk
	j.job.Finished = time.Now().UTC()
	j.job.State = agentapi.JobDone

	if err != nil {
		j.job.State = agentapi.JobFailed
		j.job.Error = copyError(err)
		m.opt.Log.ErrorContext(ctx, "site home copy failed", "job", j.job.ID, "source", j.job.Source, "target", j.job.Target, "err", err)
	}
}

func (m *Manager) copyHomeTracked(ctx context.Context, j *copyJob) (*copyWalk, error) {
	return m.copyHome(ctx, j.job.Source, j.job.Target, j.job.Replace, func(w *copyWalk) {
		m.copies.mu.Lock()
		j.walk = w
		m.copies.mu.Unlock()
	})
}

// copyError turns an error into a fixed text for the panel.
func copyError(err error) string {
	switch {
	case errors.Is(err, context.DeadlineExceeded):
		return "the copy took longer than the limit"
	case errors.Is(err, context.Canceled):
		return "the copy was canceled"
	case errors.Is(err, ErrCopyTargetExists):
		return "an entry exists in the target home already"
	case errors.Is(err, ErrHomeUnsafe), errors.Is(err, ErrAccountMismatch):
		return "a site directory or account failed safety checks"
	case errors.Is(err, ErrPoolUnavailable):
		return "a storage pool is unavailable"
	case errors.Is(err, ErrNotFound):
		return "unknown site"
	default:
		return "the copy failed, see the agent log"
	}
}

// CopyJob returns a job with its current counters.
func (m *Manager) CopyJob(id string) (agentapi.SiteHomeCopyJob, error) {
	c := &m.copies

	c.mu.Lock()
	defer c.mu.Unlock()

	j, ok := c.jobs[id]
	if !ok {
		return agentapi.SiteHomeCopyJob{}, ErrCopyNotFound
	}

	return j.snapshot(), nil
}

// waitCopy blocks until a job has ended (tests).
func (m *Manager) waitCopy(id string) {
	m.copies.mu.Lock()
	j := m.copies.jobs[id]
	m.copies.mu.Unlock()

	if j != nil {
		<-j.done
	}
}

// prune drops the oldest finished jobs beyond keepCopyJobs. c.mu is held.
func (c *copyJobs) prune() {
	if len(c.jobs) < keepCopyJobs {
		return
	}

	var ended []*copyJob

	for _, j := range c.jobs {
		if j.job.State != agentapi.JobRunning {
			ended = append(ended, j)
		}
	}

	sort.Slice(ended, func(a, b int) bool { return ended[a].job.Started.Before(ended[b].job.Started) })

	for _, j := range ended[:min(len(ended), len(c.jobs)-keepCopyJobs+1)] {
		delete(c.jobs, j.job.ID)
	}
}
