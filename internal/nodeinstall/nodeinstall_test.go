// SPDX-License-Identifier: AGPL-3.0-or-later

package nodeinstall

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/maki-it/hostashi/internal/agentapi"
)

type entry struct {
	name, link, body string
	typ              byte
	mode             int64
}

func archive(t *testing.T, entries []entry) []byte {
	t.Helper()

	var buf bytes.Buffer

	zw := gzip.NewWriter(&buf)
	tw := tar.NewWriter(zw)

	for _, e := range entries {
		h := &tar.Header{Name: e.name, Linkname: e.link, Typeflag: e.typ, Mode: e.mode, Size: int64(len(e.body))}
		if e.typ != tar.TypeReg {
			h.Size = 0
		}

		if err := tw.WriteHeader(h); err != nil {
			t.Fatal(err)
		}

		if e.typ == tar.TypeReg {
			if _, err := tw.Write([]byte(e.body)); err != nil {
				t.Fatal(err)
			}
		}
	}

	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}

	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}

	return buf.Bytes()
}

const top = "node-v24.99.0-linux-x64"

func goodEntries(version string) []entry {
	top := "node-v" + version + "-linux-x64"

	return []entry{
		{name: top + "/", typ: tar.TypeDir, mode: 0o755},
		{name: top + "/bin/", typ: tar.TypeDir, mode: 0o755},
		{name: top + "/bin/node", typ: tar.TypeReg, mode: 0o4777, body: "#!node " + version},
		{name: top + "/lib/node_modules/npm/bin/npm-cli.js", typ: tar.TypeReg, mode: 0o666, body: "npm"},
		{name: top + "/bin/npm", typ: tar.TypeSymlink, link: "../lib/node_modules/npm/bin/npm-cli.js"},
		{name: top + "/LICENSE", typ: tar.TypeReg, mode: 0o644, body: "MIT"},
	}
}

func TestUnpack(t *testing.T) {
	dir := t.TempDir()

	n, err := Unpack(bytes.NewReader(archive(t, goodEntries("24.99.0"))), dir, top)
	if err != nil || n != 6 {
		t.Fatalf("Unpack = %d, %v", n, err)
	}

	for name, want := range map[string]os.FileMode{
		"bin":                                 0o755 | os.ModeDir,
		"bin/node":                            0o755,
		"lib/node_modules/npm/bin/npm-cli.js": 0o644,
		"LICENSE":                             0o644,
	} {
		fi, err := os.Lstat(filepath.Join(dir, name))
		if err != nil {
			t.Fatal(err)
		}

		if fi.Mode() != want {
			t.Errorf("%s: mode %v, want %v", name, fi.Mode(), want)
		}
	}

	if l, err := os.Readlink(filepath.Join(dir, "bin/npm")); err != nil || l != "../lib/node_modules/npm/bin/npm-cli.js" {
		t.Errorf("bin/npm -> %q, %v", l, err)
	}
}

func TestUnpackRefuses(t *testing.T) {
	for name, e := range map[string]entry{
		"outside top":      {name: "other/x", typ: tar.TypeReg, body: "x"},
		"parent":           {name: top + "/../x", typ: tar.TypeReg, body: "x"},
		"absolute link":    {name: top + "/bin/sh", typ: tar.TypeSymlink, link: "/bin/sh"},
		"link out":         {name: top + "/bin/x", typ: tar.TypeSymlink, link: "../../etc/passwd"},
		"parent after dir": {name: top + "/bin/x", typ: tar.TypeSymlink, link: "node/../.."},
		"hard link":        {name: top + "/bin/x", typ: tar.TypeLink, link: top + "/bin/node"},
		"device":           {name: top + "/dev", typ: tar.TypeChar},
		"fifo":             {name: top + "/fifo", typ: tar.TypeFifo},
	} {
		t.Run(name, func(t *testing.T) {
			dir := t.TempDir()

			entries := append(goodEntries("24.99.0"), e)
			if _, err := Unpack(bytes.NewReader(archive(t, entries)), dir, top); err == nil {
				t.Fatal("Unpack accepted it")
			}
		})
	}
}

func server(t *testing.T, body []byte) *httptest.Server {
	t.Helper()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v24.99.0/node-v24.99.0-linux-x64.tar.gz" {
			http.NotFound(w, r)

			return
		}

		_, _ = w.Write(body)
	}))
	t.Cleanup(srv.Close)

	return srv
}

func installer(t *testing.T, root string, srv *httptest.Server, sum string) (*Installer, *bytes.Buffer) {
	t.Helper()

	var out bytes.Buffer

	return &Installer{
		Root: root, Client: srv.Client(), URL: srv.URL, GOARCH: "amd64", Out: &out,
		releases: []agentapi.NodeRelease{{Major: "24", Version: "24.99.0", SHA256: map[string]string{"x64": sum}}},
	}, &out
}

func sha(b []byte) string {
	s := sha256.Sum256(b)

	return hex.EncodeToString(s[:])
}

func TestInstall(t *testing.T) {
	body := archive(t, goodEntries("24.99.0"))
	srv := server(t, body)
	root := t.TempDir()
	in, out := installer(t, root, srv, sha(body))

	// An older release of the line is replaced in one step.
	old := filepath.Join(root, Dir, "24")
	if err := os.MkdirAll(filepath.Join(old, "bin"), 0o755); err != nil {
		t.Fatal(err)
	}

	if err := os.WriteFile(filepath.Join(old, VersionFile), []byte("24.98.0\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	if err := in.Install(context.Background(), "24"); err != nil {
		t.Fatalf("Install: %v\n%s", err, out)
	}

	if v, err := ReadVersion(old); err != nil || v != "24.99.0" {
		t.Fatalf("version %q, %v", v, err)
	}

	b, err := os.ReadFile(filepath.Join(old, "bin", "node"))
	if err != nil || string(b) != "#!node 24.99.0" {
		t.Fatalf("bin/node %q, %v", b, err)
	}

	lines := strings.Split(strings.TrimSpace(out.String()), "\n")
	if last := lines[len(lines)-1]; !strings.HasPrefix(last, agentapi.NodeDoneMarker) {
		t.Fatalf("last line %q", last)
	}

	left, _ := os.ReadDir(filepath.Join(root, Dir))
	if len(left) != 1 || left[0].Name() != "24" {
		t.Fatalf("left in %s: %v", Dir, left)
	}

	// Again: nothing to download.
	srv.Close()

	if err := in.Install(context.Background(), "24"); err != nil {
		t.Fatalf("second Install: %v", err)
	}
}

func TestInstallChecksum(t *testing.T) {
	body := archive(t, goodEntries("24.99.0"))
	srv := server(t, body)
	root := t.TempDir()
	in, _ := installer(t, root, srv, strings.Repeat("0", 64))

	if err := in.Install(context.Background(), "24"); !errors.Is(err, ErrChecksum) {
		t.Fatalf("Install = %v, want ErrChecksum", err)
	}

	left, _ := os.ReadDir(filepath.Join(root, Dir))
	if len(left) != 0 {
		t.Fatalf("left in %s: %v", Dir, left)
	}
}

func TestInstallRefuses(t *testing.T) {
	// The archive matches its checksum but has no bin/node.
	body := archive(t, []entry{{name: "node-v24.99.0-linux-x64/", typ: tar.TypeDir, mode: 0o755}})
	srv := server(t, body)
	root := t.TempDir()
	in, _ := installer(t, root, srv, sha(body))

	if err := in.Install(context.Background(), "24"); err == nil {
		t.Fatal("installed an archive without bin/node")
	}

	if _, err := os.Stat(filepath.Join(root, Dir, "24")); err == nil {
		t.Fatal("line directory created")
	}

	if err := in.Install(context.Background(), "25"); !errors.Is(err, agentapi.ErrNodeMajor) {
		t.Fatalf("Install 25 = %v", err)
	}

	in.GOARCH = "riscv64"
	if err := in.Install(context.Background(), "24"); !errors.Is(err, ErrArch) {
		t.Fatalf("Install on riscv64 = %v", err)
	}
}

type fakeUnits struct {
	mu      sync.Mutex
	active  map[string]bool
	started []string
}

func (f *fakeUnits) Start(_ context.Context, major string) error {
	f.mu.Lock()
	defer f.mu.Unlock()

	f.started = append(f.started, major)

	return nil
}

func (f *fakeUnits) Active(_ context.Context, major string) (bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()

	return f.active[major], nil
}

func (f *fakeUnits) set(major string, active bool) {
	f.mu.Lock()
	defer f.mu.Unlock()

	f.active[major] = active
}

func writeLog(t *testing.T, root, major, body string) {
	t.Helper()

	p := filepath.Join(root, LogDir, major+".log")
	if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
		t.Fatal(err)
	}

	if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestManager(t *testing.T) {
	root := t.TempDir()
	u := &fakeUnits{active: map[string]bool{}}
	m := &Manager{Runtime: Runtime, Units: u, Root: root}
	ctx := context.Background()

	st, err := m.Status(ctx)
	if err != nil || st.State != agentapi.PHPInstallIdle || len(st.Installed) != 0 {
		t.Fatalf("Status = %+v, %v", st, err)
	}

	if _, err := m.Start(ctx, "25"); err == nil {
		t.Fatal("started 25")
	}

	if _, err := m.Start(ctx, "24"); err != nil || len(u.started) != 1 {
		t.Fatalf("Start = %v, %v", err, u.started)
	}

	u.set("24", true)

	writeLog(t, root, "24", "node-install: downloading\n")

	if _, err := m.Start(ctx, "22"); !errors.Is(err, ErrBusy) {
		t.Fatalf("Start while busy = %v", err)
	}

	st, _ = m.Status(ctx)
	if st.State != agentapi.PHPInstallRunning || st.Last != "24" || len(st.Log) != 1 {
		t.Fatalf("Status running = %+v", st)
	}

	u.set("24", false)

	rel, _ := agentapi.NodeReleaseOf("24")
	writeLog(t, root, "24", "node-install: downloading\n"+agentapi.NodeDoneMarker+" "+rel.Version+"\n")

	d := filepath.Join(root, Dir, "24")
	if err := os.MkdirAll(filepath.Join(d, "bin"), 0o755); err != nil {
		t.Fatal(err)
	}

	if err := os.WriteFile(filepath.Join(d, "bin", "node"), nil, 0o755); err != nil {
		t.Fatal(err)
	}

	if err := os.WriteFile(filepath.Join(d, VersionFile), []byte(rel.Version+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	st, _ = m.Status(ctx)
	if st.State != agentapi.PHPInstallSucceeded || len(st.Installed) != 1 || st.Installed[0].Version != rel.Version {
		t.Fatalf("Status done = %+v", st)
	}

	if _, err := m.Start(ctx, "24"); !errors.Is(err, ErrInstalled) {
		t.Fatalf("Start installed = %v", err)
	}

	writeLog(t, root, "24", "node-install: download: 404 Not Found\n")

	if st, _ = m.Status(ctx); st.State != agentapi.PHPInstallFailed {
		t.Fatalf("Status failed = %+v", st)
	}
}

func TestRemove(t *testing.T) {
	root := t.TempDir()
	u := &fakeUnits{active: map[string]bool{}}
	m := &Manager{Runtime: Runtime, Units: u, Root: root}
	ctx := context.Background()

	rel, _ := agentapi.NodeReleaseOf("22")
	d := filepath.Join(root, Dir, "22")

	if err := os.MkdirAll(filepath.Join(d, "bin"), 0o755); err != nil {
		t.Fatal(err)
	}

	if err := os.WriteFile(filepath.Join(d, "bin", "node"), nil, 0o755); err != nil {
		t.Fatal(err)
	}

	if err := os.WriteFile(filepath.Join(d, VersionFile), []byte(rel.Version+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	writeLog(t, root, "22", agentapi.NodeDoneMarker+" "+rel.Version+"\n")

	if _, err := m.Remove(ctx, "25", nil); err == nil {
		t.Error("removed 25")
	}

	if _, err := m.Remove(ctx, "24", nil); !errors.Is(err, ErrNotInstalled) {
		t.Errorf("not installed: %v", err)
	}

	if _, err := m.Remove(ctx, "22", []string{"blog", "shop"}); !errors.Is(err, ErrInUse) || !strings.Contains(err.Error(), "blog, shop") {
		t.Errorf("in use: %v", err)
	}

	u.set("24", true)

	if _, err := m.Remove(ctx, "22", nil); !errors.Is(err, ErrBusy) {
		t.Errorf("busy: %v", err)
	}

	u.set("24", false)

	st, err := m.Remove(ctx, "22", nil)
	if err != nil || len(st.Installed) != 0 || st.Last != "" || st.State != agentapi.PHPInstallIdle {
		t.Fatalf("Remove = %+v, %v", st, err)
	}

	for _, p := range []string{d, filepath.Join(root, Dir, ".old-22"), filepath.Join(root, LogDir, "22.log")} {
		if _, err := os.Lstat(p); !errors.Is(err, os.ErrNotExist) {
			t.Errorf("%s left: %v", p, err)
		}
	}

	// A file in place of the line is not followed.
	if err := os.WriteFile(d, nil, 0o644); err != nil {
		t.Fatal(err)
	}

	if _, err := m.Remove(ctx, "22", nil); err == nil || errors.Is(err, ErrNotInstalled) {
		t.Errorf("file: %v", err)
	}
}

// installLine puts a release of a line in place, as the unit does.
func installLine(t *testing.T, root, major, version string) {
	t.Helper()

	d := filepath.Join(root, Dir, major)
	if err := os.MkdirAll(filepath.Join(d, "bin"), 0o755); err != nil {
		t.Fatal(err)
	}

	if err := os.WriteFile(filepath.Join(d, "bin", "node"), nil, 0o755); err != nil {
		t.Fatal(err)
	}

	if err := os.WriteFile(filepath.Join(d, VersionFile), []byte(version+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
}

// unitsThatInstall installs the pinned release when started and is
// active for a few polls.
type unitsThatInstall struct {
	mu      sync.Mutex
	root    string
	t       *testing.T
	polls   map[string]int
	started []string
	fail    map[string]bool
}

func (u *unitsThatInstall) Start(_ context.Context, major string) error {
	u.mu.Lock()
	defer u.mu.Unlock()

	u.started = append(u.started, major)
	u.polls[major] = 2

	return nil
}

func (u *unitsThatInstall) Active(_ context.Context, major string) (bool, error) {
	u.mu.Lock()
	defer u.mu.Unlock()

	if u.polls[major] == 0 {
		return false, nil
	}

	u.polls[major]--
	if u.polls[major] == 0 && !u.fail[major] {
		rel, _ := agentapi.NodeReleaseOf(major)
		installLine(u.t, u.root, major, rel.Version)
	}

	return true, nil
}

func TestUpdate(t *testing.T) {
	root := t.TempDir()
	u := &unitsThatInstall{root: root, t: t, polls: map[string]int{}, fail: map[string]bool{"22": true}}

	var mu sync.Mutex

	var done []string

	m := &Manager{Runtime: Runtime, Units: u, Root: root, Poll: time.Millisecond, Done: func(_ context.Context, major string) {
		mu.Lock()
		defer mu.Unlock()

		done = append(done, major)
	}}

	rel24, _ := agentapi.NodeReleaseOf("24")
	rel26, _ := agentapi.NodeReleaseOf("26")

	installLine(t, root, "22", "22.0.0")
	installLine(t, root, "24", "24.0.0")
	installLine(t, root, "26", rel26.Version)

	if got := m.Outdated(); !slices.Equal(got, []string{"22", "24"}) {
		t.Fatalf("Outdated = %v", got)
	}

	m.Update(t.Context())

	// 22 failed: no Done, still outdated; 24 updated and done.
	if !slices.Equal(u.started, []string{"22", "24"}) || !slices.Equal(done, []string{"24"}) {
		t.Fatalf("started %v, done %v", u.started, done)
	}

	if got := m.Outdated(); !slices.Equal(got, []string{"22"}) {
		t.Errorf("Outdated after = %v", got)
	}

	if v, _ := ReadVersion(filepath.Join(root, Dir, "24")); v != rel24.Version {
		t.Errorf("24 at %s", v)
	}

	// An install from the panel is followed as well.
	u.fail["22"] = false

	if _, err := m.Start(t.Context(), "22"); err != nil {
		t.Fatal(err)
	}

	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		mu.Lock()
		n := len(done)
		mu.Unlock()

		if n == 2 {
			break
		}

		time.Sleep(5 * time.Millisecond)
	}

	mu.Lock()
	defer mu.Unlock()

	if !slices.Equal(done, []string{"24", "22"}) {
		t.Errorf("done after panel install: %v", done)
	}
}
