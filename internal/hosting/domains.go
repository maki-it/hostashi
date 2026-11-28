// SPDX-License-Identifier: AGPL-3.0-or-later

package hosting

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"

	"github.com/maki-it/hostashi/internal/agentapi"
	"github.com/maki-it/hostashi/internal/store"
)

// DomainsRequest changes the domains of a site (ADR-0068).
type DomainsRequest struct {
	// Domains are the served domains, the primary one first.
	Domains []string
	// RedirectDomains answer with 301 to the primary domain; nil keeps
	// the current ones, except those that are served now.
	RedirectDomains []string
	// KeepRedirect: RedirectDomains is nil and the current ones stay.
	KeepRedirect bool
	// Now changes at once, even when the site would be served over HTTP
	// until a certificate for the new names is there.
	Now bool
}

// SetDomains replaces the served domains of an active site and keeps its
// redirect domains. It waits for a certificate like ChangeDomains.
func (s *Service) SetDomains(ctx context.Context, actor int64, name string, in []string) (store.Site, error) {
	site, _, err := s.ChangeDomains(ctx, actor, name, DomainsRequest{Domains: in, KeepRedirect: true})

	return site, err
}

// normalizeDomainSet checks the served and redirect domains of a site of
// type typ together. Redirect domains are exact names.
func normalizeDomainSet(domains, redirect []string, typ string) (store.DomainSet, error) {
	d, err := normalizeSiteDomains(domains, typ)
	if err != nil {
		return store.DomainSet{}, err
	}

	r := []string{}

	if len(redirect) > 0 {
		if r, err = NormalizeDomains(redirect); err != nil {
			return store.DomainSet{}, err
		}
	}

	for _, x := range r {
		if slices.Contains(d, x) {
			return store.DomainSet{}, fmt.Errorf("%w: %s is a served domain and a redirect domain", ErrInvalid, x)
		}
	}

	if len(d)+len(r) > agentapi.MaxDomainsPerSite {
		return store.DomainSet{}, fmt.Errorf("%w: at most %d domains per site, redirect domains included", ErrInvalid, agentapi.MaxDomainsPerSite)
	}

	return store.DomainSet{Domains: d, Redirect: r}, nil
}

// ChangeDomains sets the served and redirect domains of an active site.
// When the site is live on HTTPS with Let's Encrypt and its certificate
// does not cover the new names, the change waits for a new certificate
// (ADR-0068): it is stored as pending and returned, and the site stays as
// it is until RenewDue switches domains and certificate together. With
// req.Now, an HTTP site, an own certificate or a certificate that covers
// the names, it goes live at once and any pending change is dropped.
func (s *Service) ChangeDomains(ctx context.Context, actor int64, name string, req DomainsRequest) (store.Site, *store.DomainChange, error) {
	if len(req.Domains) == 0 || len(req.Domains) > agentapi.MaxDomainsPerSite {
		return store.Site{}, nil, fmt.Errorf("%w: between 1 and %d domains required", ErrInvalid, agentapi.MaxDomainsPerSite)
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	site, err := s.Get(ctx, name)
	if err != nil {
		return store.Site{}, nil, err
	}

	redirect := req.RedirectDomains
	if req.KeepRedirect {
		redirect = nil

		for _, d := range site.RedirectDomains {
			if !slices.Contains(lowerAll(req.Domains), d) {
				redirect = append(redirect, d)
			}
		}
	}

	set, err := normalizeDomainSet(req.Domains, redirect, site.Type)
	if err != nil {
		return store.Site{}, nil, err
	}

	all := set.All()

	if err := s.checkReserved(all); err != nil {
		return store.Site{}, nil, err
	}

	if site.State != store.SiteActive {
		return store.Site{}, nil, ErrState
	}

	if len(set.Redirect) > 0 && site.Type == store.SiteTypeRedirect {
		return store.Site{}, nil, fmt.Errorf("%w: a redirect site sends every domain to its target; it has no redirect domains", ErrInvalid)
	}

	if err := checkOwnCertDomains(site, all); err != nil {
		return store.Site{}, nil, err
	}

	if err := checkWildcardTLS(site.TLS, site.TLSSource, site.TLSChallenge, all); err != nil {
		return store.Site{}, nil, err
	}

	// DNS-01 needs an account for every new name (ADR-0076).
	if site.TLSChallenge == store.ChallengeDNS {
		if _, err := s.dnsAccountsFor(ctx, all); err != nil {
			return store.Site{}, nil, err
		}
	}

	// A redirect or proxy must not point to one of the new domains
	// (ADR-0054).
	if r := site.Redirect; r != nil && site.Type == store.SiteTypeRedirect {
		if _, err := checkRedirect(*r, all); err != nil {
			return store.Site{}, nil, err
		}

		if err := s.checkRedirectLoop(ctx, name, *r, all); err != nil {
			return store.Site{}, nil, err
		}
	}

	if p := site.Proxy; p != nil && site.Type == store.SiteTypeProxy {
		if _, err := agentapi.ParseProxyTarget(p.Target, all); err != nil {
			return store.Site{}, nil, fmt.Errorf("%w: %w", ErrInvalid, err)
		}
	}

	if !req.Now && s.waitsForCert(site, all) {
		if err := s.st.SetDomainChange(ctx, name, set, actor, s.now()); err != nil {
			if errors.Is(err, store.ErrDomainTaken) {
				return store.Site{}, nil, fmt.Errorf("%w: %w", ErrDomainTaken, err)
			}

			return store.Site{}, nil, err
		}

		s.audit(ctx, actor, "site.domains.pending", name, map[string]string{
			"domains": strings.Join(set.Domains, " "), "redirectDomains": strings.Join(set.Redirect, " "),
		})

		s.Kick()

		c, err := s.st.DomainChangeOf(ctx, name)
		if err != nil {
			return store.Site{}, nil, err
		}

		site.PendingDomains = &c

		return site, &c, nil
	}

	site, err = s.applyDomains(ctx, actor, site, set)
	if err != nil {
		return store.Site{}, nil, err
	}

	if _, err := s.st.DeleteDomainChange(ctx, name); err != nil {
		s.log.ErrorContext(ctx, "drop pending domain change", "site", name, "err", err)
	}

	site.PendingDomains = nil

	return site, nil, nil
}

func lowerAll(in []string) []string {
	out := make([]string, len(in))
	for i, d := range in {
		out[i] = strings.ToLower(strings.TrimSpace(d))
	}

	return out
}

// waitsForCert reports whether a change to names would drop the site
// from HTTPS: it is live on HTTPS with a Let's Encrypt certificate that
// does not name all of them.
func (s *Service) waitsForCert(site store.Site, names []string) bool {
	return s.issuer != nil && site.TLS && site.TLSLive && site.TLSSource == store.TLSLetsEncrypt &&
		len(agentapi.MissingNames(site.TLSDomains, names)) > 0
}

// applyDomains stores set and applies the web state; if that fails, the
// previous domains are stored again. The caller holds s.mu.
func (s *Service) applyDomains(ctx context.Context, actor int64, site store.Site, set store.DomainSet) (store.Site, error) {
	parked, err := s.parkedAmong(ctx, set.All())
	if err != nil {
		return store.Site{}, err
	}

	old, err := s.st.ReplaceSiteDomainSet(ctx, site.Name, set, s.now())
	if errors.Is(err, store.ErrDomainTaken) {
		return store.Site{}, fmt.Errorf("%w: %w", ErrDomainTaken, err)
	}

	if err != nil {
		return store.Site{}, err
	}

	if err := s.applyWeb(ctx); err != nil {
		if _, rerr := s.st.ReplaceSiteDomainSet(context.WithoutCancel(ctx), site.Name, old, s.now()); rerr != nil {
			s.log.ErrorContext(ctx, "restore domains after failed change", "site", site.Name, "err", rerr)
		} else {
			s.parkAgain(context.WithoutCancel(ctx), actor, site.Name, parked)
		}

		return store.Site{}, err
	}

	s.auditDomains(ctx, actor, site.Name, old, set)

	site.Domains, site.RedirectDomains = set.Domains, set.Redirect

	// A certificate for the old names no longer covers the site: the site
	// is served over HTTP until the new one is there.
	if site.TLS && site.TLSSource == store.TLSLetsEncrypt {
		s.Kick()
	}

	return site, nil
}

func (s *Service) auditDomains(ctx context.Context, actor int64, name string, old, set store.DomainSet) {
	view := func(d store.DomainSet) map[string]string {
		m := map[string]string{"domains": strings.Join(d.Domains, " ")}
		if len(d.Redirect) > 0 {
			m["redirectDomains"] = strings.Join(d.Redirect, " ")
		}

		return m
	}

	s.auditChange(ctx, actor, "site.domains", name, view(set), view(old), view(set))
}

// DomainChange returns the pending domain change of a site, or nil.
func (s *Service) DomainChange(ctx context.Context, name string) (*store.DomainChange, error) {
	c, err := s.st.DomainChangeOf(ctx, name)
	if errors.Is(err, store.ErrNotFound) {
		return nil, nil
	}

	if err != nil {
		return nil, err
	}

	return &c, nil
}

// DomainChanges returns every pending domain change by site name.
func (s *Service) DomainChanges(ctx context.Context) (map[string]store.DomainChange, error) {
	list, err := s.st.DomainChanges(ctx)
	if err != nil {
		return nil, err
	}

	out := make(map[string]store.DomainChange, len(list))
	for _, c := range list {
		out[c.Site] = c
	}

	return out, nil
}

// CancelDomainChange drops the pending domain change of a site;
// ErrNotFound when there is none.
func (s *Service) CancelDomainChange(ctx context.Context, actor int64, name string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if _, err := s.Get(ctx, name); err != nil {
		return err
	}

	ok, err := s.st.DeleteDomainChange(ctx, name)
	if err != nil {
		return err
	}

	if !ok {
		return ErrNotFound
	}

	s.audit(ctx, actor, "site.domains.cancel", name, nil)

	return nil
}

// finishDomainChanges gets the certificate for each pending domain change
// whose last attempt is older than RetryAfter and then switches domains
// and certificate in one web apply (ADR-0068). A failure stays on the
// change; the site is not touched.
func (s *Service) finishDomainChanges(ctx context.Context) {
	changes, err := s.st.DomainChanges(ctx)
	if err != nil {
		s.log.ErrorContext(ctx, "pending domain changes", "err", err)

		return
	}

	for _, c := range changes {
		if ctx.Err() != nil {
			return
		}

		if !c.FailedAt.IsZero() && s.now().Before(c.FailedAt.Add(RetryAfter)) {
			continue
		}

		if err := s.finishDomainChange(ctx, c); err != nil {
			s.log.WarnContext(ctx, "domain change failed", "site", c.Site, "err", err)

			if rerr := s.st.SetDomainChangeError(context.WithoutCancel(ctx), c.Site, err.Error(), s.now()); rerr != nil {
				s.log.ErrorContext(ctx, "record domain change failure", "site", c.Site, "err", rerr)
			}
		}
	}
}

func (s *Service) finishDomainChange(ctx context.Context, c store.DomainChange) error {
	site, err := s.Get(ctx, c.Site)
	if err != nil {
		return err
	}

	// A site that no longer needs a certificate first (HTTPS off, own
	// certificate, or covered by now) changes at once.
	if !s.waitsForCert(site, c.All()) {
		s.mu.Lock()
		defer s.mu.Unlock()

		if current, err := s.changeStill(ctx, c); !current || err != nil {
			return err
		}

		if _, err := s.applyDomains(ctx, c.RequestedBy, site, c.DomainSet); err != nil {
			return err
		}

		_, err := s.st.DeleteDomainChange(ctx, c.Site)

		return err
	}

	ictx, cancel := context.WithTimeout(ctx, issueTimeoutFor(site.TLSChallenge))
	defer cancel()

	// The key and the chain first; the live certificate is replaced only
	// together with the domains, so the agent never sees a certificate
	// that misses names of the web state it serves.
	csr, err := s.ag.TLSCSR(ictx, site.Name, c.All())
	if err != nil {
		return err
	}

	chain, err := s.obtainChain(ictx, []byte(csr), c.All(), site.TLSChallenge)
	if err != nil {
		return err
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	if current, err := s.changeStill(ctx, c); !current || err != nil {
		return err
	}

	info, err := s.ag.TLSInstall(ictx, site.Name, string(chain))
	if err != nil {
		return err
	}

	parked, err := s.parkedAmong(ctx, c.All())
	if err != nil {
		return err
	}

	old, err := s.st.FinishDomainChange(ctx, site.Name, c.DomainSet, info.Domains, info.NotAfter, s.now())
	if err != nil {
		// The live certificate names only the new domains now: record it,
		// so the site falls back to HTTP instead of a web state the agent
		// refuses, and the renewal loop gets a certificate again.
		s.recordCert(ctx, site.Name, info)

		return err
	}

	if err := s.applyWeb(ctx); err != nil {
		if _, rerr := s.st.ReplaceSiteDomainSet(context.WithoutCancel(ctx), site.Name, old, s.now()); rerr != nil {
			s.log.ErrorContext(ctx, "restore domains after failed change", "site", site.Name, "err", rerr)
		} else {
			s.parkAgain(context.WithoutCancel(ctx), c.RequestedBy, site.Name, parked)
		}

		s.Kick()

		return errors.Join(errors.New("certificate installed, but the web server change failed; the old domains are back"), err)
	}

	s.log.InfoContext(ctx, "domain change done", "site", site.Name, "domains", c.Domains, "redirectDomains", c.Redirect)
	s.auditDomains(ctx, c.RequestedBy, site.Name, old, c.DomainSet)

	return nil
}

func (s *Service) recordCert(ctx context.Context, name string, info agentapi.TLSCertInfo) {
	if err := s.st.SetSiteCert(context.WithoutCancel(ctx), name, info.Domains, info.NotAfter, s.now()); err != nil {
		s.log.ErrorContext(ctx, "record certificate after failed domain change", "site", name, "err", err)
	}

	s.Kick()
}

// changeStill reports whether c is still the pending change of its site;
// a replaced or canceled one is no error, the new request starts over.
func (s *Service) changeStill(ctx context.Context, c store.DomainChange) (bool, error) {
	cur, err := s.st.DomainChangeOf(ctx, c.Site)
	if errors.Is(err, store.ErrNotFound) {
		return false, nil
	}

	if err != nil {
		return false, err
	}

	return sameSet(cur.DomainSet, c.DomainSet), nil
}

func sameSet(a, b store.DomainSet) bool {
	return slices.Equal(a.Domains, b.Domains) && slices.Equal(a.Redirect, b.Redirect)
}
