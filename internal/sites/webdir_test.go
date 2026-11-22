// SPDX-License-Identifier: AGPL-3.0-or-later

package sites

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestCheckWebDir(t *testing.T) {
	home := t.TempDir()
	outside := t.TempDir()

	for _, d := range []string{"htdocs/dist/assets", "htdocs/public"} {
		if err := os.MkdirAll(filepath.Join(home, d), 0o755); err != nil {
			t.Fatal(err)
		}
	}

	if err := os.WriteFile(filepath.Join(home, "htdocs/file"), nil, 0o644); err != nil {
		t.Fatal(err)
	}

	if err := os.Symlink(outside, filepath.Join(home, "htdocs/escape")); err != nil {
		t.Fatal(err)
	}

	if err := os.Symlink("dist", filepath.Join(home, "htdocs/inside")); err != nil {
		t.Fatal(err)
	}

	for dir, want := range map[string]bool{"": true, "dist": true, "dist/assets": true, "public": true, "later": false, "dist/later": false} {
		got, err := CheckWebDir(home, dir)
		if err != nil || got != want {
			t.Errorf("%q: %v %v, want %v", dir, got, err, want)
		}
	}

	// Symbolic links are refused even when they stay inside htdocs:
	// nginx would follow them on every request, so the folder must be real.
	for _, dir := range []string{"escape", "escape/x", "inside", "file", "file/x"} {
		if _, err := CheckWebDir(home, dir); !errors.Is(err, ErrWebDirUnsafe) {
			t.Errorf("%q: %v", dir, err)
		}
	}

	if _, err := CheckWebDir(filepath.Join(home, "missing"), "dist"); err == nil || errors.Is(err, ErrWebDirUnsafe) {
		t.Errorf("missing home: %v", err)
	}
}
