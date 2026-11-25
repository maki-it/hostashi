// SPDX-License-Identifier: AGPL-3.0-or-later

package web

import (
	"net/http"
	"os"
	"path/filepath"
	"testing"

	"github.com/maki-it/hostashi/internal/profile"
)

// TestBuiltinProfilesRender serves one PHP site per built-in profile with a
// real nginx: every profile's rules pass nginx -t together, and its web
// root folder is served (ADR-0069).
func TestBuiltinProfilesRender(t *testing.T) {
	e := newEnv(t)
	ctx := t.Context()

	if _, err := e.r.Recover(ctx); err != nil {
		t.Fatal(err)
	}

	sock := filepath.Join(e.dir, "php.sock")
	fcgiServer(t, sock)

	var d Desired

	for _, p := range profile.Builtins() {
		s := e.site(t, p.Name, "x", p.Name+".example.com")
		if p.WebDir != "" {
			s.Root = filepath.Join(s.Root, p.WebDir)
			if err := os.MkdirAll(s.Root, 0o755); err != nil {
				t.Fatal(err)
			}
		}

		if err := os.WriteFile(filepath.Join(s.Root, "index.php"), []byte("<?php"), 0o644); err != nil {
			t.Fatal(err)
		}

		if p.Type == "php" {
			s.PHPSocket = sock
		}

		s.Hardening = profile.Effective(p, profile.Overrides{}, p.Type == "php")
		d.Sites = append(d.Sites, s)
	}

	if _, err := e.r.Apply(ctx, d); err != nil {
		t.Fatal(err)
	}

	for _, p := range profile.Builtins() {
		want := http.StatusOK
		if code, _ := e.fetchPath(t, p.Name+".example.com", "/"); code != want {
			t.Errorf("%s: %d", p.Name, code)
		}

		if code, _ := e.fetchPath(t, p.Name+".example.com", "/dump.sql"); code != http.StatusNotFound {
			t.Errorf("%s dump: %d", p.Name, code)
		}
	}
}
