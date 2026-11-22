// SPDX-License-Identifier: AGPL-3.0-or-later

package agentapi

import (
	"errors"
	"net/netip"
	"slices"
	"testing"
)

func TestRealIPValidate(t *testing.T) {
	for name, r := range map[string]WebRealIP{
		"off":           {},
		"cloudflare":    {Cloudflare: true},
		"own":           {Ranges: []string{"10.0.0.2", "192.168.0.0/24", "2001:db8::/32", "198.51.100.0/24"}},
		"cf header":     {Cloudflare: true, Header: RealIPCFConnectingIP},
		"true client":   {Ranges: []string{"203.0.113.0/24"}, Header: RealIPTrueClientIP},
		"x-real-ip":     {Ranges: []string{"203.0.113.9"}, Header: RealIPXRealIP},
		"widest v4":     {Ranges: []string{"11.0.0.0/8"}},
		"widest v6":     {Ranges: []string{"2001::/16"}},
		"both together": {Cloudflare: true, Ranges: []string{"10.1.2.3"}},
	} {
		if err := r.Validate(); err != nil {
			t.Errorf("%s: %v", name, err)
		}
	}

	many := make([]string, MaxRealIPRanges+1)
	for i := range many {
		many[i] = netip.AddrFrom4([4]byte{10, 0, byte(i >> 8), byte(i)}).String()
	}

	for name, r := range map[string]WebRealIP{
		"header only":     {Header: RealIPXForwardedFor},
		"unknown header":  {Cloudflare: true, Header: "X-Client-IP"},
		"header case":     {Cloudflare: true, Header: "x-forwarded-for"},
		"header inject":   {Cloudflare: true, Header: "X-Forwarded-For; include /etc/passwd"},
		"everything v4":   {Ranges: []string{"0.0.0.0/0"}},
		"everything v6":   {Ranges: []string{"::/0"}},
		"too wide v4":     {Ranges: []string{"8.0.0.0/7"}},
		"too wide v6":     {Ranges: []string{"2000::/3"}},
		"loopback":        {Ranges: []string{"127.0.0.1"}},
		"loopback net":    {Ranges: []string{"127.0.0.0/8"}},
		"loopback v6":     {Ranges: []string{"::1"}},
		"covers ::1":      {Ranges: []string{"::/16"}},
		"link-local":      {Ranges: []string{"169.254.0.0/16"}},
		"link-local v6":   {Ranges: []string{"fe80::/10"}},
		"metadata":        {Ranges: []string{"169.254.169.254"}},
		"multicast":       {Ranges: []string{"224.0.0.0/8"}},
		"unspecified":     {Ranges: []string{"0.0.0.0"}},
		"zero net":        {Ranges: []string{"0.0.0.0/8"}},
		"mapped":          {Ranges: []string{"::ffff:10.0.0.1"}},
		"zone":            {Ranges: []string{"fe80::1%eth0"}},
		"garbage":         {Ranges: []string{"cloudflare"}},
		"injection":       {Ranges: []string{"10.0.0.1; set_real_ip_from 0.0.0.0/0"}},
		"newline":         {Ranges: []string{"10.0.0.1\n"}},
		"duplicate":       {Ranges: []string{"10.0.0.0/24", "10.0.0.7/24"}},
		"too many ranges": {Ranges: many},
	} {
		if err := r.Validate(); !errors.Is(err, ErrRealIPRejected) {
			t.Errorf("%s: %v", name, err)
		}
	}
}

func TestRealIPPrefixes(t *testing.T) {
	r := WebRealIP{Cloudflare: true, Ranges: []string{"10.0.0.7/24", "173.245.48.0/20", "2001:db8::1"}}

	got := r.Prefixes()
	if len(got) != len(CloudflareRanges)+2 {
		t.Fatalf("%d prefixes: %v", len(got), got)
	}

	for _, want := range []string{"10.0.0.0/24", "2001:db8::1/128", "2a06:98c0::/29"} {
		if !slices.Contains(got, netip.MustParsePrefix(want)) {
			t.Errorf("%s missing: %v", want, got)
		}
	}

	if r.HeaderName() != RealIPXForwardedFor || !r.Recursive() {
		t.Error("default header is not a recursive X-Forwarded-For")
	}

	if (WebRealIP{Cloudflare: true, Header: RealIPCFConnectingIP}).Recursive() {
		t.Error("CF-Connecting-IP is recursive")
	}
}

// The shipped list must pass the same checks as an own range.
func TestCloudflareRanges(t *testing.T) {
	if err := (WebRealIP{Ranges: CloudflareRanges}).Validate(); err != nil {
		t.Fatal(err)
	}

	for _, s := range CloudflareRanges {
		if p := netip.MustParsePrefix(s); p.Masked() != p {
			t.Errorf("%s is not canonical", s)
		}
	}
}

func TestRealIPCheckOwn(t *testing.T) {
	own := []netip.Addr{netip.MustParseAddr("127.0.0.1"), netip.MustParseAddr("10.0.0.3"), netip.MustParseAddr("203.0.113.10"), netip.MustParseAddr("2001:db8::10")}

	for name, r := range map[string]WebRealIP{
		"cloudflare":      {Cloudflare: true},
		"load balancer":   {Ranges: []string{"10.0.0.2"}},
		"other network":   {Ranges: []string{"10.1.0.0/16"}},
		"neighbor":       {Ranges: []string{"203.0.113.11"}},
		"neighbor v6":    {Ranges: []string{"2001:db8::11"}},
		"different block": {Ranges: []string{"198.51.100.0/24"}},
	} {
		if err := r.CheckOwn(own); err != nil {
			t.Errorf("%s: %v", name, err)
		}
	}

	for name, r := range map[string]WebRealIP{
		"private network": {Ranges: []string{"10.0.0.0/24"}},
		"own address":     {Ranges: []string{"203.0.113.10"}},
		"own block":       {Ranges: []string{"203.0.113.0/24"}},
		"own v6 block":    {Ranges: []string{"2001:db8::/64"}},
	} {
		if err := r.CheckOwn(own); !errors.Is(err, ErrRealIPRejected) {
			t.Errorf("%s: %v", name, err)
		}
	}

	if err := (WebRealIP{Cloudflare: true}).CheckOwn([]netip.Addr{netip.MustParseAddr("104.16.0.1")}); err == nil {
		t.Error("a server inside Cloudflare's ranges trusts itself")
	}
}

func TestRealIPInApplyRequest(t *testing.T) {
	site := WebSite{User: "alpha", Domains: []string{"a.example.com"}, RealIP: &WebRealIP{Ranges: []string{"0.0.0.0/0"}}}
	if err := (WebApplyRequest{Sites: []WebSite{site}}).Validate(); !errors.Is(err, ErrRealIPRejected) {
		t.Fatalf("too wide range accepted: %v", err)
	}

	site.RealIP = &WebRealIP{Cloudflare: true}
	if err := (WebApplyRequest{Sites: []WebSite{site}}).Validate(); err != nil {
		t.Fatal(err)
	}
}
