// SPDX-License-Identifier: AGPL-3.0-or-later

package web

import (
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/maki-it/hostashi/internal/snippet"
)

func TestSnippetsAreServed(t *testing.T) {
	e := newEnv(t)
	ctx := t.Context()

	s := e.site(t, "alpha", "hello alpha", "alpha.example.com")
	s.Home = s.Root

	if err := os.MkdirAll(filepath.Join(s.Root, "media"), 0o755); err != nil {
		t.Fatal(err)
	}

	if err := os.WriteFile(filepath.Join(s.Root, "media", "a.txt"), []byte("media file"), 0o644); err != nil {
		t.Fatal(err)
	}

	s.Snippets = Snippets{
		Server: "location = /blocked { return 403; }\nlocation /files/ { alias " + s.Root + "/media/; }\nrewrite ^/old$ /new permanent;\n",
		Root:   "add_header X-Custom yes;\n",
	}

	other := e.site(t, "beta", "hello beta", "beta.example.com")

	if _, err := e.r.Recover(ctx); err != nil {
		t.Fatal(err)
	}

	if _, err := e.r.Apply(ctx, Desired{Sites: []Site{s, other}}); err != nil {
		t.Fatal(err)
	}

	if code, _ := e.fetchPath(t, "alpha.example.com", "/blocked"); code != http.StatusForbidden {
		t.Errorf("blocked: %d", code)
	}

	if code, body := e.fetchPath(t, "alpha.example.com", "/files/a.txt"); code != http.StatusOK || body != "media file" {
		t.Errorf("alias: %d %q", code, body)
	}

	if code, _ := e.fetchPath(t, "alpha.example.com", "/old"); code != http.StatusMovedPermanently {
		t.Errorf("rewrite: %d", code)
	}

	code, h, body := e.fetchFull(t, "alpha.example.com", "/")
	if code != http.StatusOK || body != "hello alpha" || h.Get("X-Custom") != "yes" {
		t.Errorf("location / snippet: %d %q %v", code, body, h)
	}

	if code, _ := e.fetchPath(t, "beta.example.com", "/blocked"); code != http.StatusNotFound {
		t.Errorf("other site got the snippet: %d", code)
	}

	// Disabled: the snippet is not rendered, the 503 page wins.
	s.Disabled = true
	if _, err := e.r.Apply(ctx, Desired{Sites: []Site{s, other}}); err != nil {
		t.Fatal(err)
	}

	if code, _ := e.fetchPath(t, "alpha.example.com", "/files/a.txt"); code != http.StatusServiceUnavailable {
		t.Errorf("disabled site served the snippet: %d", code)
	}
}

func TestSnippetsRender(t *testing.T) {
	n := &Nginx{
		Ctl: &testCtl{}, Listen: []string{"80"}, ListenTLS: []string{"443"},
		ControlSocket: "/run/x.sock", PIDFile: "/run/x.pid", ErrorLog: "/var/log/x", MimeTypes: "/etc/x",
	}

	m := &Maintenance{Page: "maint", Digest: digest64, Dir: "/var/lib/hostashi-pages/maint." + digest64}
	snips := Snippets{Server: `add_header X-A "a b";  # comment`, Root: "expires 1h;"}

	f, err := n.Render(Desired{Sites: []Site{
		{User: "php", Domains: []string{"a.example.com"}, Root: "/home/php/htdocs", Home: "/home/php", PHPSocket: "/run/php.sock", Snippets: snips},
		{User: "static", Domains: []string{"b.example.com"}, Root: "/home/static/htdocs", Home: "/home/static", Snippets: snips},
		{User: "maint", Domains: []string{"c.example.com"}, Root: "/home/maint/htdocs", Home: "/home/maint", Snippets: snips, Maintenance: m},
		{User: "off", Domains: []string{"d.example.com"}, Root: "/home/off/htdocs", Home: "/home/off", Snippets: snips, Disabled: true},
	}}, 1)
	if err != nil {
		t.Fatal(err)
	}

	for _, u := range []string{"php", "static"} {
		conf := string(f.Content["sites/"+u+".conf"])
		for _, want := range []string{"include snippets/" + u + "-server.conf;", "include snippets/" + u + "-root.conf;"} {
			if strings.Count(conf, want) != 1 {
				t.Errorf("%s: %q not exactly once:\n%s", u, want, conf)
			}
		}

		if got := string(f.Content["snippets/"+u+"-server.conf"]); !strings.Contains(got, "add_header X-A \"a b\";\n") || strings.Contains(got, "# comment") {
			t.Errorf("%s server snippet:\n%s", u, got)
		}

		if got := string(f.Content["snippets/"+u+"-root.conf"]); !strings.Contains(got, "expires 1h;\n") {
			t.Errorf("%s root snippet:\n%s", u, got)
		}
	}

	for _, u := range []string{"maint", "off"} {
		if conf := string(f.Content["sites/"+u+".conf"]); strings.Contains(conf, "snippets/") {
			t.Errorf("%s renders snippets:\n%s", u, conf)
		}

		if _, ok := f.Content["snippets/"+u+"-server.conf"]; ok {
			t.Errorf("%s has a snippet file", u)
		}
	}
}

func TestDangerousSnippetWritesNothing(t *testing.T) {
	e := newEnv(t)
	ctx := t.Context()

	if _, err := e.r.Recover(ctx); err != nil {
		t.Fatal(err)
	}

	s := e.site(t, "alpha", "x", "alpha.example.com")
	s.Home = s.Root
	log := filepath.Join(e.dir, "owned.log")
	s.Snippets = Snippets{Server: "access_log " + log + ";"}

	_, err := e.r.Apply(ctx, Desired{Sites: []Site{s}})

	var se *snippet.Error
	if !errors.Is(err, ErrInvalidDesired) || !errors.As(err, &se) {
		t.Fatalf("err = %v, want ErrInvalidDesired with a snippet error", err)
	}

	if _, err := os.Stat(log); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("log file exists: %v", err)
	}

	if n, _ := e.r.current(); n != 1 {
		t.Fatalf("current is %d, want 1", n)
	}
}

func TestSnippetsNeedHome(t *testing.T) {
	d := Desired{Sites: []Site{{User: "a", Domains: []string{"a.example.com"}, Root: "/home/a/htdocs", Snippets: Snippets{Root: "expires 1h;"}}}}
	if err := d.Validate(); err == nil || !strings.Contains(err.Error(), "invalid site") {
		t.Fatalf("err = %v", err)
	}
}
