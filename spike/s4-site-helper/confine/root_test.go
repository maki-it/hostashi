package confine

import (
	"bytes"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"golang.org/x/sys/unix"
)

func TestBasicOps(t *testing.T) {
	r, site, _ := newTree(t, Options{})
	must(t, r.Mkdir(rel(t, "a/b/c"), 0o755, true))
	must(t, r.WriteFile(rel(t, "a/b/c/f.txt"), 0o640, strings.NewReader("hello"), 5, false))
	fi, err := os.Stat(filepath.Join(site, "a/b/c/f.txt"))
	must(t, err)
	if fi.Mode().Perm() != 0o640 {
		t.Fatalf("mode %v", fi.Mode())
	}
	f, size, err := r.OpenRead(rel(t, "a/b/c/f.txt"))
	must(t, err)
	b, _ := io.ReadAll(f)
	f.Close()
	if string(b) != "hello" || size != 5 {
		t.Fatalf("read %q size %d", b, size)
	}
	in, err := r.Stat(rel(t, "a/b/c/f.txt"))
	must(t, err)
	if in.Type != "file" || in.Size != 5 || in.Mode != 0o640 {
		t.Fatalf("%+v", in)
	}
	must(t, r.Rename(rel(t, "a/b/c/f.txt"), rel(t, "a/g.txt")))
	must(t, r.Chmod(rel(t, "a/g.txt"), 0o600))
	list, trunc, err := r.List(rel(t, "a"), "", 0)
	must(t, err)
	if trunc || len(list) != 2 || list[0].Name != "b" || list[1].Name != "g.txt" || list[1].Mode != 0o600 {
		t.Fatalf("%+v", list)
	}
	wantCode(t, r.Remove(rel(t, "a"), false), CodeNotEmpty)
	must(t, r.Remove(rel(t, "a"), true))
	if _, err := os.Lstat(filepath.Join(site, "a")); !os.IsNotExist(err) {
		t.Fatal("tree still there")
	}
}

func TestListPaging(t *testing.T) {
	r, _, _ := newTree(t, Options{})
	for _, n := range []string{"d", "a", "c", "b", "e"} {
		must(t, r.WriteFile(rel(t, n), 0o644, strings.NewReader("x"), 1, false))
	}
	p1, trunc, err := r.List(rel(t, ""), "", 2)
	must(t, err)
	if !trunc || len(p1) != 2 || p1[1].Name != "b" {
		t.Fatalf("%+v %v", p1, trunc)
	}
	p2, trunc, _ := r.List(rel(t, ""), "b", 10)
	if trunc || len(p2) != 3 || p2[0].Name != "c" {
		t.Fatalf("%+v %v", p2, trunc)
	}
}

func TestEscapesRefused(t *testing.T) {
	r, site, outside := newTree(t, Options{})
	must(t, os.Symlink(outside, filepath.Join(site, "abs-dir")))                           // absolute dir link
	must(t, os.Symlink(filepath.Join(outside, "secret"), filepath.Join(site, "abs-file"))) // absolute file link
	must(t, os.Symlink("../outside", filepath.Join(site, "rel-dir")))                      // relative, leaves root
	must(t, os.Symlink("../outside/secret", filepath.Join(site, "rel-file")))
	must(t, os.Symlink("/proc/self/root", filepath.Join(site, "magic")))
	must(t, os.Symlink("loop", filepath.Join(site, "loop")))

	for _, p := range []string{"abs-dir/secret", "abs-file", "rel-dir/secret", "rel-file", "magic/etc/passwd"} {
		_, _, err := r.OpenRead(rel(t, p))
		if err == nil {
			t.Errorf("read %s succeeded", p)
			continue
		}
		if c := CodeOf(err); c != CodeEscape && c != CodeSymlink {
			t.Errorf("read %s: code %s", p, c)
		}
	}
	_, _, err := r.OpenRead(rel(t, "loop"))
	wantCode(t, err, CodeSymlink)

	// Writes and listings through an escaping link are refused too, and nothing appears outside.
	wantCode(t, r.WriteFile(rel(t, "rel-dir/new"), 0o644, strings.NewReader("x"), 1, false), CodeEscape)
	wantCode(t, r.Mkdir(rel(t, "abs-dir/sub"), 0o755, false), CodeEscape)
	_, _, err = r.List(rel(t, "rel-dir"), "", 0)
	wantCode(t, err, CodeEscape)
	if ents, _ := os.ReadDir(outside); len(ents) != 1 {
		t.Fatalf("outside changed: %v", ents)
	}
	// The same names given as plain strings never get that far.
	for _, bad := range []string{"../outside/secret", "/etc/passwd"} {
		if _, err := ParseRel(bad); err == nil {
			t.Errorf("%s parsed", bad)
		}
	}
}

func TestInsideSymlinkPolicy(t *testing.T) {
	// Default: relative links that stay inside work (Laravel/WordPress style).
	r, site, _ := newTree(t, Options{})
	must(t, os.MkdirAll(filepath.Join(site, "storage/app/public"), 0o755))
	must(t, os.WriteFile(filepath.Join(site, "storage/app/public/x.txt"), []byte("in"), 0o644))
	must(t, os.MkdirAll(filepath.Join(site, "public"), 0o755))
	must(t, os.Symlink("../storage/app/public", filepath.Join(site, "public/storage")))
	f, _, err := r.OpenRead(rel(t, "public/storage/x.txt"))
	must(t, err)
	f.Close()
	must(t, r.WriteFile(rel(t, "public/storage/y.txt"), 0o644, strings.NewReader("y"), 1, false))
	if _, err := os.Stat(filepath.Join(site, "storage/app/public/y.txt")); err != nil {
		t.Fatal("write through inside link did not land in target dir")
	}

	// Strict: the same access is refused.
	r2, site2, _ := newTree(t, Options{NoSymlinks: true})
	must(t, os.MkdirAll(filepath.Join(site2, "real"), 0o755))
	must(t, os.WriteFile(filepath.Join(site2, "real/x"), []byte("x"), 0o644))
	must(t, os.Symlink("real", filepath.Join(site2, "lnk")))
	_, _, err = r2.OpenRead(rel(t, "lnk/x"))
	wantCode(t, err, CodeSymlink)
	f, _, err = r2.OpenRead(rel(t, "real/x"))
	must(t, err)
	f.Close()
}

func TestSymlinkAsLastComponent(t *testing.T) {
	r, site, outside := newTree(t, Options{})
	secret := filepath.Join(outside, "secret")
	must(t, os.Symlink(secret, filepath.Join(site, "link")))

	// Stat reports the link itself.
	in, err := r.Stat(rel(t, "link"))
	must(t, err)
	if in.Type != "symlink" {
		t.Fatalf("%+v", in)
	}
	// Write replaces the link, the target stays untouched.
	must(t, r.WriteFile(rel(t, "link"), 0o644, strings.NewReader("mine"), 4, true))
	if b, _ := os.ReadFile(secret); string(b) != "SECRET" {
		t.Fatalf("target modified: %q", b)
	}
	if fi, _ := os.Lstat(filepath.Join(site, "link")); fi.Mode()&os.ModeSymlink != 0 {
		t.Fatal("link still a link")
	}
	// Remove, Rename and Chmod act on the link itself or refuse.
	must(t, os.Remove(filepath.Join(site, "link")))
	must(t, os.Symlink(secret, filepath.Join(site, "link")))
	if err := r.Chmod(rel(t, "link"), 0o777); err == nil {
		t.Fatal("chmod on symlink succeeded")
	}
	if fi, _ := os.Stat(secret); fi.Mode().Perm() != 0o644 {
		t.Fatalf("target mode changed to %v", fi.Mode())
	}
	must(t, r.Remove(rel(t, "link"), false))
	if _, err := os.Stat(secret); err != nil {
		t.Fatal("target removed with the link")
	}
}

func TestRecursiveRemoveDoesNotFollowLinks(t *testing.T) {
	r, site, outside := newTree(t, Options{})
	must(t, os.MkdirAll(filepath.Join(site, "tree/sub"), 0o755))
	must(t, os.Symlink(outside, filepath.Join(site, "tree/sub/escape")))
	must(t, os.WriteFile(filepath.Join(site, "tree/sub/f"), []byte("x"), 0o644))
	must(t, r.Remove(rel(t, "tree"), true))
	if _, err := os.Stat(filepath.Join(outside, "secret")); err != nil {
		t.Fatalf("outside file gone: %v", err)
	}
	if _, err := os.Lstat(filepath.Join(site, "tree")); !os.IsNotExist(err) {
		t.Fatal("tree not removed")
	}
	// Removing the root itself is not possible by construction.
	wantCode(t, r.Remove(rel(t, ""), true), CodeInvalid)
}

func TestFIFOAndSpecialFiles(t *testing.T) {
	r, site, _ := newTree(t, Options{})
	must(t, unix.Mkfifo(filepath.Join(site, "fifo"), 0o644))
	done := make(chan error, 1)
	go func() { _, _, err := r.OpenRead(rel(t, "fifo")); done <- err }()
	select {
	case err := <-done:
		wantCode(t, err, CodeWrongType)
	case <-time.After(3 * time.Second):
		t.Fatal("reading a FIFO blocked")
	}
	_, _, err := r.OpenRead(rel(t, ""))
	wantCode(t, err, CodeWrongType)
	must(t, os.Mkdir(filepath.Join(site, "d"), 0o755))
	_, _, err = r.OpenRead(rel(t, "d"))
	wantCode(t, err, CodeWrongType)
}

func TestModeBitsRefused(t *testing.T) {
	r, _, _ := newTree(t, Options{})
	for _, m := range []uint32{0o4755, 0o2755, 0o1777, 0o10000} {
		wantCode(t, r.WriteFile(rel(t, "f"), m, strings.NewReader(""), 0, false), CodeInvalid)
		wantCode(t, r.Mkdir(rel(t, "d"), m, false), CodeInvalid)
	}
	must(t, r.WriteFile(rel(t, "f"), 0o644, strings.NewReader(""), 0, false))
	wantCode(t, r.Chmod(rel(t, "f"), 0o4755), CodeInvalid)
}

func TestWriteSemantics(t *testing.T) {
	r, site, _ := newTree(t, Options{})
	must(t, r.WriteFile(rel(t, "f"), 0o644, strings.NewReader("one"), 3, false))
	wantCode(t, r.WriteFile(rel(t, "f"), 0o644, strings.NewReader("two"), 3, false), CodeExists)
	must(t, r.WriteFile(rel(t, "f"), 0o600, strings.NewReader("two"), 3, true))
	if b, _ := os.ReadFile(filepath.Join(site, "f")); string(b) != "two" {
		t.Fatalf("%q", b)
	}
	// Body shorter or longer than declared: refused, old content intact, no temp file left.
	wantCode(t, r.WriteFile(rel(t, "f"), 0o644, strings.NewReader("ab"), 3, true), CodeInvalid)
	wantCode(t, r.WriteFile(rel(t, "f"), 0o644, strings.NewReader("abcd"), 3, true), CodeInvalid)
	if b, _ := os.ReadFile(filepath.Join(site, "f")); string(b) != "two" {
		t.Fatalf("content changed: %q", b)
	}
	ents, _ := os.ReadDir(site)
	if len(ents) != 1 {
		t.Fatalf("leftovers: %v", ents)
	}
	// Missing parent is an error, not an implicit mkdir.
	wantCode(t, r.WriteFile(rel(t, "no/such/f"), 0o644, strings.NewReader(""), 0, false), CodeNotFound)
	// Writing over a directory fails.
	must(t, r.Mkdir(rel(t, "dir"), 0o755, false))
	if err := r.WriteFile(rel(t, "dir"), 0o644, strings.NewReader("x"), 1, true); err == nil {
		t.Fatal("overwrote a directory")
	}
	// Large body.
	big := bytes.Repeat([]byte("0123456789abcdef"), 1<<16) // 1 MiB
	must(t, r.WriteFile(rel(t, "big"), 0o644, bytes.NewReader(big), int64(len(big)), false))
}

func TestMkdirParentsRefusesSymlinkInChain(t *testing.T) {
	r, site, outside := newTree(t, Options{})
	must(t, os.Symlink(outside, filepath.Join(site, "lnk")))
	err := r.Mkdir(rel(t, "lnk/a/b"), 0o755, true)
	if err == nil {
		t.Fatal("mkdir -p through link succeeded")
	}
	if ents, _ := os.ReadDir(outside); len(ents) != 1 {
		t.Fatalf("outside changed: %v", ents)
	}
	must(t, r.Mkdir(rel(t, "x/y"), 0o700, true))
	must(t, r.Mkdir(rel(t, "x/y"), 0o700, true)) // like mkdir -p: idempotent
}

func TestRenameNoReplaceAndEscapes(t *testing.T) {
	r, site, outside := newTree(t, Options{})
	must(t, r.WriteFile(rel(t, "a"), 0o644, strings.NewReader("a"), 1, false))
	must(t, r.WriteFile(rel(t, "b"), 0o644, strings.NewReader("b"), 1, false))
	wantCode(t, r.Rename(rel(t, "a"), rel(t, "b")), CodeExists)
	must(t, os.Symlink(outside, filepath.Join(site, "out")))
	wantCode(t, r.Rename(rel(t, "a"), rel(t, "out/a")), CodeEscape)
	wantCode(t, r.Rename(rel(t, "out/secret"), rel(t, "stolen")), CodeEscape)
	if _, err := os.Stat(filepath.Join(outside, "secret")); err != nil {
		t.Fatal("secret moved")
	}
}

func TestRootItself(t *testing.T) {
	r, _, _ := newTree(t, Options{})
	in, err := r.Stat(rel(t, ""))
	must(t, err)
	if in.Type != "dir" || in.Name != "." {
		t.Fatalf("%+v", in)
	}
	wantCode(t, r.Chmod(rel(t, ""), 0o755), CodeInvalid)
	wantCode(t, r.Rename(rel(t, ""), rel(t, "x")), CodeInvalid)
	wantCode(t, r.WriteFile(rel(t, ""), 0o644, strings.NewReader(""), 0, true), CodeInvalid)
}

func TestNewRootRejectsFile(t *testing.T) {
	f, err := os.CreateTemp(t.TempDir(), "x")
	must(t, err)
	fd, _ := unix.Dup(int(f.Fd()))
	_, err = NewRoot(fd, Options{})
	wantCode(t, err, CodeWrongType)
}

// Names that exist on disk (created by PHP, rsync, an archive) but cannot be expressed as a Rel:
// documents a known gap instead of hiding it.
func TestExistingUnaddressableNames(t *testing.T) {
	r, site, _ := newTree(t, Options{})
	weird := map[string]string{"invalid utf8": "bad\xffname", "newline": "line\nbreak"}
	for _, n := range weird {
		must(t, os.WriteFile(filepath.Join(site, n), []byte("x"), 0o644))
	}
	list, _, err := r.List(rel(t, ""), "", 0)
	must(t, err)
	for _, e := range list {
		_, perr := ParseRel(e.Name)
		t.Logf("listed %q, ParseRel error: %v", e.Name, perr)
	}
	if len(list) != 2 {
		t.Fatalf("%+v", list)
	}
	// They are listed but cannot be addressed by name: the file manager cannot rename or delete them.
	for _, n := range weird {
		if _, err := ParseRel(n); err == nil {
			t.Errorf("%q parsed", n)
		}
	}
}
