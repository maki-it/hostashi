// SPDX-License-Identifier: AGPL-3.0-or-later

// Package netaddrs finds the public addresses of this server (ADR-0031).
package netaddrs

import (
	"fmt"
	"net"
	"net/netip"
	"slices"
)

// Public returns the addresses of the interfaces that a domain can point
// to: global unicast, not private, sorted and without duplicates.
func Public() ([]netip.Addr, error) {
	ifs, err := net.InterfaceAddrs()
	if err != nil {
		return nil, fmt.Errorf("netaddrs: %w", err)
	}

	return Filter(ifs), nil
}

// Filter keeps the public addresses of addrs.
func Filter(addrs []net.Addr) []netip.Addr {
	var out []netip.Addr

	for _, a := range addrs {
		var ip net.IP

		switch v := a.(type) {
		case *net.IPNet:
			ip = v.IP
		case *net.IPAddr:
			ip = v.IP
		default:
			continue
		}

		p, ok := netip.AddrFromSlice(ip)
		if !ok {
			continue
		}

		if p = p.Unmap(); IsPublic(p) {
			out = append(out, p)
		}
	}

	slices.SortFunc(out, netip.Addr.Compare)

	return slices.Compact(out)
}

// IsPublic reports whether a is a global unicast address outside the
// private ranges.
func IsPublic(a netip.Addr) bool {
	return a.IsValid() && a.IsGlobalUnicast() && !a.IsPrivate() && !a.Is4In6()
}
