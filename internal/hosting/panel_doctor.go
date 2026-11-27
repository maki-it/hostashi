// SPDX-License-Identifier: AGPL-3.0-or-later

package hosting

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/maki-it/hostashi/internal/agentapi"
	"github.com/maki-it/hostashi/internal/doctor"
)

// PanelTLSDoctor reports the panel's own certificate on the Health page
// (ADR-0023, ADR-0039): when it expires within the thresholds of the site
// certificates, or when getting or renewing it failed. The certificate
// itself is set under System. Nothing while the panel has no host name or
// waits for its first certificate.
func (s *Service) PanelTLSDoctor(ctx context.Context, now time.Time) ([]agentapi.DoctorCheck, error) {
	const id = "panel.tls"

	if s.panelDomain == "" {
		return nil, nil
	}

	p, err := s.PanelTLS(ctx)
	if err != nil {
		return []agentapi.DoctorCheck{doctor.New(agentapi.DoctorFail, id, "paneltls.error", "", nil)}, fmt.Errorf("panel tls: %w", err)
	}

	// Waiting for the first certificate, nothing failed yet.
	if p.NotAfter.IsZero() && p.Error == "" {
		return nil, nil
	}

	return []agentapi.DoctorCheck{panelTLSCheck(p, now)}, nil
}

func panelTLSCheck(p PanelTLS, now time.Time) agentapi.DoctorCheck {
	const id = "panel.tls"

	const hint = "paneltls.hint"

	if p.NotAfter.IsZero() {
		return doctor.New(agentapi.DoctorFail, id, "paneltls.none", hint, doctor.P{"domain": p.Domain, "error": firstErrLine(p.Error)})
	}

	left := p.NotAfter.Sub(now)
	days := int(left.Hours() / 24)
	params := doctor.P{"domain": p.Domain, "date": p.NotAfter.UTC().Format("2006-01-02"), "days": strconv.Itoa(days)}

	switch {
	case left <= 0:
		return doctor.New(agentapi.DoctorFail, id, "paneltls.expired", hint, params)
	case days < doctor.CertFailDays:
		return doctor.New(agentapi.DoctorFail, id, "paneltls.valid", hint, params)
	case days < doctor.CertWarnDays:
		return doctor.New(agentapi.DoctorWarn, id, "paneltls.valid", hint, params)
	case p.Error != "":
		params["error"] = firstErrLine(p.Error)

		return doctor.New(agentapi.DoctorWarn, id, "paneltls.renewfail", hint, params)
	}

	return doctor.New(agentapi.DoctorOK, id, "paneltls.valid", "", params)
}

func firstErrLine(s string) string {
	s, _, _ = strings.Cut(strings.TrimSpace(s), "\n")

	return s
}
