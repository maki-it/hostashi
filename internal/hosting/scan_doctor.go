// SPDX-License-Identifier: AGPL-3.0-or-later

package hosting

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/maki-it/hostashi/internal/agentapi"
	"github.com/maki-it/hostashi/internal/agentrpc"
	"github.com/maki-it/hostashi/internal/store"
)

// signaturesMaxAge is when the Health page calls the signatures stale
// (ADR-0064): freshclam updates them several times a day, so three days
// without one means updates stopped.
const signaturesMaxAge = 3 * 24 * time.Hour

// MalwareDoctor reports the malware scan on the Health page (ADR-0064):
// nothing while it is off, a warning when the install failed or the
// signatures are older than three days, and per site the open findings.
func (s *Service) MalwareDoctor(ctx context.Context, now time.Time) ([]agentapi.DoctorCheck, error) {
	const id = "panel.malware"

	eng, err := s.ScanEngine(ctx)
	if errors.Is(err, ErrNoScans) || isCode(err, agentrpc.CodePrecondition) {
		return nil, nil
	} else if err != nil {
		return []agentapi.DoctorCheck{{ID: id, Status: agentapi.DoctorFail, Summary: "cannot read the state of ClamAV", Hint: "Check the agent's log (journalctl -u hostashi-agent)."}}, fmt.Errorf("scan engine: %w", err)
	}

	var out []agentapi.DoctorCheck

	switch eng.State {
	case agentapi.ScanEngineOn:
		out = append(out, engineCheck(eng, now))
	case agentapi.ScanEngineFailed:
		out = append(out, agentapi.DoctorCheck{
			ID: id, Status: agentapi.DoctorWarn, Summary: "ClamAV could not be installed or removed",
			Hint: "See the install log under System → Virus scan and switch it on or off again.",
		})
	default:
		// Off, or installing or removing right now.
	}

	if eng.Realtime == agentapi.ScanEngineFailed {
		out = append(out, agentapi.DoctorCheck{
			ID: id, Status: agentapi.DoctorWarn, Summary: "the real-time scan could not be switched",
			Hint: "See the install log under System → Virus scan and switch the real-time scan on or off again.",
		})
	}

	sites, err := s.findingSites(ctx)
	if err != nil {
		return out, err
	}

	return append(out, sites...), nil
}

func engineCheck(eng agentapi.ScanEngineStatus, now time.Time) agentapi.DoctorCheck {
	const id = "panel.malware"

	what := "ClamAV"
	if eng.Version != "" {
		what += " " + eng.Version
	}

	if eng.Signatures == "" {
		return agentapi.DoctorCheck{
			ID: id, Status: agentapi.DoctorWarn, Summary: what + " has no signatures yet",
			Hint: "freshclam downloads them within minutes of the install. If this stays, check journalctl -u clamav-freshclam.",
		}
	}

	at, err := time.Parse(time.RFC3339, eng.Signatures)
	if err != nil {
		return agentapi.DoctorCheck{ID: id, Status: agentapi.DoctorWarn, Summary: what + ": cannot read the age of the signatures"}
	}

	if age := now.Sub(at); age > signaturesMaxAge {
		return agentapi.DoctorCheck{
			ID: id, Status: agentapi.DoctorWarn,
			Summary: fmt.Sprintf("%s: the signatures are %d days old", what, int(age.Hours()/24)),
			Hint:    "freshclam has not updated them. Check journalctl -u clamav-freshclam; a distribution that stopped updating ClamAV makes the downloads fail.",
		}
	}

	return agentapi.DoctorCheck{ID: id, Status: agentapi.DoctorOK, Summary: what + ", signatures from " + at.UTC().Format("2006-01-02 15:04") + " UTC"}
}

// findingSites is one warning per site with open findings, also while
// ClamAV is off: the findings stay until somebody acts on them.
func (s *Service) findingSites(ctx context.Context) ([]agentapi.DoctorCheck, error) {
	open, err := s.st.OpenScanFindings(ctx)
	if err != nil || len(open) == 0 {
		return nil, err
	}

	sites, err := s.st.Sites(ctx)
	if err != nil {
		return nil, err
	}

	var out []agentapi.DoctorCheck

	for _, site := range sites {
		n := open[site.ID]
		if n == 0 || site.State != store.SiteActive {
			continue
		}

		summary := fmt.Sprintf("%d open virus scan findings", n)
		if n == 1 {
			summary = "1 open virus scan finding"
		}

		out = append(out, agentapi.DoctorCheck{
			ID: "site.malware", Site: site.Name, Status: agentapi.DoctorWarn, Summary: summary,
			Hint: "Open the site's Virus scan tab: quarantine the files, or mark them as false positives.",
		})
	}

	return out, nil
}
