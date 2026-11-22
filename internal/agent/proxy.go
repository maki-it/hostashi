// SPDX-License-Identifier: AGPL-3.0-or-later

package agent

import (
	"context"
	"fmt"
	"net"
	"net/netip"
	"slices"

	"github.com/maki-it/hostashi/internal/agentapi"
)

// proxyAddrs checks the addresses the panel resolved for a reverse proxy
// site (ADR-0054). The agent has no network access of its own, so the
// panel asks DNS; the agent refuses every address that reaches this server
// (loopback, link-local, the addresses of its interfaces) or does not
// match the target's port. nginx then connects only to these addresses.
// Without addresses from the panel (the name did not resolve), the live
// addresses are kept for the same target. A refused address leaves none,
// and the site answers 502; neither fails the apply of the other sites.
func (h *siteOps) proxyAddrs(ctx context.Context, s agentapi.WebSite) []string {
	u, err := agentapi.ParseProxyTarget(s.Proxy.Target, s.Domains)
	if err != nil {
		return nil // Validate refused it already
	}

	addrs := s.ProxyAddrs
	if len(addrs) == 0 {
		addrs = h.liveProxyAddrs(s.User, s.Proxy.Target)
		if len(addrs) == 0 {
			h.log.WarnContext(ctx, "proxy target has no address", "site", s.User, "target", s.Proxy.Target)

			return nil
		}
	}

	localAddrs := h.localAddrs
	if localAddrs == nil {
		localAddrs = systemLocalAddrs
	}

	local, err := localAddrs()
	if err != nil {
		h.log.ErrorContext(ctx, "addresses of this server for a proxy site", "site", s.User, "err", err)

		return nil
	}

	port := agentapi.ProxyPort(u)

	for _, a := range addrs {
		if err := checkProxyAddr(a, port, local); err != nil {
			h.log.WarnContext(ctx, "proxy target refused", "site", s.User, "target", s.Proxy.Target, "err", err)

			return nil
		}
	}

	return slices.Clone(addrs)
}

// checkProxyAddr checks one "ip:port" of a proxy target.
func checkProxyAddr(hp, port string, local []netip.Addr) error {
	if err := agentapi.ValidateProxyAddr(hp); err != nil {
		return err
	}

	ap := netip.MustParseAddrPort(hp)

	if net.JoinHostPort(ap.Addr().String(), port) != hp {
		return fmt.Errorf("%s: the target's port is %s", hp, port)
	}

	if slices.ContainsFunc(local, func(l netip.Addr) bool { return l.WithZone("").Unmap() == ap.Addr() }) {
		return fmt.Errorf("%s is an address of this server", hp)
	}

	return nil
}

// liveProxyAddrs returns the addresses of the site in the live state if it
// proxies to the same target.
func (h *siteOps) liveProxyAddrs(site, target string) []string {
	if h.web == nil {
		return nil
	}

	cur, _, err := h.web.Current()
	if err != nil {
		return nil
	}

	for _, s := range cur.Sites {
		if s.User == site && s.Proxy != nil && s.Proxy.Target == target {
			return slices.Clone(s.ProxyAddrs)
		}
	}

	return nil
}

// systemLocalAddrs are the addresses of this server's interfaces: a proxy
// to one of them would reach the server's own services past the firewall.
func systemLocalAddrs() ([]netip.Addr, error) {
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
