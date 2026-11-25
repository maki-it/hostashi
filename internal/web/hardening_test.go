// SPDX-License-Identifier: AGPL-3.0-or-later

package web

import (
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/maki-it/hostashi/internal/agentapi"
)

// wpHardening are the WordPress rules of ADR-0017 Part 4.
var wpHardening = agentapi.WebHardening{
	ConfigFiles: true,
	NoPHPDirs:   []string{"wp-content/uploads"},
	DenyDirs:    []string{"private.d"},
	DenyFiles:   []string{"xmlrpc.php"},
	LimitFiles:  []string{"wp-login.php"},
}

func TestHardeningPHPSite(t *testing.T) {
	e := newEnv(t)
	ctx := t.Context()

	s := e.site(t, "alpha", "static", "alpha.example.com")
	s.PHPSocket = filepath.Join(e.dir, "php.sock")
	s.Hardening = wpHardening
	fcgiServer(t, s.PHPSocket)

	for name, body := range map[string]string{
		"index.php": "<?php", "xmlrpc.php": "<?php", "wp-login.php": "<?php",
		"wp-content/uploads/2026/shell.php": "<?php", "wp-content/uploads/2026/photo.jpg": "jpg",
		"wp-content/uploads/x.PHAR": "phar", "wp-content/plugins/a/a.php": "<?php",
		"backup.sql": "dump", "db.sql.gz": "dump", "wp-config.php~": "<?php", "wp-config.php.bak": "<?php",
		"wp-content/debug.log": "log", "private.d/notes.txt": "x", "private.dx/ok.txt": "ok",
		".git/hook.php": "<?php", ".well-known/security.txt": "contact",
	} {
		p := filepath.Join(s.Root, name)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}

		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	if _, err := e.r.Recover(ctx); err != nil {
		t.Fatal(err)
	}

	if _, err := e.r.Apply(ctx, Desired{Sites: []Site{s}}); err != nil {
		t.Fatal(err)
	}

	for p, want := range map[string]string{
		"/":                                  "php index.php /",
		"/wp-content/plugins/a/a.php":        "php a.php /wp-content/plugins/a/a.php",
		"/wp-content/uploads/2026/photo.jpg": "jpg",
		"/wp-content/uploads/2026/shell.php": "404",
		"/WP-CONTENT/uploads/2026/shell.php": "404",
		"/wp-content/uploads/x.PHAR":         "404",
		"/xmlrpc.php":                        "404",
		"/xmlrpc.php?x=1":                    "404",
		"/wp-login.php":                      "php wp-login.php /wp-login.php",
		"/backup.sql":                        "404",
		"/db.sql.gz":                         "404",
		"/BACKUP.SQL":                        "404",
		"/wp-config.php~":                    "404",
		"/wp-config.php.bak":                 "404",
		"/wp-content/debug.log":              "404",
		"/private.d":                         "404",
		"/private.d/notes.txt":               "404",
		"/private.dx/ok.txt":                 "ok",
		"/.git/hook.php":                     "404", // before: reached PHP
		"/.well-known/security.txt":          "contact",
	} {
		code, body := e.fetchPath(t, "alpha.example.com", p)

		got := body
		if code == http.StatusNotFound {
			got = "404"
		} else if code != http.StatusOK {
			got = strconv.Itoa(code)
		}

		if got != want {
			t.Errorf("%s: %d %q, want %q", p, code, body, want)
		}
	}

	// The login is rate limited per address: the burst passes, then 429.
	limited := 0

	for range agentapi.LimitBurst + 3 {
		if code, _ := e.fetchPath(t, "alpha.example.com", "/wp-login.php"); code == http.StatusTooManyRequests {
			limited++
		}
	}

	if limited == 0 {
		t.Fatal("wp-login.php was never rate limited")
	}

	// Other PHP files are not.
	for range agentapi.LimitBurst + 3 {
		if code, _ := e.fetchPath(t, "alpha.example.com", "/index.php"); code != http.StatusOK {
			t.Fatalf("index.php: %d", code)
		}
	}
}

func TestHardeningStaticSite(t *testing.T) {
	e := newEnv(t)
	ctx := t.Context()

	s := e.site(t, "alpha", "static", "alpha.example.com")
	s.Hardening = agentapi.WebHardening{ConfigFiles: true, DenyFiles: []string{"secret.txt"}}

	for name, body := range map[string]string{"dump.sql": "dump", "secret.txt": "s", "public.txt": "p"} {
		if err := os.WriteFile(filepath.Join(s.Root, name), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	if _, err := e.r.Recover(ctx); err != nil {
		t.Fatal(err)
	}

	if _, err := e.r.Apply(ctx, Desired{Sites: []Site{s}}); err != nil {
		t.Fatal(err)
	}

	for p, want := range map[string]int{"/dump.sql": 404, "/secret.txt": 404, "/public.txt": 200, "/": 200} {
		if code, _ := e.fetchPath(t, "alpha.example.com", p); code != want {
			t.Errorf("%s: %d, want %d", p, code, want)
		}
	}

	// Without the switch, a dump is a file like any other.
	s.Hardening = agentapi.WebHardening{}
	if _, err := e.r.Apply(ctx, Desired{Sites: []Site{s}}); err != nil {
		t.Fatal(err)
	}

	if code, _ := e.fetchPath(t, "alpha.example.com", "/dump.sql"); code != http.StatusOK {
		t.Errorf("dump.sql without the switch: %d", code)
	}
}

func TestHardeningRender(t *testing.T) {
	n := &Nginx{Ctl: &testCtl{}, Listen: []string{"80"}, ControlSocket: "/run/x.sock", PIDFile: "/run/x.pid", ErrorLog: "/var/log/x", MimeTypes: "/etc/x"}
	s := Site{User: "alpha", Domains: []string{"alpha.example.com"}, Root: "/srv/alpha/htdocs", PHPSocket: "/run/php/alpha.sock", Hardening: wpHardening}

	f, err := n.Render(Desired{Sites: []Site{s}}, 2)
	if err != nil {
		t.Fatal(err)
	}

	conf := string(f.Content["sites/alpha.conf"])
	for _, want := range []string{
		"limit_req_zone $binary_remote_addr zone=hostashi_limit_616c706861:1m rate=12r/m;",
		`location ~* "^/(?:wp-content/uploads)/.*\.(?:php|phtml|phar)$"`,
		`location ~ "^/(?:private\.d)(?:/|$)"`,
		"location = /xmlrpc.php {",
		"limit_req zone=hostashi_limit_616c706861 burst=10 nodelay;",
		`(?:\.sql|\.sql\.gz|`,
	} {
		if !strings.Contains(conf, want) {
			t.Errorf("missing %q in\n%s", want, conf)
		}
	}

	// The rules come before the PHP location, which would match first.
	if strings.Index(conf, "/\\.(?!well-known/)") > strings.Index(conf, `location ~ \.php$`) {
		t.Error("hidden-file rule after the PHP location")
	}

	for _, c := range []string{`\t`, `\r`, `\n`} {
		if strings.Contains(conf, c) {
			t.Errorf("config holds %q, which nginx turns into a control character", c)
		}
	}

	// A disabled site serves nothing, so it needs no rules and no zone.
	s.Disabled = true
	s.PHPSocket = ""
	s.Hardening = agentapi.WebHardening{ConfigFiles: true}

	f, err = n.Render(Desired{Sites: []Site{s}}, 3)
	if err != nil {
		t.Fatal(err)
	}

	if conf := string(f.Content["sites/alpha.conf"]); strings.Contains(conf, "well-known/)") || strings.Contains(conf, `\.sql`) {
		t.Errorf("disabled site renders hardening:\n%s", conf)
	}
}

func TestHardeningValidate(t *testing.T) {
	base := Site{User: "alpha", Domains: []string{"alpha.example.com"}, Root: "/srv/alpha/htdocs"}

	for name, s := range map[string]Site{
		"no php dirs without PHP": func() Site { s := base; s.Hardening.NoPHPDirs = []string{"uploads"}; return s }(),
		"limit without PHP":       func() Site { s := base; s.Hardening.LimitFiles = []string{"login.php"}; return s }(),
		"absolute path":           func() Site { s := base; s.Hardening.DenyFiles = []string{"/etc/passwd"}; return s }(),
		"parent dir":              func() Site { s := base; s.Hardening.DenyDirs = []string{"a/../b"}; return s }(),
		"nginx syntax":            func() Site { s := base; s.Hardening.DenyFiles = []string{"a;return 200 x"}; return s }(),
		"quote":                   func() Site { s := base; s.Hardening.DenyDirs = []string{`a"b`}; return s }(),
		"regexp":                  func() Site { s := base; s.Hardening.DenyDirs = []string{"(.*)"}; return s }(),
		"app site": func() Site {
			s := base
			s.App = &agentapi.WebApp{Port: 3000}
			s.Hardening.ConfigFiles = true

			return s
		}(),
	} {
		if err := (Desired{Sites: []Site{s}}).Validate(); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}

	ok := base
	ok.Hardening = agentapi.WebHardening{ConfigFiles: true, DenyDirs: []string{"vendor"}, DenyFiles: []string{"README.md"}}

	if err := (Desired{Sites: []Site{ok}}).Validate(); err != nil {
		t.Fatal(err)
	}
}
