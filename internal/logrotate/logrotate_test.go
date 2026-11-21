// SPDX-License-Identifier: AGPL-3.0-or-later

package logrotate

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func cfg() Config {
	return Config{SiteLogDir: "/var/log/nginx/sites", ServerLogDir: "/var/log/nginx/hostashi", NginxPID: "/run/nginx.pid", Days: 7}
}

func TestRender(t *testing.T) {
	b, err := Render(cfg())
	if err != nil {
		t.Fatal(err)
	}

	for _, want := range []string{
		"/var/log/nginx/sites/*/*.log /var/log/nginx/hostashi/*.log {",
		"    rotate 7\n", "    maxage 7\n", "    daily\n", "    delaycompress\n",
		`[ ! -s /run/nginx.pid ] || kill -USR1 "$(cat /run/nginx.pid)"`,
	} {
		if !strings.Contains(string(b), want) {
			t.Errorf("missing %q in\n%s", want, b)
		}
	}

	if d, ok := Days(b); !ok || d != 7 {
		t.Errorf("Days = %d %v", d, ok)
	}
}

func TestRenderRejects(t *testing.T) {
	for _, d := range []int{0, -1, 366, 100000} {
		c := cfg()
		c.Days = d

		if _, err := Render(c); !errors.Is(err, ErrDays) {
			t.Errorf("days %d: %v", d, err)
		}
	}

	for _, p := range []string{"", "relative", "/a b", "/a;b", "/a/../b", "/a/$(id)", "/a\nb", "/a/"} {
		c := cfg()
		c.NginxPID = p

		if _, err := Render(c); err == nil {
			t.Errorf("path %q accepted", p)
		}
	}
}

func TestWriteAndEnsure(t *testing.T) {
	path := filepath.Join(t.TempDir(), "hostashi")

	// A new server gets the default.
	if err := Ensure(path, cfg()); err != nil {
		t.Fatal(err)
	}

	b, _ := os.ReadFile(path)
	if d, _ := Days(b); d != DefaultDays {
		t.Fatalf("default days %d", d)
	}

	if fi, _ := os.Stat(path); fi.Mode().Perm() != 0o644 {
		t.Fatalf("mode %v", fi.Mode())
	}

	// The panel's value survives an agent start.
	c := cfg()
	c.Days = 30

	if err := Write(path, c); err != nil {
		t.Fatal(err)
	}

	c.Days = 1 // ignored by Ensure
	if err := Ensure(path, c); err != nil {
		t.Fatal(err)
	}

	b, _ = os.ReadFile(path)
	if d, _ := Days(b); d != 30 {
		t.Fatalf("days after Ensure %d, want 30", d)
	}

	// A file that someone edited by hand falls back to the default.
	if err := os.WriteFile(path, []byte("/x {\n  rotate 9999\n}\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	if err := Ensure(path, cfg()); err != nil {
		t.Fatal(err)
	}

	b, _ = os.ReadFile(path)
	if d, _ := Days(b); d != DefaultDays {
		t.Fatalf("days %d", d)
	}

	// A rejected value leaves the file alone.
	c.Days = 0
	if err := Write(path, c); err == nil {
		t.Fatal("days 0 written")
	}

	if b2, _ := os.ReadFile(path); string(b2) != string(b) {
		t.Fatal("file changed by a rejected write")
	}

	entries, _ := os.ReadDir(filepath.Dir(path))
	if len(entries) != 1 {
		t.Fatalf("temp files left: %v", entries)
	}
}
