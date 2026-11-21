// SPDX-License-Identifier: AGPL-3.0-or-later

package hoststats

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"golang.org/x/sys/unix"

	"github.com/maki-it/hostashi/internal/agentapi"
)

// Fixtures follow the format of Linux 6.x as on Debian 13 and Ubuntu 26.04.
const (
	statFixture = `cpu  10132153 290696 3084719 46828483 16683 0 25195 0 175628 0
cpu0 1393280 32966 572056 13343292 6130 0 17875 0 23933 0
cpu1 1335420 40432 513220 13451834 3536 0 2357 0 41427 0
intr 1462898 0 0 0
ctxt 115315
btime 1763712000
processes 86031
procs_running 1
`
	meminfoFixture = `MemTotal:        4002524 kB
MemFree:          189176 kB
MemAvailable:    2453616 kB
Buffers:          102340 kB
SwapTotal:       1048572 kB
SwapFree:         921340 kB
HugePages_Total:       0
`
	netFixture = `Inter-|   Receive                                                |  Transmit
 face |bytes    packets errs drop fifo frame compressed multicast|bytes    packets errs drop fifo colls carrier compressed
    lo: 2776770   11307    0    0    0     0          0         0  2776770   11307    0    0    0     0       0          0
  eth0: 1215645    2751    0    0    0     0          0         0  1782404    4324    0    0    0   427       0          0
enp7s0: 100 2 0 0 0 0 0 0 200 3 0 0 0 0 0 0
`
)

func TestParseStat(t *testing.T) {
	c, n, err := ParseStat([]byte(statFixture))
	if err != nil {
		t.Fatal(err)
	}

	if n != 2 || c.User != 10132153 || c.Idle != 46828483 || c.IOWait != 16683 || c.SoftIRQ != 25195 || c.Steal != 0 {
		t.Fatalf("%d %+v", n, c)
	}

	if c.Total() != 10132153+290696+3084719+46828483+16683+25195 {
		t.Fatalf("total %d", c.Total())
	}

	for _, bad := range []string{"", "intr 1\n", "cpu 1 2 3\n", "cpu 1 2 x 4 5\n", "cpu -1 2 3 4 5\n"} {
		if _, _, err := ParseStat([]byte(bad)); err == nil {
			t.Errorf("%q accepted", bad)
		}
	}

	// A kernel without steal: missing fields stay 0, one CPU at least.
	c, n, err = ParseStat([]byte("cpu 1 2 3 4 5\n"))
	if err != nil || n != 1 || c.Idle != 4 || c.Steal != 0 {
		t.Fatalf("%d %+v %v", n, c, err)
	}
}

func TestParseMeminfo(t *testing.T) {
	m, err := ParseMeminfo([]byte(meminfoFixture))
	if err != nil {
		t.Fatal(err)
	}

	if m.TotalBytes != 4002524*1024 || m.AvailableBytes != 2453616*1024 || m.SwapTotalBytes != 1048572*1024 || m.SwapFreeBytes != 921340*1024 {
		t.Fatalf("%+v", m)
	}

	for _, bad := range []string{"", "MemFree: 1 kB\n", "MemTotal: x kB\nMemAvailable: 1 kB\n", "MemTotal: 1 MB\nMemAvailable: 1 kB\n", "MemTotal: 1 kB extra\nMemAvailable: 1 kB\n"} {
		if _, err := ParseMeminfo([]byte(bad)); err == nil {
			t.Errorf("%q accepted", bad)
		}
	}
}

func TestParseLoadUptime(t *testing.T) {
	l1, l5, l15, err := ParseLoadavg([]byte("0.52 0.58 0.59 1/389 12345\n"))
	if err != nil || l1 != 0.52 || l5 != 0.58 || l15 != 0.59 {
		t.Fatal(l1, l5, l15, err)
	}

	for _, bad := range []string{"", "1 2", "x 1 2", "-1 2 3", "NaN 1 2", "1e13 1 2"} {
		if _, _, _, err := ParseLoadavg([]byte(bad)); err == nil {
			t.Errorf("%q accepted", bad)
		}
	}

	u, err := ParseUptime([]byte("350735.47 234388.90\n"))
	if err != nil || u != 350735.47 {
		t.Fatal(u, err)
	}

	if _, err := ParseUptime([]byte("")); err == nil {
		t.Error("empty uptime accepted")
	}
}

func TestParseNetDev(t *testing.T) {
	n, err := ParseNetDev([]byte(netFixture))
	if err != nil {
		t.Fatal(err)
	}

	want := []agentapi.HostNet{
		{Name: "eth0", RxBytes: 1215645, TxBytes: 1782404, RxPackets: 2751, TxPackets: 4324},
		{Name: "enp7s0", RxBytes: 100, TxBytes: 200, RxPackets: 2, TxPackets: 3},
	}

	if len(n) != len(want) || n[0] != want[0] || n[1] != want[1] {
		t.Fatalf("%+v", n)
	}

	// Odd names are left out, short lines are an error.
	n, err = ParseNetDev([]byte("we ird: 1 2 3 4 5 6 7 8 9 10 11 12 13 14 15 16\nx<y: 1 2 3 4 5 6 7 8 9 10 11 12 13 14 15 16\n"))
	if err != nil || len(n) != 0 {
		t.Fatal(n, err)
	}

	if _, err := ParseNetDev([]byte("eth0: 1 2 3\n")); err == nil {
		t.Error("short line accepted")
	}

	// At most MaxHostNets interfaces.
	var many []byte
	for i := range agentapi.MaxHostNets + 5 {
		many = append(many, []byte("veth"+string(rune('a'+i))+": 1 2 3 4 5 6 7 8 9 10 11 12 13 14 15 16\n")...)
	}

	if n, _ := ParseNetDev(many); len(n) != agentapi.MaxHostNets {
		t.Fatalf("got %d interfaces", len(n))
	}
}

func writeProc(t *testing.T, root string) {
	t.Helper()

	for p, c := range map[string]string{
		"proc/stat": statFixture, "proc/meminfo": meminfoFixture, "proc/loadavg": "1.00 0.50 0.25 1/1 1\n",
		"proc/uptime": "100.5 50\n", "proc/net/dev": netFixture,
	} {
		f := filepath.Join(root, p)
		if err := os.MkdirAll(filepath.Dir(f), 0o755); err != nil {
			t.Fatal(err)
		}

		if err := os.WriteFile(f, []byte(c), 0o644); err != nil {
			t.Fatal(err)
		}
	}
}

func TestRead(t *testing.T) {
	root := t.TempDir()
	writeProc(t, root)

	r := Reader{Root: root, Statfs: func(p string, st *unix.Statfs_t) error {
		switch p {
		case "/":
			st.Bsize, st.Blocks, st.Bavail, st.Files, st.Ffree = 4096, 1000, 400, 50, 20
			st.Fsid.Val = [2]int32{1, 1}
		case "/home":
			st.Bsize, st.Blocks = 4096, 1000
			st.Fsid.Val = [2]int32{1, 1} // same file system as /: left out
		case "/srv/data":
			st.Bsize, st.Blocks, st.Bavail = 1024, 10, 5
			st.Fsid.Val = [2]int32{2, 0}
		default:
			return errors.New("no such mount")
		}

		return nil
	}}

	s, err := r.Read([]Mount{{"root", "/"}, {"default", "/home"}, {"data", "/srv/data"}, {"gone", "/mnt/gone"}})
	if err != nil {
		t.Fatal(err)
	}

	if s.CPUs != 2 || s.Load1 != 1 || s.UptimeSeconds != 100.5 || len(s.Net) != 2 || s.Memory.TotalBytes == 0 {
		t.Fatalf("%+v", s)
	}

	if len(s.Disks) != 2 || s.Disks[0] != (agentapi.HostDisk{Name: "root", TotalBytes: 4096000, FreeBytes: 1638400, TotalInodes: 50, FreeInodes: 20}) || s.Disks[1].Name != "data" || s.Disks[1].FreeBytes != 5120 {
		t.Fatalf("%+v", s.Disks)
	}

	// A missing /proc file is an error, not an empty sample.
	if err := os.Remove(filepath.Join(root, "proc/meminfo")); err != nil {
		t.Fatal(err)
	}

	if _, err := r.Read(nil); err == nil {
		t.Fatal("read without meminfo")
	}
}

func TestReadRefusesHugeFile(t *testing.T) {
	root := t.TempDir()
	writeProc(t, root)

	if err := os.WriteFile(filepath.Join(root, "proc/stat"), make([]byte, maxFile+1), 0o644); err != nil {
		t.Fatal(err)
	}

	if _, err := (Reader{Root: root}).Read(nil); err == nil {
		t.Fatal("huge /proc/stat accepted")
	}
}

func TestReadRealProc(t *testing.T) {
	if _, err := os.Stat("/proc/stat"); err != nil {
		t.Skip("no /proc")
	}

	s, err := Reader{}.Read([]Mount{{"root", "/"}})
	if err != nil {
		t.Fatal(err)
	}

	if s.CPUs < 1 || s.Memory.TotalBytes == 0 || len(s.Disks) != 1 {
		t.Fatalf("%+v", s)
	}
}
