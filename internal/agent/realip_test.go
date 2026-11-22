// SPDX-License-Identifier: AGPL-3.0-or-later

package agent

import (
	"context"
	"errors"
	"log/slog"
	"net/netip"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/maki-it/hostashi/internal/agentapi"
	"github.com/maki-it/hostashi/internal/agentrpc"
	"github.com/maki-it/hostashi/internal/f2bproxies"
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

// web.apply hands every trusted proxy of the live sites to fail2ban's
// never-ban list and removes the file when no site trusts one.
func TestSyncProxyIgnore(t *testing.T) {
	path := filepath.Join(t.TempDir(), "hostashi-proxies.local")
	reloads := 0
	h := &siteOps{log: slog.New(slog.DiscardHandler), f2b: &f2bproxies.File{Path: path, Reload: func(context.Context) error { reloads++; return nil }}}

	d := web.Desired{Sites: []web.Site{
		{User: "cf", RealIP: agentapi.WebRealIP{Cloudflare: true}},
		{User: "lb", RealIP: agentapi.WebRealIP{Ranges: []string{"10.0.0.2"}}},
		{User: "plain"},
	}}

	h.syncProxyIgnore(t.Context(), d)

	b, err := os.ReadFile(path)
	if err != nil || reloads != 1 {
		t.Fatalf("%v, %d reloads", err, reloads)
	}

	for _, want := range []string{"10.0.0.2/32", "173.245.48.0/20", "2a06:98c0::/29"} {
		if !strings.Contains(string(b), want) {
			t.Errorf("never-ban list lacks %s:\n%s", want, b)
		}
	}

	h.syncProxyIgnore(t.Context(), web.Desired{Sites: []web.Site{{User: "plain"}}})

	if _, err := os.Stat(path); !errors.Is(err, os.ErrNotExist) || reloads != 2 {
		t.Errorf("file after the last proxy site: %v, %d reloads", err, reloads)
	}

	(&siteOps{}).syncProxyIgnore(t.Context(), d) // no fail2ban: nothing to do
}
