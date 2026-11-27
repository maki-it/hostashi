// SPDX-License-Identifier: AGPL-3.0-or-later

package hosting

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
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

// checkRealIP validates r; with localAddrs set (tests) it also refuses
// ranges that hold an address of this server, which the agent checks on
// the server. An error matches ErrInvalid and agentapi.ErrRealIPRejected.
func (s *Service) checkRealIP(r agentapi.WebRealIP) error {
	if err := r.Validate(); err != nil {
		return fmt.Errorf("%w: %w", ErrInvalid, err)
	}

	if r.IsZero() {
		return nil
	}

	// The web process has no netlink socket (RestrictAddressFamilies), so
	// it cannot read the interfaces: the agent checks the ranges against
	// them with web.apply and refuses with an invalid argument.
	if s.localAddrs == nil {
		return nil
	}

	own, err := s.localAddrs()
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

// panelRealIPKey is the settings key of the panel's own setting (ADR-0059),
// the JSON of an agentapi.WebRealIP; missing trusts nobody.
const panelRealIPKey = "panel.realip"

// PanelRealIP returns which proxies the panel's own host name takes the
// client address from.
func (s *Service) PanelRealIP(ctx context.Context) (agentapi.WebRealIP, error) {
	v, ok, err := s.st.Setting(ctx, panelRealIPKey)
	if err != nil || !ok {
		return agentapi.WebRealIP{}, err
	}

	var r agentapi.WebRealIP
	if err := json.Unmarshal([]byte(v), &r); err != nil {
		return agentapi.WebRealIP{}, fmt.Errorf("hosting: %s: %w", panelRealIPKey, err)
	}

	return r, nil
}

// SetPanelRealIP replaces the panel's own setting, applies the web state
// and restores the old setting if that fails. Only admins reach it.
func (s *Service) SetPanelRealIP(ctx context.Context, actor int64, r agentapi.WebRealIP) (agentapi.WebRealIP, error) {
	if err := s.checkRealIP(r); err != nil {
		return agentapi.WebRealIP{}, err
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	old, err := s.PanelRealIP(ctx)
	if err != nil {
		return agentapi.WebRealIP{}, err
	}

	if err := s.savePanelRealIP(ctx, r); err != nil {
		return agentapi.WebRealIP{}, err
	}

	if err := s.applyWeb(ctx); err != nil {
		if rerr := s.savePanelRealIP(context.WithoutCancel(ctx), old); rerr != nil {
			s.log.ErrorContext(ctx, "restore the panel's real IP setting after failed change", "err", rerr)
		}

		return agentapi.WebRealIP{}, err
	}

	s.auditChange(ctx, actor, "settings.panel_realip", "", nil, store.FieldsOf(old), store.FieldsOf(r))

	return s.PanelRealIP(ctx)
}

func (s *Service) savePanelRealIP(ctx context.Context, r agentapi.WebRealIP) error {
	b, err := json.Marshal(r)
	if err != nil {
		return err
	}

	return s.st.SetSetting(ctx, panelRealIPKey, string(b))
}
