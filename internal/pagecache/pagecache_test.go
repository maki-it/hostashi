// SPDX-License-Identifier: AGPL-3.0-or-later

package pagecache

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

func testDirs(t *testing.T) (Dirs, string, *[]string) {
	t.Helper()

	var assigned []string

	d := Dirs{
		UID: os.Getuid(), GID: os.Getgid(), RootUID: os.Getuid(),
		Assign: func(dir string, project uint32) error {
			assigned = append(assigned, fmt.Sprintf("%s:%d", dir, project))

			return nil
		},
	}

	return d, t.TempDir(), &assigned
}

func mode(t *testing.T, p string) os.FileMode {
	t.Helper()

	st, err := os.Lstat(p)
	if err != nil {
		t.Fatal(err)
	}

	return st.Mode()
}

func TestEnsureCreatesDirectories(t *testing.T) {
	d, pool, assigned := testDirs(t)

	dir, err := d.Ensure(pool, "alpha", 1001)
	if err != nil {
		t.Fatal(err)
	}

	if dir != filepath.Join(pool, DirName, "alpha") {
		t.Fatalf("dir %q", dir)
	}

	if m := mode(t, filepath.Join(pool, DirName)); m != os.ModeDir|0o711 {
		t.Errorf("root mode %v", m)
	}

	if m := mode(t, dir); m != os.ModeDir|0o700 {
		t.Errorf("site mode %v", m)
	}

	if !slices.Equal(*assigned, []string{dir + ":1001"}) {
		t.Errorf("assigned %v", *assigned)
	}

	// Idempotent, and it fixes a wrong mode.
	if err := os.Chmod(dir, 0o755); err != nil {
		t.Fatal(err)
	}

	if _, err := d.Ensure(pool, "alpha", 1001); err != nil {
		t.Fatal(err)
	}

	if m := mode(t, dir); m != os.ModeDir|0o700 {
		t.Errorf("mode not fixed: %v", m)
	}

	if err := d.Check(pool, "alpha"); err != nil {
		t.Fatal(err)
	}
}

func TestEnsureRefusesSymlinks(t *testing.T) {
	d, pool, _ := testDirs(t)
	target := t.TempDir()

	// The cache root swapped for a link.
	if err := os.Symlink(target, filepath.Join(pool, DirName)); err != nil {
		t.Fatal(err)
	}

	if _, err := d.Ensure(pool, "alpha", 1001); err == nil {
		t.Fatal("followed a symlinked cache root")
	}

	if err := os.Remove(filepath.Join(pool, DirName)); err != nil {
		t.Fatal(err)
	}

	// The site's directory swapped for a link.
	if err := os.Mkdir(filepath.Join(pool, DirName), 0o711); err != nil {
		t.Fatal(err)
	}

	if err := os.Symlink(target, filepath.Join(pool, DirName, "alpha")); err != nil {
		t.Fatal(err)
	}

	if _, err := d.Ensure(pool, "alpha", 1001); !errors.Is(err, ErrUnsafe) {
		t.Fatalf("symlinked site dir: %v", err)
	}

	if ents, _ := os.ReadDir(target); len(ents) != 0 {
		t.Fatal("wrote through the link")
	}
}

func TestEnsureRefusesForeignRoot(t *testing.T) {
	d, pool, _ := testDirs(t)
	d.RootUID = os.Getuid() + 1

	if _, err := d.Ensure(pool, "alpha", 1001); !errors.Is(err, ErrUnsafe) {
		t.Fatalf("foreign cache root: %v", err)
	}
}

func TestInvalidNames(t *testing.T) {
	d, pool, _ := testDirs(t)

	for _, u := range []string{"", ".", "..", "../x", "a/b", ".hidden", "a\x00b"} {
		if _, err := d.Ensure(pool, u, 1001); err == nil {
			t.Errorf("Ensure(%q) accepted", u)
		}

		if err := d.Purge(pool, u, 1001); err == nil {
			t.Errorf("Purge(%q) accepted", u)
		}

		if err := d.Remove(pool, u); err == nil {
			t.Errorf("Remove(%q) accepted", u)
		}
	}

	if _, err := d.Ensure("relative", "alpha", 1001); err == nil {
		t.Error("relative pool accepted")
	}
}

// fill writes a cache tree like nginx's levels=1:2, plus a symlink that a
// compromised worker could have planted.
func fill(t *testing.T, dir, outside string) {
	t.Helper()

	sub := filepath.Join(dir, "a", "bc")
	if err := os.MkdirAll(sub, 0o700); err != nil {
		t.Fatal(err)
	}

	if err := os.WriteFile(filepath.Join(sub, "0123abc"), []byte("cached"), 0o600); err != nil {
		t.Fatal(err)
	}

	if err := os.Symlink(outside, filepath.Join(dir, "a", "link")); err != nil {
		t.Fatal(err)
	}
}

func TestPurgeAndSweep(t *testing.T) {
	d, pool, _ := testDirs(t)

	dir, err := d.Ensure(pool, "alpha", 1001)
	if err != nil {
		t.Fatal(err)
	}

	outside := t.TempDir()
	if err := os.WriteFile(filepath.Join(outside, "keep"), []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}

	fill(t, dir, outside)

	if err := d.Purge(pool, "alpha", 1001); err != nil {
		t.Fatal(err)
	}

	if ents, err := os.ReadDir(dir); err != nil || len(ents) != 0 {
		t.Fatalf("after purge: %v %v", ents, err)
	}

	if err := d.Check(pool, "alpha"); err != nil {
		t.Fatal(err)
	}

	// The old contents wait under a trash name; List hides them.
	ents, _ := os.ReadDir(filepath.Join(pool, DirName))
	if len(ents) != 2 {
		t.Fatalf("entries %v", ents)
	}

	if got, err := d.List(pool); err != nil || !slices.Equal(got, []string{"alpha"}) {
		t.Fatalf("List: %v %v", got, err)
	}

	if err := d.Sweep(pool); err != nil {
		t.Fatal(err)
	}

	ents, _ = os.ReadDir(filepath.Join(pool, DirName))
	if len(ents) != 1 || ents[0].Name() != "alpha" {
		t.Fatalf("after sweep: %v", ents)
	}

	// The link was removed, not followed.
	if _, err := os.Stat(filepath.Join(outside, "keep")); err != nil {
		t.Fatalf("sweep followed a link: %v", err)
	}

	// Purge of a missing directory creates it.
	if err := os.Remove(dir); err != nil {
		t.Fatal(err)
	}

	if err := d.Purge(pool, "alpha", 1001); err != nil {
		t.Fatal(err)
	}

	if err := d.Check(pool, "alpha"); err != nil {
		t.Fatal(err)
	}
}

func TestRemove(t *testing.T) {
	d, pool, _ := testDirs(t)

	// Nothing there yet: fine, and nothing is created.
	if err := d.Remove(pool, "alpha"); err != nil {
		t.Fatal(err)
	}

	if _, err := os.Stat(filepath.Join(pool, DirName)); !os.IsNotExist(err) {
		t.Fatalf("Remove created the cache root: %v", err)
	}

	dir, err := d.Ensure(pool, "alpha", 1001)
	if err != nil {
		t.Fatal(err)
	}

	outside := t.TempDir()
	fill(t, dir, outside)

	if err := d.Remove(pool, "alpha"); err != nil {
		t.Fatal(err)
	}

	if ents, _ := os.ReadDir(filepath.Join(pool, DirName)); len(ents) != 0 {
		t.Fatalf("left %v", ents)
	}

	if err := d.Check(pool, "alpha"); err == nil {
		t.Fatal("Check passed for a removed directory")
	}
}

func TestCheckFindsWrongMode(t *testing.T) {
	d, pool, _ := testDirs(t)

	dir, err := d.Ensure(pool, "alpha", 1001)
	if err != nil {
		t.Fatal(err)
	}

	if err := os.Chmod(dir, 0o750); err != nil {
		t.Fatal(err)
	}

	if err := d.Check(pool, "alpha"); !errors.Is(err, ErrUnsafe) || !strings.Contains(err.Error(), "750") {
		t.Fatalf("Check: %v", err)
	}
}

func TestDeepTreeIsRefused(t *testing.T) {
	d, pool, _ := testDirs(t)

	dir, err := d.Ensure(pool, "alpha", 1001)
	if err != nil {
		t.Fatal(err)
	}

	deep := dir
	for range maxDepth + 2 {
		deep = filepath.Join(deep, "d")
	}

	if err := os.MkdirAll(deep, 0o700); err != nil {
		t.Fatal(err)
	}

	if err := d.Remove(pool, "alpha"); !errors.Is(err, ErrUnsafe) {
		t.Fatalf("deep tree: %v", err)
	}
}
