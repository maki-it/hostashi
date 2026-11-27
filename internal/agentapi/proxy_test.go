// SPDX-License-Identifier: AGPL-3.0-or-later

package agentapi

import (
	"context"
	"errors"
	"net/netip"
	"slices"
	"testing"
)

func TestParseProxyTarget(t *testing.T) {
	own := []string{"shop.example.com"}

	for _, ok := range []string{
		"http://10.0.0.5:8080", "https://backend.example.net", "http://192.168.1.20/app/", "https://[2001:db8::5]:8443/x",
		"http://172.16.0.1", "http://100.64.1.1", "http://[fd00::1]",
	} {
		if _, err := ParseProxyTarget(ok, own); err != nil {
			t.Errorf("%s: %v", ok, err)
		}
	}

	for _, bad := range []string{
		"", "ftp://example.com", "http://127.0.0.1:8444", "http://127.1", "http://[::1]", "http://0.0.0.0", "http://0.1.2.3",
		"http://169.254.169.254/latest", "http://[fe80::1]", "http://224.0.0.1", "http://255.255.255.255",
		"http://[::ffff:127.0.0.1]", "http://[::127.0.0.1]", "http://[64:ff9b::7f00:1]", "http://[64:ff9b::a9fe:a9fe]",
		"http://localhost", "http://app.localhost", "http://shop.example.com", "http://user@example.com",
		"http://example.com/?a=1", "http://example.com/a;b", "http://2130706433", "http://0x7f.0.0.1",
	} {
		if _, err := ParseProxyTarget(bad, own); err == nil {
			t.Errorf("%q accepted", bad)
		}
	}
}

func TestResolveProxyTarget(t *testing.T) {
	ctx := context.Background()
	names := map[string][]netip.Addr{
		"backend.example.net": {netip.MustParseAddr("10.0.0.6"), netip.MustParseAddr("10.0.0.5"), netip.MustParseAddr("10.0.0.5")},
		"v6.example.net":      {netip.MustParseAddr("2001:db8::7")},
		"rebind.example.net":  {netip.MustParseAddr("10.0.0.5"), netip.MustParseAddr("127.0.0.1")},
		"meta.example.net":    {netip.MustParseAddr("169.254.169.254")},
		"self.example.net":    {netip.MustParseAddr("203.0.113.10")},
		"mapped.example.net":  {netip.MustParseAddr("::ffff:127.0.0.1")},
		"empty.example.net":   {},
	}
	lookup := func(_ context.Context, host string) ([]netip.Addr, error) {
		a, ok := names[host]
		if !ok {
			return nil, errors.New("no such host")
		}

		return a, nil
	}
	local := []netip.Addr{netip.MustParseAddr("203.0.113.10")}

	for target, want := range map[string][]string{
		"https://backend.example.net/app": {"10.0.0.5:443", "10.0.0.6:443"},
		"http://v6.example.net:8080":      {"[2001:db8::7]:8080"},
		"http://10.0.0.9":                 {"10.0.0.9:80"},
	} {
		u, err := ParseProxyTarget(target, nil)
		if err != nil {
			t.Fatal(err)
		}

		got, err := ResolveProxyTarget(ctx, u, lookup, local)
		if err != nil || !slices.Equal(got, want) {
			t.Errorf("%s: %v %v, want %v", target, got, err, want)
		}

		for _, a := range got {
			if err := ValidateProxyAddr(a); err != nil {
				t.Errorf("%s: %v", a, err)
			}
		}
	}

	for _, target := range []string{
		"http://rebind.example.net", "http://meta.example.net", "http://self.example.net", "http://mapped.example.net",
		"http://empty.example.net", "http://missing.example.net", "http://203.0.113.10",
	} {
		u, err := ParseProxyTarget(target, nil)
		if err != nil {
			t.Fatal(err)
		}

		if got, err := ResolveProxyTarget(ctx, u, lookup, local); err == nil {
			t.Errorf("%s resolved to %v", target, got)
		}
	}
}

func TestValidateProxyAddr(t *testing.T) {
	for _, bad := range []string{"", "10.0.0.5", "10.0.0.5:0", "127.0.0.1:80", "[::1]:80", "[fe80::1%eth0]:80", "10.0.0.5:80 ", "[2001:db8:0::7]:80"} {
		if ValidateProxyAddr(bad) == nil {
			t.Errorf("%q accepted", bad)
		}
	}
}

func TestWebProxyInSite(t *testing.T) {
	site := func(f func(*WebSite)) WebApplyRequest {
		s := WebSite{User: "app", Domains: []string{"app.example.com"}, Proxy: &WebProxy{Target: "http://10.0.0.5:8080"}}
		f(&s)

		return WebApplyRequest{Sites: []WebSite{s}}
	}

	if err := site(func(*WebSite) {}).Validate(); err != nil {
		t.Fatal(err)
	}

	for name, r := range map[string]WebApplyRequest{
		"php":      site(func(s *WebSite) { s.PHP = "8.4" }),
		"snippets": site(func(s *WebSite) { s.Snippets = &WebSnippets{Server: "gzip on;"} }),
		"both":     site(func(s *WebSite) { s.Redirect = &WebRedirect{Target: "https://new.example.com", Status: 301} }),
		"loopback": site(func(s *WebSite) { s.Proxy.Target = "http://127.0.0.1:8444" }),
		"loop":     site(func(s *WebSite) { s.Proxy.Target = "https://app.example.com" }),
	} {
		if err := r.Validate(); err == nil {
			t.Errorf("%s accepted", name)
		}
	}
}

func TestProxyAddrPrivate(t *testing.T) {
	for addr, want := range map[string]bool{
		"10.0.0.5": true, "172.16.0.1": true, "172.31.255.255": true, "192.168.1.1": true, "100.64.0.1": true, "100.127.255.255": true,
		"fd00::1": true, "fc00::1": true, "::ffff:10.0.0.5": true, "64:ff9b::a00:5": true,
		"203.0.113.7": false, "172.32.0.1": false, "100.128.0.1": false, "8.8.8.8": false, "2001:db8::1": false, "::ffff:8.8.8.8": false,
	} {
		if got := ProxyAddrPrivate(netip.MustParseAddr(addr)); got != want {
			t.Errorf("%s: %v, want %v", addr, got, want)
		}
	}
}
