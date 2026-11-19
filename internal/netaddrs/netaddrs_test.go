// SPDX-License-Identifier: AGPL-3.0-or-later

package netaddrs

import (
	"net"
	"net/netip"
	"slices"
	"testing"
)

func TestFilter(t *testing.T) {
	t.Parallel()

	cidr := func(s string) net.Addr {
		ip, n, err := net.ParseCIDR(s)
		if err != nil {
			t.Fatal(err)
		}

		n.IP = ip

		return n
	}

	in := []net.Addr{
		cidr("127.0.0.1/8"), cidr("10.0.0.5/24"), cidr("192.168.1.2/24"), cidr("172.16.0.1/12"),
		cidr("203.0.113.7/32"), cidr("::1/128"), cidr("fe80::1/64"), cidr("fd00::1/64"),
		cidr("2001:db8::10/64"), &net.IPAddr{IP: net.ParseIP("198.51.100.1")}, cidr("203.0.113.7/24"),
		&net.UnixAddr{Name: "x"},
	}

	want := []netip.Addr{netip.MustParseAddr("198.51.100.1"), netip.MustParseAddr("203.0.113.7"), netip.MustParseAddr("2001:db8::10")}
	if got := Filter(in); !slices.Equal(got, want) {
		t.Fatalf("got %v, want %v", got, want)
	}
}

func TestPublicRuns(t *testing.T) {
	t.Parallel()

	got, err := Public()
	if err != nil {
		t.Fatal(err)
	}

	for _, a := range got {
		if !IsPublic(a) {
			t.Errorf("%v is not public", a)
		}
	}
}
