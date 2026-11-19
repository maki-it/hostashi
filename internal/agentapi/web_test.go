// SPDX-License-Identifier: AGPL-3.0-or-later

package agentapi

import (
	"strings"
	"testing"
)

func TestValidateDomain(t *testing.T) {
	good := []string{"example.com", "www.example.com", "a-b.example.co.uk", "xn--bcher-kva.example", "1.example.com", "a.b"}
	for _, d := range good {
		if err := ValidateDomain(d); err != nil {
			t.Errorf("%q rejected: %v", d, err)
		}
	}

	bad := []string{
		"", "example", "Example.com", "example.com.", ".example.com", "a..example.com", "-a.example.com",
		"a-.example.com", "*.example.com", "exa mple.com", "example.com;", "example.com\n", "a_b.example.com",
		"127.0.0.1", "10.0.0.1", "_hostashi_control", "$host.example.com", "{a}.example.com",
		strings.Repeat("a", 64) + ".example.com", strings.Repeat("abcdefghi.", 26) + "com",
	}
	for _, d := range bad {
		if err := ValidateDomain(d); err == nil {
			t.Errorf("%q accepted", d)
		}
	}
}

func TestWebApplyRequestValidate(t *testing.T) {
	ok := WebApplyRequest{Sites: []WebSite{
		{User: "blog", Domains: []string{"blog.example.com", "www.blog.example.com"}},
		{User: "shop", Domains: []string{"shop.example.com"}, PHP: "8.4"},
		{User: "old", Domains: []string{"old.example.com"}, Disabled: true},
	}}
	if err := ok.Validate(); err != nil {
		t.Fatal(err)
	}

	for name, r := range map[string]WebApplyRequest{
		"duplicate user":    {Sites: []WebSite{{User: "blog", Domains: []string{"a.example.com"}}, {User: "blog", Domains: []string{"b.example.com"}}}},
		"duplicate domain":  {Sites: []WebSite{{User: "blog", Domains: []string{"a.example.com"}}, {User: "shop", Domains: []string{"a.example.com"}}}},
		"no domain":         {Sites: []WebSite{{User: "blog"}}},
		"bad user":          {Sites: []WebSite{{User: "root", Domains: []string{"a.example.com"}}}},
		"too many domains":  {Sites: []WebSite{{User: "blog", Domains: make([]string, MaxDomainsPerSite+1)}}},
		"unknown php":       {Sites: []WebSite{{User: "blog", Domains: []string{"a.example.com"}, PHP: "8.0"}}},
		"php path":          {Sites: []WebSite{{User: "blog", Domains: []string{"a.example.com"}, PHP: "../8.4"}}},
		"disabled with php": {Sites: []WebSite{{User: "blog", Domains: []string{"a.example.com"}, PHP: "8.4", Disabled: true}}},
	} {
		if err := r.Validate(); err == nil {
			t.Errorf("%s accepted", name)
		}
	}
}
