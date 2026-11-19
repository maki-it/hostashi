// SPDX-License-Identifier: AGPL-3.0-or-later

package agentapi

import (
	"strings"
	"testing"
)

func TestCanonicalIPOrPrefix(t *testing.T) {
	for in, want := range map[string]string{
		"203.0.113.7":      "203.0.113.7",
		"10.0.0.0/8":       "10.0.0.0/8",
		"2001:DB8::1":      "2001:db8::1",
		"2001:db8::/32":    "2001:db8::/32",
		"::ffff:192.0.2.1": "192.0.2.1",
	} {
		if got, err := CanonicalIPOrPrefix(in); err != nil || got != want {
			t.Errorf("%q: %q %v, want %q", in, got, err, want)
		}
	}

	for _, in := range []string{"", "10.1.2.3/8", "fe80::1%eth0", "::ffff:0.0.0.0/96", "1.2.3.4; allow all", "example.com", "10.0.0.0/33"} {
		if got, err := CanonicalIPOrPrefix(in); err == nil {
			t.Errorf("%q accepted as %q", in, got)
		}
	}
}

func TestMaintenanceNeedsCanonicalAddresses(t *testing.T) {
	d := strings.Repeat("a", 64)

	if err := (WebMaintenance{Page: "maint", Digest: d, AllowIPs: []string{"10.0.0.0/8", "2001:db8::1"}}).Validate(); err != nil {
		t.Fatal(err)
	}

	if err := (WebMaintenance{Page: "maint", Digest: d, AllowIPs: []string{"2001:DB8::1"}}).Validate(); err == nil {
		t.Error("non-canonical address accepted")
	}

	many := make([]string, MaxAllowIPs+1)
	for i := range many {
		many[i] = "10.0.0." + string(rune('0'+i%10))
	}

	if err := (WebMaintenance{Page: "maint", Digest: d, AllowIPs: many}).Validate(); err == nil {
		t.Error("too many addresses accepted")
	}
}

func TestValidatePagePath(t *testing.T) {
	for _, p := range []string{"index.html", "css/site.css", "img/a/logo.PNG", "fonts/sans.woff2", "_x.js"} {
		if err := ValidatePagePath(p); err != nil {
			t.Errorf("%q: %v", p, err)
		}
	}

	for _, p := range []string{"", "/index.html", "../index.html", "a/../b.html", ".hidden.css", "-x.css", "a/b/c/d.css", "x.php", "x", "a//b.css", "x.html\x00", "ä.html"} {
		if err := ValidatePagePath(p); err == nil {
			t.Errorf("%q accepted", p)
		}
	}
}

func TestPageDigestIsUnambiguous(t *testing.T) {
	a := PageDigest(map[string][]byte{"a.css": []byte("b.cssx"), "c.css": nil})
	b := PageDigest(map[string][]byte{"a.css": nil, "b.css": []byte("x")})

	if a == b || len(a) != 64 {
		t.Fatalf("digests %s %s", a, b)
	}

	if PageDigest(map[string][]byte{"x.html": []byte("1"), "y.html": []byte("2")}) != PageDigest(map[string][]byte{"y.html": []byte("2"), "x.html": []byte("1")}) {
		t.Fatal("digest depends on order")
	}
}
