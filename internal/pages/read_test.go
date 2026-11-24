// SPDX-License-Identifier: AGPL-3.0-or-later

package pages

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const testDigest = "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"

func TestReadFile(t *testing.T) {
	dir := t.TempDir()
	page := filepath.Join(dir, "p1."+testDigest)

	if err := os.MkdirAll(filepath.Join(page, "css"), 0o755); err != nil {
		t.Fatal(err)
	}

	for name, body := range map[string]string{"index.html": "<h1>Back soon</h1>", "css/a.css": "h1{}"} {
		if err := os.WriteFile(filepath.Join(page, name), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	secret := filepath.Join(dir, "secret.html")
	if err := os.WriteFile(secret, []byte("secret"), 0o644); err != nil {
		t.Fatal(err)
	}

	if err := os.Symlink(secret, filepath.Join(page, "link.html")); err != nil {
		t.Fatal(err)
	}

	if err := os.Mkdir(filepath.Join(page, "dir.html"), 0o755); err != nil {
		t.Fatal(err)
	}

	b, err := ReadFile(dir, "p1", testDigest, "index.html")
	if err != nil || string(b) != "<h1>Back soon</h1>" {
		t.Fatalf("index: %q %v", b, err)
	}

	if b, err := ReadFile(dir, "p1", testDigest, "css/a.css"); err != nil || string(b) != "h1{}" {
		t.Fatalf("css: %q %v", b, err)
	}

	for _, rel := range []string{"missing.html", "link.html", "dir.html", "../secret.html", "/etc/passwd", "css/../index.html", ".hidden.html", "index.php", ""} {
		if _, err := ReadFile(dir, "p1", testDigest, rel); !errors.Is(err, ErrNoFile) {
			t.Errorf("%q: %v, want ErrNoFile", rel, err)
		}
	}

	if _, err := ReadFile(dir, "p2", testDigest, "index.html"); !errors.Is(err, ErrNoFile) {
		t.Errorf("unknown page: %v", err)
	}

	if _, err := ReadFile(dir, "../p1", testDigest, "index.html"); err == nil {
		t.Error("bad id accepted")
	}

	if _, err := ReadFile(dir, "p1", strings.Repeat("z", 64), "index.html"); err == nil {
		t.Error("bad digest accepted")
	}
}
