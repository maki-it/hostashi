// SPDX-License-Identifier: AGPL-3.0-or-later

package sitelogs

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/maki-it/hostashi/internal/agentapi"
	"github.com/maki-it/hostashi/internal/wpunit"
)

// The WordPress install log (ADR-0069) is one more log of the site: a site
// without an install has none, which reads as empty.
func TestWordPressLog(t *testing.T) {
	r := newReader(t)
	r.AppLogDir = filepath.Join(t.TempDir(), "app")

	p, err := r.Path("blog", agentapi.LogWordPress)
	if err != nil || filepath.Base(p) != wpunit.LogName || filepath.Dir(filepath.Dir(p)) != r.AppLogDir {
		t.Fatalf("path %s is not where wpunit writes: %v", p, err)
	}

	// No install: neither the directory nor the file exists.
	resp := read(t, r, agentapi.SiteLogsReadRequest{Site: "blog", Kind: agentapi.LogWordPress, Lines: 10})
	if resp.Lines == nil || len(resp.Lines) != 0 {
		t.Fatalf("no install: %q", resp.Lines)
	}

	// Following a log that does not exist yet starts from nothing.
	if resp := read(t, r, agentapi.SiteLogsReadRequest{Site: "blog", Kind: agentapi.LogWordPress, After: &agentapi.LogCursor{}}); len(resp.Lines) != 0 || resp.Rotated {
		t.Fatalf("follow without install: %+v", resp)
	}

	if err := os.MkdirAll(filepath.Join(r.AppLogDir, "blog"), 0o700); err != nil {
		t.Fatal(err)
	}

	// An install that wrote nothing yet.
	write(t, r, agentapi.LogWordPress, "")

	if resp := read(t, r, agentapi.SiteLogsReadRequest{Site: "blog", Kind: agentapi.LogWordPress, Lines: 10}); len(resp.Lines) != 0 {
		t.Fatalf("empty: %q", resp.Lines)
	}

	appendTo(t, p, "$ wp core download --locale=en_US\nSuccess: WordPress downloaded.\n")

	resp = read(t, r, agentapi.SiteLogsReadRequest{Site: "blog", Kind: agentapi.LogWordPress, Lines: 10})
	if strings.Join(resp.Lines, "|") != "$ wp core download --locale=en_US|Success: WordPress downloaded." {
		t.Fatalf("lines %q", resp.Lines)
	}

	appendTo(t, p, "wordpress installed: https://blog.example.com/\n")

	next := read(t, r, agentapi.SiteLogsReadRequest{Site: "blog", Kind: agentapi.LogWordPress, After: &resp.Cursor})
	if strings.Join(next.Lines, "|") != "wordpress installed: https://blog.example.com/" || next.Rotated {
		t.Fatalf("follow %+v", next)
	}

	// The app log of the same directory stays apart.
	if resp := read(t, r, agentapi.SiteLogsReadRequest{Site: "blog", Kind: agentapi.LogApp, Lines: 10}); len(resp.Lines) != 0 {
		t.Fatalf("app log shows the WordPress log: %q", resp.Lines)
	}

	// The log cannot be pointed at another file.
	if err := os.Remove(p); err != nil {
		t.Fatal(err)
	}

	secret := filepath.Join(t.TempDir(), "secret")
	if err := os.WriteFile(secret, []byte("root:x:0:0\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	if err := os.Symlink(secret, p); err != nil {
		t.Fatal(err)
	}

	if _, err := r.Read(agentapi.SiteLogsReadRequest{Site: "blog", Kind: agentapi.LogWordPress, Lines: 5}); !errors.Is(err, ErrUnsafe) {
		t.Fatalf("symlinked file: %v", err)
	}

	// Another site's name never reaches the path.
	if _, err := r.Read(agentapi.SiteLogsReadRequest{Site: "../blog", Kind: agentapi.LogWordPress, Lines: 5}); err == nil {
		t.Fatal("bad site accepted")
	}
}
