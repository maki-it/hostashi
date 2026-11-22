// SPDX-License-Identifier: AGPL-3.0-or-later

package agentapi

import (
	"errors"
	"fmt"
	"net/netip"
	"slices"
)

// Real client address behind a CDN or proxy (ADR-0059). The panel and the
// agent both check a site's setting with Validate and CheckOwn; package web
// renders it with nginx's realip module.

// Headers a site may take the client address from. Nothing else reaches
// the config, so no header name is free text.
const (
	RealIPXForwardedFor  = "X-Forwarded-For"
	RealIPXRealIP        = "X-Real-IP"
	RealIPCFConnectingIP = "CF-Connecting-IP"
	RealIPTrueClientIP   = "True-Client-IP"
)

// RealIPHeaders are the headers in the order the panel offers them; the
// first one is the default.
var RealIPHeaders = []string{RealIPXForwardedFor, RealIPXRealIP, RealIPCFConnectingIP, RealIPTrueClientIP}

// MaxRealIPRanges bounds the own ranges of one setting.
const MaxRealIPRanges = 256

// Narrowest allowed prefix lengths: a wider range would trust a large part
// of the internet. Cloudflare's widest are a /13 and a /29.
const (
	minRealIPBits4 = 8
	minRealIPBits6 = 16
)

// ErrRealIPRejected wraps every reason a real IP setting is refused.
var ErrRealIPRejected = errors.New("real IP rejected")

// WebRealIP is the real client address setting of a site or the panel. The
// zero value trusts nobody.
type WebRealIP struct {
	// Cloudflare trusts the shipped CloudflareRanges.
	Cloudflare bool `json:"cloudflare,omitempty"`
	// Ranges are own trusted proxies: CIDR prefixes or single addresses.
	Ranges []string `json:"ranges,omitempty"`
	// Header is one of RealIPHeaders; empty means RealIPXForwardedFor.
	Header string `json:"header,omitempty"`
}

// IsZero reports whether the setting trusts nobody.
func (r WebRealIP) IsZero() bool { return !r.Cloudflare && len(r.Ranges) == 0 }

// HeaderName returns the header nginx reads, with the default filled in.
func (r WebRealIP) HeaderName() string {
	if r.Header == "" {
		return RealIPXForwardedFor
	}

	return r.Header
}

// Recursive reports whether nginx should skip trusted addresses from the
// right of the header; only X-Forwarded-For carries a chain.
func (r WebRealIP) Recursive() bool { return r.HeaderName() == RealIPXForwardedFor }

// Validate checks the header and every own range. A header without any
// trusted proxy is refused, so a stored setting always means something.
func (r WebRealIP) Validate() error {
	if r.Header != "" && !slices.Contains(RealIPHeaders, r.Header) {
		return fmt.Errorf("%w: header %q is not one of %v", ErrRealIPRejected, r.Header, RealIPHeaders)
	}

	if r.IsZero() {
		if r.Header != "" {
			return fmt.Errorf("%w: a header needs Cloudflare or an own range", ErrRealIPRejected)
		}

		return nil
	}

	if len(r.Ranges) > MaxRealIPRanges {
		return fmt.Errorf("%w: more than %d ranges", ErrRealIPRejected, MaxRealIPRanges)
	}

	seen := make(map[netip.Prefix]bool, len(r.Ranges))

	for _, s := range r.Ranges {
		p, err := checkRealIPRange(s)
		if err != nil {
			return fmt.Errorf("%w: %w", ErrRealIPRejected, err)
		}

		if seen[p] {
			return fmt.Errorf("%w: range %s is listed twice", ErrRealIPRejected, p)
		}

		seen[p] = true
	}

	return nil
}

// checkRealIPRange parses one own range and refuses what must never be
// trusted: too wide prefixes, and loopback, link-local, multicast and
// unspecified addresses. Local users of the server connect from these.
func checkRealIPRange(s string) (netip.Prefix, error) {
	p, err := ParsePrefix(s)
	if err != nil {
		return netip.Prefix{}, err
	}

	a := p.Addr()

	minBits := minRealIPBits6
	if a.Is4() {
		minBits = minRealIPBits4
	}

	if p.Bits() < minBits {
		return netip.Prefix{}, fmt.Errorf("range %s is too wide (at least /%d)", s, minBits)
	}

	switch {
	case a.IsLoopback() || p.Contains(netip.IPv6Loopback()) || p.Contains(netip.AddrFrom4([4]byte{127, 0, 0, 1})):
		return netip.Prefix{}, fmt.Errorf("range %s contains loopback addresses", s)
	case a.IsLinkLocalUnicast() || a.IsLinkLocalMulticast():
		return netip.Prefix{}, fmt.Errorf("range %s is link-local", s)
	case a.IsMulticast() || a.IsInterfaceLocalMulticast():
		return netip.Prefix{}, fmt.Errorf("range %s is multicast", s)
	case a.IsUnspecified():
		return netip.Prefix{}, fmt.Errorf("range %s is unspecified", s)
	}

	return p, nil
}

// Prefixes returns every trusted prefix: the Cloudflare list when chosen,
// then the own ranges, without duplicates. Call it only after Validate.
func (r WebRealIP) Prefixes() []netip.Prefix {
	var out []netip.Prefix

	if r.Cloudflare {
		for _, s := range CloudflareRanges {
			out = append(out, netip.MustParsePrefix(s))
		}
	}

	for _, s := range r.Ranges {
		if p, err := ParsePrefix(s); err == nil && !slices.Contains(out, p) {
			out = append(out, p)
		}
	}

	return out
}

// CheckOwn refuses a setting that trusts an address of the server itself.
// Every site user can connect to the server's own addresses with any
// header, so trusting one would let a site user pick the client address
// another site sees.
func (r WebRealIP) CheckOwn(own []netip.Addr) error {
	for _, p := range r.Prefixes() {
		for _, a := range own {
			if p.Contains(a.Unmap()) {
				return fmt.Errorf("%w: range %s contains %s, an address of this server; trust the proxy's own address instead", ErrRealIPRejected, p, a.Unmap())
			}
		}
	}

	return nil
}
