// SPDX-License-Identifier: AGPL-3.0-or-later

package hosting

import (
	"context"
	"fmt"
	"net"
	"net/netip"
	"strconv"
	"time"

	"github.com/maki-it/hostashi/internal/agentapi"
	"github.com/maki-it/hostashi/internal/store"
)

// proxyLookupTimeout bounds the lookup of one proxy target.
const proxyLookupTimeout = 5 * time.Second

// noFiles reports whether a site of this type serves nothing from its
// home: redirect and reverse proxy sites (ADR-0054). Such a site may
// still have an account kept from an earlier type.
func noFiles(typ string) bool {
	return typ == store.SiteTypeRedirect || typ == store.SiteTypeProxy
}

// proxyAddrs resolves a proxy target with the service's resolver and
// checks every address (ADR-0054). The agent checks them once more, also
// against the server's own addresses.
func (s *Service) proxyAddrs(ctx context.Context, target string, domains []string) ([]string, error) {
	u, err := agentapi.ParseProxyTarget(target, domains)
	if err != nil {
		return nil, err
	}

	var r Resolver = net.DefaultResolver
	if s.resolver != nil {
		r = s.resolver
	}

	lctx, cancel := context.WithTimeout(ctx, proxyLookupTimeout)
	defer cancel()

	return agentapi.ResolveProxyTarget(lctx, u, func(ctx context.Context, host string) ([]netip.Addr, error) {
		return r.LookupNetIP(ctx, "ip", host)
	}, nil)
}

// checkProxy checks the proxy of a site with these domains: the target as
// written and every address its name resolves to now.
func (s *Service) checkProxy(ctx context.Context, p store.SiteProxy, domains []string) error {
	if _, err := s.proxyAddrs(ctx, p.Target, domains); err != nil {
		return fmt.Errorf("%w: %w", ErrInvalid, err)
	}

	return nil
}

// proxyWeb is the web state of a proxy site's target. A name that does
// not resolve now, or resolves to a refused address, is sent without
// addresses: the agent keeps the live ones for the same target, or the
// site answers 502. It never fails the apply of the other sites.
func (s *Service) proxyWeb(ctx context.Context, site store.Site) (*agentapi.WebProxy, []string) {
	p := site.Proxy
	if p == nil {
		return nil, nil
	}

	addrs, err := s.proxyAddrs(ctx, p.Target, site.Domains)
	if err != nil {
		s.log.WarnContext(ctx, "proxy target", "site", site.Name, "target", p.Target, "err", err)
	}

	return &agentapi.WebProxy{Target: p.Target, TargetHost: p.TargetHost, Insecure: p.Insecure}, addrs
}

// SetProxy changes the target of an active reverse proxy site (ADR-0054).
// If the web server does not accept it, the previous target is stored
// again.
func (s *Service) SetProxy(ctx context.Context, actor int64, name string, p store.SiteProxy) (store.Site, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	site, err := s.Get(ctx, name)
	if err != nil {
		return store.Site{}, err
	}

	if site.State != store.SiteActive {
		return store.Site{}, ErrState
	}

	if site.Type != store.SiteTypeProxy {
		return store.Site{}, ErrSiteType
	}

	if err := s.checkProxy(ctx, p, site.Domains); err != nil {
		return store.Site{}, err
	}

	old, err := s.st.SetSiteProxy(ctx, name, p, s.now())
	if err != nil {
		return store.Site{}, err
	}

	if err := s.applyWeb(ctx); err != nil {
		if _, rerr := s.st.SetSiteProxy(context.WithoutCancel(ctx), name, old, s.now()); rerr != nil {
			s.log.ErrorContext(ctx, "restore proxy after failed change", "site", name, "err", rerr)
		}

		return store.Site{}, err
	}

	s.auditChange(ctx, actor, "site.proxy", name, nil, proxyAudit(old), proxyAudit(p))

	return s.Get(ctx, name)
}

func proxyAudit(p store.SiteProxy) map[string]string {
	return map[string]string{"target": p.Target, "targetHost": strconv.FormatBool(p.TargetHost), "insecure": strconv.FormatBool(p.Insecure)}
}
