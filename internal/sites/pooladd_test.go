// SPDX-License-Identifier: AGPL-3.0-or-later

package sites

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// poolEnv is newEnv plus a would-be pool, an fstab that names it, and no
// mount check (the tests run without mounts).
func poolEnv(t *testing.T) (*env, string) {
	t.Helper()

	e := newEnv(t)
	dir := filepath.Join(filepath.Dir(e.state), "vol1")
	must(t, os.Mkdir(dir, 0o755))
	must(t, os.Mkdir(filepath.Join(dir, "lost+found"), 0o700))

	fstab := filepath.Join(filepath.Dir(e.state), "fstab")
	must(t, os.WriteFile(fstab, []byte("# comment "+dir+"\nUUID=1 / ext4 defaults 0 1\nUUID=2 "+dir+" ext4 nosuid,nodev,nofail 0 2\n"), 0o644))

	e.mgr.opt.Fstab = fstab
	e.mgr.fsCheck = func(int, string) error { return nil }

	for _, tree := range systemTrees {
		if !strings.HasPrefix(dir, tree+"/") {
			e.mgr.trees = append(e.mgr.trees, tree)
		}
	}

	return e, dir
}

func TestAddPool(t *testing.T) {
	e, dir := poolEnv(t)

	p, err := e.mgr.AddPool("vol1", dir)
	must(t, err)

	if p.Name != "vol1" || p.Path != dir || !p.RequireMount || len(p.ID) != 32 {
		t.Fatalf("pool %+v", p)
	}

	marker, err := os.ReadFile(filepath.Join(dir, MarkerName))
	must(t, err)

	if string(marker) != p.ID+"\n" {
		t.Errorf("marker %q, id %q", marker, p.ID)
	}

	if st, _ := os.Stat(dir); st.Mode().Perm() != 0o711 {
		t.Errorf("pool mode %v", st.Mode().Perm())
	}

	if st, _ := os.Stat(filepath.Join(dir, MarkerName)); st.Mode().Perm()&0o022 != 0 {
		t.Errorf("marker mode %v", st.Mode().Perm())
	}

	// Persisted: an agent restart sees it.
	reg, err := OpenRegistry(e.state, uint32(os.Getuid()))
	must(t, err)

	got, ok := reg.Pool("vol1")
	if !ok || got != p {
		t.Fatalf("after reload: %+v %v", got, ok)
	}

	if h := hiddenPaths(e.reg.Pools()); len(h) != 2 {
		t.Errorf("hidden paths %v", h) // the test's default pool is not /home
	}
}

func TestAddPoolRefuses(t *testing.T) {
	e, dir := poolEnv(t)
	base := filepath.Dir(dir)

	must(t, os.Symlink(dir, filepath.Join(base, "link")))
	must(t, os.Mkdir(filepath.Join(base, "full"), 0o755))
	must(t, os.WriteFile(filepath.Join(base, "full", "data"), nil, 0o644))
	must(t, os.Mkdir(filepath.Join(base, "nofstab"), 0o755))

	for name, c := range map[string]struct{ name, dir string }{
		"bad name":        {"Vol1", dir},
		"relative":        {"vol1", "vol1"},
		"unclean":         {"vol1", dir + "/../vol1"},
		"blank":           {"vol1", "/srv/vol 1"},
		"root":            {"vol1", "/"},
		"etc":             {"vol1", "/etc/pool"},
		"proc":            {"vol1", "/proc/1"},
		"usr":             {"vol1", "/usr"},
		"inside default":  {"vol1", e.pool + "/sub"},
		"around default":  {"vol1", filepath.Dir(e.pool)},
		"same as default": {"vol1", e.pool},
		"symlink":         {"vol1", filepath.Join(base, "link")},
		"missing":         {"vol1", filepath.Join(base, "nope")},
		"not empty":       {"vol1", filepath.Join(base, "full")},
		"no fstab entry":  {"vol1", filepath.Join(base, "nofstab")},
	} {
		if _, err := e.mgr.AddPool(c.name, c.dir); !errors.Is(err, ErrPoolRefused) {
			t.Errorf("%s: %v, want ErrPoolRefused", name, err)
		}
	}

	if _, err := e.mgr.AddPool("default", dir); !errors.Is(err, ErrPoolExists) {
		t.Errorf("existing name: %v", err)
	}

	e.mgr.fsCheck = func(int, string) error { return errors.Join(ErrPoolRefused, errors.New("not a mount point")) }
	if _, err := e.mgr.AddPool("vol1", dir); !errors.Is(err, ErrPoolRefused) {
		t.Errorf("mount check ignored: %v", err)
	}

	// Nothing changed on a refusal.
	if _, err := os.Lstat(filepath.Join(dir, MarkerName)); !os.IsNotExist(err) {
		t.Errorf("marker written on a refusal: %v", err)
	}

	if len(e.reg.Pools()) != 1 {
		t.Errorf("pools %v", e.reg.Pools())
	}

	// A second add of the same volume finds the marker of the first.
	e.mgr.fsCheck = func(int, string) error { return nil }
	_, err := e.mgr.AddPool("vol1", dir)
	must(t, err)

	if _, err := e.mgr.AddPool("vol2", dir); !errors.Is(err, ErrPoolRefused) {
		t.Errorf("same volume twice: %v", err)
	}
}

func TestRemovePool(t *testing.T) {
	e, dir := poolEnv(t)

	_, err := e.mgr.AddPool("vol1", dir)
	must(t, err)

	wantErr(t, e.mgr.RemovePool("nope"), ErrNotFound)

	if err := e.mgr.RemovePool("default"); !errors.Is(err, ErrPoolRefused) {
		t.Errorf("default removed: %v", err)
	}

	// A site on it keeps it.
	e.reg.sites["blog"] = Site{User: "blog", UID: 5000, GID: 5000, Pool: "vol1"}
	wantErr(t, e.mgr.RemovePool("vol1"), ErrPoolInUse)
	delete(e.reg.sites, "blog")

	must(t, e.mgr.RemovePool("vol1"))

	if _, ok := e.reg.Pool("vol1"); ok {
		t.Fatal("pool still registered")
	}

	if _, err := os.Lstat(filepath.Join(dir, MarkerName)); !os.IsNotExist(err) {
		t.Errorf("marker left: %v", err)
	}

	reg, err := OpenRegistry(e.state, uint32(os.Getuid()))
	must(t, err)

	if _, ok := reg.Pool("vol1"); ok {
		t.Fatal("pool back after reload")
	}

	// The volume can be added again.
	_, err = e.mgr.AddPool("vol1", dir)
	must(t, err)
}

func TestCreateSiteOnAddedPool(t *testing.T) {
	e, dir := poolEnv(t)

	p, err := e.mgr.AddPool("vol1", dir)
	must(t, err)

	// Create demands the mount the tests cannot have; the registered pool
	// without it shows the rest of the path works.
	p.RequireMount = false
	e.reg.pools["vol1"] = p

	_, err = e.mgr.Create(context.Background(), "blog", "vol1")
	must(t, err)

	if home, _ := e.mgr.Home("blog"); home != dir+"/blog" {
		t.Errorf("home %q", home)
	}
}

func TestFstabHas(t *testing.T) {
	tab := "# /srv/commented ext4\n\nUUID=a /srv/vol1 ext4 nosuid,nodev 0 2\n/dev/sdb /srv/vol2/ xfs defaults 0 2\nbroken\n"

	for dir, want := range map[string]bool{"/srv/vol1": true, "/srv/vol2": true, "/srv/commented": false, "/srv": false} {
		if got := fstabHas(strings.NewReader(tab), dir); got != want {
			t.Errorf("%s: %v, want %v", dir, got, want)
		}
	}
}
