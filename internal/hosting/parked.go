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

	if tls && s.issuer == nil {
		return store.Parked{}, ErrNoTLS
	}

	ref := int64(0)

	switch page {
	case "", statuspage.BuiltinParking:
		page = statuspage.BuiltinParking
	default:
		n, ok := parsePageID(page)
		if !ok {
			return store.Parked{}, ErrPageNotFound
		}

		ref = n
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	if err := s.checkReserved([]string{domain}); err != nil {
		return store.Parked{}, err
	}

	if ref != 0 {
		p, err := s.st.StatusPageByID(ctx, ref)
		if errors.Is(err, store.ErrNotFound) {
			return store.Parked{}, ErrPageNotFound
		}

		if err != nil {
			return store.Parked{}, err
		}

		if p.Kind != statuspage.KindParking {
			return store.Parked{}, fmt.Errorf("%w: %q is a maintenance page, choose a parking page", ErrInvalid, p.Name)
		}
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

		if !p.TLS || s.parkCovers(p, RenewBefore) || (!p.TLSFailedAt.IsZero() && s.now().Before(p.TLSFailedAt.Add(RetryAfter))) {
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
