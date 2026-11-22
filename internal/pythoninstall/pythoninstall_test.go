// SPDX-License-Identifier: AGPL-3.0-or-later

package pythoninstall

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
	"strings"
	"sync"
	"testing"

	"github.com/maki-it/hostashi/internal/agentapi"
	"github.com/maki-it/hostashi/internal/rtinstall"
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
		h := &tar.Header{Name: e.name, Linkname: e.link, Typeflag: e.typ, Mode: e.mode}
		if e.typ == tar.TypeReg {
			h.Size = int64(len(e.body))
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

// build is shaped like an install_only archive of python-build-standalone.
func build(version string) []entry {
	return []entry{
		{name: "python/", typ: tar.TypeDir, mode: 0o755},
		{name: "python/bin/python3.99", typ: tar.TypeReg, mode: 0o6777, body: "#!python " + version},
		{name: "python/bin/python3", typ: tar.TypeSymlink, link: "python3.99"},
		{name: "python/lib/python3.99/os.py", typ: tar.TypeReg, mode: 0o666, body: "os"},
		{name: "python/share/terminfo/x/xterm", typ: tar.TypeReg, mode: 0o644, body: "x"},
		{name: "python/share/terminfo/1/1178", typ: tar.TypeSymlink, link: "../x/xterm"},
	}
}

const path399 = "/20991231/cpython-3.99.1+20991231-x86_64-unknown-linux-gnu-install_only_stripped.tar.gz"

func server(t *testing.T, body []byte) *httptest.Server {
	t.Helper()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Go decodes %2B in the path; the raw path keeps it.
		if r.URL.Path != path399 || !strings.Contains(r.URL.RawPath, "%2B") {
			http.NotFound(w, r)

			return
		}

		_, _ = w.Write(body)
	}))
	t.Cleanup(srv.Close)

	return srv
}

func sha(b []byte) string {
	s := sha256.Sum256(b)

	return hex.EncodeToString(s[:])
}

func installer(root string, srv *httptest.Server, sum string) (*Installer, *bytes.Buffer) {
	var out bytes.Buffer

	return &Installer{
		Root: root, Client: srv.Client(), URL: srv.URL, GOARCH: "amd64", Out: &out,
		releases: []agentapi.PythonRelease{{Line: "3.99", Version: "3.99.1", Tag: "20991231", SHA256: map[string]string{"x86_64": sum}}},
	}, &out
}

func TestInstall(t *testing.T) {
	body := archive(t, build("3.99.1"))
	srv := server(t, body)
	root := t.TempDir()
	in, out := installer(root, srv, sha(body))

	if err := in.Install(context.Background(), "3.99"); err != nil {
		t.Fatalf("Install: %v\n%s", err, out)
	}

	d := filepath.Join(root, Dir, "3.99")
	if v, err := rtinstall.ReadVersion(d); err != nil || v != "3.99.1+20991231" {
		t.Fatalf("version %q, %v", v, err)
	}

	b, err := os.ReadFile(filepath.Join(d, "bin", "python3"))
	if err != nil || string(b) != "#!python 3.99.1" {
		t.Fatalf("bin/python3 %q, %v", b, err)
	}

	for name, want := range map[string]os.FileMode{"bin/python3.99": 0o755, "lib/python3.99/os.py": 0o644} {
		if fi, err := os.Lstat(filepath.Join(d, name)); err != nil || fi.Mode() != want {
			t.Errorf("%s: %v, %v, want %v", name, fi.Mode(), err, want)
		}
	}

	lines := strings.Split(strings.TrimSpace(out.String()), "\n")
	if last := lines[len(lines)-1]; !strings.HasPrefix(last, agentapi.PythonDoneMarker) {
		t.Fatalf("last line %q", last)
	}

	// Again: nothing to download.
	srv.Close()

	if err := in.Install(context.Background(), "3.99"); err != nil {
		t.Fatalf("second Install: %v", err)
	}
}

func TestInstallRefuses(t *testing.T) {
	body := archive(t, build("3.99.1"))
	srv := server(t, body)
	root := t.TempDir()

	in, _ := installer(root, srv, strings.Repeat("0", 64))
	if err := in.Install(context.Background(), "3.99"); !errors.Is(err, rtinstall.ErrChecksum) {
		t.Fatalf("Install = %v, want ErrChecksum", err)
	}

	// Matches its checksum, but no bin/python3.
	empty := archive(t, []entry{{name: "python/", typ: tar.TypeDir, mode: 0o755}})
	in, _ = installer(root, server(t, empty), sha(empty))

	if err := in.Install(context.Background(), "3.99"); err == nil {
		t.Fatal("installed an archive without bin/python3")
	}

	if _, err := os.Stat(filepath.Join(root, Dir, "3.99")); err == nil {
		t.Fatal("line directory created")
	}

	if err := in.Install(context.Background(), "3.10"); !errors.Is(err, agentapi.ErrPythonLine) {
		t.Fatalf("Install 3.10 = %v", err)
	}

	in.GOARCH = "riscv64"
	if err := in.Install(context.Background(), "3.99"); !errors.Is(err, rtinstall.ErrArch) {
		t.Fatalf("Install on riscv64 = %v", err)
	}
}

func TestArchive(t *testing.T) {
	rel, err := agentapi.PythonReleaseOf("3.13")
	if err != nil {
		t.Fatal(err)
	}

	a, err := Archive(DistURL, rel, "aarch64")
	if err != nil {
		t.Fatal(err)
	}

	want := DistURL + "/" + rel.Tag + "/cpython-" + rel.Version + "%2B" + rel.Tag + "-aarch64-unknown-linux-gnu-install_only_stripped.tar.gz"
	if a.URL != want || a.SHA256 != rel.SHA256["aarch64"] || a.Top != "python" {
		t.Fatalf("Archive = %+v", a)
	}

	for _, goarch := range []string{"amd64", "arm64"} {
		arch, err := Arch(goarch)
		if err != nil {
			t.Fatal(err)
		}

		for _, r := range agentapi.PythonReleases {
			if _, err := Archive(DistURL, r, arch); err != nil {
				t.Errorf("%s on %s: %v", r.Line, goarch, err)
			}
		}
	}
}

type fakeUnits struct {
	mu      sync.Mutex
	active  map[string]bool
	started []string
}

func (f *fakeUnits) Start(_ context.Context, line string) error {
	f.mu.Lock()
	defer f.mu.Unlock()

	f.started = append(f.started, line)

	return nil
}

func (f *fakeUnits) Active(_ context.Context, line string) (bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()

	return f.active[line], nil
}

func TestManager(t *testing.T) {
	root := t.TempDir()
	u := &fakeUnits{active: map[string]bool{}}
	m := NewManager(u)
	m.Root = root
	ctx := context.Background()

	st, err := m.Status(ctx)
	if out := Status(st); err != nil || out.State != agentapi.PHPInstallIdle || len(out.Installed) != 0 || out.Log == nil {
		t.Fatalf("Status = %+v, %v", out, err)
	}

	if _, err := m.Start(ctx, "3.10"); !errors.Is(err, agentapi.ErrPythonLine) {
		t.Fatalf("Start 3.10 = %v", err)
	}

	if _, err := m.Start(ctx, "3.13"); err != nil || len(u.started) != 1 {
		t.Fatalf("Start = %v, %v", err, u.started)
	}

	// The unit ran: the line is there with its pinned release.
	rel, _ := agentapi.PythonReleaseOf("3.13")
	d := filepath.Join(root, Dir, "3.13")

	if err := os.MkdirAll(filepath.Join(d, "bin"), 0o755); err != nil {
		t.Fatal(err)
	}

	if err := os.WriteFile(filepath.Join(d, "bin", "python3"), nil, 0o755); err != nil {
		t.Fatal(err)
	}

	if err := os.WriteFile(filepath.Join(d, rtinstall.VersionFile), []byte(rel.Version+"+"+rel.Tag+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	p := filepath.Join(root, LogDir, "3.13.log")
	if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
		t.Fatal(err)
	}

	if err := os.WriteFile(p, []byte("python-install: downloading\n"+agentapi.PythonDoneMarker+" "+rel.Version+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	st, _ = m.Status(ctx)
	if out := Status(st); out.State != agentapi.PHPInstallSucceeded || out.Last != "3.13" || len(out.Installed) != 1 || out.Installed[0] != (agentapi.PythonInstalled{Line: "3.13", Version: rel.Version, Build: rel.Tag}) {
		t.Fatalf("Status done = %+v", out)
	}

	if _, err := m.Start(ctx, "3.13"); !errors.Is(err, rtinstall.ErrInstalled) {
		t.Fatalf("Start installed = %v", err)
	}

	// A new build of the same version is outdated.
	if err := os.WriteFile(filepath.Join(d, rtinstall.VersionFile), []byte(rel.Version+"+20000101\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	if got := m.Outdated(); len(got) != 1 || got[0] != "3.13" {
		t.Fatalf("Outdated = %v", got)
	}

	if _, err := m.Remove(ctx, "3.13", []string{"shop"}); !errors.Is(err, rtinstall.ErrInUse) {
		t.Fatalf("Remove in use = %v", err)
	}

	st, err = m.Remove(ctx, "3.13", nil)
	if err != nil || len(st.Installed) != 0 {
		t.Fatalf("Remove = %+v, %v", st, err)
	}
}
