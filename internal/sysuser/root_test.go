// SPDX-License-Identifier: AGPL-3.0-or-later

package sysuser_test

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"os"
	"os/exec"
	"os/user"
	"path/filepath"
	"strconv"
	"syscall"
	"testing"

	"github.com/maki-it/hostashi/internal/sites"
	"github.com/maki-it/hostashi/internal/sysuser"
)

// rootEnv builds a manager on the real account database. It runs only as root
// (CI: sudo go test -run TestRoot) and cleans every account it created.
func rootEnv(t *testing.T) (m *sites.Manager, pool string, name string) {
	t.Helper()

	if os.Geteuid() != 0 {
		t.Skip("needs root")
	}

	if _, err := os.Stat(sysuser.Useradd); err != nil {
		t.Skip("useradd not installed")
	}

	b := make([]byte, 4)
	if _, err := rand.Read(b); err != nil {
		t.Fatal(err)
	}

	name = "hstest" + hex.EncodeToString(b)

	base, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}

	pool, state := filepath.Join(base, "pool"), filepath.Join(base, "state")
	for _, d := range []string{pool, state} {
		if err := os.Mkdir(d, 0o700); err != nil {
			t.Fatal(err)
		}
	}

	// The registry starts with a pool at /home. The test needs its own, so the
	// state file is written the way the installer will write it.
	conf := `{"version":1,"pools":[{"name":"test","path":"` + pool + `"}],"sites":[]}`
	if err := os.WriteFile(filepath.Join(state, sites.StateFile), []byte(conf), 0o600); err != nil {
		t.Fatal(err)
	}

	reg, err := sites.OpenRegistry(state, 0)
	if err != nil {
		t.Fatal(err)
	}

	acc := sysuser.New(nil)

	t.Cleanup(func() { _ = acc.Delete(context.Background(), name) })

	return sites.NewManager(reg, acc, sites.Options{WebGID: webGID(t)}), pool, name
}

// webGID is the gid of www-data, or 0 (the site's own group) where the test
// machine has no such group.
func webGID(t *testing.T) uint32 {
	t.Helper()

	g, err := user.LookupGroup("www-data")
	if err != nil {
		return 0
	}

	id, err := strconv.ParseUint(g.Gid, 10, 32)
	if err != nil {
		t.Fatal(err)
	}

	return uint32(id)
}

func TestRootSiteLifecycle(t *testing.T) {
	m, pool, name := rootEnv(t)
	ctx := context.Background()

	site, err := m.Create(ctx, name, "test")
	if err != nil {
		t.Fatal(err)
	}

	u, err := user.Lookup(name)
	if err != nil {
		t.Fatal(err)
	}

	if strconv.Itoa(int(site.UID)) != u.Uid || strconv.Itoa(int(site.GID)) != u.Gid || u.Uid == "0" {
		t.Fatalf("site %+v, passwd %+v", site, u)
	}

	if g, err := user.LookupGroup(name); err != nil || g.Gid != u.Gid {
		t.Fatalf("group: %v %v", g, err)
	}

	home := filepath.Join(pool, name)

	var st syscall.Stat_t
	if err := syscall.Stat(home, &st); err != nil {
		t.Fatal(err)
	}

	wantGID := webGID(t)
	if wantGID == 0 {
		wantGID = site.GID
	}

	if st.Uid != site.UID || st.Gid != wantGID || st.Mode&0o7777 != 0o750 {
		t.Fatalf("home owner %d:%d mode %o, want %d:%d 750", st.Uid, st.Gid, st.Mode&0o7777, site.UID, wantGID)
	}

	if err := syscall.Stat(filepath.Join(home, sites.WebRootName), &st); err != nil {
		t.Fatal(err)
	}

	if st.Uid != site.UID || st.Gid != site.GID || st.Mode&0o7777 != 0o755 {
		t.Fatalf("web root owner %d:%d mode %o", st.Uid, st.Gid, st.Mode&0o7777)
	}

	if u.HomeDir != home {
		t.Fatalf("passwd home %q, want %q", u.HomeDir, home)
	}

	// A second site with the same name, and an existing system account, both conflict.
	if _, err := m.Create(ctx, name, "test"); !errors.Is(err, sites.ErrExists) {
		t.Fatalf("duplicate: %v", err)
	}

	// A process of the site user blocks deletion and leaves everything in place.
	sleeper := exec.Command("/usr/bin/sleep", "60")
	sleeper.Dir = "/"
	sleeper.SysProcAttr = &syscall.SysProcAttr{Credential: &syscall.Credential{Uid: site.UID, Gid: site.GID}}

	if err := sleeper.Start(); err != nil {
		t.Fatal(err)
	}

	if err := m.Delete(ctx, name); !errors.Is(err, sites.ErrInUse) {
		t.Fatalf("delete with process: %v", err)
	}

	if _, err := os.Stat(home); err != nil {
		t.Fatal("home removed although the user is busy")
	}

	_ = sleeper.Process.Kill()
	_ = sleeper.Wait()

	if err := m.Delete(ctx, name); err != nil {
		t.Fatal(err)
	}

	if _, err := user.Lookup(name); err == nil {
		t.Fatal("user still in passwd")
	}

	if _, err := user.LookupGroup(name); err == nil {
		t.Fatal("group still there")
	}

	if _, err := os.Lstat(home); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("home still there: %v", err)
	}
}

func TestRootDeleteRefusesForeignHome(t *testing.T) {
	m, pool, name := rootEnv(t)
	ctx := context.Background()

	site, err := m.Create(ctx, name, "test")
	if err != nil {
		t.Fatal(err)
	}

	home := filepath.Join(pool, name)
	if err := os.WriteFile(filepath.Join(home, "data"), []byte("x"), 0o640); err != nil {
		t.Fatal(err)
	}

	var orig syscall.Stat_t
	if err := syscall.Stat(home, &orig); err != nil {
		t.Fatal(err)
	}

	// An admin (or an attacker with root-less access to the pool, which the
	// pool checks prevent) gave the home to another uid.
	if err := os.Chown(home, int(site.UID)+1, int(orig.Gid)); err != nil {
		t.Fatal(err)
	}

	if err := m.Delete(ctx, name); !errors.Is(err, sites.ErrHomeUnsafe) {
		t.Fatalf("delete: %v", err)
	}

	if _, err := os.Stat(filepath.Join(home, "data")); err != nil {
		t.Fatalf("data deleted: %v", err)
	}

	// Put it back: the retry finishes the job (the account is gone already).
	if err := os.Chown(home, int(site.UID), int(orig.Gid)); err != nil {
		t.Fatal(err)
	}

	if err := m.Delete(ctx, name); err != nil {
		t.Fatalf("retry: %v", err)
	}
}

func TestRootDeleteStaysOutOfMounts(t *testing.T) {
	m, pool, name := rootEnv(t)
	ctx := context.Background()

	if _, err := m.Create(ctx, name, "test"); err != nil {
		t.Fatal(err)
	}

	outside := filepath.Join(filepath.Dir(pool), "outside")
	if err := os.Mkdir(outside, 0o700); err != nil {
		t.Fatal(err)
	}

	if err := os.WriteFile(filepath.Join(outside, "keep"), []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}

	target := filepath.Join(pool, name, "mnt")
	if err := os.Mkdir(target, 0o750); err != nil {
		t.Fatal(err)
	}

	if out, err := exec.Command("/usr/bin/mount", "--bind", outside, target).CombinedOutput(); err != nil {
		t.Skipf("cannot create a bind mount here: %v %s", err, out)
	}

	t.Cleanup(func() { _ = syscall.Unmount(target, syscall.MNT_DETACH) })

	// The walk must refuse to enter the mount instead of emptying its source.
	if err := m.Delete(ctx, name); err == nil {
		t.Fatal("delete succeeded across a mount")
	}

	if _, err := os.Stat(filepath.Join(outside, "keep")); err != nil {
		t.Fatalf("data behind the mount was deleted: %v", err)
	}
}
