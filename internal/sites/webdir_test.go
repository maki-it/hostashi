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

func TestCheckWebDirDeployLink(t *testing.T) {
	home := t.TempDir()

	for _, d := range []string{".deploy/releases/7/public", ".deploy/releases/8", "htdocs-real"} {
		if err := os.MkdirAll(filepath.Join(home, d), 0o755); err != nil {
			t.Fatal(err)
		}
	}

	link := func(target string) {
		t.Helper()

		_ = os.Remove(filepath.Join(home, "htdocs"))

		if err := os.Symlink(target, filepath.Join(home, "htdocs")); err != nil {
			t.Fatal(err)
		}
	}

	// The link a deploy makes stands for its release.
	link(".deploy/releases/7")

	for dir, want := range map[string]bool{"": true, "public": true, "later": false} {
		if got, err := CheckWebDir(home, dir); err != nil || got != want {
			t.Errorf("%q: %v %v, want %v", dir, got, err, want)
		}
	}

	// A link inside the release is still refused.
	if err := os.Symlink("/etc", filepath.Join(home, ".deploy/releases/8/etc")); err != nil {
		t.Fatal(err)
	}

	link(".deploy/releases/8")

	if _, err := CheckWebDir(home, "etc"); !errors.Is(err, ErrWebDirUnsafe) {
		t.Errorf("link in release: %v", err)
	}

	// Any other link target is refused.
	outside := t.TempDir()

	for _, target := range []string{"htdocs-real", ".deploy/releases/x", "../" + filepath.Base(home) + "/.deploy/releases/7", outside, ".deploy/releases/7/../8", "./.deploy/releases/7"} {
		link(target)

		if _, err := CheckWebDir(home, ""); !errors.Is(err, ErrWebDirUnsafe) {
			t.Errorf("target %q: %v", target, err)
		}
	}

	// The releases folder itself must not be a link out of the home.
	if err := os.RemoveAll(filepath.Join(home, ".deploy")); err != nil {
		t.Fatal(err)
	}

	if err := os.MkdirAll(filepath.Join(outside, "releases/7"), 0o755); err != nil {
		t.Fatal(err)
	}

	if err := os.Symlink(outside, filepath.Join(home, ".deploy")); err != nil {
		t.Fatal(err)
	}

	link(".deploy/releases/7")

	if _, err := CheckWebDir(home, ""); !errors.Is(err, ErrWebDirUnsafe) {
		t.Errorf("releases outside: %v", err)
	}

	// A deeper deploy directory links with ../.
	home2 := t.TempDir()
	if err := os.MkdirAll(filepath.Join(home2, ".deploy/releases/3"), 0o755); err != nil {
		t.Fatal(err)
	}

	if err := os.MkdirAll(filepath.Join(home2, "htdocs"), 0o755); err != nil {
		t.Fatal(err)
	}

	if err := os.Symlink("../.deploy/releases/3", filepath.Join(home2, "htdocs/app")); err != nil {
		t.Fatal(err)
	}

	if got, err := CheckWebDir(home2, "app"); err != nil || !got {
		t.Errorf("nested: %v %v", got, err)
	}
}
