// SPDX-License-Identifier: AGPL-3.0-or-later

package hosting

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"time"

	"github.com/maki-it/hostashi/internal/agentapi"
	"github.com/maki-it/hostashi/internal/agentrpc"
	"github.com/maki-it/hostashi/internal/doctor"
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
		return []agentapi.DoctorCheck{doctor.New(agentapi.DoctorFail, id, "clamav.error", "agentlog.hint", nil)}, fmt.Errorf("scan engine: %w", err)
	}

	var out []agentapi.DoctorCheck

	switch eng.State {
	case agentapi.ScanEngineOn:
		out = append(out, engineCheck(eng, now))
	case agentapi.ScanEngineFailed:
		out = append(out, doctor.New(agentapi.DoctorWarn, id, "clamav.failed", "clamav.failed.hint", nil))
	default:
		// Off, or installing or removing right now.
	}

	if eng.Realtime == agentapi.ScanEngineFailed {
		out = append(out, doctor.New(agentapi.DoctorWarn, id, "realtime.failed", "realtime.failed.hint", nil))
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
		return doctor.New(agentapi.DoctorWarn, id, "clamav.nosigs", "clamav.nosigs.hint", doctor.P{"engine": what})
	}

	at, err := time.Parse(time.RFC3339, eng.Signatures)
	if err != nil {
		return doctor.New(agentapi.DoctorWarn, id, "clamav.sigage", "", doctor.P{"engine": what})
	}

	if age := now.Sub(at); age > signaturesMaxAge {
		return doctor.New(agentapi.DoctorWarn, id, "clamav.old", "clamav.old.hint", doctor.P{"engine": what, "days": strconv.Itoa(int(age.Hours() / 24))})
	}

	return doctor.New(agentapi.DoctorOK, id, "clamav.ok", "", doctor.P{"engine": what, "time": at.UTC().Format("2006-01-02 15:04")})
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

		key := "malware.findings"
		if n == 1 {
			key = "malware.finding"
		}

		out = append(out, doctor.NewSite(agentapi.DoctorWarn, "site.malware", site.Name, key, "malware.findings.hint", doctor.P{"count": strconv.Itoa(n)}))
	}

	return out, nil
}
