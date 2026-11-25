// SPDX-License-Identifier: AGPL-3.0-or-later

package agentapi

import (
	"strings"
	"testing"
)

func TestWebHardeningValidate(t *testing.T) {
	many := make([]string, MaxHardeningPaths+1)
	for i := range many {
		many[i] = "d" + strings.Repeat("x", i+1)
	}

	for name, c := range map[string]struct {
		h   WebHardening
		php bool
	}{
		"empty path":         {WebHardening{DenyFiles: []string{""}}, true},
		"leading slash":      {WebHardening{DenyFiles: []string{"/xmlrpc.php"}}, true},
		"trailing slash":     {WebHardening{DenyDirs: []string{"vendor/"}}, true},
		"parent dir":         {WebHardening{NoPHPDirs: []string{"../etc"}}, true},
		"space":              {WebHardening{DenyFiles: []string{"a b"}}, true},
		"semicolon":          {WebHardening{DenyFiles: []string{"a;"}}, true},
		"brace":              {WebHardening{DenyDirs: []string{"a{"}}, true},
		"dollar":             {WebHardening{DenyDirs: []string{"$uri"}}, true},
		"too deep":           {WebHardening{DenyDirs: []string{"a/b/c/d/e"}}, true},
		"twice":              {WebHardening{DenyDirs: []string{"a", "a"}}, true},
		"too many":           {WebHardening{DenyDirs: many}, true},
		"limit not php":      {WebHardening{LimitFiles: []string{"login.html"}}, true},
		"limit and deny":     {WebHardening{LimitFiles: []string{"x.php"}, DenyFiles: []string{"x.php"}}, true},
		"no php dir, no PHP": {WebHardening{NoPHPDirs: []string{"uploads"}}, false},
		"limit, no PHP":      {WebHardening{LimitFiles: []string{"x.php"}}, false},
	} {
		if err := c.h.Validate(c.php); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}

	ok := WebHardening{ConfigFiles: true, NoPHPDirs: []string{"wp-content/uploads"}, DenyDirs: []string{"var", "vendor"}, DenyFiles: []string{"xmlrpc.php"}, LimitFiles: []string{"wp-login.php"}}
	if err := ok.Validate(true); err != nil {
		t.Fatal(err)
	}

	if ok.IsZero() || !(WebHardening{}).IsZero() {
		t.Fatal("IsZero")
	}
}

func TestWebApplyHardening(t *testing.T) {
	h := &WebHardening{ConfigFiles: true}

	for name, s := range map[string]WebSite{
		"redirect": {User: "alpha", Domains: []string{"a.example.com"}, Redirect: &WebRedirect{Target: "https://b.example.com", Status: 301}, Hardening: h},
		"app":      {User: "alpha", Domains: []string{"a.example.com"}, App: &WebApp{Port: 3000}, Hardening: h},
		"no PHP":   {User: "alpha", Domains: []string{"a.example.com"}, Hardening: &WebHardening{LimitFiles: []string{"x.php"}}},
	} {
		if err := (WebApplyRequest{Sites: []WebSite{s}}).Validate(); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}

	ok := WebSite{User: "alpha", Domains: []string{"a.example.com"}, PHP: "8.4", Hardening: &WebHardening{LimitFiles: []string{"wp-login.php"}}}
	if err := (WebApplyRequest{Sites: []WebSite{ok}}).Validate(); err != nil {
		t.Fatal(err)
	}
}
