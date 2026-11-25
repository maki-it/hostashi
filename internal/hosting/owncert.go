// SPDX-License-Identifier: AGPL-3.0-or-later

package hosting

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/maki-it/hostashi/internal/agentapi"
	"github.com/maki-it/hostashi/internal/store"
)

// Own certificates, "renew now" and deleting a certificate (ADR-0039).

// RenewGap is the shortest time between two "renew now" of a site. Let's
// Encrypt limits duplicate certificates for the same names.
const RenewGap = 10 * time.Minute

// Errors of certificate management.
var (
	// ErrTooSoon: "renew now" again within RenewGap.
	ErrTooSoon = errors.New("hosting: renewed less than 10 minutes ago")
	// ErrCertInUse: the certificate cannot be deleted while HTTPS is on.
	ErrCertInUse = errors.New("hosting: turn HTTPS off before deleting the certificate")
	// ErrNotLetsEncrypt: only a Let's Encrypt site with HTTPS on can renew.
	ErrNotLetsEncrypt = errors.New("hosting: only a site with HTTPS from Let's Encrypt can be renewed")
)

// CertUpload is an own certificate: Chain holds the leaf and the
// intermediates in PEM; Key is the PEM private key, empty for the key of
// the last CSR.
type CertUpload struct {
	Chain          string
	Key            string
	AllowUntrusted bool
}

// activeSite returns an active site; s.mu is held.
func (s *Service) activeSite(ctx context.Context, name string) (store.Site, error) {
	site, err := s.Get(ctx, name)
	if err != nil {
		return store.Site{}, err
	}

	if site.State != store.SiteActive {
		return store.Site{}, ErrState
	}

	return site, nil
}

// UploadCert makes an own certificate the live one of an active site and
// turns HTTPS on with it. The agent checks the certificate; the key is only
// passed on, never stored or logged here.
func (s *Service) UploadCert(ctx context.Context, actor int64, name string, in CertUpload) (store.Site, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	site, err := s.activeSite(ctx, name)
	if err != nil {
		return store.Site{}, err
	}

	info, err := s.ag.TLSUpload(ctx, agentapi.TLSUploadRequest{
		User: name, Chain: in.Chain, Key: in.Key, Domains: site.AllDomains(), AllowUntrusted: in.AllowUntrusted,
	})
	if err != nil {
		return store.Site{}, err
	}

	if err := s.st.SetSiteCustomCert(ctx, name, info.Domains, info.NotAfter, info.Trusted, s.now()); err != nil {
		return store.Site{}, err
	}

	how := "upload"
	if in.Key == "" {
		how = "csr"
	}

	s.audit(ctx, actor, "site.cert.upload", name, map[string]string{
		"fingerprint": info.Fingerprint, "issuer": info.Issuer, "trusted": strconv.FormatBool(info.Trusted), "via": how,
	})

	// The pair is live on disk already; nginx takes it with this apply.
	if err := s.applyWeb(ctx); err != nil {
		return store.Site{}, errors.Join(errors.New("certificate stored, but the web server change failed"), err)
	}

	return s.Get(ctx, name)
}

// CertCSR creates a key on the server and returns a CSR for the domains of
// an active site, for a certificate from another CA. Upload the issued
// certificate with UploadCert without a key.
func (s *Service) CertCSR(ctx context.Context, actor int64, name string) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	site, err := s.activeSite(ctx, name)
	if err != nil {
		return "", err
	}

	csr, err := s.ag.TLSCustomCSR(ctx, name, site.AllDomains())
	if err != nil {
		return "", err
	}

	s.audit(ctx, actor, "site.cert.csr", name, map[string]string{"domains": strings.Join(site.AllDomains(), " ")})

	return csr, nil
}

// CertDetails returns the live certificate of a site as the agent sees it
// and a pending CSR.
func (s *Service) CertDetails(ctx context.Context, name string) (agentapi.TLSInfoResponse, error) {
	if _, err := s.Get(ctx, name); err != nil {
		return agentapi.TLSInfoResponse{}, err
	}

	return s.ag.TLSInfo(ctx, name)
}

// SetTLSSource chooses where the certificate of an active site comes from.
// Switching to Let's Encrypt keeps the own certificate live until the new
// one is installed. Switching to custom only stops the renewals; upload a
// certificate to use one.
func (s *Service) SetTLSSource(ctx context.Context, actor int64, name, source string) (store.Site, error) {
	if source != store.TLSLetsEncrypt && source != store.TLSCustom {
		return store.Site{}, fmt.Errorf("%w: source must be %q or %q", ErrInvalid, store.TLSLetsEncrypt, store.TLSCustom)
	}

	if source == store.TLSLetsEncrypt && s.issuer == nil {
		return store.Site{}, ErrNoTLS
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	if _, err := s.activeSite(ctx, name); err != nil {
		return store.Site{}, err
	}

	old, err := s.st.SetSiteTLSSource(ctx, name, source, s.now())
	if err != nil {
		return store.Site{}, err
	}

	if old != source {
		s.auditChange(ctx, actor, "site.cert.source", name, map[string]string{"source": source},
			map[string]string{"source": old}, map[string]string{"source": source})
	}

	if source == store.TLSLetsEncrypt {
		s.Kick()
	}

	return s.Get(ctx, name)
}

// RenewNow asks for a new Let's Encrypt certificate at once, regardless of
// the expiry, at most once per RenewGap. It returns at once; the renewal
// loop does the order.
func (s *Service) RenewNow(ctx context.Context, actor int64, name string) (store.Site, error) {
	if s.issuer == nil {
		return store.Site{}, ErrNoTLS
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	site, err := s.activeSite(ctx, name)
	if err != nil {
		return store.Site{}, err
	}

	if !site.TLS || site.TLSSource != store.TLSLetsEncrypt {
		return store.Site{}, ErrNotLetsEncrypt
	}

	if err := s.st.RequestSiteRenew(ctx, name, RenewGap, s.now()); err != nil {
		if errors.Is(err, store.ErrTooSoon) {
			return store.Site{}, ErrTooSoon
		}

		return store.Site{}, err
	}

	s.audit(ctx, actor, "site.cert.renew", name, nil)
	s.Kick()

	return s.Get(ctx, name)
}

// DeleteCert removes the certificate of an active site. HTTPS must be off
// and no longer served, so a site never serves HTTPS without one.
func (s *Service) DeleteCert(ctx context.Context, actor int64, name string) (store.Site, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	site, err := s.activeSite(ctx, name)
	if err != nil {
		return store.Site{}, err
	}

	if site.TLS || site.TLSLive {
		return store.Site{}, ErrCertInUse
	}

	if err := s.ag.TLSDelete(ctx, name); err != nil {
		return store.Site{}, err
	}

	if err := s.st.ClearSiteCert(ctx, name, s.now()); err != nil {
		return store.Site{}, err
	}

	s.audit(ctx, actor, "site.cert.delete", name, nil)

	return s.Get(ctx, name)
}

// renewDue reports whether the renewal loop should get a Let's Encrypt
// certificate for site now.
func (s *Service) renewDue(site store.Site) bool {
	if site.State != store.SiteActive || !site.TLS || site.TLSSource != store.TLSLetsEncrypt {
		return false
	}

	if site.TLSRenewForced {
		return true
	}

	// An own certificate stays live until the first one from Let's Encrypt.
	if site.TLSCertSource != store.TLSLetsEncrypt || !s.certCovers(site, RenewBefore) {
		return site.TLSFailedAt.IsZero() || !s.now().Before(site.TLSFailedAt.Add(RetryAfter))
	}

	return false
}

// checkOwnCertDomains refuses new domains that the own certificate of a
// site with HTTPS does not cover: nothing would renew it.
func checkOwnCertDomains(site store.Site, domains []string) error {
	if !site.TLS || site.TLSCertSource != store.TLSCustom || site.TLSSource != store.TLSCustom {
		return nil
	}

	if missing := agentapi.MissingNames(site.TLSDomains, domains); len(missing) > 0 {
		return fmt.Errorf("%w: the own certificate does not cover %s; upload a new one first", ErrInvalid, strings.Join(missing, ", "))
	}

	return nil
}
