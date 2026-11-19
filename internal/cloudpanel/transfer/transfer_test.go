// SPDX-License-Identifier: AGPL-3.0-or-later

package transfer

import (
	"context"
	"errors"
	"io/fs"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/pkg/sftp"
	"golang.org/x/sys/unix"
)

// remote serves the real file system over an in-memory, read-only SFTP
// session, like sftp-server -R on the old server.
func remote(t *testing.T) Remote {
	t.Helper()

	cc, sc := net.Pipe()

	srv, err := sftp.NewServer(sc, sftp.ReadOnly())
	must(t, err)

	go func() { _ = srv.Serve() }()

	c, err := sftp.NewClientPipe(cc, cc)
	must(t, err)

	t.Cleanup(func() {
		_ = c.Close()
		_ = srv.Close()
	})

	return SFTP(c)
}

func must(t *testing.T, err error) {
	t.Helper()

	if err != nil {
		t.Fatal(err)
	}
}

func write(t *testing.T, p, body string, mode os.FileMode) {
	t.Helper()
	must(t, os.MkdirAll(filepath.Dir(p), 0o755))
	must(t, os.WriteFile(p, []byte(body), 0o600))
	must(t, os.Chmod(p, mode))
}

func openDir(t *testing.T, p string) int {
	t.Helper()

	fd, err := unix.Open(p, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_CLOEXEC, 0)
	must(t, err)
	t.Cleanup(func() { _ = unix.Close(fd) })

	return fd
}

func read(t *testing.T, p string) string {
	t.Helper()

	b, err := os.ReadFile(p)
	must(t, err)

	return string(b)
}

func run(t *testing.T, r Remote, src, dst string) Stats {
	t.Helper()

	st, err := Copy(context.Background(), r, src, openDir(t, dst), Limits{})
	must(t, err)

	return st
}

func TestCopyAndSync(t *testing.T) {
	src, dst := t.TempDir(), t.TempDir()
	r := remote(t)

	write(t, filepath.Join(src, "index.php"), "<?php echo 1;", 0o644)
	write(t, filepath.Join(src, "wp-content/uploads/a.jpg"), "jpeg", 0o640)
	write(t, filepath.Join(src, "bin/tool"), "#!", 0o4755) // setuid is dropped
	must(t, os.Symlink("/etc/passwd", filepath.Join(src, "link")))
	must(t, os.Chmod(filepath.Join(src, "wp-content"), 0o500))
	t.Cleanup(func() { _ = os.Chmod(filepath.Join(src, "wp-content"), 0o755) })

	old := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	must(t, os.Chtimes(filepath.Join(src, "index.php"), old, old))

	st := run(t, r, src, dst)
	if st.Files != 3 || st.Links != 1 || st.Dirs != 3 || st.Skipped != 0 || st.Bytes != int64(len("<?php echo 1;jpeg#!")) {
		t.Fatalf("first run %+v", st)
	}

	if got := read(t, filepath.Join(dst, "wp-content/uploads/a.jpg")); got != "jpeg" {
		t.Fatalf("content %q", got)
	}

	fi, err := os.Stat(filepath.Join(dst, "bin/tool"))
	must(t, err)

	if fi.Mode() != 0o755 {
		t.Fatalf("mode %v, want 0755 without setuid", fi.Mode())
	}

	fi, err = os.Stat(filepath.Join(dst, "index.php"))
	must(t, err)

	if !fi.ModTime().Equal(old) {
		t.Fatalf("mtime %v, want %v", fi.ModTime(), old)
	}

	fi, err = os.Stat(filepath.Join(dst, "wp-content"))
	must(t, err)

	if fi.Mode().Perm() != 0o700 {
		t.Fatalf("directory mode %v, want 0700 (0500 plus owner rights)", fi.Mode().Perm())
	}

	if target, err := os.Readlink(filepath.Join(dst, "link")); err != nil || target != "/etc/passwd" {
		t.Fatalf("link %q %v", target, err)
	}

	// Nothing changed: nothing is copied again.
	st = run(t, r, src, dst)
	if st.Files != 0 || st.Bytes != 0 || st.Unchanged != 4 || st.Deleted != 0 {
		t.Fatalf("second run %+v", st)
	}

	// The final sync: one file changed, one removed, one added, a file
	// became a directory.
	write(t, filepath.Join(src, "index.php"), "<?php echo 2;;", 0o644)
	must(t, os.Remove(filepath.Join(src, "link")))
	write(t, filepath.Join(src, "new.txt"), "n", 0o644)
	must(t, os.Remove(filepath.Join(src, "bin/tool")))
	write(t, filepath.Join(src, "bin/tool/inner"), "i", 0o644)
	write(t, filepath.Join(dst, "stale/x"), "x", 0o644) // only on the new side

	st = run(t, r, src, dst)
	if st.Files != 3 || st.Deleted != 2 || st.Unchanged != 1 {
		t.Fatalf("sync %+v", st)
	}

	if got := read(t, filepath.Join(dst, "index.php")); got != "<?php echo 2;;" {
		t.Fatalf("content after sync %q", got)
	}

	if got := read(t, filepath.Join(dst, "bin/tool/inner")); got != "i" {
		t.Fatalf("file became directory: %q", got)
	}

	for _, gone := range []string{"link", "stale"} {
		if _, err := os.Lstat(filepath.Join(dst, gone)); !errors.Is(err, fs.ErrNotExist) {
			t.Fatalf("%s still there: %v", gone, err)
		}
	}

	ents, err := os.ReadDir(dst)
	must(t, err)

	for _, e := range ents {
		if strings.HasPrefix(e.Name(), tmpPrefix) {
			t.Fatalf("temporary file left: %s", e.Name())
		}
	}
}

// A local symlink where the old tree has a directory is replaced, never
// followed out of the target.
func TestLocalSymlinkNotFollowed(t *testing.T) {
	src, dst, outside := t.TempDir(), t.TempDir(), t.TempDir()

	write(t, filepath.Join(src, "uploads/a"), "a", 0o644)
	write(t, filepath.Join(src, "f"), "f", 0o644)
	must(t, os.Symlink(outside, filepath.Join(dst, "uploads")))
	must(t, os.Symlink(filepath.Join(outside, "f"), filepath.Join(dst, "f")))

	run(t, remote(t), src, dst)

	ents, err := os.ReadDir(outside)
	must(t, err)

	if len(ents) != 0 {
		t.Fatalf("wrote outside the target: %v", ents)
	}

	if fi, err := os.Lstat(filepath.Join(dst, "uploads")); err != nil || !fi.IsDir() {
		t.Fatalf("uploads is not a directory now: %v", err)
	}
}

func TestSpecialFilesSkipped(t *testing.T) {
	src, dst := t.TempDir(), t.TempDir()

	must(t, unix.Mkfifo(filepath.Join(src, "fifo"), 0o644))
	write(t, filepath.Join(src, "ok"), "ok", 0o644)

	st := run(t, remote(t), src, dst)
	if st.Files != 1 || st.Skipped != 1 || len(st.SkippedPaths) != 1 || !strings.HasPrefix(st.SkippedPaths[0], "fifo:") {
		t.Fatalf("%+v", st)
	}

	if _, err := os.Lstat(filepath.Join(dst, "fifo")); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("fifo copied: %v", err)
	}
}

// foreign reports a different owner for some names, as the old server does
// for a file of another site user or root.
type foreign struct {
	Remote

	names map[string]bool // reported as another user's in listings
	open  map[string]bool // reported as another user's once opened
}

type ownerInfo struct {
	fs.FileInfo

	st *sftp.FileStat
}

func (o ownerInfo) Sys() any { return o.st }

func other(fi fs.FileInfo) fs.FileInfo {
	st, ok := fi.Sys().(*sftp.FileStat)
	if !ok {
		panic("no sftp attributes")
	}

	st2 := *st
	st2.UID++

	return ownerInfo{fi, &st2}
}

func (f foreign) ReadDir(p string) ([]fs.FileInfo, error) {
	list, err := f.Remote.ReadDir(p)
	for i, fi := range list {
		if f.names[fi.Name()] {
			list[i] = other(fi)
		}
	}

	return list, err
}

type foreignFile struct{ File }

func (f foreignFile) Stat() (fs.FileInfo, error) {
	fi, err := f.File.Stat()
	if err != nil {
		return nil, err
	}

	return other(fi), nil
}

func (f foreign) Open(p string) (File, error) {
	file, err := f.Remote.Open(p)
	if err != nil || !f.open[filepath.Base(p)] {
		return file, err
	}

	return foreignFile{file}, nil
}

func TestForeignOwnerSkipped(t *testing.T) {
	src, dst := t.TempDir(), t.TempDir()

	write(t, filepath.Join(src, "mine"), "m", 0o644)
	write(t, filepath.Join(src, "root-file"), "r", 0o644)
	write(t, filepath.Join(src, "swapped"), "s", 0o644)
	write(t, filepath.Join(src, "rootdir/x"), "x", 0o644)
	write(t, filepath.Join(dst, "rootdir/keep"), "k", 0o644) // a skipped name keeps the local copy

	r := foreign{
		Remote: remote(t),
		names:  map[string]bool{"root-file": true, "rootdir": true},
		open:   map[string]bool{"swapped": true},
	}

	st := run(t, r, src, dst)
	if st.Files != 1 || st.Skipped != 3 || st.Deleted != 0 {
		t.Fatalf("%+v", st)
	}

	for _, n := range []string{"root-file", "swapped", "rootdir/x"} {
		if _, err := os.Lstat(filepath.Join(dst, n)); !errors.Is(err, fs.ErrNotExist) {
			t.Fatalf("%s copied: %v", n, err)
		}
	}

	if got := read(t, filepath.Join(dst, "rootdir/keep")); got != "k" {
		t.Fatalf("local file of a skipped directory removed: %q", got)
	}
}

// names returns fixed listings, as a hostile server could.
type names struct {
	Remote

	list []string
}

func (n names) ReadDir(p string) ([]fs.FileInfo, error) {
	list, err := n.Remote.ReadDir(p)
	if err != nil || len(list) == 0 {
		return list, err
	}

	out := make([]fs.FileInfo, 0, len(n.list))
	for _, name := range n.list {
		out = append(out, renamed{list[0], name})
	}

	return out, nil
}

type renamed struct {
	fs.FileInfo

	name string
}

func (r renamed) Name() string { return r.name }

func TestHostileNames(t *testing.T) {
	src := t.TempDir()
	write(t, filepath.Join(src, "a"), "a", 0o644)

	for _, n := range []string{"../escape", "a/b", "x\x00y", strings.Repeat("n", 256), ""} {
		_, err := Copy(context.Background(), names{remote(t), []string{n}}, src, openDir(t, t.TempDir()), Limits{})
		if !errors.Is(err, ErrUnsafe) {
			t.Errorf("name %q: %v, want ErrUnsafe", n, err)
		}
	}

	// "." and ".." in a listing are ignored, not followed.
	dst := t.TempDir()

	st, err := Copy(context.Background(), names{remote(t), []string{".", ".."}}, src, openDir(t, dst), Limits{})
	if err != nil || st.Files != 0 || st.Dirs != 0 {
		t.Fatalf("%+v %v", st, err)
	}
}

func TestLimits(t *testing.T) {
	src := t.TempDir()
	write(t, filepath.Join(src, "a/b/c/d"), "d", 0o644)
	write(t, filepath.Join(src, "e"), "e", 0o644)

	r := remote(t)

	if _, err := Copy(context.Background(), r, src, openDir(t, t.TempDir()), Limits{MaxDepth: 2}); !errors.Is(err, ErrUnsafe) {
		t.Fatalf("depth: %v", err)
	}

	if _, err := Copy(context.Background(), r, src, openDir(t, t.TempDir()), Limits{MaxEntries: 3}); !errors.Is(err, ErrUnsafe) {
		t.Fatalf("entries: %v", err)
	}

	for _, bad := range []string{"relative", src + "/../x", src + "/"} {
		if _, err := Copy(context.Background(), r, bad, openDir(t, t.TempDir()), Limits{}); !errors.Is(err, ErrUnsafe) {
			t.Errorf("source %q: %v", bad, err)
		}
	}

	// The source itself must be a directory, not a symlink to one.
	link := filepath.Join(t.TempDir(), "link")
	must(t, os.Symlink(src, link))

	if _, err := Copy(context.Background(), r, link, openDir(t, t.TempDir()), Limits{}); !errors.Is(err, ErrUnsafe) {
		t.Fatalf("symlinked source: %v", err)
	}
}

func TestCancel(t *testing.T) {
	src := t.TempDir()
	write(t, filepath.Join(src, "a"), "a", 0o644)

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	if _, err := Copy(ctx, remote(t), src, openDir(t, t.TempDir()), Limits{}); !errors.Is(err, context.Canceled) {
		t.Fatalf("%v", err)
	}
}

func TestRemoveAllReadOnlyDir(t *testing.T) {
	dst := t.TempDir()
	write(t, filepath.Join(dst, "ro/f"), "f", 0o644)
	must(t, os.Chmod(filepath.Join(dst, "ro"), 0o500))

	must(t, removeAll(openDir(t, dst), "ro", 0))

	if _, err := os.Lstat(filepath.Join(dst, "ro")); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("still there: %v", err)
	}
}
