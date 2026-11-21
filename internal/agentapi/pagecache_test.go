// SPDX-License-Identifier: AGPL-3.0-or-later

package agentapi

import (
	"strings"
	"testing"
)

func TestWebPageCacheValidate(t *testing.T) {
	ok := WebPageCache{Preset: PageCacheWordPress, Minutes: 10, SizeMB: 256}
	if err := ok.Validate(); err != nil {
		t.Fatal(err)
	}

	for name, c := range map[string]WebPageCache{
		"empty preset":   {Minutes: 10, SizeMB: 256},
		"unknown preset": {Preset: "varnish", Minutes: 10, SizeMB: 256},
		"zero minutes":   {Preset: PageCachePHP, SizeMB: 256},
		"odd minutes":    {Preset: PageCachePHP, Minutes: 7, SizeMB: 256},
		"zero size":      {Preset: PageCachePHP, Minutes: 10},
		"odd size":       {Preset: PageCachePHP, Minutes: 10, SizeMB: 100},
		"huge size":      {Preset: PageCachePHP, Minutes: 10, SizeMB: 8192},
		"bad exclude":    {Preset: PageCachePHP, Minutes: 10, SizeMB: 256, Exclude: []string{"/a b"}},
	} {
		if err := c.Validate(); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
}

func TestValidatePageCacheExcludes(t *testing.T) {
	good := []string{"/", "/*", "/shop", "/shop/", "/shop/*", "/warenkorb*", "/a.b/c-d_e~f", "/%C3%BC", "/" + strings.Repeat("a", 200)}
	if err := ValidatePageCacheExcludes(good); err != nil {
		t.Fatal(err)
	}

	for _, p := range []string{
		"",
		"shop",
		"/shop*/x",
		"/sh*op",
		"/**",
		"/a/../b",
		"/..",
		"/../*",
		"/./a",
		"//a",
		"/a//b",
		"/a b",
		"/a;b",
		"/a\"b",
		"/a'b",
		"/a{b",
		"/a}b",
		"/a$b",
		"/a?b",
		"/a\\b",
		"/a\nb",
		"/a\x00b",
		"/ä",
		"/" + strings.Repeat("a", 201),
	} {
		if err := ValidatePageCacheExcludes([]string{p}); err == nil {
			t.Errorf("%q accepted", p)
		}
	}

	if err := ValidatePageCacheExcludes([]string{"/a", "/a"}); err == nil {
		t.Error("duplicate accepted")
	}

	many := make([]string, MaxPageCacheExcludes+1)
	for i := range many {
		many[i] = "/p" + strings.Repeat("x", i)
	}

	if err := ValidatePageCacheExcludes(many); err == nil {
		t.Error("too many accepted")
	}

	if err := ValidatePageCacheExcludes(many[:MaxPageCacheExcludes]); err != nil {
		t.Errorf("limit refused: %v", err)
	}
}

func TestWebSitePageCacheNeedsPHP(t *testing.T) {
	c := &WebPageCache{Preset: PageCachePHP, Minutes: 10, SizeMB: 256}
	site := WebSite{User: "alpha", Domains: []string{"alpha.example.com"}, PageCache: c}

	if err := (WebApplyRequest{Sites: []WebSite{site}}).Validate(); err == nil {
		t.Fatal("page cache without PHP accepted")
	}

	site.PHP = "8.4"
	if err := (WebApplyRequest{Sites: []WebSite{site}}).Validate(); err != nil {
		t.Fatal(err)
	}

	site.PageCache = &WebPageCache{Preset: PageCachePHP, Minutes: 2, SizeMB: 256}
	if err := (WebApplyRequest{Sites: []WebSite{site}}).Validate(); err == nil {
		t.Fatal("invalid page cache accepted")
	}
}
