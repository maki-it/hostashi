// SPDX-License-Identifier: AGPL-3.0-or-later

package cpimport

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"golang.org/x/crypto/ssh"

	"github.com/maki-it/hostashi/internal/agentapi"
	"github.com/maki-it/hostashi/internal/cpsource"
	"github.com/maki-it/hostashi/internal/sites"
)

type fakeUnits struct{ calls []string }

func (f *fakeUnits) Reload(context.Context) error { f.calls = append(f.calls, "reload"); return nil }

func (f *fakeUnits) StartImport(_ context.Context, id string) error {
	f.calls = append(f.calls, "import "+id)

	return nil
}

func must(t *testing.T, err error) {
	t.Helper()

	if err != nil {
		t.Fatal(err)
	}
}

// writeRegistry writes a site registry with one site per name in pool
// dir/pool, as the agent's sites package does.
func writeRegistry(t *testing.T, dir string, names ...string) {
	t.Helper()

	type site struct {
		User string `json:"user"`
		UID  uint32 `json:"uid"`
		GID  uint32 `json:"gid"`
		Pool string `json:"pool"`
	}

	pool := filepath.Join(dir, "pool")
	must(t, os.MkdirAll(pool, 0o755))

	ss := []site{}
	for i, n := range names {
		ss = append(ss, site{User: n, UID: uint32(2000 + i), GID: uint32(2000 + i), Pool: "default"})
	}

	raw, err := json.Marshal(map[string]any{"version": 1, "pools": []map[string]string{{"name": "default", "path": pool}}, "sites": ss})
	must(t, err)
	must(t, os.WriteFile(filepath.Join(dir, "sites.json"), raw, 0o600))
}

func newAgent(t *testing.T, ready bool) (*Agent, *fakeUnits) {
	t.Helper()

	root := t.TempDir()
	state := filepath.Join(root, "state")
	must(t, os.Mkdir(state, 0o700))
	writeRegistry(t, state, "blog")

	srcDir := filepath.Join(state, "cloudpanel")
	must(t, os.Mkdir(srcDir, 0o700))

	src := &cpsource.Source{Dir: srcDir}

	_, err := src.Set(agentapi.CloudPanelSource{Host: "192.0.2.10", Port: 22})
	must(t, err)

	if ready {
		pub, _, err := ed25519.GenerateKey(rand.Reader)
		must(t, err)

		k, err := ssh.NewPublicKey(pub)
		must(t, err)

		_, err = src.SetHostKey(strings.TrimSpace(string(ssh.MarshalAuthorizedKey(k))))
		must(t, err)
	}

	u := &fakeUnits{}

	return &Agent{
		Dir: filepath.Join(srcDir, "imports"), StateDir: state, UnitDir: filepath.Join(root, "units"),
		Source: src, Units: u,
	}, u
}

var req = agentapi.CloudPanelImportRequest{Site: "blog", RemoteUser: "oldblog", DocRoot: "/home/oldblog/htdocs/blog.example.com"}

func TestStart(t *testing.T) {
	a, u := newAgent(t, true)

	r, err := a.Start(context.Background(), req)
	must(t, err)

	if r.State != agentapi.ImportQueued || r.Site != "blog" || r.DocRoot != req.DocRoot || r.Dir != agentapi.CloudPanelImportDir || !agentapi.ValidImportID(r.ID) {
		t.Fatalf("%+v", r)
	}

	if strings.Join(u.calls, ",") != "reload,import "+r.ID {
		t.Fatalf("units: %v", u.calls)
	}

	b, err := os.ReadFile(a.dropIn(r.ID))
	must(t, err)

	if !strings.Contains(string(b), "\nReadWritePaths="+filepath.Join(a.StateDir, "pool", "blog")+"\n") {
		t.Fatalf("drop-in: %s", b)
	}

	fi, err := os.Stat(a.Dir)
	must(t, err)

	if fi.Mode().Perm() != 0o700 {
		t.Fatalf("records directory %v", fi.Mode())
	}

	got, err := a.Get(r.ID)
	must(t, err)

	if got.ID != r.ID || got.State != agentapi.ImportQueued {
		t.Fatalf("get %+v", got)
	}

	// A fresh queued import blocks the next one.
	if _, err := a.Start(context.Background(), req); !errors.Is(err, ErrBusy) {
		t.Fatalf("second start: %v", err)
	}

	// After a minute without the job it is stale; the next start goes on
	// and drops the old drop-in.
	got.Created = time.Now().Add(-2 * time.Minute)
	must(t, writeRecord(a.Dir, got))

	r2, err := a.Start(context.Background(), req)
	must(t, err)

	if _, err := os.Stat(filepath.Dir(a.dropIn(r.ID))); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("old drop-in kept: %v", err)
	}

	// A running import blocks too, however old.
	r2.State, r2.Created = agentapi.ImportRunning, time.Now().Add(-time.Hour)
	must(t, writeRecord(a.Dir, r2))

	if _, err := a.Start(context.Background(), req); !errors.Is(err, ErrBusy) {
		t.Fatalf("start while running: %v", err)
	}
}

func TestStartRefused(t *testing.T) {
	a, _ := newAgent(t, false)

	if _, err := a.Start(context.Background(), req); !errors.Is(err, ErrNotReady) {
		t.Fatalf("without host key: %v", err)
	}

	a, u := newAgent(t, true)

	other := req
	other.Site = "shop"

	if _, err := a.Start(context.Background(), other); !errors.Is(err, ErrNoSite) {
		t.Fatalf("unknown site: %v", err)
	}

	for _, bad := range []agentapi.CloudPanelImportRequest{
		{Site: "blog", RemoteUser: "oldblog", DocRoot: "/home/other/htdocs"},
		{Site: "blog", RemoteUser: "oldblog", DocRoot: "/home/oldblog/../other"},
		{Site: "blog", RemoteUser: "oldblog", DocRoot: "/home/oldblog/"},
		{Site: "blog", RemoteUser: "oldblog", DocRoot: "/etc"},
		{Site: "blog", RemoteUser: "../x", DocRoot: "/home/../x/htdocs"},
		{Site: "Blog", RemoteUser: "oldblog", DocRoot: req.DocRoot},
	} {
		if _, err := a.Start(context.Background(), bad); !errors.Is(err, ErrInvalid) {
			t.Errorf("%+v: %v", bad, err)
		}
	}

	if len(u.calls) != 0 {
		t.Fatalf("units called: %v", u.calls)
	}

	if _, err := a.Get("nope"); !errors.Is(err, ErrInvalid) {
		t.Fatalf("bad id: %v", err)
	}

	must(t, ensureDir(a.Dir))

	if _, err := a.Get("0123456789abcdef"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("unknown id: %v", err)
	}
}

// The job writes its outcome through an fd it opened as root, in place.
func TestRewrite(t *testing.T) {
	dir := t.TempDir()
	must(t, os.Chmod(dir, 0o700))

	r := Import{ID: "0123456789abcdef", Site: "blog", State: agentapi.ImportRunning, SkippedPaths: []string{strings.Repeat("x", 500)}}
	must(t, writeRecord(dir, r))

	f, err := openRecord(dir, r.ID)
	must(t, err)

	defer f.Close()

	r.State, r.SkippedPaths, r.Files = agentapi.ImportDone, nil, 7
	must(t, rewrite(f, r))

	got, err := readRecord(dir, r.ID, owner())
	must(t, err)

	if got.State != agentapi.ImportDone || got.Files != 7 || len(got.SkippedPaths) != 0 {
		t.Fatalf("%+v", got)
	}
}

// TestDrop runs Drop in a child process as root: it must end with no way
// back and no supplementary groups.
func TestDrop(t *testing.T) {
	if os.Getenv("CPIMPORT_DROP_CHILD") == "1" {
		dropChild()

		return
	}

	if os.Geteuid() != 0 {
		t.Skip("needs root")
	}

	home := filepath.Join(openTemp(t), "home")
	must(t, os.Mkdir(home, 0o750))
	must(t, os.Chown(home, 65534, 65534))

	cmd := exec.CommandContext(context.Background(), os.Args[0], "-test.run=^TestDrop$")

	cmd.Env = append(os.Environ(), "CPIMPORT_DROP_CHILD=1", "CPIMPORT_HOME="+home)

	out, err := cmd.CombinedOutput()
	if err != nil || !strings.Contains(string(out), "dropped ok") {
		t.Fatalf("%v: %s", err, out)
	}

	fi, err := os.Stat(filepath.Join(home, agentapi.CloudPanelImportDir))
	must(t, err)

	if fi.Mode().Perm() != 0o700 || !fi.IsDir() {
		t.Fatalf("target %v", fi.Mode())
	}

	// A symlink in place of the target is refused, not followed.
	link := filepath.Join(openTemp(t), "home2")
	must(t, os.Mkdir(link, 0o750))
	must(t, os.Chown(link, 65534, 65534))
	must(t, os.Symlink("/etc", filepath.Join(link, agentapi.CloudPanelImportDir)))

	cmd = exec.CommandContext(context.Background(), os.Args[0], "-test.run=^TestDrop$")

	cmd.Env = append(os.Environ(), "CPIMPORT_DROP_CHILD=1", "CPIMPORT_HOME="+link)

	out, _ = cmd.CombinedOutput()
	if !strings.Contains(string(out), "target refused") {
		t.Fatalf("symlinked target: %s", out)
	}
}

// openTemp is a temporary directory that other users can enter.
func openTemp(t *testing.T) string {
	t.Helper()

	d := t.TempDir()
	must(t, os.Chmod(d, 0o755))
	must(t, os.Chmod(filepath.Dir(d), 0o755))

	return d
}

func dropChild() {
	if err := Drop(65534, 65534); err != nil {
		println("drop:", err.Error())

		return
	}

	if f, err := os.OpenFile("/root/cpimport-drop-test", os.O_CREATE|os.O_WRONLY, 0o600); err == nil {
		_ = f.Close()

		println("still root")

		return
	}

	fd, err := openTarget(sites.Home{Path: os.Getenv("CPIMPORT_HOME"), UID: 65534, GID: 65534})
	if err != nil {
		println("target refused:", err.Error())

		return
	}

	println("dropped ok", fd)
}

func TestDropRefusesRoot(t *testing.T) {
	if err := Drop(0, 0); err == nil {
		t.Fatal("dropped to root")
	}
}
