// SPDX-License-Identifier: AGPL-3.0-or-later

package agentapi

import (
	"slices"
	"strings"
	"testing"
)

func TestCertCovers(t *testing.T) {
	names := []string{"*.example.com", "example.org", "Shop.Example.NET"}

	for d, want := range map[string]bool{
		"example.org":      true,
		"a.example.com":    true,
		"A.Example.com":    true,
		"shop.example.net": true,
		"example.com":      false, // a wildcard needs one more label
		"a.b.example.com":  false, // and exactly one
		".example.com":     false,
		"www.example.org":  false,
		"badexample.com":   false,
	} {
		if got := CertCovers(names, d); got != want {
			t.Errorf("%s: %v, want %v", d, got, want)
		}
	}

	if got := MissingNames(names, []string{"a.example.com", "example.com", "x.y.example.com"}); !slices.Equal(got, []string{"example.com", "x.y.example.com"}) {
		t.Fatalf("missing %v", got)
	}

	if got := MissingNames(nil, nil); got == nil || len(got) != 0 {
		t.Fatalf("missing of nothing %v", got)
	}
}

func TestTLSUploadValidate(t *testing.T) {
	ok := TLSUploadRequest{User: "blog", Chain: "x", Domains: []string{"blog.example.com"}}
	if err := ok.Validate(); err != nil {
		t.Fatal(err)
	}

	for name, r := range map[string]TLSUploadRequest{
		"bad user":    {User: "../x", Chain: "x", Domains: ok.Domains},
		"no chain":    {User: "blog", Domains: ok.Domains},
		"big chain":   {User: "blog", Chain: strings.Repeat("x", MaxCertChain+1), Domains: ok.Domains},
		"big key":     {User: "blog", Chain: "x", Key: strings.Repeat("x", MaxCertKey+1), Domains: ok.Domains},
		"no domains":  {User: "blog", Chain: "x"},
		"twice":       {User: "blog", Chain: "x", Domains: []string{"a.example.com", "a.example.com"}},
		"bad pattern": {User: "blog", Chain: "x", Domains: []string{"*.com"}},
	} {
		if r.Validate() == nil {
			t.Errorf("%s accepted", name)
		}
	}

	for _, n := range []string{"_panel", "_park-3", "blog"} {
		if (TLSNameRequest{User: n}).Validate() != nil {
			t.Errorf("%s refused", n)
		}
	}
}
