// SPDX-License-Identifier: AGPL-3.0-or-later

package php

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func readVersion(t *testing.T, m *Manager, user string) string {
	t.Helper()

	p := filepath.Join(m.VersionDir, user)

	fi, err := os.Lstat(p)
	if errors.Is(err, os.ErrNotExist) {
		return ""
	}

	if err != nil {
		t.Fatal(err)
	}

	if !fi.Mode().IsRegular() || fi.Mode().Perm() != 0o644 {
		t.Fatalf("%s: mode %v", p, fi.Mode())
	}

	b, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}

	return string(b)
}

func TestVersionFileFollowsThePool(t *testing.T) {
	m, ctl := newManager(t)
	m.VersionDir = filepath.Join(t.TempDir(), "hostashi-php")
	ctx := t.Context()

	blog := Pool{User: "blog", Version: "8.4", Home: "/home/blog"}
	shop := Pool{User: "shop", Version: "8.3", Home: "/home/shop"}

	if err := m.Ensure(ctx, []Pool{blog, shop}); err != nil {
		t.Fatal(err)
	}

	if got := readVersion(t, m, "blog"); got != "8.4\n" {
		t.Fatalf("blog: %q", got)
	}

	if got := readVersion(t, m, "shop"); got != "8.3\n" {
		t.Fatalf("shop: %q", got)
	}

	fi, err := os.Stat(m.VersionDir)
	if err != nil || fi.Mode().Perm() != 0o711 {
		t.Fatalf("directory: %v %v", fi, err)
	}

	// A switch writes the new version.
	blog.Version = "8.3"
	if err := m.Ensure(ctx, []Pool{blog}); err != nil {
		t.Fatal(err)
	}

	if got := readVersion(t, m, "blog"); got != "8.3\n" {
		t.Fatalf("blog after switch: %q", got)
	}

	// A version that does not start leaves the old one.
	ctl.startErr["hostashi-php-8.4@blog.service"] = errors.New("exit 78")

	if err := m.Ensure(ctx, []Pool{{User: "blog", Version: "8.4", Home: "/home/blog"}}); !errors.Is(err, ErrStartFailed) {
		t.Fatalf("err %v", err)
	}

	if got := readVersion(t, m, "blog"); got != "8.3\n" {
		t.Fatalf("blog after failed switch: %q", got)
	}

	// Prune removes the file of a site that is gone.
	if err := m.Prune(ctx, map[string]bool{"blog": true}); err != nil {
		t.Fatal(err)
	}

	if got := readVersion(t, m, "shop"); got != "" {
		t.Fatalf("shop after prune: %q", got)
	}

	// A new site that does not start gets no file.
	ctl.startErr["hostashi-php-8.4@news.service"] = errors.New("exit 78")

	if err := m.Ensure(ctx, []Pool{{User: "news", Version: "8.4", Home: "/home/news"}}); !errors.Is(err, ErrStartFailed) {
		t.Fatalf("err %v", err)
	}

	if got := readVersion(t, m, "news"); got != "" {
		t.Fatalf("news: %q", got)
	}
}

// An unchanged pool from before the version files gets its file, and a
// damaged or replaced file is written again, never followed.
func TestVersionFileRepaired(t *testing.T) {
	m, _ := newManager(t)
	ctx := t.Context()

	blog := Pool{User: "blog", Version: "8.4", Home: "/home/blog"}
	if err := m.Ensure(ctx, []Pool{blog}); err != nil {
		t.Fatal(err)
	}

	m.VersionDir = filepath.Join(t.TempDir(), "hostashi-php")

	if err := m.Ensure(ctx, []Pool{blog}); err != nil {
		t.Fatal(err)
	}

	if got := readVersion(t, m, "blog"); got != "8.4\n" {
		t.Fatalf("unchanged pool: %q", got)
	}

	target := filepath.Join(t.TempDir(), "target")
	if err := os.WriteFile(target, []byte("keep\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	file := filepath.Join(m.VersionDir, "blog")
	if err := os.Remove(file); err != nil {
		t.Fatal(err)
	}

	if err := os.Symlink(target, file); err != nil {
		t.Fatal(err)
	}

	if err := os.Chmod(m.VersionDir, 0o755); err != nil {
		t.Fatal(err)
	}

	if err := m.Recover(ctx); err != nil {
		t.Fatal(err)
	}

	if got := readVersion(t, m, "blog"); got != "8.4\n" {
		t.Fatalf("after recover: %q", got)
	}

	if b, _ := os.ReadFile(target); string(b) != "keep\n" {
		t.Fatalf("symlink target written: %q", b)
	}

	if fi, err := os.Stat(m.VersionDir); err != nil || fi.Mode().Perm() != 0o711 {
		t.Fatalf("directory mode not repaired: %v %v", fi, err)
	}
}

// A directory that is not one makes no file and does not stop the pool.
func TestVersionDirNotADirectory(t *testing.T) {
	m, ctl := newManager(t)
	m.VersionDir = filepath.Join(t.TempDir(), "hostashi-php")

	if err := os.WriteFile(m.VersionDir, nil, 0o600); err != nil {
		t.Fatal(err)
	}

	if err := m.Ensure(t.Context(), []Pool{{User: "blog", Version: "8.4", Home: "/home/blog"}}); err != nil {
		t.Fatal(err)
	}

	if ctl.units() != "hostashi-php-8.4@blog.service" {
		t.Fatalf("units %s", ctl.units())
	}

	if err := m.ensureVersionFile(Pool{User: "blog", Version: "8.4"}); err == nil {
		t.Fatalf("err %v", err)
	}

	if err := m.ensureVersionFile(Pool{User: "../x", Version: "8.4"}); !errors.Is(err, ErrInvalid) {
		t.Fatalf("bad user: %v", err)
	}

	if err := m.ensureVersionFile(Pool{User: "blog", Version: "8.4\n; x"}); !errors.Is(err, ErrInvalid) {
		t.Fatalf("bad version: %v", err)
	}
}
