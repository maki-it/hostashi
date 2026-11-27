// SPDX-License-Identifier: AGPL-3.0-or-later

package gitsync

import (
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/plumbing"
	"github.com/go-git/go-git/v5/plumbing/object"
	"github.com/go-git/go-git/v5/plumbing/transport/client"
	"github.com/go-git/go-git/v5/plumbing/transport/server"
	"golang.org/x/crypto/ssh"

	"github.com/maki-it/hostashi/internal/agentapi"
)

// The tests serve a local repository in process, never through
// git-upload-pack.
func init() { client.InstallProtocol("file", server.DefaultServer) }

type srcRepo struct {
	t    *testing.T
	dir  string
	repo *git.Repository
}

func newSrc(t *testing.T) *srcRepo {
	t.Helper()

	dir := t.TempDir()

	r, err := git.PlainInitWithOptions(dir, &git.PlainInitOptions{
		InitOptions: git.InitOptions{DefaultBranch: plumbing.NewBranchReferenceName("main")},
	})
	if err != nil {
		t.Fatal(err)
	}

	return &srcRepo{t: t, dir: dir, repo: r}
}

func (s *srcRepo) write(name, content string, mode os.FileMode) {
	s.t.Helper()

	p := filepath.Join(s.dir, name)
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		s.t.Fatal(err)
	}

	if err := os.WriteFile(p, []byte(content), mode); err != nil {
		s.t.Fatal(err)
	}

	if err := os.Chmod(p, mode); err != nil {
		s.t.Fatal(err)
	}
}

func (s *srcRepo) link(name, target string) {
	s.t.Helper()

	if err := os.Symlink(target, filepath.Join(s.dir, name)); err != nil {
		s.t.Fatal(err)
	}
}

func (s *srcRepo) commit(msg string) string {
	s.t.Helper()

	wt, err := s.repo.Worktree()
	if err != nil {
		s.t.Fatal(err)
	}

	if err := wt.AddWithOptions(&git.AddOptions{All: true}); err != nil {
		s.t.Fatal(err)
	}

	h, err := wt.Commit(msg, &git.CommitOptions{
		All:    true,
		Author: &object.Signature{Name: "Kim", Email: "kim@example.com", When: time.Unix(2_000_000_000, 0)},
	})
	if err != nil {
		s.t.Fatal(err)
	}

	return h.String()
}

func opts(t *testing.T, src *srcRepo, base, release string) Options {
	t.Helper()

	return Options{
		URL: "file://" + src.dir + "/.git", Branch: "main",
		Cache: filepath.Join(base, "repo"), Release: filepath.Join(base, "releases", release),
		SharedDir:  filepath.Join(base, "shared"),
		allowLocal: true, fullFetch: true,
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

func TestSync(t *testing.T) {
	src := newSrc(t)
	src.write("public/index.php", "<?php echo 1;", 0o644)
	src.write("bin/build", "#!/bin/sh\n", 0o755)
	src.write(".env", "APP_ENV=example\n", 0o644)
	src.link("public/current", "../bin")
	first := src.commit("First release\n\nMore text.")

	base := t.TempDir()
	if err := os.MkdirAll(filepath.Join(base, "releases"), 0o755); err != nil {
		t.Fatal(err)
	}

	o := opts(t, src, base, "1")
	o.Shared = []string{".env", "storage/uploads"}

	res, err := Sync(t.Context(), o)
	if err != nil {
		t.Fatal(err)
	}

	if res.Commit != first || res.Author != "Kim" || res.Message != "First release" || res.Files < 4 {
		t.Fatalf("result %+v", res)
	}

	rel := o.Release
	if got := read(t, filepath.Join(rel, "public/index.php")); got != "<?php echo 1;" {
		t.Fatalf("index %q", got)
	}

	if fi, err := os.Stat(filepath.Join(rel, "bin/build")); err != nil || fi.Mode().Perm() != 0o755 {
		t.Fatalf("executable: %v %v", fi, err)
	}

	if fi, _ := os.Stat(filepath.Join(rel, "public/index.php")); fi.Mode().Perm() != 0o644 {
		t.Fatalf("file mode %v", fi.Mode())
	}

	if target, err := os.Readlink(filepath.Join(rel, "public/current")); err != nil || target != "../bin" {
		t.Fatalf("link %q %v", target, err)
	}

	if _, err := os.Stat(filepath.Join(rel, ".git")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("release has .git: %v", err)
	}

	// Shared: the commit's .env moved to shared, a missing path is made.
	if target, _ := os.Readlink(filepath.Join(rel, ".env")); target != filepath.Join(o.SharedDir, ".env") {
		t.Fatalf(".env link %q", target)
	}

	if got := read(t, filepath.Join(o.SharedDir, ".env")); got != "APP_ENV=example\n" {
		t.Fatalf("shared .env %q", got)
	}

	if target, _ := os.Readlink(filepath.Join(rel, "storage/uploads")); target != filepath.Join(o.SharedDir, "storage/uploads") {
		t.Fatalf("uploads link %q", target)
	}

	// The site changes its .env; the next deploy keeps it.
	if err := os.WriteFile(filepath.Join(o.SharedDir, ".env"), []byte("APP_ENV=production\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	src.write("public/index.php", "<?php echo 2;", 0o644)
	second := src.commit("Second")

	o2 := opts(t, src, base, "2")
	o2.Shared = o.Shared

	res, err = Sync(t.Context(), o2)
	if err != nil {
		t.Fatal(err)
	}

	if res.Commit != second || read(t, filepath.Join(o2.Release, "public/index.php")) != "<?php echo 2;" {
		t.Fatalf("second %+v", res)
	}

	if got := read(t, filepath.Join(o2.Release, ".env")); got != "APP_ENV=production\n" {
		t.Fatalf("shared .env after deploy %q", got)
	}

	// The release must be new.
	if _, err := Sync(t.Context(), o2); err == nil {
		t.Fatal("existing release overwritten")
	}

	// A pinned commit that is the head.
	o3 := opts(t, src, base, "3")
	o3.Commit = second

	if res, err := Sync(t.Context(), o3); err != nil || res.Commit != second {
		t.Fatalf("pinned %+v %v", res, err)
	}

	// An unknown commit fails.
	o4 := opts(t, src, base, "4")
	o4.Commit = strings.Repeat("ab", 20)

	if _, err := Sync(t.Context(), o4); err == nil {
		t.Fatal("unknown commit accepted")
	}

	// A missing branch fails.
	o5 := opts(t, src, base, "5")
	o5.Branch = "nope"

	if _, err := Sync(t.Context(), o5); err == nil {
		t.Fatal("missing branch accepted")
	}
}

func TestSyncRefusesLinksOut(t *testing.T) {
	for name, target := range map[string]string{
		"parent":   "../../etc",
		"absolute": "/etc/passwd",
		"git":      "../.git/config",
	} {
		t.Run(name, func(t *testing.T) {
			src := newSrc(t)
			src.write("a/index.html", "x", 0o644)
			src.link("a/bad", target)
			src.commit("bad link")

			base := t.TempDir()
			if err := os.MkdirAll(filepath.Join(base, "releases"), 0o755); err != nil {
				t.Fatal(err)
			}

			if _, err := Sync(t.Context(), opts(t, src, base, "1")); err == nil || !strings.Contains(err.Error(), "points out") {
				t.Fatalf("link %q: %v", target, err)
			}
		})
	}
}

func TestCheck(t *testing.T) {
	base := t.TempDir()
	good := Options{URL: "https://github.com/acme/site.git", Branch: "main", Cache: base + "/repo", Release: base + "/r"}

	if err := good.check(); err != nil {
		t.Fatal(err)
	}

	for name, mod := range map[string]func(*Options){
		"file url":        func(o *Options) { o.URL = "file:///srv/x" },
		"local path":      func(o *Options) { o.URL = "/srv/x" },
		"bad branch":      func(o *Options) { o.Branch = "-x" },
		"short commit":    func(o *Options) { o.Commit = "abc" },
		"upper commit":    func(o *Options) { o.Commit = strings.Repeat("AB", 20) },
		"relative cache":  func(o *Options) { o.Cache = "repo" },
		"shared, no dir":  func(o *Options) { o.Shared = []string{".env"} },
		"shared traverse": func(o *Options) { o.Shared, o.SharedDir = []string{"../x"}, base+"/s" },
	} {
		o := good
		mod(&o)

		if err := o.check(); err == nil {
			t.Errorf("%s accepted", name)
		}
	}

	// SSH needs the confirmed host key.
	a := Auth{Kind: agentapi.GitAuthSSH, Key: []byte("x")}
	if _, err := a.method("git@github.com:acme/site.git"); !errors.Is(err, ErrHostKey) {
		t.Fatalf("no host key: %v", err)
	}

	if _, err := (Auth{Kind: "password"}).method(good.URL); err == nil {
		t.Fatal("unknown auth accepted")
	}
}

func TestEntryNamesAndLinks(t *testing.T) {
	for _, n := range []string{"index.php", "a/b/c.txt", ".env", ".github/workflows/x.yml"} {
		if err := checkEntryName(n); err != nil {
			t.Errorf("%s: %v", n, err)
		}
	}

	for _, n := range []string{"", "/etc/passwd", "../x", "a/../../x", "a/./b", ".git/config", "a/.GIT/hooks/x", "a\\b", "a\x00b"} {
		if err := checkEntryName(n); err == nil {
			t.Errorf("%q accepted", n)
		}
	}

	for name, ok := range map[[2]string]bool{
		{"a/link", "b"}:            true,
		{"a/link", "../b"}:         true,
		{"a/b/link", "../../c"}:    true,
		{"link", "../x"}:           false,
		{"a/link", "../../x"}:      false,
		{"a/link", "/etc"}:         false,
		{"a/link", ""}:             false,
		{"a/link", "../.git/HEAD"}: false,
	} {
		if got := linkInside(name[0], name[1]); got != ok {
			t.Errorf("%v: %v", name, got)
		}
	}

	if got := truncate("äää", 3); got != "ä" {
		t.Errorf("truncate %q", got)
	}
}

func TestOnlyHTTPSAndSSH(t *testing.T) {
	for _, scheme := range []string{"git", "http"} {
		if _, ok := client.Protocols[scheme]; ok {
			t.Errorf("%s transport installed", scheme)
		}
	}

	for _, scheme := range []string{"https", "ssh"} {
		if _, ok := client.Protocols[scheme]; !ok {
			t.Errorf("%s transport missing", scheme)
		}
	}
}

// The web server passes the deploy base as "other": a new base and one left
// closed get the search bit, the cache stays private.
func TestSyncBaseMode(t *testing.T) {
	src := newSrc(t)
	src.write("index.html", "hi", 0o644)
	src.commit("First")

	for _, existing := range []bool{false, true} {
		base := filepath.Join(t.TempDir(), ".deploy")

		if existing {
			if err := os.Mkdir(base, 0o700); err != nil {
				t.Fatal(err)
			}
		}

		if _, err := Sync(t.Context(), opts(t, src, base, "1")); err != nil {
			t.Fatal(err)
		}

		for dir, want := range map[string]os.FileMode{base: BaseMode, filepath.Join(base, "repo"): 0o700} {
			fi, err := os.Stat(dir)
			if err != nil {
				t.Fatal(err)
			}

			if got := fi.Mode().Perm(); got&0o001 != want&0o001 || (dir != base && got != want) {
				t.Fatalf("existing %v: %s is %o", existing, dir, got)
			}
		}
	}
}

func TestHostKeyAlgorithms(t *testing.T) {
	if got := hostKeyAlgorithms(ssh.KeyAlgoRSA); !slices.Equal(got, []string{ssh.KeyAlgoRSASHA512, ssh.KeyAlgoRSASHA256}) {
		t.Fatalf("rsa %v", got)
	}

	if got := hostKeyAlgorithms(ssh.KeyAlgoED25519); !slices.Equal(got, []string{ssh.KeyAlgoED25519}) {
		t.Fatalf("ed25519 %v", got)
	}
}
