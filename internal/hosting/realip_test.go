// SPDX-License-Identifier: AGPL-3.0-or-later

package hosting

import (
	"errors"
	"net/netip"
	"testing"

	"github.com/maki-it/hostashi/internal/agentapi"
)

func TestAddsRanges(t *testing.T) {
	for _, c := range []struct {
		cur, next []string
		want      bool
	}{
		{nil, nil, false},
		{[]string{"10.0.0.0/24"}, nil, false},
		{[]string{"10.0.0.0/24"}, []string{"10.0.0.9/24"}, false},
		{[]string{"10.0.0.2"}, []string{"10.0.0.2/32"}, false},
		{[]string{"10.0.0.2"}, []string{"10.0.0.2", "10.0.0.3"}, true},
		{nil, []string{"198.51.100.0/24"}, true},
		{[]string{"10.0.0.0/24"}, []string{"10.0.0.0/23"}, true},
		{nil, []string{"garbage"}, true},
	} {
		if got := addsRanges(c.cur, c.next); got != c.want {
			t.Errorf("%v -> %v: %v, want %v", c.cur, c.next, got, c.want)
		}
	}
}

// The panel refuses a range holding one of the server's addresses itself,
// so the admin gets a clear 422 instead of a failed web.apply.
func TestCheckRealIPOwnAddress(t *testing.T) {
	s := &Service{localAddrs: func() ([]netip.Addr, error) {
		return []netip.Addr{netip.MustParseAddr("127.0.0.1"), netip.MustParseAddr("10.0.0.3")}, nil
	}}

	if err := s.checkRealIP(agentapi.WebRealIP{Ranges: []string{"10.0.0.2"}}); err != nil {
		t.Fatal(err)
	}

	err := s.checkRealIP(agentapi.WebRealIP{Ranges: []string{"10.0.0.0/24"}})
	if !errors.Is(err, ErrInvalid) || !errors.Is(err, agentapi.ErrRealIPRejected) {
		t.Fatalf("own network: %v", err)
	}

	s.localAddrs = func() ([]netip.Addr, error) { return nil, errors.New("no interfaces") }
	if err := s.checkRealIP(agentapi.WebRealIP{Cloudflare: true}); err == nil || errors.Is(err, ErrInvalid) {
		t.Fatalf("unknown addresses: %v", err)
	}

	if err := s.checkRealIP(agentapi.WebRealIP{}); err != nil {
		t.Fatalf("off: %v", err)
	}
}
