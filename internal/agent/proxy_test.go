// SPDX-License-Identifier: AGPL-3.0-or-later

package agent

import (
	"context"
	"log/slog"
	"net/netip"
	"path/filepath"
	"slices"
	"testing"

	"github.com/maki-it/hostashi/internal/agentapi"
	"github.com/maki-it/hostashi/internal/web"
)

// The agent checks the addresses the panel resolved: nothing that reaches
// this server, the target's port only; without addresses it keeps the
// live ones of the same target (ADR-0054).
func TestProxyAddrs(t *testing.T) {
	base := filepath.Join(t.TempDir(), "web")
	h := &siteOps{
		web: &web.Reconciler{Base: base}, log: slog.New(slog.DiscardHandler),
		localAddrs: func() ([]netip.Addr, error) {
			return []netip.Addr{netip.MustParseAddr("127.0.0.1"), netip.MustParseAddr("203.0.113.10"), netip.MustParseAddr("2001:db8::10")}, nil
		},
	}

	fakeLiveGeneration(t, base, `{"sites":[{"user":"app","domains":["app.example.com"],"proxy":{"target":"http://backend.example.net"},"proxyAddrs":["10.0.0.5:80"]}]}`)

	ctx := context.Background()
	site := func(target string, addrs ...string) agentapi.WebSite {
		return agentapi.WebSite{User: "app", Domains: []string{"app.example.com"}, Proxy: &agentapi.WebProxy{Target: target}, ProxyAddrs: addrs}
	}

	for name, c := range map[string]struct {
		s    agentapi.WebSite
		want []string
	}{
		"resolved":        {site("http://backend.example.net", "10.0.0.6:80", "[2001:db8::6]:80"), []string{"10.0.0.6:80", "[2001:db8::6]:80"}},
		"https port":      {site("https://backend.example.net", "10.0.0.6:443"), []string{"10.0.0.6:443"}},
		"kept":            {site("http://backend.example.net"), []string{"10.0.0.5:80"}},
		"other target":    {site("http://other.example.net"), nil},
		"own public":      {site("http://backend.example.net", "10.0.0.6:80", "203.0.113.10:80"), nil},
		"own v6":          {site("http://backend.example.net", "[2001:db8::10]:80"), nil},
		"loopback":        {site("http://backend.example.net", "127.0.0.1:80"), nil},
		"metadata":        {site("http://backend.example.net", "169.254.169.254:80"), nil},
		"wrong port":      {site("http://backend.example.net:8080", "10.0.0.6:80"), nil},
		"not an address":  {site("http://backend.example.net", "backend:80"), nil},
		"loopback target": {site("http://127.0.0.1"), nil},
	} {
		if got := h.proxyAddrs(ctx, c.s); !slices.Equal(got, c.want) {
			t.Errorf("%s: %v, want %v", name, got, c.want)
		}
	}

	if !h.webOnly("app") {
		t.Error("a live proxy site is not web-only")
	}
}

func TestSystemLocalAddrs(t *testing.T) {
	addrs, err := systemLocalAddrs()
	if err != nil {
		t.Skip(err)
	}

	if !slices.Contains(addrs, netip.MustParseAddr("127.0.0.1")) {
		t.Errorf("no loopback in %v", addrs)
	}
}
