// SPDX-License-Identifier: AGPL-3.0-or-later

package agentapi

import (
	"fmt"
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
			[]WWWName{{From: "example.com", To: "www.example.com"}, {From: "example.de", To: "www.example.de"}},
		},
		{
			"to-non-www",
			[]string{"example.com", "shop.example.com", "example.de"},
			[]WWWName{{From: "www.example.com", To: "example.com"}, {From: "www.example.de", To: "example.de"}},
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

func TestSplitRedirects(t *testing.T) {
	for _, c := range []struct {
		domains, redirect []string
		mode              string
		serve, out        string
	}{
		{[]string{"a.example"}, nil, "", "[a.example]", "[]"},
		{[]string{"a.example", "b.example"}, []string{"b.example"}, "", "[a.example]", "[{b.example a.example true}]"},
		// The www redirect moves the target to www, the old name goes there directly.
		{[]string{"a.example", "www.a.example", "b.example"}, []string{"b.example"}, WWWRedirectToWWW, "[www.a.example]", "[{a.example www.a.example false} {b.example www.a.example true}]"},
		// A redirect domain never forms a www pair.
		{[]string{"a.example", "www.a.example"}, []string{"www.a.example"}, WWWRedirectToNonWWW, "[a.example]", "[{www.a.example a.example true}]"},
	} {
		serve, out := SplitRedirects(c.domains, c.mode, c.redirect)
		if got := fmt.Sprint(out); fmt.Sprint(serve) != c.serve || got != c.out && (len(out) != 0 || c.out != "[]") {
			t.Errorf("SplitRedirects(%v, %q, %v) = %v %v, want %s %s", c.domains, c.mode, c.redirect, serve, out, c.serve, c.out)
		}
	}
}

func TestValidateRedirectDomains(t *testing.T) {
	d := []string{"a.example", "b.example"}
	if err := ValidateRedirectDomains(d, []string{"b.example"}); err != nil {
		t.Error(err)
	}

	for _, r := range [][]string{{"a.example"}, {"c.example"}, {"b.example", "b.example"}} {
		if err := ValidateRedirectDomains(d, r); err == nil {
			t.Errorf("%v accepted", r)
		}
	}
}
