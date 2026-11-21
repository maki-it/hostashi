// SPDX-License-Identifier: AGPL-3.0-or-later

package hosting

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/maki-it/hostashi/internal/agentapi"
	"github.com/maki-it/hostashi/internal/statuspage"
	"github.com/maki-it/hostashi/internal/store"
)

// ErrParkedNotFound means the domain is not parked.
var ErrParkedNotFound = errors.New("hosting: the domain is not parked")

// ParkedPageID is the page id of a parked domain.
func ParkedPageID(p store.Parked) string {
	if p.Page == 0 {
		return statuspage.BuiltinParking
	}

	return pageAgentID(p.Page)
}

// parkCovers reports whether a parked domain has a certificate valid for
// at least margin. Its domain never changes, so the name always fits.
func (s *Service) parkCovers(p store.Parked, margin time.Duration) bool {
	return !p.TLSNotAfter.IsZero() && s.now().Add(margin).Before(p.TLSNotAfter)
}

// ParkedTLSActive reports whether the domain is served over HTTPS now.
func (s *Service) ParkedTLSActive(p store.Parked) bool { return p.TLSLive }

// ParkedDomains returns the parked domains sorted by domain.
func (s *Service) ParkedDomains(ctx context.Context) ([]store.Parked, error) {
	return s.st.ParkedDomains(ctx)
}

// ParkedDomain returns one parked domain.
func (s *Service) ParkedDomain(ctx context.Context, domain string) (store.Parked, error) {
	p, err := s.st.ParkedDomain(ctx, strings.ToLower(domain))
	if errors.Is(err, store.ErrNotFound) {
		return store.Parked{}, ErrParkedNotFound
	}

	return p, err
}

// SetParked parks a domain or changes its page and HTTPS. page "" is the
// built-in parking page. With HTTPS the certificate comes in the
// background; until then the domain is served over HTTP.
func (s *Service) SetParked(ctx context.Context, actor int64, domain, page string, tls bool) (store.Parked, error) {
	domain = strings.ToLower(domain)
	if err := agentapi.ValidateDomain(domain); err != nil {
		return store.Parked{}, fmt.Errorf("%w: %w", ErrInvalid, err)
	}

	// An own certificate needs no ACME client.
	if tls && s.issuer == nil {
		if cur, err := s.st.ParkedDomain(ctx, domain); err != nil || cur.TLSSource != store.TLSCustom {
			return store.Parked{}, ErrNoTLS
		}
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	if err := s.checkReserved([]string{domain}); err != nil {
		return store.Parked{}, err
	}

	ref, page, err := s.parkingPage(ctx, page)
	if err != nil {
		return store.Parked{}, err
	}

	old, existed, err := s.st.PutParked(ctx, domain, ref, tls, s.now())

	switch {
	case errors.Is(err, store.ErrDomainTaken):
		return store.Parked{}, fmt.Errorf("%w: %s", ErrDomainTaken, domain)
	case errors.Is(err, store.ErrNotFound):
		return store.Parked{}, ErrPageNotFound
	case err != nil:
		return store.Parked{}, err
	}

	if err := s.applyWeb(ctx); err != nil {
		if rerr := s.st.RestoreParked(context.WithoutCancel(ctx), domain, old, existed, s.now()); rerr != nil {
			s.log.ErrorContext(ctx, "restore parked domain after failed change", "domain", domain, "err", rerr)
		}

		return store.Parked{}, err
	}

	s.audit(ctx, actor, "parked.set", domain, map[string]string{"page": page, "tls": strconv.FormatBool(tls)})

	if tls {
		s.Kick()
	}

	return s.st.ParkedDomain(ctx, domain)
}

// parkingPage resolves the page of a parked domain: "" or the built-in id
// is 0, anything else must be a stored parking page. It returns the store
// reference and the page id for the audit log.
func (s *Service) parkingPage(ctx context.Context, page string) (int64, string, error) {
	if page == "" || page == statuspage.BuiltinParking {
		return 0, statuspage.BuiltinParking, nil
	}

	ref, ok := parsePageID(page)
	if !ok {
		return 0, "", ErrPageNotFound
	}

	p, err := s.st.StatusPageByID(ctx, ref)
	if errors.Is(err, store.ErrNotFound) {
		return 0, "", ErrPageNotFound
	}

	if err != nil {
		return 0, "", err
	}

	if p.Kind != statuspage.KindParking {
		return 0, "", fmt.Errorf("%w: %q is a maintenance page, choose a parking page", ErrInvalid, p.Name)
	}

	return ref, page, nil
}

// DeleteParked ends the parking of a domain.
func (s *Service) DeleteParked(ctx context.Context, actor int64, domain string) error {
	domain = strings.ToLower(domain)

	s.mu.Lock()
	defer s.mu.Unlock()

	old, err := s.st.ParkedDomain(ctx, domain)
	if errors.Is(err, store.ErrNotFound) {
		return ErrParkedNotFound
	}

	if err != nil {
		return err
	}

	if err := s.st.DeleteParked(ctx, domain); err != nil {
		return err
	}

	if err := s.applyWeb(ctx); err != nil {
		if _, _, rerr := s.st.PutParked(context.WithoutCancel(ctx), domain, old.Page, old.TLS, s.now()); rerr != nil {
			s.log.ErrorContext(ctx, "restore parked domain after failed delete", "domain", domain, "err", rerr)
		}

		return err
	}

	s.audit(ctx, actor, "parked.delete", domain, nil)

	return nil
}

// renewParked gets or renews the certificates of parked domains that want
// HTTPS, one after the other.
func (s *Service) renewParked(ctx context.Context) {
	parked, err := s.st.ParkedDomains(ctx)
	if err != nil {
		s.log.ErrorContext(ctx, "parked certificate check", "err", err)

		return
	}

	for _, p := range parked {
		if ctx.Err() != nil {
			return
		}

		if !s.parkRenewDue(p) {
			continue
		}

		if err := s.issueParked(ctx, p); err != nil {
			s.log.WarnContext(ctx, "certificate of a parked domain failed", "domain", p.Domain, "err", err)

			if rerr := s.st.SetParkedTLSError(context.WithoutCancel(ctx), p.ID, err.Error(), s.now()); rerr != nil {
				s.log.ErrorContext(ctx, "record certificate failure", "domain", p.Domain, "err", rerr)
			}
		}
	}
}

func (s *Service) issueParked(ctx context.Context, p store.Parked) error {
	ctx, cancel := context.WithTimeout(ctx, issueTimeout)
	defer cancel()

	info, err := s.obtain(ctx, agentapi.ParkName(p.ID), []string{p.Domain})
	if err != nil {
		return err
	}

	if err := s.st.SetParkedCert(ctx, p.ID, info.NotAfter, s.now()); err != nil {
		return err
	}

	s.log.InfoContext(ctx, "certificate installed", "parked", p.Domain, "notAfter", info.NotAfter)

	s.mu.Lock()
	defer s.mu.Unlock()

	if err := s.applyWeb(ctx); err != nil {
		return errors.Join(errors.New("certificate installed, but the web server change failed"), err)
	}

	return nil
}

// ParkRequest says which domains of a site to park when the site is
// deleted (ADR-0048), and how.
type ParkRequest struct {
	Domains []string // at least one, all of them the site's
	Page    string   // "" is the built-in parking page
	TLS     bool
	Confirm string // the site's name, typed again
}

// DeleteAndPark deletes an active site and parks the chosen domains of it
// (ADR-0048). The site's vhost and the parked domains swap in one web
// state, so the domains are answered all the time. If that web state fails,
// nothing changed. If removing the site stops halfway, the site stays in
// state deleting with its domains parked, and Delete finishes the job.
func (s *Service) DeleteAndPark(ctx context.Context, actor int64, name string, req ParkRequest) error {
	if req.Confirm != name {
		return fmt.Errorf("%w: type the site's name to confirm", ErrInvalid)
	}

	if len(req.Domains) == 0 {
		return fmt.Errorf("%w: choose at least one domain to park", ErrInvalid)
	}

	if req.TLS && s.issuer == nil {
		return ErrNoTLS
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	site, err := s.Get(ctx, name)
	if err != nil {
		return err
	}

	if site.State != store.SiteActive {
		return ErrState
	}

	domains, err := chosenDomains(site, req.Domains)
	if err != nil {
		return err
	}

	ref, page, err := s.parkingPage(ctx, req.Page)
	if err != nil {
		return err
	}

	err = s.st.ParkSiteDomains(ctx, name, domains, ref, req.TLS, s.now())

	switch {
	case errors.Is(err, store.ErrNotFound):
		return ErrPageNotFound
	case err != nil:
		return err
	}

	if err := s.applyWeb(ctx); err != nil {
		if rerr := s.st.UnparkSiteDomains(context.WithoutCancel(ctx), name, domains, s.now()); rerr != nil {
			s.log.ErrorContext(ctx, "undo parking after failed delete", "site", name, "err", rerr)
		}

		return err
	}

	s.audit(ctx, actor, "site.park", name, map[string]string{
		"domains": strings.Join(domains, " "), "page": page, "tls": strconv.FormatBool(req.TLS),
	})

	if req.TLS {
		s.Kick()
	}

	if err := s.removeOfflineSite(ctx, actor, name); err != nil {
		return err
	}

	s.audit(ctx, actor, "site.delete", name, nil)

	return nil
}

// chosenDomains checks that every chosen domain is one of the site's, once,
// and returns them in the site's order.
func chosenDomains(site store.Site, in []string) ([]string, error) {
	want := make(map[string]bool, len(in))

	for _, d := range in {
		d = strings.ToLower(strings.TrimSpace(d))
		if want[d] {
			return nil, fmt.Errorf("%w: %s is chosen twice", ErrInvalid, d)
		}

		want[d] = true
	}

	out := make([]string, 0, len(want))

	for _, d := range site.Domains {
		if want[d] {
			out = append(out, d)
			delete(want, d)
		}
	}

	if len(want) > 0 {
		return nil, fmt.Errorf("%w: %s is not a domain of %s", ErrInvalid, slices.Sorted(maps.Keys(want))[0], site.Name)
	}

	return out, nil
}
