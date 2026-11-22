// SPDX-License-Identifier: AGPL-3.0-or-later

package agent

import (
	"errors"
	"net/netip"
	"testing"

	"github.com/maki-it/hostashi/internal/agentapi"
	"github.com/maki-it/hostashi/internal/agentrpc"
	"github.com/maki-it/hostashi/internal/web"
)

// The agent refuses trusted ranges that hold one of its own addresses
// (ADR-0059): a site user could connect from there with any header.
func TestRealIPFor(t *testing.T) {
	h := &siteOps{localAddrs: func() ([]netip.Addr, error) {
		return []netip.Addr{netip.MustParseAddr("127.0.0.1"), netip.MustParseAddr("10.0.0.3"), netip.MustParseAddr("2001:db8::10")}, nil
	}}

	site := web.Site{User: "alpha"}
	if err := h.realIPFor(&site, &agentapi.WebRealIP{Cloudflare: true, Ranges: []string{"10.0.0.2"}}); err != nil {
		t.Fatal(err)
	}

	if !site.RealIP.Cloudflare || len(site.RealIP.Ranges) != 1 {
		t.Errorf("setting not passed on: %+v", site.RealIP)
	}

	for _, r := range []string{"10.0.0.0/24", "2001:db8::/64"} {
		site := web.Site{User: "alpha"}

		var rpcErr *agentrpc.Error

		err := h.realIPFor(&site, &agentapi.WebRealIP{Ranges: []string{r}})
		if !errors.As(err, &rpcErr) || rpcErr.Code != agentrpc.CodeInvalidArgument || !site.RealIP.IsZero() {
			t.Errorf("%s: %v %+v", r, err, site.RealIP)
		}
	}

	h.localAddrs = func() ([]netip.Addr, error) { return nil, errors.New("no interfaces") }
	if err := h.realIPFor(&web.Site{}, &agentapi.WebRealIP{Cloudflare: true}); err == nil {
		t.Error("unknown own addresses accepted")
	}

	if err := h.realIPFor(&web.Site{}, nil); err != nil {
		t.Errorf("no setting: %v", err)
	}
}
