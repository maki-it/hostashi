package confine

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"golang.org/x/sys/unix"
)

func mountTmpfs(t *testing.T, dir, opts string) {
	t.Helper()
	if err := unix.Mount("tmpfs", dir, "tmpfs", 0, opts); err != nil {
		t.Skipf("cannot mount tmpfs (needs root): %v", err)
	}
	t.Cleanup(func() { _ = unix.Unmount(dir, unix.MNT_DETACH) })
}

// A mount inside the tree (bind mount, volume put there by an admin or a mistake) is not traversed.
func TestNoXdevRefusesMountInsideTree(t *testing.T) {
	r, site, outside := newTree(t, Options{})
	mp := filepath.Join(site, "mnt")
	must(t, os.Mkdir(mp, 0o755))
	if err := unix.Mount(outside, mp, "", unix.MS_BIND, ""); err != nil {
		t.Skipf("cannot bind mount: %v", err)
	}
	t.Cleanup(func() { _ = unix.Unmount(mp, unix.MNT_DETACH) })
	if b, err := os.ReadFile(filepath.Join(mp, "secret")); err != nil || string(b) != "SECRET" {
		t.Fatalf("bind mount not working: %v", err)
	}
	_, _, err := r.OpenRead(rel(t, "mnt/secret"))
	wantCode(t, err, CodeEscape)
	wantCode(t, r.WriteFile(rel(t, "mnt/x"), 0o644, strings.NewReader("x"), 1, false), CodeEscape)
	// Recursive remove refuses to descend into the mount instead of emptying it.
	if err := r.Remove(rel(t, "mnt"), true); err == nil {
		t.Fatal("remove of mount point succeeded")
	}
	if _, err := os.Stat(filepath.Join(outside, "secret")); err != nil {
		t.Fatal("secret deleted through mount")
	}
}

// Stand-in for a quota-limited pool: a tiny tmpfs. Real project quota was not available in the
// sandbox kernel (see S8); the error path (ENOSPC/EDQUOT -> no_space, no partial file) is the same.
func TestNoSpaceLeavesNoPartialFile(t *testing.T) {
	tmp := t.TempDir()
	mountTmpfs(t, tmp, "size=256k")
	fd, err := unix.Open(tmp, unix.O_RDONLY|unix.O_DIRECTORY, 0)
	must(t, err)
	r, err := NewRoot(fd, Options{})
	must(t, err)
	defer r.Close()

	must(t, r.WriteFile(rel(t, "keep"), 0o644, strings.NewReader("old"), 3, false))
	big := strings.NewReader(strings.Repeat("x", 1<<20))
	wantCode(t, r.WriteFile(rel(t, "big"), 0o644, big, 1<<20, false), CodeNoSpace)
	// Overwrite that runs out of space leaves the previous content.
	wantCode(t, r.WriteFile(rel(t, "keep"), 0o644, strings.NewReader(strings.Repeat("y", 1<<20)), 1<<20, true), CodeNoSpace)
	if b, _ := os.ReadFile(filepath.Join(tmp, "keep")); string(b) != "old" {
		t.Fatalf("old content lost: %d bytes", len(b))
	}
	ents, _ := os.ReadDir(tmp)
	if len(ents) != 1 {
		t.Fatalf("leftover files: %v", ents)
	}
	// Space is usable again afterwards.
	must(t, r.WriteFile(rel(t, "after"), 0o644, strings.NewReader("ok"), 2, false))
}
