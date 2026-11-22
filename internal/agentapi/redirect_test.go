// SPDX-License-Identifier: AGPL-3.0-or-later

package agentapi

import (
	"strings"
	"testing"
)

func TestParseRedirectTarget(t *testing.T) {
	own := []string{"old.example.com"}

	for _, ok := range []string{
		"https://new.example.com", "https://new.example.com/", "http://new.example.com:8080/a/b-c_d.e~f",
		"https://10.0.0.5/x", "https://[2001:db8::1]:8443/", "https://new.example.com/%C3%A4(1)",
	} {
		if _, err := ParseRedirectTarget(ok, own); err != nil {
			t.Errorf("%s: %v", ok, err)
		}
	}

	for _, bad := range []string{
		"", "new.example.com", "ftp://new.example.com", "https://", "https://user@new.example.com",
		"https://new.example.com/?a=1", "https://new.example.com/#x", "https://new.example.com/a b",
		"https://new.example.com/a;b", "https://new.example.com/$host", "https://new.example.com/a\"b",
		"https://new.example.com/{a}", "https://new.example.com:0/", "https://new.example.com:99999/",
		"https://new.example.com:/", "https://New.Example.com/", "https://old.example.com/x",
		"https://bad_host/", "https://" + strings.Repeat("a", MaxRedirectTarget) + ".com",
		"https://new.example.com/\n", "javascript://new.example.com/",
	} {
		if _, err := ParseRedirectTarget(bad, own); err == nil {
			t.Errorf("%q accepted", bad)
		}
	}
}

func TestWebRedirectInSite(t *testing.T) {
	site := func(f func(*WebSite)) WebApplyRequest {
		s := WebSite{User: "old", Domains: []string{"old.example.com"}, Redirect: &WebRedirect{Target: "https://new.example.com", Status: 301}}
		f(&s)

		return WebApplyRequest{Sites: []WebSite{s}}
	}

	if err := site(func(*WebSite) {}).Validate(); err != nil {
		t.Fatal(err)
	}

	for name, r := range map[string]WebApplyRequest{
		"php":    site(func(s *WebSite) { s.PHP = "8.4" }),
		"dir":    site(func(s *WebSite) { s.WebDir = "public" }),
		"cache":  site(func(s *WebSite) { s.AssetCache = true }),
		"status": site(func(s *WebSite) { s.Redirect.Status = 303 }),
		"loop":   site(func(s *WebSite) { s.Redirect.Target = "http://old.example.com/" }),
	} {
		if err := r.Validate(); err == nil {
			t.Errorf("%s accepted", name)
		}
	}
}
