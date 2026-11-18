// SPDX-License-Identifier: AGPL-3.0-or-later

package hosting

import (
	"context"
	"errors"
	"slices"
	"time"

	"github.com/maki-it/hostashi/internal/agentapi"
	"github.com/maki-it/hostashi/internal/store"
)

const (
	// RenewBefore is how long before expiry a certificate is renewed.
	// Let's Encrypt certificates last 90 days (or less); a third left is
	// the usual point.
	RenewBefore = 30 * 24 * time.Hour
	// RetryAfter is the pause after a failed attempt per site. It keeps a
	// site with wrong DNS well below the CA's limit of failed validations.
	RetryAfter = time.Hour
	// tlsCheckEvery is how often the renewal loop looks at all sites.
	tlsCheckEvery = time.Hour
	// issueTimeout bounds one certificate: CSR, ACME order with HTTP-01
	// validation, install and web server switch.
	issueTimeout = 5 * time.Minute
)

// certCovers reports whether the live certificate of site names all its
// domains and is valid for at least margin.
func (s *Service) certCovers(site store.Site, margin time.Duration) bool {
	if site.TLSNotAfter.IsZero() || !s.now().Add(margin).Before(site.TLSNotAfter) {
		return false
	}

	for _, d := range site.Domains {
		if !slices.Contains(site.TLSDomains, d) {
			return false
		}
	}

	return true
}

// TLSActive reports whether site is served over HTTPS now: the last web
// state the agent accepted had HTTPS for it. Between storing a new
// certificate and the web server switch it is still false.
func (s *Service) TLSActive(site store.Site) bool { return site.TLSLive }

// Kick asks the renewal loop to look at all sites now.
func (s *Service) Kick() {
	select {
	case s.kick <- struct{}{}:
	default:
	}
}

// SetTLS turns HTTPS of an active site on or off. Turning it on returns at
// once: the certificate is obtained in the background (RunTLS), and the site
// switches to HTTPS when it is there. Turning it off goes live immediately.
func (s *Service) SetTLS(ctx context.Context, actor int64, name string, on bool) (store.Site, error) {
	if on && s.issuer == nil {
		return store.Site{}, ErrNoTLS
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	site, err := s.Get(ctx, name)
	if err != nil {
		return store.Site{}, err
	}

	if site.State != store.SiteActive {
		return store.Site{}, ErrState
	}

	old, err := s.st.SetSiteTLS(ctx, name, on, s.now())
	if err != nil {
		return store.Site{}, err
	}

	if err := s.applyWeb(ctx); err != nil {
		if _, rerr := s.st.SetSiteTLS(context.WithoutCancel(ctx), name, old, s.now()); rerr != nil {
			s.log.Error("restore tls setting after failed change", "site", name, "err", rerr)
		}

		return store.Site{}, err
	}

	action := "site.tls.off"
	if on {
		action = "site.tls.on"

		s.Kick()
	}

	s.audit(ctx, actor, action, name, nil)

	return s.Get(ctx, name)
}

// SetHSTS turns Strict-Transport-Security of an active site on or off. It
// takes effect while the site is served over HTTPS; it can be set before.
func (s *Service) SetHSTS(ctx context.Context, actor int64, name string, on bool) (store.Site, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	site, err := s.Get(ctx, name)
	if err != nil {
		return store.Site{}, err
	}

	if site.State != store.SiteActive {
		return store.Site{}, ErrState
	}

	old, err := s.st.SetSiteHSTS(ctx, name, on, s.now())
	if err != nil {
		return store.Site{}, err
	}

	if err := s.applyWeb(ctx); err != nil {
		if _, rerr := s.st.SetSiteHSTS(context.WithoutCancel(ctx), name, old, s.now()); rerr != nil {
			s.log.Error("restore hsts setting after failed change", "site", name, "err", rerr)
		}

		return store.Site{}, err
	}

	action := "site.hsts.off"
	if on {
		action = "site.hsts.on"
	}

	s.audit(ctx, actor, action, name, nil)

	return s.Get(ctx, name)
}

// obtain gets a certificate for a site or agentapi.PanelCert: a key and
// CSR from the agent, the chain from the CA, the pair live through the
// agent. The web server switch is the caller's.
func (s *Service) obtain(ctx context.Context, name string, domains []string) (agentapi.TLSCertInfo, error) {
	ctx, cancel := context.WithTimeout(ctx, issueTimeout)
	defer cancel()

	csr, err := s.ag.TLSCSR(ctx, name, domains)
	if err != nil {
		return agentapi.TLSCertInfo{}, err
	}

	chain, err := s.issuer.Obtain(ctx, []byte(csr))
	if err != nil {
		return agentapi.TLSCertInfo{}, err
	}

	return s.ag.TLSInstall(ctx, name, string(chain))
}

// RunTLS obtains and renews certificates until ctx ends: at start, every
// hour and after Kick.
func (s *Service) RunTLS(ctx context.Context) {
	t := time.NewTicker(tlsCheckEvery)
	defer t.Stop()

	for {
		s.RenewDue(ctx)

		select {
		case <-ctx.Done():
			return
		case <-t.C:
		case <-s.kick:
		}
	}
}

// RenewDue obtains a certificate for every active site that wants HTTPS and
// has none that covers its domains for RenewBefore, unless its last attempt
// failed less than RetryAfter ago. Sites are handled one after the other.
func (s *Service) RenewDue(ctx context.Context) {
	if s.issuer == nil {
		return
	}

	s.renewPanel(ctx)

	sites, err := s.st.Sites(ctx)
	if err != nil {
		s.log.Error("certificate check", "err", err)

		return
	}

	for _, site := range sites {
		if ctx.Err() != nil {
			return
		}

		if site.State != store.SiteActive || !site.TLS || s.certCovers(site, RenewBefore) {
			continue
		}

		if !site.TLSFailedAt.IsZero() && s.now().Before(site.TLSFailedAt.Add(RetryAfter)) {
			continue
		}

		if err := s.issue(ctx, site); err != nil {
			s.log.Warn("certificate failed", "site", site.Name, "err", err)

			if rerr := s.st.SetSiteTLSError(context.WithoutCancel(ctx), site.Name, err.Error(), s.now()); rerr != nil {
				s.log.Error("record certificate failure", "site", site.Name, "err", rerr)
			}
		}
	}
}

// issue gets one certificate: a key and CSR from the agent, the chain from
// the CA, the pair live through the agent, then HTTPS for the site.
func (s *Service) issue(ctx context.Context, site store.Site) error {
	ctx, cancel := context.WithTimeout(ctx, issueTimeout)
	defer cancel()

	info, err := s.obtain(ctx, site.Name, site.Domains)
	if err != nil {
		return err
	}

	if err := s.st.SetSiteCert(ctx, site.Name, info.Domains, info.NotAfter, s.now()); err != nil {
		return err
	}

	s.log.Info("certificate installed", "site", site.Name, "domains", info.Domains, "notAfter", info.NotAfter)

	s.mu.Lock()
	defer s.mu.Unlock()

	if err := s.applyWeb(ctx); err != nil {
		return errors.Join(errors.New("certificate installed, but the web server change failed"), err)
	}

	return nil
}
