// SPDX-License-Identifier: AGPL-3.0-or-later

package serve

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"time"

	"github.com/maki-it/hostashi/internal/openapi"

	"github.com/maki-it/hostashi/internal/agentapi"
	"github.com/maki-it/hostashi/internal/agentrpc"
	"github.com/maki-it/hostashi/internal/auth"
	"github.com/maki-it/hostashi/internal/doctor"
)

// Doctor runs the read-only checks of hostashi doctor in the agent.
type Doctor func(ctx context.Context) (agentapi.DoctorReport, error)

// RPCDoctor asks the agent over c.
func RPCDoctor(c *agentrpc.Client) Doctor {
	return func(ctx context.Context) (agentapi.DoctorReport, error) {
		return agentapi.Doctor.Call(ctx, c, agentapi.DoctorRequest{})
	}
}

type doctorJSON struct {
	Status    string                 `json:"status"` // the worst status: ok, warn or fail
	CheckedAt time.Time              `json:"checkedAt"`
	Checks    []agentapi.DoctorCheck `json:"checks"`
	Counts    map[string]int         `json:"counts"`
}

// PanelChecks are checks the panel process runs itself, such as those of
// the external database servers it reaches and the agent cannot.
type PanelChecks func(ctx context.Context, now time.Time) ([]agentapi.DoctorCheck, error)

// registerDoctor adds GET /api/v1/doctor. The report names paths and
// services of the server, so only administrators read it. panel may be nil.
func registerDoctor(mux *routeMux, a authn, run Doctor, panel PanelChecks, log *slog.Logger) {
	mux.Op("GET /api/v1/doctor", openapi.Operation{
		ID: "runDoctor", Tag: "system", Summary: "Check the server and every site without changing anything", Out: doctorJSON{},
		Query: []openapi.Param{{Name: "lang", Description: "de for German summaries and hints; anything else is English"}},
	}, a.allow("doctor:read", func(w http.ResponseWriter, r *http.Request, _ auth.Principal) {
		ctx, cancel := context.WithTimeout(r.Context(), agentTimeout)
		defer cancel()

		rep, err := run(ctx)
		if err != nil {
			log.WarnContext(r.Context(), "doctor", "err", err)

			if errors.Is(err, context.DeadlineExceeded) {
				writeError(w, http.StatusGatewayTimeout, "agent_timeout", "the agent did not answer in time")
			} else {
				writeError(w, http.StatusServiceUnavailable, "agent_unavailable", "the checks could not be run, try again")
			}

			return
		}

		if panel != nil {
			more, err := panel(ctx, time.Now())
			if err != nil {
				log.WarnContext(r.Context(), "doctor: panel checks", "err", err)

				more = append(more, doctor.New(agentapi.DoctorWarn, "panel.checks", "panelchecks.error", "", nil))
			}

			rep.Checks = append(rep.Checks, more...)
		}

		out := doctorJSON{
			Status: rep.Worst(), CheckedAt: time.Now().UTC().Truncate(time.Second), Checks: rep.Checks,
			Counts: map[string]int{agentapi.DoctorOK: 0, agentapi.DoctorWarn: 0, agentapi.DoctorFail: 0},
		}
		if out.Checks == nil {
			out.Checks = []agentapi.DoctorCheck{}
		}

		german := r.URL.Query().Get("lang") == "de"

		for i, c := range out.Checks {
			out.Counts[c.Status]++

			if german {
				out.Checks[i].Summary, out.Checks[i].Hint = doctor.German(c)
			}
		}

		w.Header().Set("Cache-Control", "no-store")
		writeJSON(w, http.StatusOK, out)
	}))
}
