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
	} {
		if err := r.Validate(); err != nil {
			t.Errorf("%s: %v", name, err)
		}
	}

	for name, r := range map[string]WebApplyRequest{
		"php spa":         site(func(s *WebSite) { s.PHP, s.SPA = "8.4", true }),
		"php asset cache": site(func(s *WebSite) { s.PHP, s.AssetCache = "8.4", true }),
		"bad dir":         site(func(s *WebSite) { s.WebDir = "../other" }),
	} {
		if err := r.Validate(); err == nil {
			t.Errorf("%s accepted", name)
		}
	}
}
