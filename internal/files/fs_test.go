// SPDX-License-Identifier: AGPL-3.0-or-later

package files

import (
	"bytes"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
)

// newRoot makes a home with some content:
//
//	htdocs/index.php
//	htdocs/sub/a.txt
//	link -> /outside (symlink)
//	fifo (FIFO)
func newRoot(t *testing.T) (*Root, string, string) {
	t.Helper()

	base := t.TempDir()
	home := filepath.Join(base, "home")
	outside := filepath.Join(base, "outside")

	for _, d := range []string{home, outside, filepath.Join(home, "htdocs", "sub")} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}

	write(t, filepath.Join(home, "htdocs", "index.php"), "<?php echo 1;")
	write(t, filepath.Join(home, "htdocs", "sub", "a.txt"), "a")
	write(t, filepath.Join(outside, "secret"), "secret")

	if err := os.Symlink(outside, filepath.Join(home, "link")); err != nil {
		t.Fatal(err)
	}

	if err := syscall.Mkfifo(filepath.Join(home, "fifo"), 0o644); err != nil {
		t.Fatal(err)
	}

	r, err := Open(home)
	if err != nil {
		t.Fatal(err)
	}

	t.Cleanup(func() { _ = r.Close() })

	return r, home, outside
}

func write(t *testing.T, p, s string) {
	t.Helper()

	if err := os.WriteFile(p, []byte(s), 0o644); err != nil {
		t.Fatal(err)
	}
}

func read(t *testing.T, p string) string {
	t.Helper()

	b, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}

	return string(b)
}

func TestCleanPath(t *testing.T) {
	ok := map[string]string{
		"": "", "/": "", "a": "a", "/a/b": "a/b", "a/b/": "a/b", "ä ö/ß.txt": "ä ö/ß.txt", ".htaccess": ".htaccess", "..a": "..a",
	}
	for in, want := range ok {
		got, err := CleanPath(in)
		if err != nil || got != want {
			t.Errorf("CleanPath(%q) = %q, %v; want %q", in, got, err, want)
		}
	}

	for _, in := range []string{"..", "a/..", "../a", "a/./b", "a//b", "//", "a\x00b", "\xff", strings.Repeat("a", 256), strings.Repeat("a/", 2049)} {
		if _, err := CleanPath(in); !errors.Is(err, ErrInvalidPath) {
			t.Errorf("CleanPath(%q) = %v, want ErrInvalidPath", in, err)
		}
	}
}

func FuzzCleanPath(f *testing.F) {
	for _, s := range []string{"", "a/b", "../x", "a//b", "/a/", "\xff"} {
		f.Add(s)
	}

	f.Fuzz(func(t *testing.T, s string) {
		p, err := CleanPath(s)
		if err != nil {
			return
		}

		// An accepted path is stable and has no way up or out.
		if again, err := CleanPath(p); err != nil || again != p {
			t.Fatalf("not stable: %q -> %q -> %q, %v", s, p, again, err)
		}

		if p == "" {
			return
		}

		for _, c := range strings.Split(p, "/") {
			if c == "" || c == "." || c == ".." {
				t.Fatalf("%q accepted with component %q", s, c)
			}
		}
	})
}

func TestOpenRefusesSymlinkedHome(t *testing.T) {
	base := t.TempDir()
	if err := os.Symlink(t.TempDir(), filepath.Join(base, "home")); err != nil {
		t.Fatal(err)
	}

	if _, err := Open(filepath.Join(base, "home")); !errors.Is(err, ErrUnsafeHome) {
		t.Fatalf("got %v, want ErrUnsafeHome", err)
	}
}

func TestList(t *testing.T) {
	r, home, _ := newRoot(t)

	write(t, filepath.Join(home, "bad\xffname"), "x")

	l, err := r.List("")
	if err != nil {
		t.Fatal(err)
	}

	types := map[string]string{}
	for _, e := range l.Entries {
		types[e.Name] = e.Type
	}

	want := map[string]string{"htdocs": "dir", "link": "symlink", "fifo": "other"}
	for n, ty := range want {
		if types[n] != ty {
			t.Errorf("%s: type %q, want %q", n, types[n], ty)
		}
	}

	if len(l.Entries) != 3 || l.Unnamed != 1 || l.Truncated {
		t.Errorf("got %d entries, %d unnamed, truncated %v", len(l.Entries), l.Unnamed, l.Truncated)
	}

	for _, e := range l.Entries {
		if e.Name == "link" && e.Target == "" {
			t.Error("symlink target missing")
		}
	}

	sub, err := r.List("htdocs/")
	if err != nil || len(sub.Entries) != 2 || sub.Entries[0].Name != "index.php" || sub.Entries[0].Version == "" || sub.Entries[0].Mode != "0644" {
		t.Fatalf("htdocs: %+v, %v", sub, err)
	}
}

// The helper keeps one descriptor of the home for its whole life. Listing
// the home must not consume that descriptor's directory offset, or every
// later listing of the home comes back empty until the helper restarts.
func TestListHomeTwice(t *testing.T) {
	r, _, _ := newRoot(t)

	for i := range 3 {
		l, err := r.List("")
		if err != nil {
			t.Fatal(err)
		}

		if len(l.Entries) != 3 {
			t.Fatalf("listing %d: got %d entries, want 3: %+v", i+1, len(l.Entries), l.Entries)
		}
	}

	// Starting an upload into the home reads the home for stale parts; that
	// must not drain it either.
	if _, err := r.StartUpload("new.txt", UploadOptions{Size: 1}); err != nil {
		t.Fatal(err)
	}

	// The new part file shows up next to the three entries.
	if l, err := r.List(""); err != nil || len(l.Entries) != 4 {
		t.Fatalf("after upload start: %+v, %v", l, err)
	}
}

func TestNoWayOut(t *testing.T) {
	r, home, outside := newRoot(t)

	for _, p := range []string{"link", "link/secret", "../outside/secret"} {
		if _, err := r.List(p); err == nil {
			t.Errorf("List(%q) succeeded", p)
		}

		if f, _, err := r.OpenFile(p); err == nil {
			_ = f.Close()

			t.Errorf("OpenFile(%q) succeeded", p)
		}
	}

	if _, _, err := r.OpenFile("link/secret"); !errors.Is(err, ErrSymlink) {
		t.Errorf("OpenFile through a symlink: %v, want ErrSymlink", err)
	}

	if _, err := r.WriteFile("link/new", strings.NewReader("x"), WriteOptions{}); !errors.Is(err, ErrSymlink) {
		t.Errorf("write through a symlink: %v, want ErrSymlink", err)
	}

	if err := r.Mkdir("link/new"); !errors.Is(err, ErrSymlink) {
		t.Errorf("mkdir through a symlink: %v", err)
	}

	if err := r.Chmod([]string{"link"}, 0o777); !errors.Is(err, ErrSymlink) {
		t.Errorf("chmod of a symlink: %v, want ErrSymlink", err)
	}

	if err := r.Delete([]string{"link"}); err != nil {
		t.Fatal(err)
	}

	if read(t, filepath.Join(outside, "secret")) != "secret" {
		t.Fatal("outside file changed")
	}

	if _, err := os.Lstat(filepath.Join(home, "link")); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("symlink itself not deleted")
	}
}

func TestOpenFile(t *testing.T) {
	r, _, _ := newRoot(t)

	f, e, err := r.OpenFile("htdocs/index.php")
	if err != nil {
		t.Fatal(err)
	}

	b, _ := io.ReadAll(f)
	_ = f.Close()

	if string(b) != "<?php echo 1;" || e.Size != int64(len(b)) || e.Version == "" {
		t.Fatalf("got %q, %+v", b, e)
	}

	for p, want := range map[string]error{"fifo": ErrNotRegular, "htdocs": ErrNotRegular, "": ErrNotRegular, "nope": ErrNotFound} {
		if _, _, err := r.OpenFile(p); !errors.Is(err, want) {
			t.Errorf("OpenFile(%q) = %v, want %v", p, err, want)
		}
	}
}

func TestWriteFile(t *testing.T) {
	r, home, _ := newRoot(t)
	p := filepath.Join(home, "htdocs", "index.php")

	if err := os.Chmod(p, 0o640); err != nil {
		t.Fatal(err)
	}

	e, err := r.Stat("htdocs/index.php")
	if err != nil {
		t.Fatal(err)
	}

	if _, err := r.WriteFile("htdocs/index.php", strings.NewReader("new"), WriteOptions{}); !errors.Is(err, ErrExists) {
		t.Fatalf("create over existing: %v", err)
	}

	ne, err := r.WriteFile("htdocs/index.php", strings.NewReader("new"), WriteOptions{Version: e.Version})
	if err != nil {
		t.Fatal(err)
	}

	if read(t, p) != "new" || ne.Version == e.Version {
		t.Fatalf("content %q, version %q -> %q", read(t, p), e.Version, ne.Version)
	}

	if st, _ := os.Stat(p); st.Mode().Perm() != 0o640 {
		t.Errorf("mode %o, want 640 kept", st.Mode().Perm())
	}

	if _, err := r.WriteFile("htdocs/index.php", strings.NewReader("other"), WriteOptions{Version: e.Version}); !errors.Is(err, ErrChanged) {
		t.Fatalf("stale version: %v, want ErrChanged", err)
	}

	if _, err := r.WriteFile("htdocs/gone.php", strings.NewReader("x"), WriteOptions{Version: e.Version}); !errors.Is(err, ErrChanged) {
		t.Fatalf("removed file: %v, want ErrChanged", err)
	}

	if _, err := r.WriteFile("htdocs/big", bytes.NewReader(make([]byte, MaxEditBytes+1)), WriteOptions{}); !errors.Is(err, ErrTooLarge) {
		t.Fatalf("too large: %v", err)
	}

	if _, err := r.WriteFile("htdocs/new.txt", strings.NewReader("hi"), WriteOptions{}); err != nil || read(t, filepath.Join(home, "htdocs", "new.txt")) != "hi" {
		t.Fatalf("create: %v", err)
	}

	if _, err := r.WriteFile("fifo", strings.NewReader("x"), WriteOptions{Version: "x"}); !errors.Is(err, ErrNotRegular) {
		t.Fatalf("write over FIFO: %v", err)
	}

	if _, err := r.WriteFile("", strings.NewReader("x"), WriteOptions{}); !errors.Is(err, ErrHome) {
		t.Fatalf("write home: %v", err)
	}

	// No temporary files are left behind.
	ents, _ := os.ReadDir(filepath.Join(home, "htdocs"))
	for _, e := range ents {
		if strings.HasPrefix(e.Name(), tmpPrefix) {
			t.Errorf("left over %s", e.Name())
		}
	}
}

func TestCreateAndRename(t *testing.T) {
	r, home, _ := newRoot(t)

	if err := r.Mkdir("htdocs/img"); err != nil {
		t.Fatal(err)
	}

	if err := r.Mkdir("htdocs/img"); !errors.Is(err, ErrExists) {
		t.Fatalf("mkdir twice: %v", err)
	}

	if err := r.CreateFile("htdocs/img/x.txt"); err != nil {
		t.Fatal(err)
	}

	if err := r.CreateFile("htdocs/img/x.txt"); !errors.Is(err, ErrExists) {
		t.Fatalf("touch twice: %v", err)
	}

	if err := r.Rename("htdocs/img/x.txt", "htdocs/index.php"); !errors.Is(err, ErrExists) {
		t.Fatalf("rename over existing: %v", err)
	}

	if err := r.Rename("htdocs/img", "htdocs/img/deeper"); !errors.Is(err, ErrIntoItself) {
		t.Fatalf("rename into itself: %v", err)
	}

	if err := r.Rename("htdocs/img/x.txt", "htdocs/y.txt"); err != nil {
		t.Fatal(err)
	}

	if _, err := os.Stat(filepath.Join(home, "htdocs", "y.txt")); err != nil {
		t.Fatal(err)
	}

	if err := r.Rename("", "x"); !errors.Is(err, ErrHome) {
		t.Fatalf("rename home: %v", err)
	}
}

func TestMoveCopyDelete(t *testing.T) {
	r, home, _ := newRoot(t)

	if err := os.Symlink("index.php", filepath.Join(home, "htdocs", "sub", "rel")); err != nil {
		t.Fatal(err)
	}

	if err := syscall.Mkfifo(filepath.Join(home, "htdocs", "sub", "pipe"), 0o644); err != nil {
		t.Fatal(err)
	}

	if err := os.Chmod(filepath.Join(home, "htdocs", "sub", "a.txt"), 0o600); err != nil {
		t.Fatal(err)
	}

	if err := r.Mkdir("copy"); err != nil {
		t.Fatal(err)
	}

	res, err := r.Copy([]string{"htdocs/sub", "htdocs/index.php"}, "copy")
	if err != nil {
		t.Fatal(err)
	}

	if res.Skipped != 1 {
		t.Errorf("skipped %d, want 1 (the FIFO)", res.Skipped)
	}

	if read(t, filepath.Join(home, "copy", "sub", "a.txt")) != "a" || read(t, filepath.Join(home, "copy", "index.php")) != "<?php echo 1;" {
		t.Fatal("copied content differs")
	}

	if st, _ := os.Stat(filepath.Join(home, "copy", "sub", "a.txt")); st.Mode().Perm() != 0o600 {
		t.Errorf("copied mode %o, want 600", st.Mode().Perm())
	}

	if tgt, err := os.Readlink(filepath.Join(home, "copy", "sub", "rel")); err != nil || tgt != "index.php" {
		t.Errorf("copied symlink: %q, %v", tgt, err)
	}

	if _, err := r.Copy([]string{"htdocs/index.php"}, "copy"); !errors.Is(err, ErrExists) {
		t.Fatalf("copy over existing: %v", err)
	}

	if _, err := r.Copy([]string{"htdocs"}, "htdocs/sub"); !errors.Is(err, ErrIntoItself) {
		t.Fatalf("copy into itself: %v", err)
	}

	if _, err := r.Copy([]string{"htdocs"}, ""); !errors.Is(err, ErrIntoItself) && !errors.Is(err, ErrExists) {
		t.Fatalf("copy onto itself: %v", err)
	}

	if err := r.Move([]string{"copy/sub", "copy/index.php"}, "htdocs/sub"); err != nil {
		t.Fatal(err)
	}

	if _, err := os.Stat(filepath.Join(home, "htdocs", "sub", "sub", "a.txt")); err != nil {
		t.Fatal(err)
	}

	if err := r.Move([]string{"htdocs"}, "htdocs/sub"); !errors.Is(err, ErrIntoItself) {
		t.Fatalf("move into itself: %v", err)
	}

	if err := r.Delete([]string{"htdocs/sub", "copy"}); err != nil {
		t.Fatal(err)
	}

	for _, p := range []string{"htdocs/sub", "copy"} {
		if _, err := os.Lstat(filepath.Join(home, p)); !errors.Is(err, os.ErrNotExist) {
			t.Errorf("%s still there", p)
		}
	}

	if err := r.Delete([]string{"nope"}); !errors.Is(err, ErrNotFound) {
		t.Fatalf("delete missing: %v", err)
	}

	if err := r.Delete([]string{""}); !errors.Is(err, ErrHome) {
		t.Fatalf("delete home: %v", err)
	}

	if err := r.Delete(nil); !errors.Is(err, ErrInvalidPath) {
		t.Fatalf("delete nothing: %v", err)
	}

	if err := r.Delete(make([]string, MaxSelection+1)); !errors.Is(err, ErrInvalidPath) {
		t.Fatalf("delete too many: %v", err)
	}
}

func TestChmod(t *testing.T) {
	r, home, _ := newRoot(t)

	if err := r.Chmod([]string{"htdocs/index.php", "htdocs/sub"}, 0o700); err != nil {
		t.Fatal(err)
	}

	for _, p := range []string{"htdocs/index.php", "htdocs/sub"} {
		if st, _ := os.Stat(filepath.Join(home, p)); st.Mode().Perm() != 0o700 {
			t.Errorf("%s: mode %o", p, st.Mode().Perm())
		}
	}

	for _, s := range []string{"4755", "2755", "1777", "", "9", "0o644", "-1", "07777", "abc"} {
		if _, err := ParseMode(s); !errors.Is(err, ErrInvalidMode) {
			t.Errorf("ParseMode(%q) = %v, want ErrInvalidMode", s, err)
		}
	}

	for s, want := range map[string]uint32{"644": 0o644, "0755": 0o755, "0": 0, "0777": 0o777} {
		if m, err := ParseMode(s); err != nil || m != want {
			t.Errorf("ParseMode(%q) = %o, %v", s, m, err)
		}
	}

	if err := r.Chmod([]string{"htdocs/index.php"}, 0o4755); !errors.Is(err, ErrInvalidMode) {
		t.Fatalf("setuid: %v", err)
	}
}
