// SPDX-License-Identifier: AGPL-3.0-or-later

package agent

import (
	"context"
	"errors"

	"github.com/maki-it/hostashi/internal/agentapi"
	"github.com/maki-it/hostashi/internal/agentrpc"
	"github.com/maki-it/hostashi/internal/dbjob"
)

var errNoDBJobs = &agentrpc.Error{Code: agentrpc.CodePrecondition, Message: "import and export for external databases are not available"}

// registerDBJobs serves import and export of databases on external servers
// (ADR-0041 addendum 1).
func registerDBJobs(s *agentrpc.Server, jobs *dbjob.Agent) {
	agentrpc.Handle(s, agentapi.DBRemoteJobStart, func(ctx context.Context, _ agentrpc.Caller, req agentapi.DBRemoteJobRequest) (agentapi.DBJob, error) {
		if jobs == nil {
			return agentapi.DBJob{}, errNoDBJobs
		}

		j, err := jobs.Start(ctx, req)

		return j, mapDBJobError(err)
	})

	agentrpc.Handle(s, agentapi.DBRemoteJobGet, func(_ context.Context, _ agentrpc.Caller, req agentapi.DBJobRequest) (agentapi.DBJob, error) {
		if jobs == nil {
			return agentapi.DBJob{}, errNoDBJobs
		}

		j, err := jobs.Get(req.ID)

		return j, mapDBJobError(err)
	})
}

func mapDBJobError(err error) error {
	switch {
	case err == nil:
		return nil
	case errors.Is(err, dbjob.ErrInvalid):
		return &agentrpc.Error{Code: agentrpc.CodeInvalidArgument, Message: err.Error()}
	case errors.Is(err, dbjob.ErrNoSite), errors.Is(err, dbjob.ErrNotFound):
		return &agentrpc.Error{Code: agentrpc.CodeNotFound, Message: err.Error()}
	case errors.Is(err, dbjob.ErrBusy):
		return &agentrpc.Error{Code: agentrpc.CodeConflict, Message: "another import or export of this database is running"}
	default:
		return err
	}
}
