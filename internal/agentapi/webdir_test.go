// SPDX-License-Identifier: AGPL-3.0-or-later

package agentapi

import (
	"strings"
	"testing"
)

func TestValidateWebDir(t *testing.T) {
	for _, ok := range []string{"", "public", "dist", "build/output", "a/b/c/d", "_site", "v1.2", "my-app"} {
		if err := ValidateWebDir(ok); err != nil {
			t.Errorf("%q: %v", ok, err)
		}
	}

	for _, bad := range []string{
		"/public", "public/", "..", ".", "a/../b", "a//b", "./a", "a/./b",
		"a b", "a\\b", "a;b", "a\x00b", "ä", "a/b/c/d/e", "$HOME",
		strings.Repeat("a", 65), strings.Repeat("abcdefgh/", 32),
	} {
		if err := ValidateWebDir(bad); err == nil {
			t.Errorf("%q accepted", bad)
		}
	}
}

func TestWebApplyStaticOptions(t *testing.T) {
	site := func(f func(*WebSite)) WebApplyRequest {
		s := WebSite{User: "docs", Domains: []string{"docs.example.com"}}
		f(&s)

		return WebApplyRequest{Sites: []WebSite{s}}
	}

	for name, r := range map[string]WebApplyRequest{
		"static spa":   site(func(s *WebSite) { s.SPA, s.AssetCache, s.WebDir = true, true, "dist" }),
		"php with dir": site(func(s *WebSite) { s.PHP, s.WebDir = "8.4", "public" }),
		"disabled spa": site(func(s *WebSite) { s.SPA, s.Disabled = true, true }),
		"php assets":   site(func(s *WebSite) { s.PHP, s.AssetCache = "8.4", true }),
	} {
		if err := r.Validate(); err != nil {
			t.Errorf("%s: %v", name, err)
		}
	}

	for name, r := range map[string]WebApplyRequest{
		"php spa": site(func(s *WebSite) { s.PHP, s.SPA = "8.4", true }),
		"bad dir": site(func(s *WebSite) { s.WebDir = "../other" }),
	} {
		if err := r.Validate(); err == nil {
			t.Errorf("%s accepted", name)
		}
	}
}

func TestValidateAssetCache(t *testing.T) {
	for _, c := range []struct {
		sec  int
		exts []string
	}{{0, nil}, {60, []string{"css"}}, {MaxAssetCacheSeconds, DefaultAssetCacheExts}, {3600, []string{"html", "json", "mp4"}}} {
		if err := ValidateAssetCache(c.sec, c.exts); err != nil {
			t.Errorf("%v: %v", c, err)
		}
	}

	many := make([]string, MaxAssetCacheExts+1)
	for i := range many {
		many[i] = "e" + strings.Repeat("x", i%9) + string(rune('a'+i%26))
	}

	for _, c := range []struct {
		sec  int
		exts []string
	}{
		{59, nil},
		{MaxAssetCacheSeconds + 1, nil},
		{-1, nil},
		{0, []string{".css"}},
		{0, []string{"CSS"}},
		{0, []string{"c s"}},
		{0, []string{""}},
		{0, []string{"abcdefghijk"}},
		{0, []string{"css", "css"}},
		{0, []string{"css", "php"}},
		{0, []string{"css|js"}},
		{0, []string{"css)$"}},
		{0, many},
	} {
		if err := ValidateAssetCache(c.sec, c.exts); err == nil {
			t.Errorf("%v accepted", c)
		}
	}
}
