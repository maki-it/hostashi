package agent

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"golang.org/x/sys/unix"

	rpc "github.com/maki-it/hostashi/spike/s1-agent-rpc/agentrpc"
)

func sh(t *testing.T, name string, args ...string) string {
	t.Helper()
	out, err := exec.Command(name, args...).CombinedOutput()
	if err != nil {
		t.Fatalf("%s %v: %v\n%s", name, args, err, out)
	}
	return strings.TrimSpace(string(out))
}

// Real ext4 project quota (ADR-0015). Needs a kernel with quota_v2 and the quota tools, which the
// sandbox kernel lacks (S8): it runs inside the S8-style Debian 13 VM, see harness/. Question: does a
// file created by the helper (site uid, no chattr by the helper) count against the site's project
// through directory inheritance, and does the limit turn into no_space?
func TestRPCRealProjectQuotaExt4(t *testing.T) {
	needRoot(t)
	for _, tool := range []string{"mkfs.ext4", "setquota", "quotaon", "chattr", "lsattr", "repquota", "losetup"} {
		if _, err := exec.LookPath(tool); err != nil {
			t.Skipf("%s not installed", tool)
		}
	}
	// S4_QUOTA_DEV: a scratch block device (the VM's second disk). Otherwise a loop device on an image file.
	loop := os.Getenv("S4_QUOTA_DEV")
	if loop == "" {
		img := filepath.Join(t.TempDir(), "ext4.img")
		f, err := os.Create(img)
		must(t, err)
		must(t, f.Truncate(300<<20))
		f.Close()
		loop = sh(t, "losetup", "-f", "--show", img)
		t.Cleanup(func() { exec.Command("losetup", "-d", loop).Run() })
	}
	sh(t, "mkfs.ext4", "-q", "-O", "quota,project", "-E", "quotatype=prjquota", loop)
	mp, err := os.MkdirTemp("", "s4ext4")
	must(t, err)
	t.Cleanup(func() { unix.Unmount(mp, unix.MNT_DETACH); os.RemoveAll(mp) })
	must(t, os.Chmod(mp, 0o755))
	if err := unix.Mount(loop, mp, "ext4", 0, "prjquota"); err != nil {
		t.Skipf("cannot mount ext4 with prjquota (kernel without quota_v2?): %v", err)
	}
	// S8 finding 3: mounting with prjquota only counts; enforcement must be switched on.
	exec.Command("quotaon", "-P", mp).Run()
	must(t, os.Chmod(mp, 0o755))
	home := filepath.Join(mp, "alpha")
	must(t, os.Mkdir(home, 0o750))
	must(t, os.Chown(home, uidA, uidA))
	// What the first-run setup / SiteCreate would do as root: project id plus inherit flag on the home, then a limit.
	sh(t, "chattr", "-p", "42", "+P", home)
	sh(t, "setquota", "-P", "42", "0", "256", "0", "0", mp)

	reg := &Registry{
		Pools: map[string]Pool{"vol": {Name: "vol", Path: mp, RequireMount: true}},
		Sites: map[string]Site{"alpha": {User: "alpha", UID: uidA, GID: uidA, Pool: "vol"}},
	}
	c := startRPC(t, reg)
	data := make([]byte, InlineMax)
	_, err = rpc.Call[MkdirReq, Empty](c, "SiteFileMkdir", MkdirReq{target: tgt("alpha", "sub/dir"), Mode: 0o750, Parents: true})
	must(t, err)
	var okWrites int
	var failed bool
	for i := 0; i < 20; i++ {
		_, err := rpc.Call[WriteReq, Empty](c, "SiteFileWrite", WriteReq{target: tgt("alpha", "sub/dir/f"+string(rune('a'+i))), Data: data, Mode: 0o600})
		if err != nil {
			code(t, err, "no_space")
			failed = true
			break
		}
		okWrites++
	}
	if !failed {
		t.Fatal("quota never hit")
	}
	// Inheritance: files and directories the helper created carry project 42 without anyone running chattr on them.
	for _, p := range []string{"sub", "sub/dir", "sub/dir/fa"} {
		out := sh(t, "lsattr", "-p", "-d", filepath.Join(home, p))
		if !strings.HasPrefix(strings.TrimSpace(out), "42") {
			t.Errorf("%s: project id not inherited: %q", p, out)
		}
	}
	rep := sh(t, "repquota", "-P", "-n", mp)
	t.Logf("%d writes of %d KiB succeeded before no_space; quota report:\n%s", okWrites, InlineMax>>10, rep)
	if !strings.Contains(rep, "42") {
		t.Errorf("project 42 missing from report")
	}
	if ents, _ := os.ReadDir(filepath.Join(home, "sub/dir")); func() bool {
		for _, e := range ents {
			if strings.HasPrefix(e.Name(), ".hostashi-tmp-") {
				return true
			}
		}
		return false
	}() {
		t.Error("temp file left behind")
	}
	// Deleting works at the limit and frees the budget again.
	_, err = rpc.Call[RemoveReq, Empty](c, "SiteFileRemove", RemoveReq{target: tgt("alpha", "sub/dir/fa")})
	must(t, err)
	_, err = rpc.Call[WriteReq, Empty](c, "SiteFileWrite", WriteReq{target: tgt("alpha", "sub/dir/again"), Data: []byte("ok"), Mode: 0o600})
	must(t, err)
	// A neighbour without a limit on the same file system is unaffected.
	must(t, os.Mkdir(filepath.Join(mp, "beta"), 0o750))
	must(t, os.Chown(filepath.Join(mp, "beta"), uidB, uidB))
	reg.Sites["beta"] = Site{User: "beta", UID: uidB, GID: uidB, Pool: "vol"}
	for i := 0; i < 8; i++ {
		_, err := rpc.Call[WriteReq, Empty](c, "SiteFileWrite", WriteReq{target: tgt("beta", "f"+string(rune('a'+i))), Data: data, Mode: 0o600})
		must(t, err)
	}
}
