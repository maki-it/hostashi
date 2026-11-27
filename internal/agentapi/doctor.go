// SPDX-License-Identifier: AGPL-3.0-or-later

package agentapi

import "github.com/maki-it/hostashi/internal/agentrpc"

// Doctor check results, from good to bad.
const (
	DoctorOK   = "ok"
	DoctorWarn = "warn"
	DoctorFail = "fail"
)

// DoctorCheck is the result of one check of hostashi doctor. Summary says
// what was found, Hint what to do about it (empty when all is well).
type DoctorCheck struct {
	ID      string `json:"id"`             // for example "system.quota" or "site.disk"
	Site    string `json:"site,omitempty"` // the site of a per-site check
	Pool    string `json:"pool,omitempty"` // the storage pool of a per-pool check
	Status  string `json:"status"`         // DoctorOK, DoctorWarn or DoctorFail
	Summary string `json:"summary"`
	Hint    string `json:"hint,omitempty"`
}

// DoctorRequest has no parameters: the agent runs every check.
type DoctorRequest struct{}

// Validate accepts every request.
func (DoctorRequest) Validate() error { return nil }

// DoctorReport lists the checks in a fixed order: the system first, then
// the sites sorted by name.
type DoctorReport struct {
	Checks []DoctorCheck `json:"checks"`
}

// Worst returns the worst status in the report, DoctorOK for an empty one.
func (r DoctorReport) Worst() string {
	worst := DoctorOK

	for _, c := range r.Checks {
		switch c.Status {
		case DoctorFail:
			return DoctorFail
		case DoctorWarn:
			worst = DoctorWarn
		}
	}

	return worst
}

// Doctor runs the read-only health checks of the server. It changes
// nothing.
var Doctor = agentrpc.Op[DoctorRequest, DoctorReport]{Name: "doctor.run"}
