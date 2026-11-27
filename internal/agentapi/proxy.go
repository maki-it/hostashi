// SPDX-License-Identifier: AGPL-3.0-or-later

package agentapi

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/netip"
	"net/url"
	"slices"
	"strconv"
	"strings"
)

// WebProxy makes a site pass every request on to another web server (site
// type proxy, ADR-0054). Such a site has no Linux account and no files.
type WebProxy struct {
	// Target is an http or https URL with a host, an optional port and an
	// optional path prefix; no user info, query or fragment.
	Target string `json:"target"`
	// TargetHost sends the target's host as Host instead of the visitor's.
	TargetHost bool `json:"targetHost,omitempty"`
	// Insecure does not verify the certificate of an https target.
	Insecure bool `json:"insecure,omitempty"`
}

// Validate checks the proxy of a site with the given domains. It checks the
// address literally; names are checked when they are resolved
// (ResolveProxyTarget).
func (p *WebProxy) Validate(own []string) error {
	_, err := ParseProxyTarget(p.Target, own)

	return err
}

// ParseProxyTarget checks a target and returns it parsed. own lists the
// domains of the site: a target on one of them would pass requests to the
// site itself. An IP address must be one a proxy may reach
// (ProxyAddrAllowed); a name must not be below .localhost.
func ParseProxyTarget(target string, own []string) (*url.URL, error) {
	u, err := parseTarget("proxy target", target, own)
	if err != nil {
		return nil, err
	}

	host := u.Hostname()
	if a, err := netip.ParseAddr(host); err == nil {
		if err := ProxyAddrAllowed(a); err != nil {
			return nil, fmt.Errorf("proxy target: %w", err)
		}
	} else if strings.HasSuffix(host, ".localhost") {
		return nil, errors.New("proxy target: .localhost names point to this server")
	}

	return u, nil
}

// ProxyPort is the port of a parsed target: its own or the scheme's.
func ProxyPort(u *url.URL) string {
	if p := u.Port(); p != "" {
		return p
	}

	if u.Scheme == "https" {
		return "443"
	}

	return "80"
}

var (
	// thisNetwork is 0.0.0.0/8: Linux connects 0.x.y.z to this host.
	thisNetwork = netip.MustParsePrefix("0.0.0.0/8")
	// ipv4Compat holds the deprecated IPv4-compatible IPv6 addresses.
	ipv4Compat = netip.MustParsePrefix("::/96")
	// nat64 embeds an IPv4 address in IPv6 (RFC 6052).
	nat64 = netip.MustParsePrefix("64:ff9b::/96")
)

// ErrProxyLookup: the target's name did not resolve.
var ErrProxyLookup = errors.New("lookup failed")

// ErrProxyAddr: the address is one a reverse proxy must not reach.
var ErrProxyAddr = errors.New("address not allowed")

// ProxyAddrAllowed refuses the addresses through which a reverse proxy
// would reach this server or the cloud's metadata service (ADR-0054):
// loopback, link-local, multicast, unspecified, broadcast and 0.0.0.0/8,
// also when they are wrapped in IPv6. Private networks are allowed.
func ProxyAddrAllowed(a netip.Addr) error {
	a = a.WithZone("")
	if a.Is4In6() {
		a = a.Unmap()
	}

	// :: and ::1 are caught below; the rest of ::/96 wraps IPv4.
	if a.Is6() && ipv4Compat.Contains(a) && !a.IsUnspecified() && !a.IsLoopback() {
		return fmt.Errorf("%w: %s is an IPv4-compatible address", ErrProxyAddr, a)
	}

	if nat64.Contains(a) {
		b := a.As16()
		a = netip.AddrFrom4([4]byte(b[12:]))
	}

	switch {
	case a.IsLoopback():
		return fmt.Errorf("%w: %s is this server (loopback)", ErrProxyAddr, a)
	case a.IsUnspecified(), a.Is4() && thisNetwork.Contains(a):
		return fmt.Errorf("%w: %s means this server", ErrProxyAddr, a)
	case a.IsLinkLocalUnicast():
		return fmt.Errorf("%w: %s is link-local (for example the cloud metadata service)", ErrProxyAddr, a)
	case a.IsMulticast(), a.IsLinkLocalMulticast(), a.IsInterfaceLocalMulticast():
		return fmt.Errorf("%w: %s is multicast", ErrProxyAddr, a)
	case a == netip.AddrFrom4([4]byte{255, 255, 255, 255}):
		return fmt.Errorf("%w: %s is broadcast", ErrProxyAddr, a)
	}

	return nil
}

// sharedAddressSpace is 100.64.0.0/10 (RFC 6598), the carrier-grade NAT
// range that overlay networks such as Tailscale use as well.
var sharedAddressSpace = netip.MustParsePrefix("100.64.0.0/10")

// ProxyAddrPrivate reports whether a proxy target address lies in a
// private network: 10/8, 172.16/12, 192.168/16, fc00::/7 and 100.64/10,
// also when they are wrapped in IPv6. Only admins and managers may point a
// reverse proxy there (second own security review, 2026-11-26): such an
// address reaches the server's internal network, not the internet.
func ProxyAddrPrivate(a netip.Addr) bool {
	a = a.WithZone("")
	if a.Is4In6() {
		a = a.Unmap()
	}

	if nat64.Contains(a) {
		b := a.As16()
		a = netip.AddrFrom4([4]byte(b[12:]))
	}

	return a.IsPrivate() || sharedAddressSpace.Contains(a)
}

// ProxyResolver looks up the addresses of a host name.
type ProxyResolver func(ctx context.Context, host string) ([]netip.Addr, error)

// MaxProxyAddrs bounds the addresses of one target.
const MaxProxyAddrs = 16

// ResolveProxyTarget returns the addresses a proxy target reaches, as
// "ip:port" ("[ip]:port" for IPv6), sorted. A name is resolved with
// lookup. It fails if any address is refused by ProxyAddrAllowed or is
// one of local, the addresses of this server: one bad answer is enough
// to suspect a name that points back here.
func ResolveProxyTarget(ctx context.Context, u *url.URL, lookup ProxyResolver, local []netip.Addr) ([]string, error) {
	host := u.Hostname()

	var addrs []netip.Addr

	if a, err := netip.ParseAddr(host); err == nil {
		addrs = []netip.Addr{a}
	} else {
		var err error

		addrs, err = lookup(ctx, host)
		if err != nil {
			return nil, fmt.Errorf("proxy target: %w: %s does not resolve: %w", ErrProxyLookup, host, err)
		}

		if len(addrs) == 0 {
			return nil, fmt.Errorf("proxy target: %w: %s has no address", ErrProxyLookup, host)
		}
	}

	port := ProxyPort(u)
	out := make([]string, 0, len(addrs))

	for _, a := range addrs {
		a = a.WithZone("").Unmap()

		if err := ProxyAddrAllowed(a); err != nil {
			return nil, fmt.Errorf("proxy target %s: %w", host, err)
		}

		if slices.ContainsFunc(local, func(l netip.Addr) bool { return l.WithZone("").Unmap() == a }) {
			return nil, fmt.Errorf("proxy target %s: %w: %s is an address of this server", host, ErrProxyAddr, a)
		}

		hp := net.JoinHostPort(a.String(), port)
		if !slices.Contains(out, hp) {
			out = append(out, hp)
		}
	}

	if len(out) > MaxProxyAddrs {
		out = out[:MaxProxyAddrs]
	}

	slices.Sort(out)

	return out, nil
}

// ValidateProxyAddr checks one resolved address "ip:port" of a proxy site.
func ValidateProxyAddr(hp string) error {
	ap, err := netip.ParseAddrPort(hp)
	if err != nil || ap.Port() == 0 || ap.Addr().Zone() != "" || net.JoinHostPort(ap.Addr().String(), strconv.FormatUint(uint64(ap.Port()), 10)) != hp {
		return fmt.Errorf("invalid proxy address %q", hp)
	}

	return ProxyAddrAllowed(ap.Addr())
}
