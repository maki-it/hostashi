// SPDX-License-Identifier: AGPL-3.0-or-later

package sites

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestCreateAndDelete(t *testing.T) {
	t.Parallel()

	e := newEnv(t)
	ctx := context.Background()

	site, err := e.mgr.Create(ctx, "blog", "")
	must(t, err)

	uid, gid := testIDs()
	if site.User != "blog" || site.UID != uid || site.GID != gid || site.Pool != "default" {
		t.Fatalf("site = %+v", site)
	}

	st, err := os.Stat(filepath.Join(e.pool, "blog"))
	must(t, err)

	if !st.IsDir() || st.Mode().Perm() != HomeMode {
		t.Fatalf("home is %v %v", st.IsDir(), st.Mode())
	}

	root, ok := e.mgr.WebRoot("blog")
	if !ok || root != filepath.Join(e.pool, "blog", WebRootName) {
		t.Fatalf("WebRoot = %q, %v", root, ok)
	}

	st, err = os.Stat(root)
	must(t, err)

	if !st.IsDir() || st.Mode().Perm() != WebRootMode {
		t.Fatalf("web root is %v %v", st.IsDir(), st.Mode())
	}

	// The registry survives a restart.
	e.open()

	if got := e.mgr.List(); len(got) != 1 || got[0] != site {
		t.Fatalf("after reload: %+v", got)
	}

	// A file inside the home goes away with the site.
	must(t, os.MkdirAll(filepath.Join(e.pool, "blog", "public", "a"), 0o750))
	must(t, os.WriteFile(filepath.Join(e.pool, "blog", "public", "a", "index.html"), []byte("x"), 0o640))

	must(t, e.mgr.Delete(ctx, "blog"))

	if _, err := os.Lstat(filepath.Join(e.pool, "blog")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("home still there: %v", err)
	}

	if _, ok := e.acc.users["blog"]; ok {
		t.Fatal("account still there")
	}

	e.open()

	if got := e.mgr.List(); len(got) != 0 {
		t.Fatalf("registry not empty: %+v", got)
	}
}

func TestCreateRejectsBadInputBeforeAnyChange(t *testing.T) {
	t.Parallel()

	e := newEnv(t)

	for _, name := range []string{"", "../x", "Root", "root", "a b", "x/y"} {
		if _, err := e.mgr.Create(context.Background(), name, ""); err == nil {
			t.Errorf("%q: want error", name)
		}
	}

	if _, err := e.mgr.Create(context.Background(), "blog", "../pool"); err == nil {
		t.Error("bad pool name accepted")
	}

	if e.acc.creates != 0 {
		t.Fatal("an account was created for invalid input")
	}
}

func TestCreateConflicts(t *testing.T) {
	t.Parallel()

	e := newEnv(t)
	ctx := context.Background()

	_, err := e.mgr.Create(ctx, "blog", "")
	must(t, err)

	_, err = e.mgr.Create(ctx, "blog", "")
	wantErr(t, err, ErrExists)

	// A system account or group with that name blocks creation.
	e.acc.groups["shop"] = true
	_, err = e.mgr.Create(ctx, "shop", "")
	wantErr(t, err, ErrExists)

	if e.acc.creates != 1 {
		t.Fatalf("creates = %d", e.acc.creates)
	}
}

func TestCreateUnknownPool(t *testing.T) {
	t.Parallel()

	e := newEnv(t)

	_, err := e.mgr.Create(context.Background(), "blog", "fast")
	wantErr(t, err, ErrPoolUnavailable)

	if e.acc.creates != 0 {
		t.Fatal("account created for unknown pool")
	}
}

func TestCreateAccountFailureLeavesNothing(t *testing.T) {
	t.Parallel()

	e := newEnv(t)
	e.acc.createErr = errors.New("useradd failed")

	if _, err := e.mgr.Create(context.Background(), "blog", ""); err == nil {
		t.Fatal("want error")
	}

	if _, err := os.Lstat(filepath.Join(e.pool, "blog")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("home exists: %v", err)
	}

	if len(e.mgr.List()) != 0 {
		t.Fatal("registry not empty")
	}
}

func TestCreateWithExistingHomeRollsBackAndKeepsTheDirectory(t *testing.T) {
	t.Parallel()

	e := newEnv(t)

	home := filepath.Join(e.pool, "blog")
	must(t, os.Mkdir(home, 0o700))
	must(t, os.WriteFile(filepath.Join(home, "precious"), []byte("data"), 0o600))

	_, err := e.mgr.Create(context.Background(), "blog", "")
	wantErr(t, err, ErrHomeExists)

	if _, ok := e.acc.users["blog"]; ok {
		t.Fatal("account was not rolled back")
	}

	if _, err := os.Stat(filepath.Join(home, "precious")); err != nil {
		t.Fatalf("existing data touched: %v", err)
	}

	if len(e.mgr.List()) != 0 {
		t.Fatal("registry not empty")
	}
}

func TestCreateRollsBackWhenRegistryCannotBeWritten(t *testing.T) {
	t.Parallel()

	e := newEnv(t)

	// A non-empty directory in the place of the temp file makes every save fail,
	// even for root.
	blocker := filepath.Join(e.state, "."+StateFile+".tmp")
	must(t, os.MkdirAll(filepath.Join(blocker, "x"), 0o700))

	if _, err := e.mgr.Create(context.Background(), "blog", ""); err == nil {
		t.Fatal("want error")
	}

	if _, ok := e.acc.users["blog"]; ok {
		t.Fatal("account not rolled back")
	}

	if _, err := os.Lstat(filepath.Join(e.pool, "blog")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("home not rolled back: %v", err)
	}

	if len(e.mgr.List()) != 0 {
		t.Fatal("memory and disk disagree")
	}
}

func TestDeleteUnknownSite(t *testing.T) {
	t.Parallel()

	e := newEnv(t)

	wantErr(t, e.mgr.Delete(context.Background(), "blog"), ErrNotFound)
}

func TestDeleteWithRunningProcessesChangesNothing(t *testing.T) {
	t.Parallel()

	e := newEnv(t)
	ctx := context.Background()

	_, err := e.mgr.Create(ctx, "blog", "")
	must(t, err)
	must(t, os.WriteFile(filepath.Join(e.pool, "blog", "f"), []byte("x"), 0o640))

	e.acc.inUse = true

	wantErr(t, e.mgr.Delete(ctx, "blog"), ErrInUse)

	if _, err := os.Stat(filepath.Join(e.pool, "blog", "f")); err != nil {
		t.Fatalf("home touched: %v", err)
	}

	if len(e.mgr.List()) != 1 {
		t.Fatal("registry entry gone")
	}
}

func TestDeleteRefusesChangedAccount(t *testing.T) {
	t.Parallel()

	e := newEnv(t)
	ctx := context.Background()

	_, err := e.mgr.Create(ctx, "blog", "")
	must(t, err)

	// Someone recreated the user with other ids.
	e.acc.users["blog"] = Account{UID: 4242, GID: 4242}

	wantErr(t, e.mgr.Delete(ctx, "blog"), ErrAccountMismatch)

	if e.acc.deletes != 0 {
		t.Fatal("userdel was called")
	}

	if _, err := os.Stat(filepath.Join(e.pool, "blog")); err != nil {
		t.Fatal("home removed")
	}
}

func TestDeleteRefusesHomeOwnedBySomeoneElse(t *testing.T) {
	t.Parallel()

	e := newEnv(t)
	uid, gid := testIDs()

	// The registry claims other ids than the directory has.
	must(t, e.reg.add(Site{User: "blog", UID: uid + 1, GID: gid, Pool: "default"}))

	home := filepath.Join(e.pool, "blog")
	must(t, os.Mkdir(home, 0o750))
	must(t, os.Chown(home, int(uid), int(gid)))
	must(t, os.WriteFile(filepath.Join(home, "f"), []byte("x"), 0o640))

	wantErr(t, e.mgr.Delete(context.Background(), "blog"), ErrHomeUnsafe)

	if _, err := os.Stat(filepath.Join(home, "f")); err != nil {
		t.Fatal("foreign data deleted")
	}
}

func TestDeleteCanBeRetriedAfterPartialFailure(t *testing.T) {
	t.Parallel()

	e := newEnv(t)
	ctx := context.Background()

	_, err := e.mgr.Create(ctx, "blog", "")
	must(t, err)

	// Account and home are already gone, only the registry entry is left.
	delete(e.acc.users, "blog")
	must(t, os.RemoveAll(filepath.Join(e.pool, "blog")))

	must(t, e.mgr.Delete(ctx, "blog"))

	if len(e.mgr.List()) != 0 {
		t.Fatal("entry stays")
	}
}

func TestDeleteNeverFollowsSymlinks(t *testing.T) {
	t.Parallel()

	e := newEnv(t)
	ctx := context.Background()

	outside := filepath.Join(filepath.Dir(e.pool), "outside")
	must(t, os.Mkdir(outside, 0o700))
	must(t, os.WriteFile(filepath.Join(outside, "keep"), []byte("x"), 0o600))

	_, err := e.mgr.Create(ctx, "blog", "")
	must(t, err)

	home := filepath.Join(e.pool, "blog")
	must(t, os.MkdirAll(filepath.Join(home, "d", "e"), 0o750))
	must(t, os.Symlink(outside, filepath.Join(home, "link")))
	must(t, os.Symlink(outside, filepath.Join(home, "d", "e", "link2")))
	must(t, os.Symlink("/etc", filepath.Join(home, "etc")))

	must(t, e.mgr.Delete(ctx, "blog"))

	if _, err := os.Stat(filepath.Join(outside, "keep")); err != nil {
		t.Fatalf("data outside the home was deleted: %v", err)
	}
}

func TestDeleteRefusesDeepTree(t *testing.T) {
	t.Parallel()

	e := newEnv(t)
	ctx := context.Background()

	_, err := e.mgr.Create(ctx, "blog", "")
	must(t, err)

	dir := filepath.Join(e.pool, "blog")
	for range maxRemoveDepth + 5 {
		dir = filepath.Join(dir, "d")
		if err := os.Mkdir(dir, 0o750); err != nil {
			t.Skipf("path too long for this file system: %v", err)
		}
	}

	wantErr(t, e.mgr.Delete(ctx, "blog"), ErrHomeUnsafe)

	if len(e.mgr.List()) != 1 {
		t.Fatal("registry entry gone although the tree is still there")
	}
}
