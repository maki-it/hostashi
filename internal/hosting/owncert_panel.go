// SPDX-License-Identifier: AGPL-3.0-or-later

package hosting

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"time"

	"github.com/maki-it/hostashi/internal/agentapi"
	"github.com/maki-it/hostashi/internal/store"
)

// Own certificate of the panel's address (ADR-0039). It must cover the
// panel host name. There is no delete: going back to Let's Encrypt keeps
// the own certificate live until the new one is there, so the panel is
// never without a certificate.

// ErrNoPanel means the panel has no address of its own.
var ErrNoPanel = errors.New("hosting: the panel has no address of its own")

// UploadPanelCert makes an own certificate the panel's live one.
func (s *Service) UploadPanelCert(ctx context.Context, actor int64, in CertUpload) (PanelTLS, error) {
	if s.panelDomain == "" {
		return PanelTLS{}, ErrNoPanel
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	p, err := s.PanelTLS(ctx)
	if err != nil {
		return PanelTLS{}, err
	}

	info, err := s.ag.TLSUpload(ctx, agentapi.TLSUploadRequest{
		User: agentapi.PanelCert, Chain: in.Chain, Key: in.Key, Domains: []string{s.panelDomain}, AllowUntrusted: in.AllowUntrusted,
	})
	if err != nil {
		return PanelTLS{}, err
	}

	p.Domains, p.NotAfter, p.Error, p.FailedAt = info.Domains, info.NotAfter, "", time.Time{}
	p.Source, p.CertSource, p.Untrusted, p.RenewForced = store.TLSCustom, store.TLSCustom, !info.Trusted, false

	if err := s.savePanelTLS(ctx, p); err != nil {
		return PanelTLS{}, err
	}

	how := "upload"
	if in.Key == "" {
		how = "csr"
	}

	s.audit(ctx, actor, "panel.cert.upload", s.panelDomain, map[string]string{
		"fingerprint": info.Fingerprint, "issuer": info.Issuer, "trusted": strconv.FormatBool(info.Trusted), "via": how,
	})

	if err := s.applyWeb(ctx); err != nil {
		return PanelTLS{}, errors.Join(errors.New("certificate stored, but the web server change failed"), err)
	}

	return s.PanelTLS(ctx)
}

// PanelCertCSR creates a key on the server and a CSR for the panel address.
func (s *Service) PanelCertCSR(ctx context.Context, actor int64) (string, error) {
	if s.panelDomain == "" {
		return "", ErrNoPanel
	}

	csr, err := s.ag.TLSCustomCSR(ctx, agentapi.PanelCert, []string{s.panelDomain})
	if err != nil {
		return "", err
	}

	s.audit(ctx, actor, "panel.cert.csr", s.panelDomain, nil)

	return csr, nil
}

// PanelCertDetails returns the panel's live certificate and a pending CSR.
func (s *Service) PanelCertDetails(ctx context.Context) (agentapi.TLSInfoResponse, error) {
	if s.panelDomain == "" {
		return agentapi.TLSInfoResponse{}, ErrNoPanel
	}

	return s.ag.TLSInfo(ctx, agentapi.PanelCert)
}

// SetPanelTLSSource chooses where the panel's certificate comes from.
func (s *Service) SetPanelTLSSource(ctx context.Context, actor int64, source string) (PanelTLS, error) {
	if source != store.TLSLetsEncrypt && source != store.TLSCustom {
		return PanelTLS{}, fmt.Errorf("%w: source must be %q or %q", ErrInvalid, store.TLSLetsEncrypt, store.TLSCustom)
	}

	if s.panelDomain == "" {
		return PanelTLS{}, ErrNoPanel
	}

	if source == store.TLSLetsEncrypt && s.issuer == nil {
		return PanelTLS{}, ErrNoTLS
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	p, err := s.PanelTLS(ctx)
	if err != nil {
		return PanelTLS{}, err
	}

	if p.source() != source {
		s.audit(ctx, actor, "panel.cert.source", s.panelDomain, map[string]string{"source": source})
	}

	p.Source, p.Error, p.FailedAt = source, "", time.Time{}
	if err := s.savePanelTLS(ctx, p); err != nil {
		return PanelTLS{}, err
	}

	if source == store.TLSLetsEncrypt {
		s.Kick()
	}

	return p, nil
}

// RenewPanelNow asks for a new Let's Encrypt certificate of the panel at
// once, at most once per RenewGap.
func (s *Service) RenewPanelNow(ctx context.Context, actor int64) (PanelTLS, error) {
	if s.panelDomain == "" {
		return PanelTLS{}, ErrNoPanel
	}

	if s.issuer == nil {
		return PanelTLS{}, ErrNoTLS
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	p, err := s.PanelTLS(ctx)
	if err != nil {
		return PanelTLS{}, err
	}

	if p.source() != store.TLSLetsEncrypt {
		return PanelTLS{}, ErrNotLetsEncrypt
	}

	if !p.RenewAt.IsZero() && s.now().Before(p.RenewAt.Add(RenewGap)) {
		return PanelTLS{}, ErrTooSoon
	}

	p.RenewAt, p.RenewForced, p.Error, p.FailedAt = s.now(), true, "", time.Time{}
	if err := s.savePanelTLS(ctx, p); err != nil {
		return PanelTLS{}, err
	}

	s.audit(ctx, actor, "panel.cert.renew", s.panelDomain, nil)
	s.Kick()

	return p, nil
}

// panelRenewDue reports whether the renewal loop should get a Let's Encrypt
// certificate for the panel now.
func (s *Service) panelRenewDue(p PanelTLS) bool {
	if p.source() != store.TLSLetsEncrypt {
		return false
	}

	if p.RenewForced {
		return true
	}

	if p.certSource() != store.TLSLetsEncrypt || !s.panelCovers(p, RenewBefore) {
		return p.FailedAt.IsZero() || !s.now().Before(p.FailedAt.Add(RetryAfter))
	}

	return false
}
