// SPDX-License-Identifier: AGPL-3.0-or-later

package web

import (
	"net/http"
	"strings"
	"testing"

	"github.com/maki-it/hostashi/internal/agentapi"
)

func TestRedirectRender(t *testing.T) {
	n := &Nginx{Ctl: &testCtl{}, Listen: []string{"80"}, ControlSocket: "/run/x.sock", PIDFile: "/run/x.pid", ErrorLog: "/var/log/x", MimeTypes: "/etc/x"}

	f, err := n.Render(Desired{Sites: []Site{
		{User: "old", Domains: []string{"old.example.com"}, Redirect: &agentapi.WebRedirect{Target: "https://new.example.com/", Status: 301, KeepPath: true}},
		{User: "one", Domains: []string{"one.example.com"}, Redirect: &agentapi.WebRedirect{Target: "https://new.example.com/landing", Status: 302}},
	}}, 1)
	if err != nil {
		t.Fatal(err)
	}

	old := string(f.Content["sites/old.conf"])
	for _, want := range []string{"return 301 https://new.example.com$request_uri;", "location = /.hostashi/health"} {
		if !strings.Contains(old, want) {
			t.Errorf("old.conf lacks %q:\n%s", want, old)
		}
	}

	if strings.Contains(old, "root ") || strings.Contains(old, "fastcgi") {
		t.Errorf("old.conf serves files:\n%s", old)
	}

	if one := string(f.Content["sites/one.conf"]); !strings.Contains(one, "return 302 https://new.example.com/landing;") {
		t.Errorf("one.conf:\n%s", one)
	}

	r := func(target string) *agentapi.WebRedirect { return &agentapi.WebRedirect{Target: target, Status: 301} }

	for name, s := range map[string]Site{
		"with root":    {User: "a", Domains: []string{"a.example.com"}, Root: "/srv/a", Redirect: r("https://b.example.com")},
		"with php":     {User: "a", Domains: []string{"a.example.com"}, PHPSocket: "/run/a.sock", Redirect: r("https://b.example.com")},
		"with cache":   {User: "a", Domains: []string{"a.example.com"}, AssetCache: true, Redirect: r("https://b.example.com")},
		"loop":         {User: "a", Domains: []string{"a.example.com"}, Redirect: r("https://a.example.com/x")},
		"injection":    {User: "a", Domains: []string{"a.example.com"}, Redirect: r("https://b.example.com/; return 200 x")},
		"variable":     {User: "a", Domains: []string{"a.example.com"}, Redirect: r("https://b.example.com/$host")},
		"bad status":   {User: "a", Domains: []string{"a.example.com"}, Redirect: &agentapi.WebRedirect{Target: "https://b.example.com", Status: 200}},
		"no root":      {User: "a", Domains: []string{"a.example.com"}},
		"with snippet": {User: "a", Domains: []string{"a.example.com"}, Snippets: Snippets{Root: "expires 1d;"}, Redirect: r("https://b.example.com")},
	} {
		if err := (Desired{Sites: []Site{s}}).Validate(); err == nil {
			t.Errorf("%s accepted", name)
		}
	}
}

// TestRedirectServed checks the redirects with the real nginx, next to a
// site with files.
func TestRedirectServed(t *testing.T) {
	e := newEnv(t)
	ctx := t.Context()

	if _, err := e.r.Recover(ctx); err != nil {
		t.Fatal(err)
	}

	plain := e.site(t, "plain", "plain index", "plain.example.com")
	keep := Site{User: "keep", Domains: []string{"keep.example.com"}, Redirect: &agentapi.WebRedirect{Target: "https://new.example.com/", Status: 308, KeepPath: true}}
	fixed := Site{User: "fixed", Domains: []string{"fixed.example.com"}, Redirect: &agentapi.WebRedirect{Target: "https://new.example.com/landing", Status: 301}}

	if _, err := e.r.Apply(ctx, Desired{Sites: []Site{plain, keep, fixed}}); err != nil {
		t.Fatal(err)
	}

	if code, h, _ := e.fetchFull(t, "keep.example.com", "/a/b?x=1"); code != http.StatusPermanentRedirect || h.Get("Location") != "https://new.example.com/a/b?x=1" {
		t.Errorf("keep: %d %v", code, h)
	}

	if code, h, _ := e.fetchFull(t, "fixed.example.com", "/a/b?x=1"); code != http.StatusMovedPermanently || h.Get("Location") != "https://new.example.com/landing" {
		t.Errorf("fixed: %d %v", code, h)
	}

	if code, body := e.fetch(t, "plain.example.com"); code != http.StatusOK || body != "plain index" {
		t.Errorf("plain: %d %q", code, body)
	}
}
