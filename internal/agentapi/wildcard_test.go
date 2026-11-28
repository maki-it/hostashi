// SPDX-License-Identifier: AGPL-3.0-or-later

package agentapi

import (
	"strings"
	"testing"
)

func TestValidateDomainPattern(t *testing.T) {
	good := []string{"example.com", "*.example.com", "*.shop.example.com", "*.xn--bcher-kva.example", "*.a.b"}
	for _, d := range good {
		if err := ValidateDomainPattern(d); err != nil {
			t.Errorf("%q rejected: %v", d, err)
		}
	}

	bad := []string{
		"", "*", "*.", "*.com", "a.*.example.com", "*example.com", "**.example.com", "*.*.example.com",
		"*.Example.com", "*.example.com.", ".*.example.com", "*..example.com", "*.-a.example.com",
		"*.127.0.0.1", "*.example.com;", "*.example.com\n", "* .example.com", "*.$host.example.com",
		"?.example.com", "~^.+\\.example\\.com$", "example.*", "*.a_b.example.com",
		"*." + strings.Repeat("abcdefghi.", 25) + "com",
	}
	for _, d := range bad {
		if err := ValidateDomainPattern(d); err == nil {
			t.Errorf("%q accepted", d)
		}
	}

	// ValidateDomain itself stays strict.
	if ValidateDomain("*.example.com") == nil {
		t.Error("ValidateDomain accepts a wildcard")
	}
}

func TestWebApplyWildcard(t *testing.T) {
	redirect := &WebRedirect{Status: 301, Target: "https://www.example.org"}
	proxy := &WebProxy{Target: "http://10.0.0.5:8080"}

	ok := WebApplyRequest{Sites: []WebSite{
		{User: "fwd", Domains: []string{"example.com", "*.example.com"}, Redirect: redirect},
		{User: "app", Domains: []string{"app.example.net", "*.app.example.net"}, Proxy: proxy, ProxyAddrs: []string{"10.0.0.5:8080"}},
		// An exact name below another site's wildcard is its own site.
		{User: "www", Domains: []string{"www.example.com"}},
		// A longer wildcard next to a shorter one.
		{User: "shop", Domains: []string{"shop.example.com", "*.shop.example.com"}, Redirect: redirect},
	}}
	if err := ok.Validate(); err != nil {
		t.Fatal(err)
	}

	for name, s := range map[string]WebSite{
		"static site":     {User: "blog", Domains: []string{"blog.example.com", "*.blog.example.com"}},
		"php site":        {User: "blog", Domains: []string{"blog.example.com", "*.blog.example.com"}, PHP: "8.4"},
		"app site":        {User: "blog", Domains: []string{"blog.example.com", "*.blog.example.com"}, App: &WebApp{Port: 3000}},
		"primary":         {User: "fwd", Domains: []string{"*.example.com", "example.com"}, Redirect: redirect},
		"redirect domain": {User: "app", Domains: []string{"app.example.net", "*.app.example.net"}, RedirectDomains: []string{"*.app.example.net"}, Proxy: proxy},
		"bad pattern":     {User: "fwd", Domains: []string{"example.com", "*.com"}, Redirect: redirect},
		"inner wildcard":  {User: "fwd", Domains: []string{"example.com", "a.*.example.com"}, Redirect: redirect},
	} {
		if err := (WebApplyRequest{Sites: []WebSite{s}}).Validate(); err == nil {
			t.Errorf("%s accepted", name)
		}
	}

	// The literal pattern belongs to one site only.
	twice := WebApplyRequest{Sites: []WebSite{
		{User: "fwd", Domains: []string{"example.com", "*.example.com"}, Redirect: redirect},
		{User: "fwd2", Domains: []string{"a.example.com", "*.example.com"}, Redirect: redirect},
	}}
	if err := twice.Validate(); err == nil {
		t.Error("wildcard on two sites accepted")
	}

	// Parked domains and the panel keep exact names.
	if err := (WebApplyRequest{Panel: &WebPanel{Domain: "*.example.com"}}).Validate(); err == nil {
		t.Error("wildcard panel address accepted")
	}

	if err := (WebApplyRequest{Parked: []WebParked{{Name: "p1", Domain: "*.example.com"}}}).Validate(); err == nil {
		t.Error("wildcard parked domain accepted")
	}
}

func TestTLSCSRWildcard(t *testing.T) {
	if err := (TLSCSRRequest{User: "fwd", Domains: []string{"example.com", "*.example.com"}}).Validate(); err != nil {
		t.Fatal(err)
	}

	for _, d := range []string{"*.com", "*", "a.*.example.com"} {
		if err := (TLSCSRRequest{User: "fwd", Domains: []string{"example.com", d}}).Validate(); err == nil {
			t.Errorf("%q accepted", d)
		}
	}
}
