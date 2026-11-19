// SPDX-License-Identifier: AGPL-3.0-or-later

package scan

import (
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
)

func blocks(t *testing.T, p string) int64 {
	t.Helper()

	var st syscall.Stat_t
	if err := syscall.Lstat(p, &st); err != nil {
		t.Fatal(err)
	}

	return st.Blocks * 512
}

func TestUsage(t *testing.T) {
	root := t.TempDir()
	home := filepath.Join(root, "home", "shop")
	outside := filepath.Join(t.TempDir(), "big")

	if err := os.MkdirAll(filepath.Join(home, "htdocs", "wp-content"), 0o755); err != nil {
		t.Fatal(err)
	}

	for p, n := range map[string]int{"htdocs/index.php": 100, "htdocs/wp-content/a.jpg": 100_000} {
		if err := os.WriteFile(filepath.Join(home, p), []byte(strings.Repeat("x", n)), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	if err := os.WriteFile(outside, make([]byte, 1<<20), 0o644); err != nil {
		t.Fatal(err)
	}

	// A hard link counts once; a symlink counts itself, not its target.
	if err := os.Link(filepath.Join(home, "htdocs/wp-content/a.jpg"), filepath.Join(home, "a-link.jpg")); err != nil {
		t.Fatal(err)
	}

	if err := os.Symlink(outside, filepath.Join(home, "big")); err != nil {
		t.Fatal(err)
	}

	var want int64
	for _, p := range []string{"", "htdocs", "htdocs/wp-content", "htdocs/index.php", "htdocs/wp-content/a.jpg", "big"} {
		want += blocks(t, filepath.Join(home, p))
	}

	r, err := newReader(root, false)
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()

	u, err := r.Usage("/home/shop")
	if err != nil {
		t.Fatal(err)
	}

	if u.Inodes != 6 || u.Bytes != want || u.Partial {
		t.Fatalf("usage %+v, want 6 inodes and %d bytes", u, want)
	}

	if _, err := r.Usage("/home/shop/big"); err == nil {
		t.Fatal("a symlinked home was followed")
	}

	if _, err := r.Usage("/home/none"); err == nil {
		t.Fatal("missing home measured")
	}
}

func TestUsageDepthLimit(t *testing.T) {
	root := t.TempDir()
	p := filepath.Join(root, "home", "deep")

	for range maxUsageDepth + 2 {
		p = filepath.Join(p, "d")
	}

	if err := os.MkdirAll(p, 0o755); err != nil {
		t.Skip("cannot build a tree this deep here:", err)
	}

	r, err := newReader(root, false)
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()

	u, err := r.Usage("/home/deep")
	if err != nil || !u.Partial || u.Inodes < maxUsageDepth {
		t.Fatalf("usage %+v %v", u, err)
	}
}
