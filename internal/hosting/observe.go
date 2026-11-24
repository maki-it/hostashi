// SPDX-License-Identifier: AGPL-3.0-or-later

package hosting

import (
	"context"
	"time"

	"github.com/maki-it/hostashi/internal/agentapi"
	"github.com/maki-it/hostashi/internal/store"
)

// ObserveApply calls fn after every web.apply with whether the agent
// accepted the state and, if it did, the live generation. Set it before
// the first change.
func (s *Service) ObserveApply(fn func(ok bool, generation int)) { s.onApply = fn }

// ObserveFailedDeploys calls fn with the site and the run after a Git
// deploy failed (ADR-0062). Set it before RunDeploys.
func (s *Service) ObserveFailedDeploys(fn func(ctx context.Context, site string, d store.SiteDeploy)) {
	s.onDeployFailed = fn
}

// CertExpiry is the end of one live certificate. Cert is the agent's
// certificate name: the site's system name, or agentapi.PanelCert for the
// panel's own address. Site is empty for the panel.
type CertExpiry struct {
	Site     string
	Cert     string
	NotAfter time.Time
}

// CertExpiries lists the certificates the panel knows, for metrics.
func (s *Service) CertExpiries(ctx context.Context) ([]CertExpiry, error) {
	sites, err := s.st.Sites(ctx)
	if err != nil {
		return nil, err
	}

	out := []CertExpiry{}

	for _, site := range sites {
		if !site.TLSNotAfter.IsZero() {
			out = append(out, CertExpiry{Site: site.Name, Cert: site.Name, NotAfter: site.TLSNotAfter})
		}
	}

	if s.panelDomain != "" {
		p, err := s.PanelTLS(ctx)
		if err != nil {
			return nil, err
		}

		if !p.NotAfter.IsZero() {
			out = append(out, CertExpiry{Cert: agentapi.PanelCert, NotAfter: p.NotAfter})
		}
	}

	return out, nil
}
