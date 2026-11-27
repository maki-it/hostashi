// SPDX-License-Identifier: AGPL-3.0-or-later

package sitestats

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeFiles(t *testing.T, dir string, files map[string]string) {
	t.Helper()

	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}

	for name, v := range files {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(v), 0o644); err != nil {
			t.Fatal(err)
		}
	}
}

func TestSliceCgroupPaths(t *testing.T) {
	if got := SliceCgroup("/sys/fs/cgroup", "my-shop"); got != `/sys/fs/cgroup/hostashi.slice/hostashi-site.slice/hostashi-site-my\x2dshop.slice` {
		t.Fatal(got)
	}

	if got := UserSliceCgroup("/sys/fs/cgroup", 1001); got != "/sys/fs/cgroup/user.slice/user-1001.slice" {
		t.Fatal(got)
	}
}

func TestReadUsage(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "site.slice")
	writeFiles(t, dir, map[string]string{
		"cpu.stat":       "usage_usec 123456\nuser_usec 100000\nsystem_usec 23456\nnr_periods 0\n",
		"memory.current": "10485760\n",
		"memory.stat":    "anon 4194304\nfile 6291456\nactive_file 2097152\ninactive_file 4194304\nbad line\n",
		"pids.current":   "7\n",
		"io.stat":        "8:0 rbytes=1000 wbytes=2000 rios=1 wios=2 dbytes=0 dios=0\n259:0 rbytes=10 wbytes=20 rios=1 wios=1 dbytes=0 dios=0\n",
	})

	u, err := ReadUsage(dir)
	if err != nil {
		t.Fatal(err)
	}

	want := Usage{CPUUsec: 123456, MemoryBytes: 6291456, CacheBytes: 6291456, MemoryKnown: true, Tasks: 7, IOReadBytes: 1010, IOWriteBytes: 2020, IOKnown: true}
	if u != want {
		t.Fatalf("got %+v, want %+v", u, want)
	}
}

func TestReadUsageMissing(t *testing.T) {
	u, err := ReadUsage(filepath.Join(t.TempDir(), "nope"))
	if err != nil || u != (Usage{}) {
		t.Fatalf("%+v %v", u, err)
	}
}

func TestReadUsageAccountingOff(t *testing.T) {
	// Only cpu.stat: memory, pids and io controllers are off.
	dir := filepath.Join(t.TempDir(), "s")
	writeFiles(t, dir, map[string]string{"cpu.stat": "usage_usec 5\n", "io.stat": ""})

	u, err := ReadUsage(dir)
	if err != nil {
		t.Fatal(err)
	}

	if u.CPUUsec != 5 || u.MemoryKnown || u.Tasks != 0 || !u.IOKnown || u.IOReadBytes != 0 {
		t.Fatalf("%+v", u)
	}
}

func TestReadUsageInactiveAboveCurrent(t *testing.T) {
	// The files are not read atomically; the working set never wraps.
	dir := filepath.Join(t.TempDir(), "s")
	writeFiles(t, dir, map[string]string{"memory.current": "100\n", "memory.stat": "inactive_file 200\n"})

	u, err := ReadUsage(dir)
	if err != nil || u.MemoryBytes != 0 || !u.MemoryKnown {
		t.Fatalf("%+v %v", u, err)
	}
}

func TestReadUsageBadValues(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "s")
	writeFiles(t, dir, map[string]string{"memory.current": "lots\n"})

	if _, err := ReadUsage(dir); err == nil {
		t.Fatal("want error for a value that is no number")
	}

	dir = filepath.Join(t.TempDir(), "big")
	writeFiles(t, dir, map[string]string{"cpu.stat": strings.Repeat("x", maxStatFile+1)})

	if _, err := ReadUsage(dir); err == nil {
		t.Fatal("want error for an oversized file")
	}
}

func TestUsageAdd(t *testing.T) {
	a := Usage{CPUUsec: 1, MemoryBytes: 2, MemoryKnown: true, Tasks: 3}
	b := Usage{CPUUsec: 10, IOReadBytes: 5, IOKnown: true}

	if got := a.Add(b); got != (Usage{CPUUsec: 11, MemoryBytes: 2, MemoryKnown: true, Tasks: 3, IOReadBytes: 5, IOKnown: true}) {
		t.Fatalf("%+v", got)
	}
}
