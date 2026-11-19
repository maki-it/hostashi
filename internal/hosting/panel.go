// SPDX-License-Identifier: AGPL-3.0-or-later

package hosting

import (
	"context"
	"encoding/json"
	"errors"
	"slices"
	"strings"
	"time"

	"github.com/maki-it/hostashi/internal/agentapi"
)

// panelTLSKey is the settings key of the panel certificate's state.
const panelTLSKey = "panel.tls"

// PanelTLS is the state of the panel's own certificate (ADR-0023).
type PanelTLS struct {
	Domain   string    `json:"domain"`
	Domains  []string  `json:"domains"`  // names of the live certificate
	NotAfter time.Time `json:"notAfter"` // zero: none
	Error    string    `json:"error,omitempty"`
	FailedAt time.Time `json:"failedAt,omitzero"`
	// Live: the last accepted web state served the panel over HTTPS.
	Live bool `json:"live"`
}

// EnablePanel serves the panel's own address through nginx (ADR-0023) and
// gets a certificate for it once an issuer is set. domain is the host name
// of the panel's public URL.
func (s *Service) EnablePanel(domain string) error {
	if err := agentapi.ValidateDomain(domain); err != nil {
		return err
	}

	s.panelDomain = domain

	return nil
}

// PanelTLS returns the panel certificate's state.
func (s *Service) PanelTLS(ctx context.Context) (PanelTLS, error) {
	v, ok, err := s.st.Setting(ctx, panelTLSKey)
	if err != nil || !ok {
		return PanelTLS{Domain: s.panelDomain, Domains: []string{}}, err
	}

	var p PanelTLS
	if err := json.Unmarshal([]byte(v), &p); err != nil {
		return PanelTLS{}, err
	}

	// A changed panel address starts over.
	if p.Domain != s.panelDomain {
		return PanelTLS{Domain: s.panelDomain, Domains: []string{}}, nil
	}

	if p.Domains == nil {
		p.Domains = []string{}
	}

	return p, nil
}

func (s *Service) savePanelTLS(ctx context.Context, p PanelTLS) error {
	p.Domain = s.panelDomain

	b, err := json.Marshal(p)
	if err != nil {
		return err
	}

	return s.st.SetSetting(ctx, panelTLSKey, string(b))
}

// panelCovers reports whether the panel's certificate names its domain and is
// valid for at least margin.
func (s *Service) panelCovers(p PanelTLS, margin time.Duration) bool {
	return !p.NotAfter.IsZero() && s.now().Add(margin).Before(p.NotAfter) && slices.Contains(p.Domains, s.panelDomain)
}

// panelWeb is the panel part of web.apply, nil without a panel address.
func (s *Service) panelWeb(ctx context.Context) (*agentapi.WebPanel, error) {
	if s.panelDomain == "" {
		return nil, nil //nolint:nilnil // no panel address
	}

	p, err := s.PanelTLS(ctx)
	if err != nil {
		return nil, err
	}

	return &agentapi.WebPanel{Domain: s.panelDomain, TLS: s.panelCovers(p, 0)}, nil
}

// panelApplied records whether the accepted web state serves the panel over
// HTTPS.
func (s *Service) panelApplied(ctx context.Context, panel *agentapi.WebPanel) {
	if panel == nil {
		s.panelServed = ""

		return
	}

	s.panelServed = panel.Domain

	p, err := s.PanelTLS(ctx)
	if err == nil && p.Live != panel.TLS {
		p.Live = panel.TLS
		err = s.savePanelTLS(ctx, p)
	}

	if err != nil {
		s.log.ErrorContext(ctx, "record panel https state", "err", err)
	}
}

// renewPanel gets or renews the panel's certificate when it is due.
func (s *Service) renewPanel(ctx context.Context) {
	if s.panelDomain == "" {
		return
	}

	p, err := s.PanelTLS(ctx)
	if err != nil {
		s.log.ErrorContext(ctx, "panel certificate check", "err", err)

		return
	}

	if s.panelCovers(p, RenewBefore) || (!p.FailedAt.IsZero() && s.now().Before(p.FailedAt.Add(RetryAfter))) {
		return
	}

	// HTTP-01 needs nginx to answer for the panel name on port 80 first.
	s.mu.Lock()
	if s.panelServed != s.panelDomain {
		err = s.applyWeb(ctx)
	}
	s.mu.Unlock()

	var info agentapi.TLSCertInfo
	if err == nil {
		info, err = s.obtain(ctx, agentapi.PanelCert, []string{s.panelDomain})
	}

	if err == nil {
		p.Domains, p.NotAfter, p.Error, p.FailedAt = info.Domains, info.NotAfter, "", time.Time{}
		if err = s.savePanelTLS(ctx, p); err == nil {
			s.log.InfoContext(ctx, "panel certificate installed", "domain", s.panelDomain, "notAfter", info.NotAfter)

			s.mu.Lock()
			err = s.applyWeb(ctx)
			s.mu.Unlock()

			if err != nil {
				err = errors.Join(errors.New("certificate installed, but the web server change failed"), err)
			}
		}
	}

	if err == nil {
		return
	}

	s.log.WarnContext(ctx, "panel certificate failed", "domain", s.panelDomain, "err", err)

	p, rerr := s.PanelTLS(context.WithoutCancel(ctx))
	if rerr == nil {
		msg := err.Error()
		if len(msg) > 1000 {
			msg = strings.ToValidUTF8(msg[:1000], "")
		}

		p.Error, p.FailedAt = msg, s.now()
		rerr = s.savePanelTLS(context.WithoutCancel(ctx), p)
	}

	if rerr != nil {
		s.log.ErrorContext(ctx, "record panel certificate failure", "err", rerr)
	}
}
