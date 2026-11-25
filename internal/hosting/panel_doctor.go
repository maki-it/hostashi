// SPDX-License-Identifier: AGPL-3.0-or-later

package hosting

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/maki-it/hostashi/internal/agentapi"
	"github.com/maki-it/hostashi/internal/doctor"
)

// panelTLSHint says where the panel's certificate is managed.
const panelTLSHint = "See System → Panel certificate. Let's Encrypt needs the panel's host name to point at this server; " +
	"an own certificate is never renewed automatically: upload a new one."

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
		return []agentapi.DoctorCheck{{ID: id, Status: agentapi.DoctorFail, Summary: "cannot read the state of the panel's certificate"}}, fmt.Errorf("panel tls: %w", err)
	}

	// Waiting for the first certificate, nothing failed yet.
	if p.NotAfter.IsZero() && p.Error == "" {
		return nil, nil
	}

	return []agentapi.DoctorCheck{panelTLSCheck(p, now)}, nil
}

func panelTLSCheck(p PanelTLS, now time.Time) agentapi.DoctorCheck {
	const id = "panel.tls"

	c := agentapi.DoctorCheck{ID: id, Status: agentapi.DoctorOK}

	if p.NotAfter.IsZero() {
		c.Status, c.Summary, c.Hint = agentapi.DoctorFail, "no certificate for "+p.Domain+": "+firstErrLine(p.Error), panelTLSHint

		return c
	}

	left := p.NotAfter.Sub(now)
	days := int(left.Hours() / 24)
	until := p.NotAfter.UTC().Format("2006-01-02")
	c.Summary = fmt.Sprintf("certificate for %s valid until %s (%d days)", p.Domain, until, days)

	switch {
	case left <= 0:
		c.Status, c.Summary, c.Hint = agentapi.DoctorFail, "certificate for "+p.Domain+" expired on "+until, panelTLSHint
	case days < doctor.CertFailDays:
		c.Status, c.Hint = agentapi.DoctorFail, panelTLSHint
	case days < doctor.CertWarnDays:
		c.Status, c.Hint = agentapi.DoctorWarn, panelTLSHint
	case p.Error != "":
		c.Status, c.Summary, c.Hint = agentapi.DoctorWarn, c.Summary+"; the last renewal failed: "+firstErrLine(p.Error), panelTLSHint
	}

	return c
}

func firstErrLine(s string) string {
	s, _, _ = strings.Cut(strings.TrimSpace(s), "\n")

	return s
}
