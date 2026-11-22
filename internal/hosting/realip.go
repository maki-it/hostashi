// SPDX-License-Identifier: AGPL-3.0-or-later

package hosting

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/netip"
	"slices"

	"github.com/maki-it/hostashi/internal/agentapi"
	"github.com/maki-it/hostashi/internal/store"
)

// ErrRealIPRanges is a change that adds own proxy ranges without the right
// to: own ranges also go into fail2ban's server-wide never-ban list
// (ADR-0059), so only admins add them.
var ErrRealIPRanges = errors.New("hosting: own proxy ranges need an admin")

// RealIP returns the real client address setting of a site (ADR-0059).
func (s *Service) RealIP(ctx context.Context, name string) (store.SiteRealIP, error) {
	site, err := s.Get(ctx, name)
	if err != nil {
		return store.SiteRealIP{}, err
	}

	return s.st.SiteRealIP(ctx, site.ID)
}

// checkRealIP validates r and refuses ranges that hold an address of this
// server, as the agent does; an error matches ErrInvalid and
// agentapi.ErrRealIPRejected.
func (s *Service) checkRealIP(r agentapi.WebRealIP) error {
	if err := r.Validate(); err != nil {
		return fmt.Errorf("%w: %w", ErrInvalid, err)
	}

	if r.IsZero() {
		return nil
	}

	local := s.localAddrs
	if local == nil {
		local = interfaceAddrs
	}

	own, err := local()
	if err != nil {
		return fmt.Errorf("hosting: addresses of this server: %w", err)
	}

	if err := r.CheckOwn(own); err != nil {
		return fmt.Errorf("%w: %w", ErrInvalid, err)
	}

	return nil
}

// SetRealIP replaces the setting of an active site, applies the web state
// and restores the old setting if that fails. addRanges says whether the
// caller may add own ranges; removing them is always allowed.
func (s *Service) SetRealIP(ctx context.Context, actor int64, name string, r agentapi.WebRealIP, addRanges bool) (store.SiteRealIP, error) {
	if err := s.checkRealIP(r); err != nil {
		return store.SiteRealIP{}, err
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	site, err := s.Get(ctx, name)
	if err != nil {
		return store.SiteRealIP{}, err
	}

	if site.State != store.SiteActive {
		return store.SiteRealIP{}, ErrState
	}

	cur, err := s.st.SiteRealIP(ctx, site.ID)
	if err != nil {
		return store.SiteRealIP{}, err
	}

	if !addRanges && addsRanges(cur.RealIP.Ranges, r.Ranges) {
		return store.SiteRealIP{}, ErrRealIPRanges
	}

	old, err := s.st.SetSiteRealIP(ctx, store.SiteRealIP{SiteID: site.ID, RealIP: r, UpdatedBy: actor}, s.now())
	if err != nil {
		return store.SiteRealIP{}, err
	}

	if err := s.applyWeb(ctx); err != nil {
		if _, rerr := s.st.SetSiteRealIP(context.WithoutCancel(ctx), old, old.UpdatedAt); rerr != nil {
			s.log.ErrorContext(ctx, "restore real IP setting after failed change", "site", name, "err", rerr)
		}

		return store.SiteRealIP{}, err
	}

	s.auditChange(ctx, actor, "site.realip.set", name, nil, store.FieldsOf(old.RealIP), store.FieldsOf(r))

	return s.st.SiteRealIP(ctx, site.ID)
}

// addsRanges reports whether next holds a range that cur does not, by
// parsed prefix, so "10.0.0.7/24" and "10.0.0.0/24" are the same.
func addsRanges(cur, next []string) bool {
	have := make([]netip.Prefix, 0, len(cur))
	for _, c := range cur {
		if p, err := agentapi.ParsePrefix(c); err == nil {
			have = append(have, p)
		}
	}

	for _, n := range next {
		p, err := agentapi.ParsePrefix(n)
		if err != nil || !slices.Contains(have, p) {
			return true
		}
	}

	return false
}

// webRealIP is the setting sent with web.apply; nil trusts nobody.
func webRealIP(all map[int64]store.SiteRealIP, site store.Site) *agentapi.WebRealIP {
	r, ok := all[site.ID]
	if !ok || r.RealIP.IsZero() {
		return nil
	}

	v := r.RealIP

	return &v
}

// interfaceAddrs returns every address of the server's interfaces,
// loopback and private ones included.
func interfaceAddrs() ([]netip.Addr, error) {
	addrs, err := net.InterfaceAddrs()
	if err != nil {
		return nil, err
	}

	out := make([]netip.Addr, 0, len(addrs))

	for _, a := range addrs {
		if n, ok := a.(*net.IPNet); ok {
			if ip, ok := netip.AddrFromSlice(n.IP); ok {
				out = append(out, ip.Unmap())
			}
		}
	}

	return out, nil
}
