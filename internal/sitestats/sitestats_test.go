// SPDX-License-Identifier: AGPL-3.0-or-later

package sitestats

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestPHPCgroup(t *testing.T) {
	got := PHPCgroup("/sys/fs/cgroup", "8.4", "shop")
	want := `/sys/fs/cgroup/system.slice/system-hostashi\x2dphp\x2d8.4.slice/hostashi-php-8.4@shop.service`

	if got != want {
		t.Fatalf("%s\nwant %s", got, want)
	}
}

func writeCgroup(t *testing.T, root, version, user, procs, mem string) {
	t.Helper()

	dir := PHPCgroup(root, version, user)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}

	if err := os.WriteFile(filepath.Join(dir, "cgroup.procs"), []byte(procs), 0o644); err != nil {
		t.Fatal(err)
	}

	if mem != "" {
		if err := os.WriteFile(filepath.Join(dir, "memory.current"), []byte(mem), 0o644); err != nil {
			t.Fatal(err)
		}
	}
}

func TestReadPHP(t *testing.T) {
	root := t.TempDir()

	if p, err := ReadPHP(root, "8.4", "gone"); err != nil || p.Up || p.MemoryKnown {
		t.Fatalf("missing: %+v %v", p, err)
	}

	writeCgroup(t, root, "8.4", "shop", "123\n456\n", "52428800\n")

	if p, err := ReadPHP(root, "8.4", "shop"); err != nil || !p.Up || !p.MemoryKnown || p.MemoryBytes != 52428800 {
		t.Fatalf("running: %+v %v", p, err)
	}

	writeCgroup(t, root, "8.3", "idle", "", "0\n")

	if p, err := ReadPHP(root, "8.3", "idle"); err != nil || p.Up || !p.MemoryKnown {
		t.Fatalf("empty: %+v %v", p, err)
	}

	writeCgroup(t, root, "8.5", "nomem", "1\n", "")

	if p, err := ReadPHP(root, "8.5", "nomem"); err != nil || !p.Up || p.MemoryKnown {
		t.Fatalf("no memory file: %+v %v", p, err)
	}

	writeCgroup(t, root, "8.2", "bad", "1\n", "lots\n")

	if _, err := ReadPHP(root, "8.2", "bad"); err == nil {
		t.Fatal("garbage in memory.current accepted")
	}
}

func TestDiskUsage(t *testing.T) {
	home := t.TempDir()

	big := make([]byte, 64<<10)

	if err := os.MkdirAll(filepath.Join(home, "htdocs", "wp"), 0o755); err != nil {
		t.Fatal(err)
	}

	if err := os.WriteFile(filepath.Join(home, "htdocs", "wp", "a.bin"), big, 0o644); err != nil {
		t.Fatal(err)
	}

	base, err := DiskUsage(context.Background(), home)
	if err != nil || base < 64<<10 {
		t.Fatalf("%d %v", base, err)
	}

	// A hard link adds nothing, a symlink to a big outside tree only itself.
	if err := os.Link(filepath.Join(home, "htdocs", "wp", "a.bin"), filepath.Join(home, "copy.bin")); err != nil {
		t.Fatal(err)
	}

	outside := t.TempDir()
	if err := os.WriteFile(filepath.Join(outside, "huge.bin"), make([]byte, 1<<20), 0o644); err != nil {
		t.Fatal(err)
	}

	if err := os.Symlink(outside, filepath.Join(home, "link")); err != nil {
		t.Fatal(err)
	}

	got, err := DiskUsage(context.Background(), home)
	if err != nil || got > base+8<<10 {
		t.Fatalf("%d (base %d) %v", got, base, err)
	}

	if _, err := DiskUsage(context.Background(), filepath.Join(home, "missing")); err == nil {
		t.Fatal("missing root accepted")
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	if _, err := DiskUsage(ctx, home); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled: %v", err)
	}
}

func TestDiskCacheKeepsOldValueOnFailure(t *testing.T) {
	home := t.TempDir()
	now := time.Date(2026, 11, 19, 10, 0, 0, 0, time.UTC)
	clock := func() time.Time { return now }

	var c DiskCache

	if d := c.Get("shop"); !d.MeasuredAt.IsZero() {
		t.Fatal(d)
	}

	if err := c.Refresh(context.Background(), map[string]string{"shop": home}, clock); err != nil {
		t.Fatal(err)
	}

	first := c.Get("shop")
	if !first.MeasuredAt.Equal(now) {
		t.Fatal(first)
	}

	now = now.Add(time.Hour)

	err := c.Refresh(context.Background(), map[string]string{"shop": filepath.Join(home, "gone"), "blog": home}, clock)
	if err == nil {
		t.Fatal("failure not reported")
	}

	if got := c.Get("shop"); got != first {
		t.Fatalf("old value lost: %+v", got)
	}

	if got := c.Get("blog"); !got.MeasuredAt.Equal(now) {
		t.Fatalf("blog: %+v", got)
	}

	// A site that is gone leaves the cache with the next refresh.
	if err := c.Refresh(context.Background(), map[string]string{"blog": home}, clock); err != nil || !c.Get("shop").MeasuredAt.IsZero() {
		t.Fatal(err, c.Get("shop"))
	}
}

func TestReadPHPInSlice(t *testing.T) {
	root := t.TempDir()

	got := PHPSliceCgroup("/sys/fs/cgroup", "8.4", "my-shop")
	want := `/sys/fs/cgroup/hostashi.slice/hostashi-site.slice/hostashi-site-my\x2dshop.slice/hostashi-php-8.4@my-shop.service`

	if got != want {
		t.Fatalf("%s\nwant %s", got, want)
	}

	dir := PHPSliceCgroup(root, "8.4", "shop")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}

	if err := os.WriteFile(filepath.Join(dir, "cgroup.procs"), []byte("7\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	if err := os.WriteFile(filepath.Join(dir, "memory.current"), []byte("1024\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	// A stale cgroup at the old place must not win over the slice.
	writeCgroup(t, root, "8.4", "shop", "", "0\n")

	if p, err := ReadPHP(root, "8.4", "shop"); err != nil || !p.Up || p.MemoryBytes != 1024 {
		t.Fatalf("in slice: %+v %v", p, err)
	}
}

func TestReadCache(t *testing.T) {
	root := t.TempDir()

	got := CacheCgroup("/sys/fs/cgroup", "my-shop")
	want := `/sys/fs/cgroup/hostashi.slice/hostashi-site.slice/hostashi-site-my\x2dshop.slice/hostashi-valkey@my-shop.service`

	if got != want {
		t.Fatalf("%s\nwant %s", got, want)
	}

	if c, err := ReadCache(root, "shop"); err != nil || c.Up {
		t.Fatalf("missing: %+v %v", c, err)
	}

	dir := CacheCgroup(root, "shop")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}

	for name, v := range map[string]string{"cgroup.procs": "42\n", "memory.current": "4194304\n"} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(v), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	if c, err := ReadCache(root, "shop"); err != nil || !c.Up || !c.MemoryKnown || c.MemoryBytes != 4194304 {
		t.Fatalf("running: %+v %v", c, err)
	}
}
