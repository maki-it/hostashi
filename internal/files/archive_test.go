// SPDX-License-Identifier: AGPL-3.0-or-later

package files

import (
	"archive/zip"
	"bytes"
	"errors"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

func TestWriteZip(t *testing.T) {
	r, home, _ := newRoot(t)

	if err := os.Symlink("index.php", filepath.Join(home, "htdocs", "sub", "rel")); err != nil {
		t.Fatal(err)
	}

	if err := os.Chmod(filepath.Join(home, "htdocs", "sub", "a.txt"), 0o600); err != nil {
		t.Fatal(err)
	}

	var buf bytes.Buffer

	res, err := r.WriteZip(&buf, []string{"htdocs", "fifo"})
	if err != nil {
		t.Fatal(err)
	}

	if res.Files != 2 || res.Skipped != 2 { // the symlink and the FIFO
		t.Errorf("result %+v", res)
	}

	zr, err := zip.NewReader(bytes.NewReader(buf.Bytes()), int64(buf.Len()))
	if err != nil {
		t.Fatal(err)
	}

	got := map[string]string{}

	for _, f := range zr.File {
		rc, _ := f.Open()
		b, _ := io.ReadAll(rc)
		_ = rc.Close()

		got[f.Name] = string(b)

		if f.Name == "htdocs/sub/a.txt" && f.Mode().Perm() != 0o600 {
			t.Errorf("mode %v", f.Mode())
		}
	}

	names := make([]string, 0, len(got))
	for n := range got {
		names = append(names, n)
	}

	sort.Strings(names)

	if strings.Join(names, " ") != "htdocs/ htdocs/index.php htdocs/sub/ htdocs/sub/a.txt" || got["htdocs/index.php"] != "<?php echo 1;" {
		t.Fatalf("archive %v", names)
	}

	// Through a symlink or out of the home: refused before the first byte.
	for _, p := range []string{"link/secret", "../outside", "nope"} {
		if _, err := r.CheckSelection([]string{p}); err == nil {
			t.Errorf("CheckSelection(%q) accepted", p)
		}
	}

	// A selected symlink is skipped, not followed.
	buf.Reset()

	res, err = r.WriteZip(&buf, []string{"link"})
	if err != nil || res.Files != 0 || res.Skipped != 1 {
		t.Fatalf("symlink: %+v %v", res, err)
	}
}

type zipItem struct {
	name, body string
	mode       os.FileMode
}

func makeZip(t *testing.T, path string, items []zipItem) {
	t.Helper()

	var buf bytes.Buffer

	zw := zip.NewWriter(&buf)

	for _, it := range items {
		h := &zip.FileHeader{Name: it.name, Method: zip.Deflate}
		if it.mode != 0 {
			h.SetMode(it.mode)
		}

		w, err := zw.CreateHeader(h)
		if err != nil {
			t.Fatal(err)
		}

		_, _ = w.Write([]byte(it.body))
	}

	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}

	if err := os.WriteFile(path, buf.Bytes(), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestExtract(t *testing.T) {
	r, home, outside := newRoot(t)

	makeZip(t, filepath.Join(home, "a.zip"), []zipItem{
		{name: "site/", mode: os.ModeDir | 0o755},
		{name: "site/index.php", body: "new"},
		{name: "site/deep/x/y.txt", body: "y", mode: 0o4755},
		{name: "site/link", body: "/etc/passwd", mode: os.ModeSymlink | 0o777},
		{name: "htdocs/index.php", body: "replaced"},
	})

	res, err := r.Extract("a.zip", "", ExtractOptions{})
	if err != nil {
		t.Fatal(err)
	}

	if res.Files != 2 || res.Kept != 1 || res.Skipped != 1 || res.Dirs != 3 {
		t.Errorf("result %+v", res)
	}

	if read(t, filepath.Join(home, "site", "deep", "x", "y.txt")) != "y" || read(t, filepath.Join(home, "htdocs", "index.php")) != "<?php echo 1;" {
		t.Fatal("content")
	}

	if st, _ := os.Stat(filepath.Join(home, "site", "deep", "x", "y.txt")); st.Mode()&os.ModeSetuid != 0 || st.Mode().Perm() != 0o755 {
		t.Errorf("mode %v", st.Mode())
	}

	if _, err := os.Lstat(filepath.Join(home, "site", "link")); !errors.Is(err, os.ErrNotExist) {
		t.Error("symlink entry extracted")
	}

	// With overwrite the existing file is replaced.
	res, err = r.Extract("a.zip", "", ExtractOptions{Overwrite: true})
	if err != nil || res.Kept != 0 || read(t, filepath.Join(home, "htdocs", "index.php")) != "replaced" {
		t.Fatalf("overwrite: %+v %v", res, err)
	}

	// Archives with a way out are refused as a whole, before anything is
	// written.
	for i, bad := range []string{"../evil.txt", "/etc/evil", "a/../../evil", "ok/./x"} {
		zipPath := filepath.Join(home, "bad.zip")
		makeZip(t, zipPath, []zipItem{{name: "first.txt", body: "x"}, {name: bad, body: "x"}})

		if _, err := r.Extract("bad.zip", "", ExtractOptions{}); !errors.Is(err, ErrInvalidPath) {
			t.Errorf("%d %q: %v", i, bad, err)
		}

		if _, err := os.Stat(filepath.Join(home, "first.txt")); err == nil {
			t.Errorf("%q: archive partly extracted", bad)
		}
	}

	// A folder in the target that is a symlink is not followed.
	makeZip(t, filepath.Join(home, "l.zip"), []zipItem{{name: "link/planted", body: "x"}})

	if _, err := r.Extract("l.zip", "", ExtractOptions{}); !errors.Is(err, ErrSymlink) {
		t.Errorf("through symlink: %v", err)
	}

	if _, err := os.Stat(filepath.Join(outside, "planted")); err == nil {
		t.Fatal("extracted outside the home")
	}

	if _, err := r.Extract("htdocs/index.php", "", ExtractOptions{}); !errors.Is(err, ErrNotRegular) {
		t.Errorf("not a zip: %v", err)
	}
}

func TestExtractLimits(t *testing.T) {
	r, home, _ := newRoot(t)

	items := make([]zipItem, MaxZipEntries+1)
	for i := range items {
		items[i] = zipItem{name: "d/", mode: os.ModeDir | 0o755}
	}

	makeZip(t, filepath.Join(home, "many.zip"), items)

	if _, err := r.Extract("many.zip", "", ExtractOptions{}); !errors.Is(err, ErrTooLarge) {
		t.Fatalf("too many entries: %v", err)
	}
}

func TestZipName(t *testing.T) {
	for _, n := range []string{"a", "a/b.txt", "dir/", "ä/ö.txt"} {
		if _, err := zipName(n); err != nil {
			t.Errorf("%q: %v", n, err)
		}
	}

	for _, n := range []string{"", "/", "/a", "..", "../a", "a/../b", "a//b", "\xff"} {
		if _, err := zipName(n); err == nil {
			t.Errorf("%q accepted", n)
		}
	}
}

func FuzzZipName(f *testing.F) {
	for _, s := range []string{"a/b", "../x", "/abs", "a/./b"} {
		f.Add(s)
	}

	f.Fuzz(func(t *testing.T, s string) {
		n, err := zipName(s)
		if err != nil {
			return
		}

		if strings.HasPrefix(n, "/") || n == "" {
			t.Fatalf("%q -> %q", s, n)
		}

		for _, c := range strings.Split(n, "/") {
			if c == ".." || c == "." || c == "" {
				t.Fatalf("%q -> %q", s, n)
			}
		}
	})
}
