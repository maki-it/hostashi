// SPDX-License-Identifier: AGPL-3.0-or-later

package hosting

import (
	"context"
	"errors"
	"fmt"
	"strconv"

	"github.com/maki-it/hostashi/internal/agentapi"
	"github.com/maki-it/hostashi/internal/store"
)

// Own certificates of parked domains (ADR-0039), as for sites in owncert.go.
// A parked domain has one fixed name, so the certificate is checked once.

// UploadParkedCert makes an own certificate the live one of a parked domain
// and turns HTTPS on with it.
func (s *Service) UploadParkedCert(ctx context.Context, actor int64, domain string, in CertUpload) (store.Parked, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	p, err := s.ParkedDomain(ctx, domain)
	if err != nil {
		return store.Parked{}, err
	}

	info, err := s.ag.TLSUpload(ctx, agentapi.TLSUploadRequest{
		User: agentapi.ParkName(p.ID), Chain: in.Chain, Key: in.Key, Domains: []string{p.Domain}, AllowUntrusted: in.AllowUntrusted,
	})
	if err != nil {
		return store.Parked{}, err
	}

	if err := s.st.SetParkedCustomCert(ctx, p.ID, info.NotAfter, info.Trusted, s.now()); err != nil {
		return store.Parked{}, err
	}

	how := "upload"
	if in.Key == "" {
		how = "csr"
	}

	s.audit(ctx, actor, "parked.cert.upload", p.Domain, map[string]string{
		"fingerprint": info.Fingerprint, "issuer": info.Issuer, "trusted": strconv.FormatBool(info.Trusted), "via": how,
	})

	if err := s.applyWeb(ctx); err != nil {
		return store.Parked{}, errors.Join(errors.New("certificate stored, but the web server change failed"), err)
	}

	return s.st.ParkedDomain(ctx, p.Domain)
}

// ParkedCertCSR creates a key on the server and a CSR for a parked domain.
func (s *Service) ParkedCertCSR(ctx context.Context, actor int64, domain string) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	p, err := s.ParkedDomain(ctx, domain)
	if err != nil {
		return "", err
	}

	csr, err := s.ag.TLSCustomCSR(ctx, agentapi.ParkName(p.ID), []string{p.Domain})
	if err != nil {
		return "", err
	}

	s.audit(ctx, actor, "parked.cert.csr", p.Domain, nil)

	return csr, nil
}

// ParkedCertDetails returns the live certificate and a pending CSR of a
// parked domain.
func (s *Service) ParkedCertDetails(ctx context.Context, domain string) (agentapi.TLSInfoResponse, error) {
	p, err := s.ParkedDomain(ctx, domain)
	if err != nil {
		return agentapi.TLSInfoResponse{}, err
	}

	return s.ag.TLSInfo(ctx, agentapi.ParkName(p.ID))
}

// SetParkedTLSSource chooses where the certificate of a parked domain comes
// from; the live one stays until a new one is installed.
func (s *Service) SetParkedTLSSource(ctx context.Context, actor int64, domain, source string) (store.Parked, error) {
	if source != store.TLSLetsEncrypt && source != store.TLSCustom {
		return store.Parked{}, fmt.Errorf("%w: source must be %q or %q", ErrInvalid, store.TLSLetsEncrypt, store.TLSCustom)
	}

	if source == store.TLSLetsEncrypt && s.issuer == nil {
		return store.Parked{}, ErrNoTLS
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	p, err := s.ParkedDomain(ctx, domain)
	if err != nil {
		return store.Parked{}, err
	}

	if err := s.st.SetParkedTLSSource(ctx, p.ID, source, s.now()); err != nil {
		return store.Parked{}, err
	}

	if p.TLSSource != source {
		s.audit(ctx, actor, "parked.cert.source", p.Domain, map[string]string{"source": source})
	}

	if source == store.TLSLetsEncrypt {
		s.Kick()
	}

	return s.st.ParkedDomain(ctx, p.Domain)
}

// RenewParkedNow asks for a new Let's Encrypt certificate of a parked
// domain at once, at most once per RenewGap.
func (s *Service) RenewParkedNow(ctx context.Context, actor int64, domain string) (store.Parked, error) {
	if s.issuer == nil {
		return store.Parked{}, ErrNoTLS
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	p, err := s.ParkedDomain(ctx, domain)
	if err != nil {
		return store.Parked{}, err
	}

	if !p.TLS || p.TLSSource != store.TLSLetsEncrypt {
		return store.Parked{}, ErrNotLetsEncrypt
	}

	if err := s.st.RequestParkedRenew(ctx, p.ID, RenewGap, s.now()); err != nil {
		if errors.Is(err, store.ErrTooSoon) {
			return store.Parked{}, ErrTooSoon
		}

		return store.Parked{}, err
	}

	s.audit(ctx, actor, "parked.cert.renew", p.Domain, nil)
	s.Kick()

	return s.st.ParkedDomain(ctx, p.Domain)
}

// DeleteParkedCert removes the certificate of a parked domain with HTTPS
// off.
func (s *Service) DeleteParkedCert(ctx context.Context, actor int64, domain string) (store.Parked, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	p, err := s.ParkedDomain(ctx, domain)
	if err != nil {
		return store.Parked{}, err
	}

	if p.TLS || p.TLSLive {
		return store.Parked{}, ErrCertInUse
	}

	if err := s.ag.TLSDelete(ctx, agentapi.ParkName(p.ID)); err != nil {
		return store.Parked{}, err
	}

	if err := s.st.ClearParkedCert(ctx, p.ID, s.now()); err != nil {
		return store.Parked{}, err
	}

	s.audit(ctx, actor, "parked.cert.delete", p.Domain, nil)

	return s.st.ParkedDomain(ctx, p.Domain)
}

// parkRenewDue reports whether the renewal loop should get a Let's Encrypt
// certificate for a parked domain now.
func (s *Service) parkRenewDue(p store.Parked) bool {
	if !p.TLS || p.TLSSource != store.TLSLetsEncrypt {
		return false
	}

	if p.TLSRenewForced {
		return true
	}

	if p.TLSCertSource != store.TLSLetsEncrypt || !s.parkCovers(p, RenewBefore) {
		return p.TLSFailedAt.IsZero() || !s.now().Before(p.TLSFailedAt.Add(RetryAfter))
	}

	return false
}
