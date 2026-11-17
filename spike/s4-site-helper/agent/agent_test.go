package agent

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"golang.org/x/sys/unix"

	rpc "github.com/maki-it/hostashi/spike/s1-agent-rpc/agentrpc"
	"github.com/maki-it/hostashi/spike/s4-site-helper/helper"
)

func TestMain(m *testing.M) {
	if len(os.Args) == 2 && os.Args[1] == helper.Arg0 {
		os.Exit(helper.Main()) // the agent binary re-executes itself as the helper
	}
	os.Exit(m.Run())
}

var (
	exeOnce sync.Once
	exePath string
)

func installedExe(t *testing.T) string {
	t.Helper()
	exeOnce.Do(func() {
		dir, _ := os.MkdirTemp("", "s4agent")
		os.Chmod(dir, 0o755)
		self, _ := os.Executable()
		b, err := os.ReadFile(self)
		if err != nil {
			t.Fatal(err)
		}
		exePath = filepath.Join(dir, "agent")
		if err := os.WriteFile(exePath, b, 0o755); err != nil {
			t.Fatal(err)
		}
	})
	return exePath
}

func needRoot(t *testing.T) {
	t.Helper()
	if os.Geteuid() != 0 {
		t.Skip("needs root")
	}
}

func must(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}

const (
	uidA = 20001
	uidB = 20002
)

// world builds a pool directory (root-owned 0755, with marker) holding sites alpha (A) and beta (B).
func world(t *testing.T) (*Registry, string) {
	t.Helper()
	needRoot(t)
	pool, err := os.MkdirTemp("", "s4pool")
	must(t, err)
	t.Cleanup(func() { os.RemoveAll(pool) })
	must(t, os.Chmod(pool, 0o755))
	must(t, os.WriteFile(filepath.Join(pool, MarkerName), []byte("pool-1234\n"), 0o644))
	for name, uid := range map[string]int{"alpha": uidA, "beta": uidB} {
		p := filepath.Join(pool, name)
		must(t, os.Mkdir(p, 0o750))
		must(t, os.Chown(p, uid, uid))
	}
	return &Registry{
		Pools: map[string]Pool{"default": {Name: "default", Path: pool, ID: "pool-1234"}},
		Sites: map[string]Site{
			"alpha": {User: "alpha", UID: uidA, GID: uidA, Pool: "default"},
			"beta":  {User: "beta", UID: uidB, GID: uidB, Pool: "default"},
		},
	}, pool
}

func TestOpenRootChecks(t *testing.T) {
	reg, pool := world(t)
	f, id, err := reg.OpenRoot("alpha")
	must(t, err)
	f.Close()
	if id.UID != uidA || id.GID != uidA {
		t.Fatalf("%+v", id)
	}

	if _, _, err := reg.OpenRoot("ghost"); !errors.Is(err, ErrUnknownSite) {
		t.Errorf("ghost: %v", err)
	}
	for _, bad := range []string{"../beta", "a/b", "", "root", ".", "ALPHA", "alpha\x00"} {
		if _, _, err := reg.OpenRoot(bad); err == nil {
			t.Errorf("%q accepted", bad)
		}
	}

	// Registry says A, directory belongs to B: refuse (and the helper would refuse again).
	reg.Sites["alpha"] = Site{User: "alpha", UID: uidB, GID: uidB, Pool: "default"}
	if _, _, err := reg.OpenRoot("alpha"); !errors.Is(err, ErrHomeUnsafe) {
		t.Errorf("owner mismatch: %v", err)
	}
	reg.Sites["alpha"] = Site{User: "alpha", UID: uidA, GID: uidA, Pool: "default"}

	// Home writable by others.
	must(t, os.Chmod(filepath.Join(pool, "alpha"), 0o777))
	if _, _, err := reg.OpenRoot("alpha"); !errors.Is(err, ErrHomeUnsafe) {
		t.Errorf("0777 home: %v", err)
	}
	must(t, os.Chmod(filepath.Join(pool, "alpha"), 0o750))

	// Home replaced by a symlink to another site's directory.
	must(t, os.Rename(filepath.Join(pool, "alpha"), filepath.Join(pool, "alpha.old")))
	must(t, os.Symlink(filepath.Join(pool, "beta"), filepath.Join(pool, "alpha")))
	if _, _, err := reg.OpenRoot("alpha"); !errors.Is(err, ErrHomeUnsafe) {
		t.Errorf("symlinked home: %v", err)
	}
	must(t, os.Remove(filepath.Join(pool, "alpha")))
	must(t, os.Rename(filepath.Join(pool, "alpha.old"), filepath.Join(pool, "alpha")))

	// Marker problems.
	p := reg.Pools["default"]
	p.ID = "other-pool"
	reg.Pools["default"] = p
	if _, _, err := reg.OpenRoot("alpha"); !errors.Is(err, ErrPoolUnavailable) {
		t.Errorf("wrong marker id: %v", err)
	}
	p.ID = "pool-1234"
	reg.Pools["default"] = p
	must(t, os.Remove(filepath.Join(pool, MarkerName)))
	if _, _, err := reg.OpenRoot("alpha"); !errors.Is(err, ErrPoolUnavailable) {
		t.Errorf("missing marker: %v", err)
	}
	must(t, os.Symlink("/etc/hostname", filepath.Join(pool, MarkerName)))
	if _, _, err := reg.OpenRoot("alpha"); !errors.Is(err, ErrPoolUnavailable) {
		t.Errorf("symlink marker: %v", err)
	}
	must(t, os.Remove(filepath.Join(pool, MarkerName)))
	must(t, os.WriteFile(filepath.Join(pool, MarkerName), []byte("pool-1234"), 0o666))
	must(t, os.Chmod(filepath.Join(pool, MarkerName), 0o666))
	if _, _, err := reg.OpenRoot("alpha"); !errors.Is(err, ErrPoolUnavailable) {
		t.Errorf("writable marker: %v", err)
	}
	must(t, os.Chmod(filepath.Join(pool, MarkerName), 0o644))
	f, _, err = reg.OpenRoot("alpha")
	must(t, err)
	f.Close()

	// Pool directory writable by others, or unknown pool.
	must(t, os.Chmod(pool, 0o777))
	if _, _, err := reg.OpenRoot("alpha"); !errors.Is(err, ErrPoolUnavailable) {
		t.Errorf("0777 pool: %v", err)
	}
	must(t, os.Chmod(pool, 0o755))
	reg.Sites["alpha"] = Site{User: "alpha", UID: uidA, GID: uidA, Pool: "nope"}
	if _, _, err := reg.OpenRoot("alpha"); !errors.Is(err, ErrPoolUnavailable) {
		t.Errorf("unknown pool: %v", err)
	}
}

// The unmounted-volume trap from ADR-0016: with the volume gone, the mount point is a plain
// directory on the root file system. The check must refuse instead of letting a site fill /.
func TestUnmountedPoolIsRefused(t *testing.T) {
	needRoot(t)
	mp, err := os.MkdirTemp("", "s4vol")
	must(t, err)
	t.Cleanup(func() { unix.Unmount(mp, unix.MNT_DETACH); os.RemoveAll(mp) })
	must(t, os.Chmod(mp, 0o755))
	reg := &Registry{
		Pools: map[string]Pool{"vol1": {Name: "vol1", Path: mp, ID: "vol-1", RequireMount: true}},
		Sites: map[string]Site{"alpha": {User: "alpha", UID: uidA, GID: uidA, Pool: "vol1"}},
	}
	// 1. Not mounted: plain directory. Even if someone recreated marker and home there, no.
	if _, _, err := reg.OpenRoot("alpha"); !errors.Is(err, ErrPoolUnavailable) {
		t.Fatalf("plain dir accepted as pool: %v", err)
	}
	// 2. Mounted and prepared: works.
	if err := unix.Mount("tmpfs", mp, "tmpfs", 0, "size=1m,mode=0755"); err != nil {
		t.Skipf("cannot mount: %v", err)
	}
	must(t, os.WriteFile(filepath.Join(mp, MarkerName), []byte("vol-1"), 0o644))
	must(t, os.Mkdir(filepath.Join(mp, "alpha"), 0o750))
	must(t, os.Chown(filepath.Join(mp, "alpha"), uidA, uidA))
	f, _, err := reg.OpenRoot("alpha")
	must(t, err)
	f.Close()
	// 3. Volume detached again: refused.
	must(t, unix.Unmount(mp, unix.MNT_DETACH))
	if _, _, err := reg.OpenRoot("alpha"); !errors.Is(err, ErrPoolUnavailable) {
		t.Fatalf("detached volume accepted: %v", err)
	}
}

// --- through the S1 RPC server ---------------------------------------------------------

func startRPC(t *testing.T, reg *Registry) *rpc.Client {
	t.Helper()
	srv := rpc.NewServer(func(c rpc.Caller) bool { return c.UID == 0 }, nil) // stand-in for "the serve user"
	(&Files{Reg: reg, Run: &helper.Runner{Exe: installedExe(t), MinID: 10000}}).Register(srv)
	sock := filepath.Join(t.TempDir(), "agent.sock")
	must(t, srv.Listen(sock, -1))
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { _ = srv.Serve(ctx); close(done) }()
	t.Cleanup(func() { cancel(); <-done })
	c, err := rpc.Dial(sock)
	must(t, err)
	t.Cleanup(func() { c.Close() })
	return c
}

func code(t *testing.T, err error, want string) {
	t.Helper()
	var e *rpc.Error
	if !errors.As(err, &e) || e.Code != want {
		t.Fatalf("want %s, got %v", want, err)
	}
}

func tgt(site, path string) target { return target{Site: site, Path: path} }

func TestRPCEndToEnd(t *testing.T) {
	reg, pool := world(t)
	c := startRPC(t, reg)

	_, err := rpc.Call[MkdirReq, Empty](c, "SiteFileMkdir", MkdirReq{target: tgt("alpha", "htdocs/example.com"), Mode: 0o750, Parents: true})
	must(t, err)
	_, err = rpc.Call[WriteReq, Empty](c, "SiteFileWrite", WriteReq{target: tgt("alpha", "htdocs/example.com/index.php"), Data: []byte("<?php echo 1;"), Mode: 0o640})
	must(t, err)
	// Same relative path, other site: separate worlds.
	_, err = rpc.Call[ReadReq, ReadResp](c, "SiteFileRead", ReadReq{tgt("beta", "htdocs/example.com/index.php")})
	code(t, err, "not_found")
	_, err = rpc.Call[WriteReq, Empty](c, "SiteFileWrite", WriteReq{target: tgt("beta", "secret.txt"), Data: []byte("B"), Mode: 0o600})
	must(t, err)

	r, err := rpc.Call[ReadReq, ReadResp](c, "SiteFileRead", ReadReq{tgt("alpha", "htdocs/example.com/index.php")})
	must(t, err)
	if string(r.Data) != "<?php echo 1;" {
		t.Fatalf("%q", r.Data)
	}
	l, err := rpc.Call[ListReq, ListResp](c, "SiteFileList", ListReq{target: tgt("alpha", "htdocs/example.com")})
	must(t, err)
	if len(l.Entries) != 1 || l.Entries[0].Name != "index.php" || l.Entries[0].Mode != 0o640 {
		t.Fatalf("%+v", l)
	}
	st, err := rpc.Call[StatReq, StatResp](c, "SiteFileStat", StatReq{tgt("alpha", "")})
	must(t, err)
	if st.Info.Type != "dir" {
		t.Fatalf("%+v", st)
	}

	// Attack attempts from a "compromised web process" or from a site's own content.
	for name, req := range map[string]target{
		"dotdot into other site": tgt("alpha", "../beta/secret.txt"),
		"absolute path":          tgt("alpha", "/etc/passwd"),
		"dotdot only":            tgt("alpha", ".."),
		"nul":                    tgt("alpha", "a\x00b"),
		"newline":                tgt("alpha", "a\nb"),
		"pool marker name tmp":   tgt("alpha", confineTmp()),
		"site is a path":         tgt("../beta", "secret.txt"),
		"site is root":           tgt("root", "x"),
		"site with slash":        tgt("alpha/x", "x"),
		"empty site":             tgt("", "x"),
	} {
		_, err := rpc.Call[ReadReq, ReadResp](c, "SiteFileRead", ReadReq{req})
		if err == nil {
			t.Errorf("%s accepted", name)
			continue
		}
		code(t, err, "invalid_argument")
	}
	_, err = rpc.Call[ReadReq, ReadResp](c, "SiteFileRead", ReadReq{tgt("ghost", "x")})
	code(t, err, "not_found")

	// A symlink planted by site A that points at site B: refused by the helper (and DAC would stop it anyway).
	must(t, os.Symlink(filepath.Join(pool, "beta"), filepath.Join(pool, "alpha", "to-beta")))
	must(t, os.Lchown(filepath.Join(pool, "alpha", "to-beta"), uidA, uidA))
	_, err = rpc.Call[ReadReq, ReadResp](c, "SiteFileRead", ReadReq{tgt("alpha", "to-beta/secret.txt")})
	code(t, err, "path_escape")
	// Rename out through the link, remove through the link: same.
	_, err = rpc.Call[RenameReq, Empty](c, "SiteFileRename", RenameReq{target: tgt("alpha", "htdocs"), To: "to-beta/htdocs"})
	code(t, err, "path_escape")
	_, err = rpc.Call[RemoveReq, Empty](c, "SiteFileRemove", RemoveReq{target: tgt("alpha", "to-beta/secret.txt")})
	code(t, err, "path_escape")
	if b, _ := os.ReadFile(filepath.Join(pool, "beta", "secret.txt")); string(b) != "B" {
		t.Fatalf("beta's file changed: %q", b)
	}
	// Removing the link itself is fine and leaves beta alone.
	_, err = rpc.Call[RemoveReq, Empty](c, "SiteFileRemove", RemoveReq{target: tgt("alpha", "to-beta")})
	must(t, err)

	// Oversized inline data, setuid mode, bad rename target.
	_, err = rpc.Call[WriteReq, Empty](c, "SiteFileWrite", WriteReq{target: tgt("alpha", "big"), Data: make([]byte, InlineMax+1), Mode: 0o600})
	code(t, err, "invalid_argument")
	_, err = rpc.Call[WriteReq, Empty](c, "SiteFileWrite", WriteReq{target: tgt("alpha", "suid"), Data: []byte("x"), Mode: 0o4755})
	code(t, err, "invalid_argument")
	_, err = rpc.Call[RenameReq, Empty](c, "SiteFileRename", RenameReq{target: tgt("alpha", "htdocs"), To: "../x"})
	code(t, err, "invalid_argument")

	_, err = rpc.Call[RemoveReq, Empty](c, "SiteFileRemove", RemoveReq{target: tgt("alpha", "htdocs"), Recursive: true})
	must(t, err)
}

func confineTmp() string { return "dir/.hostashi-tmp-abc" }

func TestRPCUnsafeHomeIsOpaqueInternalError(t *testing.T) {
	reg, pool := world(t)
	must(t, os.Chmod(filepath.Join(pool, "alpha"), 0o777))
	c := startRPC(t, reg)
	_, err := rpc.Call[StatReq, StatResp](c, "SiteFileStat", StatReq{tgt("alpha", "")})
	code(t, err, "internal")
	var e *rpc.Error
	errors.As(err, &e)
	if strings.Contains(e.Message, pool) || strings.Contains(e.Message, "0777") {
		t.Fatalf("detail leaked to the client: %q", e.Message)
	}
}

func TestRPCQuotaStandIn(t *testing.T) {
	needRoot(t)
	mp, err := os.MkdirTemp("", "s4q")
	must(t, err)
	t.Cleanup(func() { unix.Unmount(mp, unix.MNT_DETACH); os.RemoveAll(mp) })
	must(t, os.Chmod(mp, 0o755))
	if err := unix.Mount("tmpfs", mp, "tmpfs", 0, "size=96k,mode=0755"); err != nil {
		t.Skipf("cannot mount: %v", err)
	}
	must(t, os.Mkdir(filepath.Join(mp, "alpha"), 0o750))
	must(t, os.Chown(filepath.Join(mp, "alpha"), uidA, uidA))
	reg := &Registry{
		Pools: map[string]Pool{"small": {Name: "small", Path: mp, RequireMount: true}},
		Sites: map[string]Site{"alpha": {User: "alpha", UID: uidA, GID: uidA, Pool: "small"}},
	}
	c := startRPC(t, reg)
	data := make([]byte, InlineMax)
	var failed bool
	for i := 0; i < 6; i++ {
		_, err := rpc.Call[WriteReq, Empty](c, "SiteFileWrite", WriteReq{target: tgt("alpha", "f"+string(rune('a'+i))), Data: data, Mode: 0o600})
		if err != nil {
			code(t, err, "no_space")
			failed = true
			break
		}
	}
	if !failed {
		t.Fatal("never ran out of space")
	}
	ents, _ := os.ReadDir(filepath.Join(mp, "alpha"))
	for _, e := range ents {
		if strings.HasPrefix(e.Name(), ".hostashi-tmp-") {
			t.Fatalf("temp file left behind: %s", e.Name())
		}
	}
	// Freeing space works again through the same path (delete must succeed at 100 % full).
	_, err = rpc.Call[RemoveReq, Empty](c, "SiteFileRemove", RemoveReq{target: tgt("alpha", "fa")})
	must(t, err)
	_, err = rpc.Call[WriteReq, Empty](c, "SiteFileWrite", WriteReq{target: tgt("alpha", "again"), Data: []byte("ok"), Mode: 0o600})
	must(t, err)
}

func TestRPCUnauthorizedPeer(t *testing.T) {
	reg, _ := world(t)
	srv := rpc.NewServer(func(c rpc.Caller) bool { return false }, nil)
	(&Files{Reg: reg, Run: &helper.Runner{Exe: installedExe(t), MinID: 10000}}).Register(srv)
	sock := filepath.Join(t.TempDir(), "s")
	must(t, srv.Listen(sock, -1))
	ctx, cancel := context.WithCancel(context.Background())
	go srv.Serve(ctx)
	defer cancel()
	c, err := rpc.Dial(sock)
	must(t, err)
	defer c.Close()
	if _, err := rpc.Call[StatReq, StatResp](c, "SiteFileStat", StatReq{tgt("alpha", "")}); err == nil {
		t.Fatal("unauthorized peer got an answer")
	}
}
