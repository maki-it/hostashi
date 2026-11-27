// SPDX-License-Identifier: AGPL-3.0-or-later

package gitsync

import (
	"context"
	"errors"
	"net/netip"
	"testing"
)

// Not parallel: it replaces lookupHost.
func TestIPv4Only(t *testing.T) {
	old := lookupHost

	t.Cleanup(func() { lookupHost = old })

	hosts := map[string][]netip.Addr{
		"github.com":  {netip.MustParseAddr("140.82.121.3")},
		"gitlab.com":  {netip.MustParseAddr("172.65.251.78"), netip.MustParseAddr("2606:4700:90:0:f22e:fbec:5bed:a9b9")},
		"2001:db8::1": {netip.MustParseAddr("2001:db8::1")},
	}
	lookupHost = func(_ context.Context, _, host string) ([]netip.Addr, error) {
		if a, ok := hosts[host]; ok {
			return a, nil
		}

		return nil, errors.New("no such host")
	}

	unreach := errors.New("dial tcp 140.82.121.3:443: connect: network is unreachable")
	for _, tc := range []struct {
		url  string
		err  error
		want bool
	}{
		{"https://github.com/maki-it/hostashi.git", unreach, true},
		{"git@github.com:maki-it/hostashi.git", unreach, true},
		{"https://github.com/maki-it/hostashi.git", errors.New("authentication required"), false},
		{"https://gitlab.com/x/y.git", unreach, false},
		{"https://[2001:db8::1]/x/y.git", unreach, false},
		{"https://unknown.example/x/y.git", unreach, false},
	} {
		if got := ipv4Only(context.Background(), tc.url, tc.err); got != tc.want {
			t.Errorf("ipv4Only(%s, %v) = %v, want %v", tc.url, tc.err, got, tc.want)
		}
	}
}
