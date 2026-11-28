// SPDX-License-Identifier: AGPL-3.0-or-later

package serve

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"strconv"

	"github.com/maki-it/hostashi/internal/openapi"

	"github.com/maki-it/hostashi/internal/agentapi"
	"github.com/maki-it/hostashi/internal/auth"
	"github.com/maki-it/hostashi/internal/hosting"
	"github.com/maki-it/hostashi/internal/store"
)

// cronJobJSON is a job with its last and next run, when the agent knows
// them. Times are Unix seconds.
type cronJobJSON struct {
	ID         int64  `json:"id"`
	Schedule   string `json:"schedule"`
	Command    string `json:"command"`
	TimeoutSec int64  `json:"timeoutSec"` // 0: the default (3600)
	Nice       *int   `json:"nice"`       // null: the default (10)
	Paused     bool   `json:"paused"`
	CreatedAt  int64  `json:"createdAt"`
	UpdatedAt  int64  `json:"updatedAt"`

	Status *cronStatusJSON `json:"status,omitempty"`
}

type cronStatusJSON struct {
	Running    bool   `json:"running"`
	Result     string `json:"result,omitempty"`
	ExitStatus *int   `json:"exitStatus,omitempty"`
	LastStart  int64  `json:"lastStart,omitempty"`
	LastEnd    int64  `json:"lastEnd,omitempty"`
	Next       int64  `json:"next,omitempty"`
}

func toCronJSON(j store.CronJob) cronJobJSON {
	return cronJobJSON{
		ID: j.ID, Schedule: j.Schedule, Command: j.Command, TimeoutSec: j.TimeoutSec, Nice: j.Nice, Paused: j.Paused,
		CreatedAt: j.CreatedAt.Unix(), UpdatedAt: j.UpdatedAt.Unix(),
	}
}

func toCronStatusJSON(s agentapi.CronJobStatus) *cronStatusJSON {
	return &cronStatusJSON{
		Running: s.Running, Result: s.Result, ExitStatus: s.ExitStatus, LastStart: s.LastStart, LastEnd: s.LastEnd, Next: s.Next,
	}
}

// cronOutputJSON is what the last run of a job wrote (ADR-0028 Addendum
// 1): the last lines, oldest first. ran is false when the job has not run
// since the server started or its units were written.
type cronOutputJSON struct {
	Ran       bool     `json:"ran"`
	Lines     []string `json:"lines"`
	Truncated bool     `json:"truncated"`
}

// cronRunsJSON lists the last runs of a job, newest first (ADR-0028
// Addendum 2). Times are Unix seconds; end is absent while a run goes on
// or when the journal has no end. result is systemd's ("success",
// "exit-code", "timeout", ...), absent when unknown.
type cronRunsJSON struct {
	Runs []cronRunJSON `json:"runs"`
}

type cronRunJSON struct {
	ID         string `json:"id"`
	Start      int64  `json:"start"`
	End        int64  `json:"end,omitempty"`
	Running    bool   `json:"running"`
	Result     string `json:"result,omitempty"`
	ExitStatus *int   `json:"exitStatus,omitempty"`
}

type cronInputJSON struct {
	Schedule   string `json:"schedule"`
	Command    string `json:"command"`
	TimeoutSec int64  `json:"timeoutSec"`
	Nice       *int   `json:"nice"`
	Paused     bool   `json:"paused"`
}

func (in cronInputJSON) input() hosting.CronInput {
	return hosting.CronInput{Schedule: in.Schedule, Command: in.Command, TimeoutSec: in.TimeoutSec, Nice: in.Nice, Paused: in.Paused}
}

func cronFail(log *slog.Logger, w http.ResponseWriter, err error, what string) {
	switch {
	case errors.Is(err, hosting.ErrNoCron):
		writeError(w, http.StatusServiceUnavailable, "cron_unavailable", "cron jobs are not available on this panel")
	case errors.Is(err, hosting.ErrCronNotFound):
		writeError(w, http.StatusNotFound, "not_found", "no such cron job")
	case errors.Is(err, hosting.ErrCronRunNotFound):
		writeError(w, http.StatusNotFound, "run_not_found", "no such run among the last runs of the cron job")
	case errors.Is(err, hosting.ErrCronLimit):
		writeError(w, http.StatusConflict, "too_many_cron_jobs", err.Error())
	default:
		siteFail(log, w, err, what)
	}
}

func cronID(w http.ResponseWriter, r *http.Request) (int64, bool) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil || id < 1 {
		writeError(w, http.StatusNotFound, "not_found", "no such cron job")

		return 0, false
	}

	return id, true
}

// registerCron adds the cron job routes of a site (ADR-0028). Every change
// sends the site's complete list to the agent; a refused change is undone.
// cronListJSON lists a site's cron jobs; statusKnown is false when the
// agent could not report the last runs.
type cronListJSON struct {
	Jobs        []cronJobJSON `json:"jobs"`
	StatusKnown bool          `json:"statusKnown"`
}

func registerCron(mux *routeMux, a authn, svc *hosting.Service, log *slog.Logger) {
	// The list carries each job's last and next run. Without an answer of
	// the agent the jobs come without status.
	mux.Op("GET /api/v1/sites/{name}/cron", openapi.Operation{
		ID: "listCronJobs", Tag: "cron", Summary: "A site's cron jobs with their last run", Out: cronListJSON{},
	}, a.allow("cron:read", func(w http.ResponseWriter, r *http.Request, _ auth.Principal) {
		site := r.PathValue("name")

		jobs, err := svc.CronJobs(r.Context(), site)
		if err != nil {
			cronFail(log, w, err, "list cron jobs")

			return
		}

		out := make([]cronJobJSON, 0, len(jobs))
		for _, j := range jobs {
			out = append(out, toCronJSON(j))
		}

		statusKnown := false

		if len(jobs) > 0 {
			ctx, cancel := context.WithTimeout(r.Context(), agentTimeout)
			defer cancel()

			st, err := svc.CronStatus(ctx, site)
			if err == nil {
				statusKnown = true

				byID := map[int64]agentapi.CronJobStatus{}
				for _, s := range st.Jobs {
					byID[s.ID] = s
				}

				for i := range out {
					if s, ok := byID[out[i].ID]; ok {
						out[i].Status = toCronStatusJSON(s)
					}
				}
			} else if !errors.Is(err, hosting.ErrNoCron) {
				log.WarnContext(r.Context(), "cron status", "site", site, "err", err)
			}
		}

		writeJSON(w, http.StatusOK, cronListJSON{Jobs: out, StatusKnown: statusKnown})
	}))

	mux.Op("GET /api/v1/sites/{name}/cron/{id}", openapi.Operation{
		ID: "getCronJob", Tag: "cron", Summary: "One cron job", Out: cronJobJSON{},
	}, a.allow("cron:read", func(w http.ResponseWriter, r *http.Request, _ auth.Principal) {
		id, ok := cronID(w, r)
		if !ok {
			return
		}

		j, err := svc.CronJob(r.Context(), r.PathValue("name"), id)
		if err != nil {
			cronFail(log, w, err, "get cron job")

			return
		}

		writeJSON(w, http.StatusOK, toCronJSON(j))
	}))

	mux.Op("POST /api/v1/sites/{name}/cron", openapi.Operation{
		ID: "createCronJob", Tag: "cron", Summary: "Add a cron job", In: cronInputJSON{}, Out: cronJobJSON{}, Status: http.StatusCreated,
	}, a.allow("cron:write", func(w http.ResponseWriter, r *http.Request, p auth.Principal) {
		var in cronInputJSON
		if !decodeJSON(w, r, &in) {
			return
		}

		ctx, cancel := context.WithTimeout(r.Context(), agentTimeout)
		defer cancel()

		j, err := svc.CreateCronJob(ctx, p.User.ID, r.PathValue("name"), in.input())
		if err != nil {
			cronFail(log, w, err, "create cron job")

			return
		}

		writeJSON(w, http.StatusCreated, toCronJSON(j))
	}))

	// PUT replaces all fields of a job; pausing is "paused": true.
	mux.Op("PUT /api/v1/sites/{name}/cron/{id}", openapi.Operation{
		ID: "updateCronJob", Tag: "cron", Summary: "Change a cron job", In: cronInputJSON{}, Out: cronJobJSON{},
	}, a.allow("cron:write", func(w http.ResponseWriter, r *http.Request, p auth.Principal) {
		id, ok := cronID(w, r)
		if !ok {
			return
		}

		var in cronInputJSON
		if !decodeJSON(w, r, &in) {
			return
		}

		ctx, cancel := context.WithTimeout(r.Context(), agentTimeout)
		defer cancel()

		j, err := svc.UpdateCronJob(ctx, p.User.ID, r.PathValue("name"), id, in.input())
		if err != nil {
			cronFail(log, w, err, "change cron job")

			return
		}

		writeJSON(w, http.StatusOK, toCronJSON(j))
	}))

	mux.Op("DELETE /api/v1/sites/{name}/cron/{id}", openapi.Operation{
		ID: "deleteCronJob", Tag: "cron", Summary: "Remove a cron job",
	}, a.allow("cron:write", func(w http.ResponseWriter, r *http.Request, p auth.Principal) {
		id, ok := cronID(w, r)
		if !ok {
			return
		}

		ctx, cancel := context.WithTimeout(r.Context(), agentTimeout)
		defer cancel()

		if err := svc.DeleteCronJob(ctx, p.User.ID, r.PathValue("name"), id); err != nil {
			cronFail(log, w, err, "delete cron job")

			return
		}

		w.WriteHeader(http.StatusNoContent)
	}))

	mux.Op("GET /api/v1/sites/{name}/cron/{id}/output", openapi.Operation{
		ID: "getCronJobOutput", Tag: "cron", Summary: "What a run of a cron job wrote, by default the last one", Out: cronOutputJSON{},
		Query: []openapi.Param{{Name: "run", Description: "id of one of the last runs, from the runs list; absent for the last run"}},
	}, a.allow("cron:read", func(w http.ResponseWriter, r *http.Request, _ auth.Principal) {
		id, ok := cronID(w, r)
		if !ok {
			return
		}

		ctx, cancel := context.WithTimeout(r.Context(), agentTimeout)
		defer cancel()

		out, err := svc.CronOutput(ctx, r.PathValue("name"), id, r.URL.Query().Get("run"))
		if err != nil {
			cronFail(log, w, err, "read cron job output")

			return
		}

		lines := out.Lines
		if lines == nil {
			lines = []string{}
		}

		writeJSON(w, http.StatusOK, cronOutputJSON{Ran: out.Ran, Lines: lines, Truncated: out.Truncated})
	}))

	mux.Op("GET /api/v1/sites/{name}/cron/{id}/runs", openapi.Operation{
		ID: "listCronJobRuns", Tag: "cron", Summary: "The last runs of a cron job, newest first", Out: cronRunsJSON{},
	}, a.allow("cron:read", func(w http.ResponseWriter, r *http.Request, _ auth.Principal) {
		id, ok := cronID(w, r)
		if !ok {
			return
		}

		ctx, cancel := context.WithTimeout(r.Context(), agentTimeout)
		defer cancel()

		runs, err := svc.CronRuns(ctx, r.PathValue("name"), id)
		if err != nil {
			cronFail(log, w, err, "list cron job runs")

			return
		}

		out := cronRunsJSON{Runs: make([]cronRunJSON, 0, len(runs.Runs))}

		for _, run := range runs.Runs {
			out.Runs = append(out.Runs, cronRunJSON{
				ID: run.Invocation, Start: run.Start / 1_000_000, End: run.End / 1_000_000,
				Running: run.Running, Result: run.Result, ExitStatus: run.ExitStatus,
			})
		}

		writeJSON(w, http.StatusOK, out)
	}))

	// Starts the job now, paused or not; the run goes on in the background.
	mux.Op("POST /api/v1/sites/{name}/cron/{id}/run", openapi.Operation{
		ID: "runCronJob", Tag: "cron", Summary: "Run a cron job once now", Status: http.StatusAccepted,
	}, a.allow("cron:write", func(w http.ResponseWriter, r *http.Request, p auth.Principal) {
		id, ok := cronID(w, r)
		if !ok {
			return
		}

		ctx, cancel := context.WithTimeout(r.Context(), agentTimeout)
		defer cancel()

		if err := svc.RunCronJob(ctx, p.User.ID, r.PathValue("name"), id); err != nil {
			cronFail(log, w, err, "run cron job")

			return
		}

		w.WriteHeader(http.StatusAccepted)
	}))
}
