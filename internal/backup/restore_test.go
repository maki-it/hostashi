// SPDX-License-Identifier: AGPL-3.0-or-later

package backup

import (
	"archive/tar"
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"golang.org/x/sys/unix"

	"github.com/maki-it/hostashi/internal/agentapi"
)

type tarEntry struct {
	name, link, body string
	typ              byte
	mode             int64
}

func tarOf(t *testing.T, entries ...tarEntry) *bytes.Buffer {
	t.Helper()

	var buf bytes.Buffer

	tw := tar.NewWriter(&buf)

	for _, e := range entries {
		h := &tar.Header{Name: e.name, Typeflag: e.typ, Mode: e.mode, Linkname: e.link, Size: int64(len(e.body)), ModTime: time.Unix(1700000000, 0)}
		if e.typ != tar.TypeReg {
			h.Size = 0
		}

		if err := tw.WriteHeader(h); err != nil {
			t.Fatal(err)
		}

		if e.typ == tar.TypeReg {
			_, _ = tw.Write([]byte(e.body))
		}
	}

	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}

	return &buf
}

func extractInto(t *testing.T, prefix string, tr *bytes.Buffer) (string, extracted, error) {
	t.Helper()

	dir := t.TempDir()

	fd, err := unix.Open(dir, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_CLOEXEC, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer unix.Close(fd)

	x := &extractor{root: fd, prefix: prefix, uid: os.Getuid(), gid: os.Getgid()}
	err = x.extract(tr)

	return dir, x.n, err
}

func TestExtractFiles(t *testing.T) {
	dir, n, err := extractInto(t, "home/blog", tarOf(t,
		tarEntry{name: "home", typ: tar.TypeDir, mode: 0o755},
		tarEntry{name: "home/blog", typ: tar.TypeDir, mode: 0o750},
		tarEntry{name: "home/blog/htdocs", typ: tar.TypeDir, mode: 0o755},
		tarEntry{name: "home/blog/htdocs/index.php", typ: tar.TypeReg, mode: 0o4755, body: "<?php"},
		tarEntry{name: "home/blog/htdocs/link", typ: tar.TypeSymlink, link: "/etc/passwd"},
		tarEntry{name: "home/blog/deep/er/file", typ: tar.TypeReg, mode: 0o640, body: "x"},
		tarEntry{name: "home/blog/fifo", typ: tar.TypeFifo, mode: 0o644},
		tarEntry{name: "home/other/secret", typ: tar.TypeReg, mode: 0o600, body: "not this site"},
	))
	if err != nil {
		t.Fatal(err)
	}

	if n.files != 3 || n.bytes != 6 || n.skipped != 1 {
		t.Errorf("counts %+v", n)
	}

	st, err := os.Stat(filepath.Join(dir, "htdocs/index.php"))
	if err != nil || st.Mode() != 0o755 || st.ModTime().Unix() != 1700000000 {
		t.Errorf("index.php: %v %v (setuid must be dropped)", st, err)
	}

	if l, _ := os.Readlink(filepath.Join(dir, "htdocs/link")); l != "/etc/passwd" {
		t.Errorf("symlink %q", l)
	}

	if b, _ := os.ReadFile(filepath.Join(dir, "deep/er/file")); string(b) != "x" {
		t.Errorf("deep file %q", b)
	}

	for _, p := range []string{"other", "home", "fifo"} {
		if _, err := os.Lstat(filepath.Join(dir, p)); !errors.Is(err, os.ErrNotExist) {
			t.Errorf("%s restored: %v", p, err)
		}
	}
}

func TestExtractDump(t *testing.T) {
	dir, n, err := extractInto(t, "blog_wp.sql", tarOf(t, tarEntry{name: "blog_wp.sql", typ: tar.TypeReg, mode: 0o644, body: "CREATE TABLE t;"}))
	if err != nil || n.files != 1 {
		t.Fatal(n, err)
	}

	if b, _ := os.ReadFile(filepath.Join(dir, "blog_wp.sql")); string(b) != "CREATE TABLE t;" {
		t.Errorf("dump %q", b)
	}
}

// A backup is the site user's data: a symlink in it must never lead the
// restore out of the new directory.
func TestExtractRefusesEscapes(t *testing.T) {
	outside := t.TempDir()
	if err := os.Chmod(outside, 0o700); err != nil {
		t.Fatal(err)
	}

	cases := map[string][]tarEntry{
		"file through a symlink": {
			{name: "h/s/esc", typ: tar.TypeSymlink, link: outside},
			{name: "h/s/esc/planted", typ: tar.TypeReg, mode: 0o644, body: "x"},
		},
		"directory through a symlink": {
			{name: "h/s/esc", typ: tar.TypeSymlink, link: outside},
			{name: "h/s/esc/sub", typ: tar.TypeDir, mode: 0o777},
		},
		"chmod through a symlink": {
			{name: "h/s/esc", typ: tar.TypeSymlink, link: outside},
			{name: "h/s/esc", typ: tar.TypeDir, mode: 0o777},
		},
		"file over a symlink": {
			{name: "h/s/esc", typ: tar.TypeSymlink, link: outside + "/planted"},
			{name: "h/s/esc", typ: tar.TypeReg, mode: 0o644, body: "x"},
		},
		"dot-dot":       {{name: "h/s/../../planted", typ: tar.TypeReg, mode: 0o644, body: "x"}},
		"absolute name": {{name: "/h/s/planted", typ: tar.TypeReg, mode: 0o644, body: "x"}},
	}

	for name, entries := range cases {
		t.Run(name, func(t *testing.T) {
			_, _, err := extractInto(t, "h/s", tarOf(t, entries...))
			if err == nil {
				t.Error("accepted")
			}

			if es, _ := os.ReadDir(outside); len(es) != 0 {
				t.Fatalf("wrote outside: %v", es)
			}

			if st, _ := os.Stat(outside); st.Mode().Perm() != 0o700 {
				t.Fatalf("changed outside: %v", st.Mode())
			}
		})
	}
}

func TestCreateRestoreDir(t *testing.T) {
	home := t.TempDir()

	fd, err := createRestoreDir(home, "restore-1", owner())
	if err != nil {
		t.Fatal(err)
	}

	_ = unix.Close(fd)

	if st, _ := os.Stat(filepath.Join(home, "restore-1")); st.Mode().Perm() != 0o700 {
		t.Errorf("mode %v", st.Mode())
	}

	if _, err := createRestoreDir(home, "restore-1", owner()); err == nil {
		t.Error("existing directory reused")
	}

	link := filepath.Join(t.TempDir(), "home")
	if err := os.Symlink(home, link); err != nil {
		t.Fatal(err)
	}

	if _, err := createRestoreDir(link, "restore-2", owner()); err == nil {
		t.Error("home through a symlink accepted")
	}

	if _, err := createRestoreDir(home, "restore-3", owner()+1); err == nil {
		t.Error("home of another user accepted")
	}
}

func readyAgent(t *testing.T) (*Agent, *fakeUnits) {
	t.Helper()

	a, units := newAgent(t)

	a.StateDir = t.TempDir()
	if err := os.Chmod(a.StateDir, 0o700); err != nil {
		t.Fatal(err)
	}

	a.UnitDir = t.TempDir()

	s := DefaultSettings()
	s.Target = storageBox()

	if _, err := a.Set(context.Background(), s); err != nil {
		t.Fatal(err)
	}

	pub, _, _ := ed25519.GenerateKey(rand.Reader)
	if _, err := a.SetHostKey(context.Background(), authorized(t, pub)); err != nil {
		t.Fatal(err)
	}

	writeRegistry(t, a.StateDir, "blog")

	units.calls = nil

	return a, units
}

func TestStartRestore(t *testing.T) {
	ctx := context.Background()
	a, units := readyAgent(t)
	id := strings.Repeat("ab", 32)

	if _, err := a.StartRestore(ctx, "blog", id); !errors.Is(err, ErrSnapshotNotFound) {
		t.Fatalf("unknown snapshot: %v", err)
	}

	if err := WriteSnapshots(a.StatusDir, []SnapshotEntry{
		{ID: id, Kind: KindFiles, Site: "blog"},
		{ID: strings.Repeat("cd", 32), Kind: KindPanel},
	}); err != nil {
		t.Fatal(err)
	}

	if _, err := a.StartRestore(ctx, "shop", id); !errors.Is(err, ErrSnapshotNotFound) {
		t.Errorf("snapshot of another site: %v", err)
	}

	if _, err := a.StartRestore(ctx, "blog", strings.Repeat("cd", 32)); !errors.Is(err, ErrSnapshotNotFound) {
		t.Errorf("panel snapshot into a site: %v", err)
	}

	r, err := a.StartRestore(ctx, "blog", id)
	if err != nil {
		t.Fatal(err)
	}

	if r.State != agentapi.RestoreQueued || r.Kind != KindFiles || !strings.HasPrefix(r.Dir, "restore-") || strings.Join(units.calls, ",") != "reload,restore "+r.ID {
		t.Fatalf("restore %+v, units %v", r, units.calls)
	}

	drop, _ := os.ReadFile(a.restoreDropIn(r.ID))
	if !strings.Contains(string(drop), "\nReadWritePaths="+filepath.Join(a.StateDir, "pool", "blog")+"\n") {
		t.Errorf("drop-in %q", drop)
	}

	if got, err := a.GetRestore(r.ID); err != nil || got != r {
		t.Errorf("get: %+v, %v", got, err)
	}

	if _, err := a.StartRestore(ctx, "blog", id); !errors.Is(err, ErrRestoreBusy) {
		t.Errorf("second restore while the first is queued: %v", err)
	}

	if _, err := a.GetRestore(strings.Repeat("0", 16)); !errors.Is(err, ErrRestoreNotFound) {
		t.Errorf("unknown restore: %v", err)
	}

	if _, err := a.StartRestore(ctx, "blog", "abc"); !errors.Is(err, ErrInvalid) {
		t.Errorf("short id: %v", err)
	}

	// Once it ended, the next one may start and the old drop-in goes.
	r.State = agentapi.RestoreDone
	if err := writeJSON(a.StatusDir, restoreFile(r.ID), r); err != nil {
		t.Fatal(err)
	}

	if _, err := a.StartRestore(ctx, "blog", id); err != nil {
		t.Fatal(err)
	}

	if _, err := os.Stat(filepath.Dir(a.restoreDropIn(r.ID))); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("old drop-in kept: %v", err)
	}
}

func TestRestoreNotReady(t *testing.T) {
	a, _ := newAgent(t)

	if _, err := a.StartRestore(context.Background(), "blog", strings.Repeat("ab", 32)); !errors.Is(err, ErrNotReady) {
		t.Errorf("got %v", err)
	}
}

// writeRegistry writes a site registry with one site per name in pool
// dir/pool, as the agent's sites package does.
func writeRegistry(t *testing.T, dir string, names ...string) {
	t.Helper()

	type site struct {
		User string `json:"user"`
		UID  uint32 `json:"uid"`
		GID  uint32 `json:"gid"`
		Pool string `json:"pool"`
	}

	pool := filepath.Join(dir, "pool")
	if err := os.MkdirAll(pool, 0o755); err != nil {
		t.Fatal(err)
	}

	state := map[string]any{"version": 1, "pools": []map[string]string{{"name": "default", "path": pool}}}

	var ss []site
	for i, n := range names {
		ss = append(ss, site{User: n, UID: uint32(2000 + i), GID: uint32(2000 + i), Pool: "default"})
	}

	state["sites"] = ss

	raw, _ := json.Marshal(state)
	if err := os.WriteFile(filepath.Join(dir, "sites.json"), raw, 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestFits(t *testing.T) {
	limited := Space{Limited: true, BytesLimit: 1000, InodesLimit: 100, FreeBytes: 400, FreeInodes: 10}

	for _, c := range []struct {
		sp           Space
		bytes, files int64
		ok           bool
	}{
		{Space{}, 1 << 40, 1 << 30, true}, // no limits
		{limited, 400, 10, true},          // exactly fits
		{limited, 401, 1, false},          // too many bytes
		{limited, 1, 11, false},           // too many files
		{limited, 0, 0, true},             // size unknown: no check
		{Space{Limited: true, InodesLimit: 100, FreeInodes: 5}, 1 << 40, 5, true}, // bytes unlimited
	} {
		if ok, why := fits(c.sp, c.bytes, c.files); ok != c.ok || (ok == (why != "")) {
			t.Errorf("fits(%+v, %d, %d) = %v %q", c.sp, c.bytes, c.files, ok, why)
		}
	}
}

func TestStartRestoreChecksSpace(t *testing.T) {
	ctx := context.Background()
	a, units := readyAgent(t)
	id := strings.Repeat("ab", 32)

	if err := WriteSnapshots(a.StatusDir, []SnapshotEntry{{ID: id, Kind: KindFiles, Site: "blog", Bytes: 5000, Files: 20}}); err != nil {
		t.Fatal(err)
	}

	var asked string

	a.Space = func(site string) (Space, error) {
		asked = site

		return Space{Limited: true, BytesLimit: 10000, FreeBytes: 4000}, nil
	}

	_, err := a.StartRestore(ctx, "blog", id)
	if !errors.Is(err, ErrNoSpace) || !strings.Contains(err.Error(), "5000 bytes") || asked != "blog" || len(units.calls) != 0 {
		t.Fatalf("restore over the limit: %v, asked %q, units %v", err, asked, units.calls)
	}

	a.Space = func(string) (Space, error) { return Space{}, errors.New("quota read failed") }

	if _, err := a.StartRestore(ctx, "blog", id); err == nil || errors.Is(err, ErrNoSpace) {
		t.Fatalf("failed space check: %v", err)
	}

	a.Space = func(string) (Space, error) { return Space{Limited: true, BytesLimit: 10000, FreeBytes: 6000}, nil }

	if _, err := a.StartRestore(ctx, "blog", id); err != nil {
		t.Fatalf("restore that fits: %v", err)
	}
}
