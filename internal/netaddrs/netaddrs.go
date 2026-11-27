// SPDX-License-Identifier: AGPL-3.0-or-later

// Package netaddrs finds the public addresses of this server (ADR-0031).
package netaddrs

import (
	"errors"
	"fmt"
	"net"
	"net/netip"
	"slices"
	"strings"
	"syscall"
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

// NAT64Hint explains a failed connection of an IPv6-only server to a host
// that has only IPv4 addresses, such as github.com.
const NAT64Hint = "the host has only IPv4 addresses and this server has no IPv4 route: " +
	"an IPv6-only server reaches it through NAT64 (DNS64 resolvers and a NAT64 gateway, from the provider or a public service)"

// Unreachable reports whether err says that the network or host could not
// be routed to, as a connect on an IPv6-only server to an IPv4 address does.
func Unreachable(err error) bool {
	if err == nil {
		return false
	}

	if errors.Is(err, syscall.ENETUNREACH) || errors.Is(err, syscall.EHOSTUNREACH) {
		return true
	}

	// Some libraries flatten the error into text.
	s := err.Error()

	return strings.Contains(s, "network is unreachable") || strings.Contains(s, "no route to host")
}

// OnlyIPv4 reports whether addrs is not empty and holds IPv4 addresses
// only (IPv4-mapped IPv6 included).
func OnlyIPv4(addrs []netip.Addr) bool {
	if len(addrs) == 0 {
		return false
	}

	for _, a := range addrs {
		if !a.Unmap().Is4() {
			return false
		}
	}

	return true
}
