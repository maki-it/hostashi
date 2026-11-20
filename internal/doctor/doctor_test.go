// SPDX-License-Identifier: AGPL-3.0-or-later

package doctor

import (
	"context"
	"encoding/json"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"golang.org/x/sys/unix"

	"github.com/maki-it/hostashi/internal/agentapi"
)

func write(t *testing.T, content string) string {
	t.Helper()

	p := filepath.Join(t.TempDir(), "f")
	if err := os.WriteFile(p, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}

	return p
}

func TestOS(t *testing.T) {
	for _, tc := range []struct {
		release, status, want string
	}{
		{"PRETTY_NAME=\"Debian GNU/Linux 13 (trixie)\"\nID=debian\nVERSION_ID=\"13\"\n", agentapi.DoctorOK, "Debian GNU/Linux 13 (trixie) is supported"},
		{"ID=ubuntu\nVERSION_ID=\"26.04\"\nPRETTY_NAME=\"Ubuntu 26.04 LTS\"\n", agentapi.DoctorOK, "Ubuntu 26.04 LTS is supported"},
		{"ID=ubuntu\nVERSION_ID=\"24.04\"\n", agentapi.DoctorWarn, "ubuntu 24.04 is not in the support matrix"},
		{"ID=debian\nVERSION_ID=12\n", agentapi.DoctorWarn, "debian 12 is not in the support matrix"},
	} {
		c := System{OSRelease: write(t, tc.release)}.OS()
		if c.Status != tc.status || c.Summary != tc.want {
			t.Errorf("%q: got %s %q", tc.release, c.Status, c.Summary)
		}
	}

	if c := (System{OSRelease: "/nonexistent"}).OS(); c.Status != agentapi.DoctorWarn {
		t.Errorf("missing os-release: %+v", c)
	}
}

func TestDisk(t *testing.T) {
	fsys := map[string]unix.Statfs_t{
		"/":      {Bsize: 4096, Blocks: 10 << 20, Bavail: 5 << 20, Fsid: unix.Fsid{Val: [2]int32{1, 0}}}, // 40 GiB, half free
		"/var":   {Bsize: 4096, Blocks: 10 << 20, Bavail: 5 << 20, Fsid: unix.Fsid{Val: [2]int32{1, 0}}}, // same file system
		"/data":  {Bsize: 4096, Blocks: 10 << 20, Bavail: 1 << 20, Fsid: unix.Fsid{Val: [2]int32{2, 0}}}, // 10 % free
		"/full":  {Bsize: 4096, Blocks: 10 << 20, Bavail: 1 << 18, Fsid: unix.Fsid{Val: [2]int32{3, 0}}}, // 2.5 % free
		"/small": {Bsize: 4096, Blocks: 1 << 18, Bavail: 1 << 17, Fsid: unix.Fsid{Val: [2]int32{4, 0}}},  // 512 MiB free of 1 GiB
		"/proc":  {Bsize: 4096, Fsid: unix.Fsid{Val: [2]int32{5, 0}}},
	}
	s := System{Statfs: func(p string, st *unix.Statfs_t) error {
		v, ok := fsys[p]
		if !ok {
			return unix.ENOENT
		}

		*st = v

		return nil
	}}

	got := s.Disk([]string{"/", "/var", "/data", "/full", "/small", "/proc", "/missing"})

	want := []struct{ status, summary string }{
		{agentapi.DoctorOK, "/: 20.0 GiB free of 40.0 GiB (50 %)"},
		{agentapi.DoctorWarn, "/data: 4.0 GiB free of 40.0 GiB (10 %)"},
		{agentapi.DoctorFail, "/full: 1.0 GiB free of 40.0 GiB (2 %)"},
		{agentapi.DoctorFail, "/small: 512.0 MiB free of 1.0 GiB (50 %)"},
		{agentapi.DoctorFail, "/missing: not found"},
	}

	if len(got) != len(want) {
		t.Fatalf("got %d checks: %+v", len(got), got)
	}

	for i, w := range want {
		if got[i].Status != w.status || got[i].Summary != w.summary || got[i].ID != "system.disk" {
			t.Errorf("check %d = %+v, want %s %q", i, got[i], w.status, w.summary)
		}
	}
}

func TestProc(t *testing.T) {
	const base = "22 1 0:5 / /dev rw,nosuid - devtmpfs udev rw\n"

	for _, tc := range []struct{ line, status string }{
		{"23 1 0:21 / /proc rw,nosuid,nodev,noexec,relatime shared:12 - proc none rw,hidepid=invisible,gid=998\n", agentapi.DoctorOK},
		{"23 1 0:21 / /proc rw,relatime - proc none rw,hidepid=2\n", agentapi.DoctorOK},
		{"23 1 0:21 / /proc rw,nosuid,nodev,noexec,relatime shared:12 - proc none rw\n", agentapi.DoctorWarn},
		{"", agentapi.DoctorWarn},
	} {
		c := System{MountInfo: write(t, base+tc.line)}.Proc()
		if c.Status != tc.status {
			t.Errorf("%q: %+v", tc.line, c)
		}
	}
}

func TestPoolMode(t *testing.T) {
	d := t.TempDir()
	if err := os.Chmod(d, 0o755); err != nil {
		t.Fatal(err)
	}

	c := PoolMode(d)
	if c.Status != agentapi.DoctorWarn || !strings.Contains(c.Summary, "0755") || c.Hint == "" {
		t.Errorf("0755 pool: %+v", c)
	}

	if os.Getuid() == 0 {
		if err := os.Chmod(d, 0o711); err != nil {
			t.Fatal(err)
		}

		if c := PoolMode(d); c.Status != agentapi.DoctorOK {
			t.Errorf("root 0711 pool: %+v", c)
		}
	}

	if c := PoolMode(filepath.Join(d, "missing")); c.Status != agentapi.DoctorFail {
		t.Errorf("missing pool: %+v", c)
	}
}

func TestQuota(t *testing.T) {
	if c := Quota("/home", true, nil); c.Status != agentapi.DoctorOK {
		t.Errorf("available: %+v", c)
	}

	if c := Quota("/home", false, nil); c.Status != agentapi.DoctorWarn || !strings.Contains(c.Hint, "installer") {
		t.Errorf("unavailable: %+v", c)
	}

	if c := Quota("/home", false, os.ErrPermission); c.Status != agentapi.DoctorFail {
		t.Errorf("error: %+v", c)
	}
}

func TestUsage(t *testing.T) {
	for _, tc := range []struct {
		used, limit uint64
		status      string
	}{
		{0, 0, agentapi.DoctorOK},
		{1 << 40, 0, agentapi.DoctorOK},
		{79, 100, agentapi.DoctorOK},
		{80, 100, agentapi.DoctorWarn},
		{94, 100, agentapi.DoctorWarn},
		{95, 100, agentapi.DoctorFail},
		{120, 100, agentapi.DoctorFail},
	} {
		c := Usage("shop", "disk space", tc.used, tc.limit, Bytes)
		if c.Status != tc.status || c.Site != "shop" || c.ID != "site.disk" {
			t.Errorf("%d of %d: %+v", tc.used, tc.limit, c)
		}
	}

	if c := Usage("shop", "disk space", 9<<30, 10<<30, Bytes); c.Summary != "disk space: 9.0 GiB of 10.0 GiB (90 %)" {
		t.Errorf("summary %q", c.Summary)
	}
}

func TestCert(t *testing.T) {
	now := time.Date(2026, 11, 20, 8, 0, 0, 0, time.UTC)
	for _, tc := range []struct {
		days   int
		status string
	}{
		{60, agentapi.DoctorOK}, {21, agentapi.DoctorOK}, {20, agentapi.DoctorWarn}, {7, agentapi.DoctorWarn}, {6, agentapi.DoctorFail}, {-1, agentapi.DoctorFail},
	} {
		c := Cert("shop", now.Add(time.Duration(tc.days)*24*time.Hour+time.Minute), now)
		if c.Status != tc.status || c.ID != "site.tls" {
			t.Errorf("%d days: %+v", tc.days, c)
		}
	}

	if c := Cert("shop", now.Add(-time.Hour), now); !strings.Contains(c.Summary, "expired") {
		t.Errorf("expired: %q", c.Summary)
	}
}

func listen(t *testing.T) (string, net.Listener) {
	t.Helper()

	dir, err := os.MkdirTemp("", "doc") // short: Unix socket paths are limited
	if err != nil {
		t.Fatal(err)
	}

	t.Cleanup(func() { _ = os.RemoveAll(dir) })

	p := filepath.Join(dir, "s")

	l, err := net.Listen("unix", p)
	if err != nil {
		t.Fatal(err)
	}

	t.Cleanup(func() { _ = l.Close() })

	return p, l
}

func TestSocket(t *testing.T) {
	ctx := context.Background()
	p, _ := listen(t)

	if c := Socket(ctx, "service.x", "x", p, "hint"); c.Status != agentapi.DoctorOK || c.Summary != "x is running" {
		t.Errorf("listening: %+v", c)
	}

	c := Socket(ctx, "service.x", "x", p+"-missing", "hint")
	if c.Status != agentapi.DoctorFail || c.Hint != "hint" || !strings.Contains(c.Summary, "not found") {
		t.Errorf("missing: %+v", c)
	}
}

func TestPanel(t *testing.T) {
	ctx := context.Background()
	status := http.StatusOK
	body := map[string]string{"status": "ok", "agent": "ok", "db": "ok", "version": "1.2.3"}
	p, l := listen(t)
	srv := &http.Server{ReadHeaderTimeout: time.Second, Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/health" || r.Header.Get("X-Real-IP") == "" { // like the panel's fromProxy
			http.NotFound(w, r)

			return
		}

		w.WriteHeader(status)
		_ = json.NewEncoder(w).Encode(body)
	})}

	go func() { _ = srv.Serve(l) }()

	t.Cleanup(func() { _ = srv.Close() })

	if c := Panel(ctx, p); c.Status != agentapi.DoctorOK || c.Summary != "the panel is running, version 1.2.3" {
		t.Errorf("healthy: %+v", c)
	}

	status, body = http.StatusServiceUnavailable, map[string]string{"status": "degraded", "agent": "ok", "db": "error"}

	if c := Panel(ctx, p); c.Status != agentapi.DoctorFail || !strings.Contains(c.Summary, `database "error"`) {
		t.Errorf("degraded: %+v", c)
	}

	if c := Panel(ctx, p+"-missing"); c.Status != agentapi.DoctorFail {
		t.Errorf("missing: %+v", c)
	}
}

func TestBytes(t *testing.T) {
	for n, want := range map[uint64]string{0: "0 B", 1023: "1023 B", 1024: "1.0 KiB", 1536: "1.5 KiB", 10 << 30: "10.0 GiB", 3 << 40: "3.0 TiB"} {
		if got := Bytes(n); got != want {
			t.Errorf("Bytes(%d) = %q, want %q", n, got, want)
		}
	}
}

func TestWorst(t *testing.T) {
	r := agentapi.DoctorReport{}
	if r.Worst() != agentapi.DoctorOK {
		t.Error("empty report is not ok")
	}

	r.Checks = []agentapi.DoctorCheck{{Status: agentapi.DoctorOK}, {Status: agentapi.DoctorWarn}}
	if r.Worst() != agentapi.DoctorWarn {
		t.Error("want warn")
	}

	r.Checks = append(r.Checks, agentapi.DoctorCheck{Status: agentapi.DoctorFail}, agentapi.DoctorCheck{Status: agentapi.DoctorWarn})
	if r.Worst() != agentapi.DoctorFail {
		t.Error("want fail")
	}
}
