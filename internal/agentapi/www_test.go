// SPDX-License-Identifier: AGPL-3.0-or-later

package agentapi

import (
	"reflect"
	"strings"
	"testing"
)

func TestValidateWWWRedirect(t *testing.T) {
	for _, ok := range []string{"", "to-www", "to-non-www"} {
		if err := ValidateWWWRedirect(ok); err != nil {
			t.Errorf("%q: %v", ok, err)
		}
	}

	for _, bad := range []string{"off", "www", "TO-WWW", "to-www ", "to-non-www\n", "apex"} {
		if err := ValidateWWWRedirect(bad); err == nil {
			t.Errorf("%q accepted", bad)
		}
	}
}

func TestWWWPairs(t *testing.T) {
	cases := []struct {
		domains []string
		want    []WWWPair
	}{
		{nil, nil},
		{[]string{"example.com"}, nil},
		{[]string{"www.example.com"}, nil},
		{[]string{"www.example.com", "example.com"}, []WWWPair{{"example.com", "www.example.com"}}},
		// Several pairs, and a domain without a partner.
		{
			[]string{"example.com", "shop.example.com", "example.de", "www.example.de", "www.example.com"},
			[]WWWPair{{"example.com", "www.example.com"}, {"example.de", "www.example.de"}},
		},
		// www.www.* is never part of a pair: no chains.
		{[]string{"example.com", "www.example.com", "www.www.example.com"}, []WWWPair{{"example.com", "www.example.com"}}},
		// A sub domain pairs with its own www name only.
		{[]string{"sub.example.com", "www.example.com"}, nil},
		{[]string{"sub.example.com", "www.sub.example.com"}, []WWWPair{{"sub.example.com", "www.sub.example.com"}}},
	}

	for _, c := range cases {
		if got := WWWPairs(c.domains); !reflect.DeepEqual(got, c.want) {
			t.Errorf("%v: got %v, want %v", c.domains, got, c.want)
		}
	}
}

func TestWWWMissing(t *testing.T) {
	long := strings.Repeat("a", 63) + "." + strings.Repeat("b", 63) + "." + strings.Repeat("c", 63) + "." + strings.Repeat("d", 61)

	cases := []struct {
		domains, want []string
	}{
		{[]string{"example.com"}, []string{"www.example.com"}},
		{[]string{"www.example.com"}, []string{"example.com"}},
		{[]string{"example.com", "www.example.com"}, nil},
		{[]string{"example.com", "www.example.com", "example.de"}, []string{"www.example.de"}},
		// "de" is not a domain, www.www.* has no partner, a too long name
		// gets none either.
		{[]string{"www.de", "www.www.example.com", long}, nil},
	}

	for _, c := range cases {
		if got := WWWMissing(c.domains); !reflect.DeepEqual(got, c.want) {
			t.Errorf("%v: got %v, want %v", c.domains, got, c.want)
		}
	}
}

func TestSplitWWW(t *testing.T) {
	d := []string{"example.com", "www.example.com", "shop.example.com", "www.example.de", "example.de"}

	cases := []struct {
		mode  string
		serve []string
		redir []WWWName
	}{
		{"", d, nil},
		{
			"to-www",
			[]string{"www.example.com", "shop.example.com", "www.example.de"},
			[]WWWName{{"example.com", "www.example.com"}, {"example.de", "www.example.de"}},
		},
		{
			"to-non-www",
			[]string{"example.com", "shop.example.com", "example.de"},
			[]WWWName{{"www.example.com", "example.com"}, {"www.example.de", "example.de"}},
		},
	}

	for _, c := range cases {
		serve, redir := SplitWWW(d, c.mode)
		if !reflect.DeepEqual(serve, c.serve) || !reflect.DeepEqual(redir, c.redir) {
			t.Errorf("%q: got %v %v, want %v %v", c.mode, serve, redir, c.serve, c.redir)
		}
	}

	// No pair: every domain is served, whatever the mode.
	one := []string{"example.com", "shop.example.com"}
	if serve, redir := SplitWWW(one, "to-www"); !reflect.DeepEqual(serve, one) || redir != nil {
		t.Errorf("no pair: got %v %v", serve, redir)
	}

	// Every pair always leaves its target served, so a site always has a
	// served name.
	for _, mode := range []string{"to-www", "to-non-www"} {
		serve, _ := SplitWWW([]string{"www.example.com", "example.com"}, mode)
		if len(serve) != 1 {
			t.Errorf("%s: served %v", mode, serve)
		}
	}
}

func TestWebApplyRequestChecksWWWRedirect(t *testing.T) {
	r := WebApplyRequest{Sites: []WebSite{{User: "blog", Domains: []string{"blog.example.com", "www.blog.example.com"}, WWWRedirect: "to-www"}}}
	if err := r.Validate(); err != nil {
		t.Fatal(err)
	}

	r.Sites[0].WWWRedirect = "both"
	if err := r.Validate(); err == nil {
		t.Error("unknown mode accepted")
	}

	r.Sites[0].WWWRedirect = "to-non-www"

	r.Sites[0].Redirect = &WebRedirect{Target: "https://example.org/", Status: 301}
	if err := r.Validate(); err == nil || !strings.Contains(err.Error(), "www redirect") {
		t.Errorf("redirect site: got %v", err)
	}
}
