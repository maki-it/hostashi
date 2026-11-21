// SPDX-License-Identifier: AGPL-3.0-or-later

package quota

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"syscall"
	"testing"

	"golang.org/x/sys/unix"
)

func TestQcmd(t *testing.T) {
	// Values from linux/quota.h: QCMD(Q_GETQUOTA, PRJQUOTA) and so on.
	cases := map[int]int{
		qQuotaOn: 0x80000202, qQuotaOff: 0x80000302, qGetInfo: 0x80000502, qGetQuota: 0x80000702,
		qSetQuota: 0x80000802, qXGetQStatV: 0x580802,
	}
	for cmd, want := range cases {
		if got := qcmd(cmd, prjQuota); got != want {
			t.Errorf("qcmd(%#x) = %#x, want %#x", cmd, got, want)
		}
	}
}

func TestRefusals(t *testing.T) {
	dir := t.TempDir()

	if err := Assign(dir, 0, false); err == nil {
		t.Error("Assign accepted project id 0")
	}

	if err := SetLimits(dir, 0, 1, 1); err == nil {
		t.Error("SetLimits accepted project id 0")
	}

	for _, p := range []string{"", "relative/dir"} {
		if err := Assign(p, 1000, false); err == nil {
			t.Errorf("Assign accepted path %q", p)
		}

		if _, err := Get(p, 1000); err == nil {
			t.Errorf("Get accepted path %q", p)
		}
	}

	link := filepath.Join(t.TempDir(), "link")
	if err := os.Symlink(dir, link); err != nil {
		t.Fatal(err)
	}

	if err := Assign(link, 1000, false); err == nil {
		t.Error("Assign followed a symlink as the root")
	}
}

// TestNoQuota runs on any file system: without project quota the functions
// report ErrUnavailable (or Available false) instead of failing obscurely.
func TestNoQuota(t *testing.T) {
	if os.Getenv("HOSTASHI_QUOTA_TEST_DIR") != "" {
		t.Skip("running on a file system with project quota")
	}

	dir := t.TempDir()

	ok, err := Available(dir)
	if err != nil {
		t.Logf("Available: %v (not root or no quotactl_fd)", err)
	} else if ok {
		t.Skip("the temp dir has project quota on")
	}

	if _, err := Get(dir, 1000); err == nil {
		t.Error("Get succeeded without project quota")
	}

	if st, err := Status(dir); err == nil && st != Off {
		t.Errorf("Status = %v without project quota, want off", st)
	}

	if err := Enforce(dir); err == nil {
		t.Error("Enforce succeeded without project quota")
	}
}

func TestStateString(t *testing.T) {
	for st, want := range map[State]string{Off: "off", Counting: "counting", Enforced: "enforced", State(9): "off"} {
		if got := st.String(); got != want {
			t.Errorf("%d.String() = %q, want %q", st, got, want)
		}
	}
}

// TestEnforce switches enforcement off (accounting stays, as after a mount
// that only counts) and on again through Enforce, without quotaon.
func TestEnforce(t *testing.T) {
	base := os.Getenv("HOSTASHI_QUOTA_TEST_DIR")
	if base == "" {
		t.Skip("HOSTASHI_QUOTA_TEST_DIR not set")
	}

	fd, err := openRoot(base)
	if err != nil {
		t.Fatal(err)
	}
	defer unix.Close(fd)

	// Q_QUOTAOFF on a file system with quota as a feature drops only the
	// enforcement.
	if err := quotactlFD(fd, qQuotaOff, 0, nil); err != nil && !errors.Is(err, unix.EINVAL) {
		t.Fatalf("quota off: %v", err)
	}

	if st, err := Status(base); err != nil || st != Counting {
		t.Fatalf("Status after quota off = %v, %v; want counting", st, err)
	}

	if ok, err := Available(base); err != nil || !ok {
		t.Errorf("Available while counting = %v, %v; want true", ok, err)
	}

	if err := Enforce(base); err != nil {
		t.Fatalf("Enforce: %v", err)
	}

	if st, err := Status(base); err != nil || st != Enforced {
		t.Fatalf("Status after Enforce = %v, %v; want enforced", st, err)
	}

	// Again: nothing to do.
	if err := Enforce(base); err != nil {
		t.Errorf("second Enforce: %v", err)
	}
}

// TestProjectQuota needs root and a directory on an ext4 (or XFS) file system
// mounted with prjquota, named by HOSTASHI_QUOTA_TEST_DIR.
// CI creates one on a loop device (go.yml, job quota).
func TestProjectQuota(t *testing.T) {
	base := os.Getenv("HOSTASHI_QUOTA_TEST_DIR")
	if base == "" {
		t.Skip("HOSTASHI_QUOTA_TEST_DIR not set")
	}

	ok, err := Available(base)
	if err != nil || !ok {
		t.Fatalf("Available(%s) = %v, %v; want true", base, ok, err)
	}

	if err := Enforce(base); err != nil {
		t.Fatalf("Enforce: %v", err)
	}

	id := uint32(40000 + os.Getpid()%10000)
	home := filepath.Join(base, "site"+strconv.Itoa(int(id)))
	sub := filepath.Join(home, "htdocs", "wp-content")

	if err := os.MkdirAll(sub, 0o755); err != nil {
		t.Fatal(err)
	}

	t.Cleanup(func() { _ = os.RemoveAll(home) })

	// Existing content before the assignment, plus things the walk must skip.
	if err := os.WriteFile(filepath.Join(sub, "old.txt"), make([]byte, 64<<10), 0o644); err != nil {
		t.Fatal(err)
	}

	if err := os.Symlink("/etc/passwd", filepath.Join(sub, "link")); err != nil {
		t.Fatal(err)
	}

	if err := Assign(home, id, false); err != nil {
		t.Fatalf("Assign: %v", err)
	}

	if err := SetLimits(home, id, 1<<20, 100); err != nil {
		t.Fatalf("SetLimits: %v", err)
	}

	u, err := Get(home, id)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}

	if u.BytesLimit != 1<<20 || u.InodesLimit != 100 {
		t.Errorf("limits = %d bytes, %d inodes; want %d, 100", u.BytesLimit, u.InodesLimit, 1<<20)
	}

	if u.Bytes < 64<<10 || u.Inodes < 4 {
		t.Errorf("usage %d bytes, %d inodes does not include the existing files", u.Bytes, u.Inodes)
	}

	// A new file inherits the project and the limit stops it. Root bypasses
	// quota (CAP_SYS_RESOURCE), so an unprivileged child writes.
	if err := os.Chmod(sub, 0o777); err != nil {
		t.Fatal(err)
	}

	switch code := writeAsNobody(t, filepath.Join(sub, "big.bin"), 2<<20); code {
	case 0:
		t.Fatal("writing 2 MiB into a 1 MiB project succeeded")
	case exitEDQUOT:
	default:
		t.Errorf("unprivileged write exited %d, want %d (EDQUOT)", code, exitEDQUOT)
	}

	// The symlink's target was not touched.
	if p, err := projectOf("/etc/passwd"); err == nil && p == id {
		t.Errorf("the walk followed a symlink to /etc/passwd")
	}

	// Unlimited again, and a second Assign is a no-op.
	if err := SetLimits(home, id, 0, 0); err != nil {
		t.Fatalf("SetLimits(0, 0): %v", err)
	}

	if u, err := Get(home, id); err != nil || u.BytesLimit != 0 || u.InodesLimit != 0 {
		t.Errorf("after unlimit: %+v, %v", u, err)
	}

	if err := Assign(home, id, false); err != nil {
		t.Errorf("second Assign: %v", err)
	}
}

func projectOf(path string) (uint32, error) {
	fd, err := unix.Open(path, unix.O_RDONLY|unix.O_CLOEXEC|unix.O_NOFOLLOW, 0)
	if err != nil {
		return 0, err
	}
	defer unix.Close(fd) // read-only fd, nothing to flush

	var x fsxattr
	if err := ioctlFsxattr(fd, fsIOCFSGetXattr, &x); err != nil {
		return 0, err
	}

	return x.Projid, nil
}

const (
	writeEnv   = "HOSTASHI_QUOTA_TEST_WRITE"
	exitEDQUOT = 3
	nobodyID   = 65534
)

// TestMain doubles as the unprivileged writer of TestProjectQuota.
func TestMain(m *testing.M) {
	if path := os.Getenv(writeEnv); path != "" {
		size, _ := strconv.Atoi(os.Getenv(writeEnv + "_SIZE"))

		err := os.WriteFile(path, make([]byte, size), 0o644)
		switch {
		case err == nil:
			os.Exit(0)
		case errors.Is(err, unix.EDQUOT):
			os.Exit(exitEDQUOT)
		default:
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
	}

	os.Exit(m.Run())
}

// writeAsNobody runs this test binary as uid/gid 65534 to write size bytes
// to path and returns its exit code.
func writeAsNobody(t *testing.T, path string, size int) int {
	t.Helper()

	cmd := exec.CommandContext(t.Context(), os.Args[0], "-test.run=^$")
	cmd.Env = []string{writeEnv + "=" + path, writeEnv + "_SIZE=" + strconv.Itoa(size)}
	cmd.SysProcAttr = &syscall.SysProcAttr{Credential: &syscall.Credential{Uid: nobodyID, Gid: nobodyID}}
	cmd.Stderr = os.Stderr

	err := cmd.Run()
	if err == nil {
		return 0
	}

	var ee *exec.ExitError
	if !errors.As(err, &ee) {
		t.Fatalf("run writer: %v", err)
	}

	return ee.ExitCode()
}
