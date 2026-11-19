// SPDX-License-Identifier: AGPL-3.0-or-later

package pages

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/maki-it/hostashi/internal/agentapi"
)

func upload(t *testing.T, s *Store, id string, files map[string][]byte, chunk int) string {
	t.Helper()

	if err := s.Begin(id); err != nil {
		t.Fatal(err)
	}

	for name, b := range files {
		for off := 0; off < len(b) || off == 0; off += chunk {
			end := min(off+chunk, len(b))
			if err := s.Write(id, name, int64(off), b[off:end]); err != nil {
				t.Fatalf("write %s: %v", name, err)
			}

			if len(b) == 0 {
				break
			}
		}
	}

	d := agentapi.PageDigest(files)
	if err := s.Commit(id, d); err != nil {
		t.Fatalf("commit: %v", err)
	}

	return d
}

func TestUploadCommitAndServeLayout(t *testing.T) {
	s := &Store{Dir: filepath.Join(t.TempDir(), "pages")}
	files := map[string][]byte{
		"index.html":        []byte("<h1>back soon</h1>"),
		"css/site.css":      []byte(strings.Repeat("a{}", 5000)),
		"img/logo/logo.svg": []byte("<svg/>"),
		"empty.js":          {},
	}

	d := upload(t, s, "main", files, 1000)

	for name, want := range files {
		p := filepath.Join(s.Path("main", d), name)

		got, err := os.ReadFile(p)
		if err != nil || string(got) != string(want) {
			t.Fatalf("%s: %v", name, err)
		}

		info, _ := os.Stat(p)
		if info.Mode().Perm() != 0o644 {
			t.Errorf("%s mode %v", name, info.Mode())
		}
	}

	if info, _ := os.Stat(filepath.Join(s.Path("main", d), "img")); info.Mode().Perm() != 0o755 {
		t.Errorf("dir mode %v", info.Mode())
	}

	if _, err := os.Stat(filepath.Join(s.Dir, ".upload-main")); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("upload directory left behind")
	}

	if !s.Has("main", d) || s.Has("main", strings.Repeat("0", 64)) {
		t.Fatal("Has")
	}

	// The same content again is fine.
	if got := upload(t, s, "main", files, 4096); got != d {
		t.Fatal("digest changed")
	}
}

func TestCommitRefusesWrongContent(t *testing.T) {
	s := &Store{Dir: t.TempDir()}

	if err := s.Commit("main", strings.Repeat("0", 64)); !errors.Is(err, ErrNoUpload) {
		t.Fatalf("commit without upload: %v", err)
	}

	if err := s.Write("main", "index.html", 0, []byte("x")); !errors.Is(err, ErrNoUpload) {
		t.Fatalf("write without upload: %v", err)
	}

	if err := s.Begin("main"); err != nil {
		t.Fatal(err)
	}

	if err := s.Write("main", "style.css", 0, []byte("x")); err != nil {
		t.Fatal(err)
	}

	if err := s.Commit("main", agentapi.PageDigest(map[string][]byte{"style.css": []byte("x")})); !errors.Is(err, ErrNoIndex) {
		t.Fatalf("no index: %v", err)
	}

	if err := s.Write("main", "index.html", 0, []byte("hi")); err != nil {
		t.Fatal(err)
	}

	if err := s.Commit("main", agentapi.PageDigest(map[string][]byte{"index.html": []byte("other")})); !errors.Is(err, ErrDigest) {
		t.Fatalf("wrong digest: %v", err)
	}

	if vs, _ := s.List(); len(vs) != 0 {
		t.Fatalf("listed %v", vs)
	}
}

func TestWriteRefusesBadPaths(t *testing.T) {
	s := &Store{Dir: t.TempDir()}
	if err := s.Begin("main"); err != nil {
		t.Fatal(err)
	}

	for _, p := range []string{"../x.html", "/etc/x.html", ".env.html", "a/../../x.html", "x.php", "a/b/c/d.html", "-x.html", "a//b.html", ""} {
		if err := s.Write("main", p, 0, []byte("x")); err == nil {
			t.Errorf("%q accepted", p)
		}
	}

	// A symlink planted in the upload is not followed.
	if err := os.Symlink("/etc", filepath.Join(s.Dir, ".upload-main", "css")); err != nil {
		t.Fatal(err)
	}

	if err := s.Write("main", "css/x.css", 0, []byte("x")); err == nil {
		t.Fatal("wrote through a symlink")
	}

	if err := s.Write("main", "index.html", agentapi.MaxPageBytes, []byte("x")); err == nil {
		t.Fatal("offset beyond the limit accepted")
	}
}

func TestCommitRefusesSymlinkAndTooMuch(t *testing.T) {
	s := &Store{Dir: t.TempDir()}
	if err := s.Begin("main"); err != nil {
		t.Fatal(err)
	}

	if err := s.Write("main", "index.html", 0, []byte("x")); err != nil {
		t.Fatal(err)
	}

	if err := os.Symlink("/etc/passwd", filepath.Join(s.Dir, ".upload-main", "pw.html")); err != nil {
		t.Fatal(err)
	}

	if err := s.Commit("main", agentapi.PageDigest(map[string][]byte{"index.html": []byte("x")})); err == nil {
		t.Fatal("committed a symlink")
	}

	if err := s.Begin("big"); err != nil {
		t.Fatal(err)
	}

	for i := range agentapi.MaxPageFiles + 1 {
		if err := s.Write("big", "f"+strings.Repeat("x", i%3)+string(rune('a'+i%26))+string(rune('a'+i/26))+".css", 0, []byte("x")); err != nil {
			t.Fatal(err)
		}
	}

	if err := s.Commit("big", strings.Repeat("0", 64)); !errors.Is(err, ErrTooLarge) {
		t.Fatalf("too many files: %v", err)
	}
}

func TestPruneAndDelete(t *testing.T) {
	s := &Store{Dir: t.TempDir()}
	v1 := upload(t, s, "main", map[string][]byte{"index.html": []byte("1")}, 10)
	v2 := upload(t, s, "main", map[string][]byte{"index.html": []byte("2")}, 10)
	v3 := upload(t, s, "main", map[string][]byte{"index.html": []byte("3")}, 10)
	other := upload(t, s, "other", map[string][]byte{"index.html": []byte("o")}, 10)

	// Make the order of modification times certain.
	for i, d := range []string{v1, v2, v3} {
		ts := testTime(i)
		if err := os.Chtimes(s.Path("main", d), ts, ts); err != nil {
			t.Fatal(err)
		}
	}

	if err := s.Prune(map[string]bool{"main." + v1: true}); err != nil {
		t.Fatal(err)
	}

	if !s.Has("main", v1) || s.Has("main", v2) || !s.Has("main", v3) || !s.Has("other", other) {
		t.Fatal("prune kept or removed the wrong versions")
	}

	if err := s.Delete("main"); err != nil {
		t.Fatal(err)
	}

	vs, err := s.List()
	if err != nil || len(vs) != 1 || vs[0].ID != "other" {
		t.Fatalf("%v %v", vs, err)
	}
}

func testTime(i int) time.Time { return time.Unix(1_700_000_000+int64(i)*60, 0) }
