// SPDX-License-Identifier: AGPL-3.0-or-later

package sites

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"testing"

	"golang.org/x/sys/unix"
)

func newSite(t *testing.T) (*env, string) {
	t.Helper()

	e := newEnv(t)

	_, err := e.mgr.Create(context.Background(), "blog", "")
	must(t, err)

	return e, filepath.Join(e.pool, "blog")
}

func wantMode(t *testing.T, path string, want os.FileMode) {
	t.Helper()

	st, err := os.Lstat(path)
	must(t, err)

	if got := st.Mode() & (os.ModePerm | os.ModeSetuid | os.ModeSetgid | os.ModeSticky); got != want {
		t.Errorf("%s: mode %v, want %v", path, got, want)
	}
}

func wantOwner(t *testing.T, path string, uid, gid uint32) {
	t.Helper()

	var st unix.Stat_t
	must(t, unix.Lstat(path, &st))

	if st.Uid != uid || st.Gid != gid {
		t.Errorf("%s: owner %d:%d, want %d:%d", path, st.Uid, st.Gid, uid, gid)
	}
}

func TestResetPermissionsModes(t *testing.T) {
	t.Parallel()

	e, home := newSite(t)
	j := func(p string) string { return filepath.Join(home, p) }

	must(t, os.MkdirAll(j("htdocs/wp-content/uploads"), 0o777))
	must(t, os.WriteFile(j("htdocs/index.php"), []byte("x"), 0o600))
	must(t, os.WriteFile(j("htdocs/open.txt"), []byte("x"), 0o600))
	must(t, os.Chmod(j("htdocs/open.txt"), 0o666))
	must(t, os.WriteFile(j("htdocs/run.sh"), []byte("x"), 0o700))
	must(t, os.WriteFile(j("htdocs/odd-exec"), []byte("x"), 0o610))
	must(t, os.Chmod(j("htdocs/wp-content/uploads"), 0o777))
	must(t, os.Chmod(j("htdocs"), 0o700))
	must(t, os.Chmod(home, 0o777))
	must(t, os.MkdirAll(j(".ssh"), 0o755))
	must(t, os.WriteFile(j(".ssh/id_ed25519"), []byte("k"), 0o644))
	must(t, os.MkdirAll(j(".venv/bin"), 0o755))
	must(t, os.WriteFile(j(".venv/bin/python"), []byte("x"), 0o755))
	mkfifo(t, j("pipe"))
	giveToSite(t, home)

	res, err := e.mgr.ResetPermissions(context.Background(), "blog")
	must(t, err)

	wantMode(t, home, HomeMode)
	wantMode(t, j("htdocs"), 0o755)
	wantMode(t, j("htdocs/wp-content/uploads"), 0o755)
	wantMode(t, j("htdocs/index.php"), 0o644)
	wantMode(t, j("htdocs/open.txt"), 0o644)
	wantMode(t, j("htdocs/run.sh"), 0o755)
	wantMode(t, j("htdocs/odd-exec"), 0o755)
	wantMode(t, j(".ssh"), 0o700)
	wantMode(t, j(".ssh/id_ed25519"), 0o600)
	wantMode(t, j(".venv/bin/python"), 0o755)
	wantMode(t, j("pipe"), 0o600) // a FIFO keeps its mode

	// home, htdocs, wp-content, uploads, 4 files, .ssh, key, .venv, bin, python, pipe
	if res.Checked != 14 || res.Skipped != 0 {
		t.Fatalf("result %+v", res)
	}

	// home, htdocs, uploads, index.php, open.txt, run.sh, odd-exec, .ssh, key
	if res.Changed != 9 {
		t.Fatalf("changed %d, want 9 (%+v)", res.Changed, res)
	}

	// A second run finds nothing to do.
	res, err = e.mgr.ResetPermissions(context.Background(), "blog")
	must(t, err)

	if res.Changed != 0 || res.Checked != 14 {
		t.Fatalf("second run %+v", res)
	}
}

// giveToSite hands a tree to the test site's ids, so that a run as root
// counts only mode changes like an unprivileged run.
func giveToSite(t *testing.T, dir string) {
	t.Helper()

	uid, gid := testIDs()
	must(t, filepath.WalkDir(dir, func(p string, _ os.DirEntry, err error) error {
		if err != nil {
			return err
		}

		return os.Lchown(p, int(uid), int(gid))
	}))
}

func TestResetPermissionsDoesNotFollowLinks(t *testing.T) {
	t.Parallel()

	e, home := newSite(t)

	outside := filepath.Join(filepath.Dir(e.pool), "outside")
	must(t, os.MkdirAll(filepath.Join(outside, "dir"), 0o700))
	must(t, os.WriteFile(filepath.Join(outside, "secret"), []byte("s"), 0o600))

	must(t, os.Symlink(filepath.Join(outside, "secret"), filepath.Join(home, "htdocs/secret")))
	must(t, os.Symlink(outside, filepath.Join(home, "out")))
	must(t, os.Symlink("../../..", filepath.Join(home, "htdocs/up")))

	// The Git deploy layout (ADR-0062): htdocs is a link to a release.
	must(t, os.RemoveAll(filepath.Join(home, "htdocs")))
	must(t, os.MkdirAll(filepath.Join(home, ".deploy/releases/3/public"), 0o700))
	must(t, os.WriteFile(filepath.Join(home, ".deploy/releases/3/public/index.html"), []byte("x"), 0o600))
	must(t, os.Symlink(".deploy/releases/3", filepath.Join(home, "htdocs")))

	_, err := e.mgr.ResetPermissions(context.Background(), "blog")
	must(t, err)

	wantMode(t, outside, 0o700)
	wantMode(t, filepath.Join(outside, "dir"), 0o700)
	wantMode(t, filepath.Join(outside, "secret"), 0o600)

	if st, err := os.Lstat(filepath.Join(home, "htdocs")); err != nil || st.Mode()&os.ModeSymlink == 0 {
		t.Fatalf("htdocs link replaced: %v %v", st, err)
	}

	// The release is reached through its real path.
	wantMode(t, filepath.Join(home, ".deploy/releases/3/public"), 0o755)
	wantMode(t, filepath.Join(home, ".deploy/releases/3/public/index.html"), 0o644)
}

func TestResetPermissionsErrors(t *testing.T) {
	t.Parallel()

	t.Run("unknown site", func(t *testing.T) {
		t.Parallel()

		e := newEnv(t)
		_, err := e.mgr.ResetPermissions(context.Background(), "nosuch")
		wantErr(t, err, ErrNotFound)
	})

	t.Run("bad name", func(t *testing.T) {
		t.Parallel()

		e := newEnv(t)
		if _, err := e.mgr.ResetPermissions(context.Background(), "../etc"); err == nil {
			t.Fatal("bad name accepted")
		}
	})

	t.Run("account changed", func(t *testing.T) {
		t.Parallel()

		e, home := newSite(t)
		must(t, os.Chmod(home, 0o777))

		a := e.acc.users["blog"]
		a.UID++
		e.acc.users["blog"] = a

		_, err := e.mgr.ResetPermissions(context.Background(), "blog")
		wantErr(t, err, ErrAccountMismatch)
		wantMode(t, home, 0o777)
	})

	t.Run("account gone", func(t *testing.T) {
		t.Parallel()

		e, _ := newSite(t)
		delete(e.acc.users, "blog")

		_, err := e.mgr.ResetPermissions(context.Background(), "blog")
		wantErr(t, err, ErrAccountMismatch)
	})

	t.Run("home swapped for a link", func(t *testing.T) {
		t.Parallel()

		e, home := newSite(t)
		other := home + "-real"
		must(t, os.Rename(home, other))
		must(t, os.Chmod(other, 0o777))
		must(t, os.Symlink(other, home))

		_, err := e.mgr.ResetPermissions(context.Background(), "blog")
		wantErr(t, err, ErrHomeUnsafe)
		wantMode(t, other, 0o777)
	})

	t.Run("pool gone", func(t *testing.T) {
		t.Parallel()

		e, home := newSite(t)
		must(t, os.RemoveAll(home))
		must(t, os.Remove(e.pool))

		_, err := e.mgr.ResetPermissions(context.Background(), "blog")
		wantErr(t, err, ErrPoolUnavailable)
	})

	t.Run("canceled", func(t *testing.T) {
		t.Parallel()

		e, home := newSite(t)
		for i := range resetCheckRate + 10 {
			must(t, os.WriteFile(filepath.Join(home, "htdocs", "f"+strconv.Itoa(i)), nil, 0o600))
		}

		ctx, cancel := context.WithCancel(context.Background())
		cancel()

		_, err := e.mgr.ResetPermissions(ctx, "blog")
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("got %v", err)
		}
	})
}

func TestFileMode(t *testing.T) {
	t.Parallel()

	for _, c := range []struct {
		in      uint32
		private bool
		want    uint32
	}{
		{0o600, false, 0o644},
		{0o4755, false, 0o755},
		{0o2644, false, 0o644},
		{0o001, false, 0o755},
		{0o777, true, 0o700},
		{0o644, true, 0o600},
	} {
		if got := fileMode(c.in, c.private); got != c.want {
			t.Errorf("fileMode(%o, %v) = %o, want %o", c.in, c.private, got, c.want)
		}
	}
}

// TestRootResetPermissions needs root: it hands files of other owners back
// to the site and checks hard links and mounts. CI runs it with sudo.
func TestRootResetPermissions(t *testing.T) {
	if os.Getuid() != 0 {
		t.Skip("needs root")
	}

	e := newEnv(t)
	e.mgr.opt.WebGID = 33
	must(t, os.Chown(e.pool, 0, 0))
	must(t, os.Chmod(e.pool, 0o711))

	_, err := e.mgr.Create(context.Background(), "blog", "")
	must(t, err)

	home := filepath.Join(e.pool, "blog")
	j := func(p string) string { return filepath.Join(home, p) }
	uid, gid := testIDs()

	must(t, os.MkdirAll(j("htdocs/up"), 0o755))
	must(t, os.Chown(j("htdocs/up"), 0, 0))
	must(t, os.WriteFile(j("htdocs/root.php"), []byte("x"), 0o644))
	must(t, os.Chown(j("htdocs/root.php"), 0, 0))
	must(t, os.WriteFile(j("htdocs/suid"), []byte("x"), 0o755))
	must(t, os.Chmod(j("htdocs/suid"), 0o4755))
	must(t, os.Chown(home, 0, 0))
	must(t, os.Symlink("/etc/passwd", j("htdocs/passwd")))
	must(t, os.Lchown(j("htdocs/passwd"), 0, 0))

	// A foreign file hard-linked into the home stays untouched.
	foreign := filepath.Join(filepath.Dir(e.pool), "foreign")
	must(t, os.WriteFile(foreign, []byte("f"), 0o600))
	must(t, os.Link(foreign, j("htdocs/foreign")))

	// A device node is skipped, not handed over.
	must(t, unix.Mknod(j("htdocs/null"), unix.S_IFCHR|0o666, int(unix.Mkdev(1, 3))))

	// Another file system inside the home (when mounting is allowed here).
	mounted := false
	must(t, os.Mkdir(j("mnt"), 0o755))

	if err := unix.Mount("tmpfs", j("mnt"), "tmpfs", 0, "mode=0777"); err == nil {
		mounted = true

		t.Cleanup(func() { _ = unix.Unmount(j("mnt"), unix.MNT_DETACH) })
	}

	t.Logf("tmpfs mounted in the home: %v", mounted)

	var before unix.Stat_t
	must(t, unix.Stat("/etc/passwd", &before))

	res, err := e.mgr.ResetPermissions(context.Background(), "blog")
	must(t, err)

	wantOwner(t, home, uid, 33)
	wantMode(t, home, HomeMode)
	wantOwner(t, j("htdocs/up"), uid, gid)
	wantOwner(t, j("htdocs/root.php"), uid, gid)
	wantMode(t, j("htdocs/suid"), 0o755)
	wantOwner(t, j("htdocs/passwd"), uid, gid)
	wantOwner(t, foreign, 0, 0)
	wantMode(t, foreign, 0o600)
	wantOwner(t, j("htdocs/null"), 0, 0)

	var after unix.Stat_t
	must(t, unix.Stat("/etc/passwd", &after))

	if after.Uid != before.Uid || after.Mode != before.Mode {
		t.Fatal("/etc/passwd changed through the link")
	}

	wantSkips := 2
	if mounted {
		wantSkips = 3

		wantOwner(t, j("mnt"), 0, 0)
		wantMode(t, j("mnt"), 0o777)
	}

	if res.Skipped != int64(wantSkips) || len(res.SkippedPaths) != wantSkips {
		t.Fatalf("skipped %+v", res)
	}
}
