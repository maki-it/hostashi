// SPDX-License-Identifier: AGPL-3.0-or-later

package plan_test

import (
	"reflect"
	"testing"

	"github.com/maki-it/hostashi/internal/cloudpanel/plan"
)

func TestWWWRedirect(t *testing.T) {
	cases := []struct {
		name               string
		domains, redirects []string
		mode               string
		rest               []string
	}{
		{"none", []string{"example.com"}, nil, "", nil},
		{"www to bare", []string{"example.com"}, []string{"www.example.com"}, "to-non-www", nil},
		{"bare to www", []string{"www.example.com"}, []string{"example.com"}, "to-www", nil},
		{"other host stays", []string{"example.com"}, []string{"www.example.com", "example.net"}, "to-non-www", []string{"example.net"}},
		{"no partner", []string{"example.com"}, []string{"example.net"}, "", []string{"example.net"}},
		// An alias pair that the old site served must not start to redirect.
		{"served alias pair", []string{"example.com", "example.de", "www.example.de"}, []string{"www.example.com"}, "", []string{"www.example.com"}},
		// A www host that went to the primary domain, not to its own bare name.
		{"other target", []string{"example.com", "example.de"}, []string{"www.example.de"}, "", []string{"www.example.de"}},
	}

	for _, c := range cases {
		mode, rest := plan.WWWRedirect(c.domains, c.redirects)
		if mode != c.mode || !reflect.DeepEqual(rest, c.rest) {
			t.Errorf("%s: got %q %v, want %q %v", c.name, mode, rest, c.mode, c.rest)
		}
	}
}

func TestBuildTakesTheWWWRedirect(t *testing.T) {
	s := base()
	s.RedirectFrom = []string{"www.shop.example.com"}

	got := one(s, plan.Target{PHP: []string{"8.4"}})
	if got.Action != plan.ActionImport || got.WWWRedirect != "to-non-www" || hasCode(got, "redirects") {
		t.Fatalf("got %s %q %v", got.Action, got.WWWRedirect, got.Reasons)
	}
}
