// SPDX-License-Identifier: AGPL-3.0-or-later

package scanunit

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/maki-it/hostashi/internal/agentapi"
)

func write(t *testing.T, name, data string) {
	t.Helper()

	if err := os.MkdirAll(filepath.Dir(name), 0o750); err != nil {
		t.Fatal(err)
	}

	if err := os.WriteFile(name, []byte(data), 0o640); err != nil {
		t.Fatal(err)
	}
}

func list(t *testing.T, home string, targets []string) []string {
	t.Helper()

	var got []string

	err := walkList(context.Background(), home, targets, func(p string) error {
		got = append(got, strings.TrimPrefix(p, home))

		return nil
	})
	if err != nil {
		t.Fatal(err)
	}

	return got
}

func TestWalkList(t *testing.T) {
	home := filepath.Join(t.TempDir(), "blog")
	write(t, home+"/index.php", "x")
	write(t, home+"/htdocs/a.php", "x")
	write(t, home+"/htdocs/sub/b.php", "x")
	write(t, home+"/.cache/c", "x")
	write(t, home+"/htdocs/.cache/kept", "x")

	if err := os.Symlink("/etc/passwd", home+"/link"); err != nil {
		t.Fatal(err)
	}

	if err := os.Symlink("/etc", home+"/dirlink"); err != nil {
		t.Fatal(err)
	}

	if err := syscall.Mkfifo(home+"/fifo", 0o600); err != nil {
		t.Fatal(err)
	}

	// Only regular files; no symlinks, no fifo, and not the home's .cache.
	want := []string{"/htdocs/.cache/kept", "/htdocs/a.php", "/htdocs/sub/b.php", "/index.php"}
	if got := list(t, home, nil); !slices.Equal(got, want) {
		t.Fatalf("list %q, want %q", got, want)
	}

	if got := list(t, home, []string{"htdocs/sub", "index.php"}); !slices.Equal(got, []string{"/htdocs/sub/b.php", "/index.php"}) {
		t.Fatalf("targets %q", got)
	}

	// A file name with a newline cannot stand in the list: its folder
	// does, and nothing else below that folder after it (what came before
	// is scanned twice, which only costs time).
	write(t, home+"/htdocs/sub/evil\n.php", "x")
	write(t, home+"/htdocs/sub/z.php", "x")

	want = []string{"/htdocs/.cache/kept", "/htdocs/a.php", "/htdocs/sub/b.php", "/htdocs/sub", "/index.php"}
	if got := list(t, home, nil); !slices.Equal(got, want) {
		t.Fatalf("newline file: %q, want %q", got, want)
	}

	// The same for a folder.
	if err := os.Remove(home + "/htdocs/sub/evil\n.php"); err != nil {
		t.Fatal(err)
	}

	write(t, home+"/htdocs/bad\rdir/x.php", "x")

	want = []string{"/htdocs/.cache/kept", "/htdocs/a.php", "/htdocs", "/index.php"}
	if got := list(t, home, nil); !slices.Equal(got, want) {
		t.Fatalf("newline folder: %q, want %q", got, want)
	}

	// A walk stops with its context.
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	if err := walkList(ctx, home, nil, func(string) error { return nil }); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled walk: %v", err)
	}
}

func TestWriteList(t *testing.T) {
	ctx := context.Background()
	base := t.TempDir()
	home := filepath.Join(base, "blog")
	name := filepath.Join(base, "lists", "blog.list")

	write(t, home+"/a.php", "x")
	write(t, home+"/b/c.php", "x")

	n, err := writeList(ctx, name, home, nil, uint32(os.Getgid()))
	if err != nil || n != 2 {
		t.Fatalf("n %d %v", n, err)
	}

	b, _ := os.ReadFile(name)
	if string(b) != home+"/a.php\n"+home+"/b/c.php\n" || countLines(name) != 2 {
		t.Fatalf("list %q", b)
	}

	for p, mode := range map[string]os.FileMode{name: 0o640, filepath.Dir(name): 0o711 | os.ModeDir} {
		if fi, err := os.Stat(p); err != nil || fi.Mode() != mode {
			t.Errorf("%s: %v %v", p, fi.Mode(), err)
		}
	}

	// Too many files: the roots, for the scanner to walk, and no total.
	old := maxListEntries
	maxListEntries = 1

	t.Cleanup(func() { maxListEntries = old })

	if n, err := writeList(ctx, name, home, nil, uint32(os.Getgid())); err != nil || n != 0 {
		t.Fatalf("too many: %d %v", n, err)
	}

	if b, _ := os.ReadFile(name); string(b) != home+"\n" {
		t.Fatalf("too many: %q", b)
	}

	if _, err := writeList(ctx, name, home, []string{"a.php", "b"}, uint32(os.Getgid())); err != nil {
		t.Fatal(err)
	}

	if b, _ := os.ReadFile(name); string(b) != home+"/a.php\n"+home+"/b\n" {
		t.Fatalf("too many targets: %q", b)
	}

	if _, err := os.Stat(name + ".tmp"); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("temporary file left: %v", err)
	}
}

func TestProgressAdvance(t *testing.T) {
	log := filepath.Join(t.TempDir(), "blog.log")
	p := &progress{atStart: true, total: 4}

	// Lines about files count; warnings and empty lines do not; a line
	// still being written counts once it ends.
	write(t, log, "/h/a: OK\nWARNING: /h/f: Not supported file type\n\nLibClamAV Warning: x\n/h/b: Empty file\n/h/c: O")
	p.advance(log)

	if p.scanned != 2 {
		t.Fatalf("scanned %d", p.scanned)
	}

	f, err := os.OpenFile(log, os.O_APPEND|os.O_WRONLY, 0)
	if err != nil {
		t.Fatal(err)
	}

	_, _ = f.WriteString("K\n/h/d: X FOUND\n")
	_ = f.Close()

	p.advance(log)

	if got := p.report(); *got != (agentapi.ScanProgress{Scanned: 4, Total: 4}) {
		t.Fatalf("report %+v", got)
	}

	// A new, shorter log starts over.
	write(t, log, "/h/a: OK\n")
	p.advance(log)

	if p.scanned != 1 {
		t.Fatalf("new log: %d", p.scanned)
	}

	// No log yet.
	q := &progress{atStart: true}
	q.advance(filepath.Join(t.TempDir(), "missing"))

	if q.scanned != 0 {
		t.Fatal(q.scanned)
	}
}

func TestStatusVanishedAndTail(t *testing.T) {
	ctx := context.Background()
	m, ctl, home := newManager(t)

	if _, err := m.Start(ctx, agentapi.ScanStartRequest{Site: "blog", ID: 5}); err != nil {
		t.Fatal(err)
	}

	m.WaitLists()

	// Files deleted between the list and the scan are no error.
	write(t, home+"/a.php", "x")
	write(t, m.LogFile("blog"), home+"/a.php: OK\n"+home+"/gone.php: No such file or directory\nWARNING: "+home+"/gone.php: Can't access file\nERROR: Can't access file "+home+"/gone2.php\n")

	ctl.props["ActiveState"], ctl.props["ExecMainStatus"] = "active", "2"

	if st, _ := m.Status(ctx, agentapi.ScanStatusRequest{Site: "blog", ID: 5}); st.State != agentapi.ScanSucceeded || st.Error != "" {
		t.Fatalf("vanished: %+v", st)
	}

	// A listed file that is there but could not be read is.
	write(t, m.LogFile("blog"), home+"/a.php: No such file or directory\n")

	if st, _ := m.Status(ctx, agentapi.ScanStatusRequest{Site: "blog", ID: 5}); st.Error != "some files could not be scanned" {
		t.Fatalf("not gone: %+v", st)
	}

	// A failed run says why without the lines about single files.
	write(t, m.LogFile("blog"), strings.Repeat(home+"/a.php: OK\n", 5000)+"LibClamAV Error: out of memory\n")

	ctl.props["ActiveState"], ctl.props["Result"] = "failed", "exit-code"

	if st, _ := m.Status(ctx, agentapi.ScanStatusRequest{Site: "blog", ID: 5}); st.Error != "the scanner failed: LibClamAV Error: out of memory" {
		t.Fatalf("tail: %q", st.Error)
	}
}

func TestStartListFails(t *testing.T) {
	ctx := context.Background()
	m, ctl, _ := newManager(t)

	// The list directory cannot be created: a file is in the way.
	blocker := filepath.Join(t.TempDir(), "file")
	write(t, blocker, "x")
	m.ListDir = filepath.Join(blocker, "lists")

	if _, err := m.Start(ctx, agentapi.ScanStartRequest{Site: "blog", ID: 9}); err != nil {
		t.Fatal(err)
	}

	m.WaitLists()

	if len(ctl.started) != 0 {
		t.Fatalf("started %v", ctl.started)
	}

	if st, _ := m.Status(ctx, agentapi.ScanStatusRequest{Site: "blog", ID: 9}); st.State != agentapi.ScanFailed || st.Error != "the files of the site could not be listed" {
		t.Fatalf("status %+v", st)
	}

	// The next run may start.
	m.ListDir = filepath.Join(t.TempDir(), "lists")

	if _, err := m.Start(ctx, agentapi.ScanStartRequest{Site: "blog", ID: 10}); err != nil {
		t.Fatal(err)
	}

	m.WaitLists()

	if len(ctl.started) != 1 {
		t.Fatalf("started %v", ctl.started)
	}
}

// After a restart of the agent, a running scan's progress comes from its
// list and log.
func TestProgressAfterRestart(t *testing.T) {
	ctx := context.Background()
	m, ctl, home := newManager(t)
	write(t, home+"/a.php", "x")
	write(t, home+"/b.php", "x")

	if _, err := m.Start(ctx, agentapi.ScanStartRequest{Site: "blog", ID: 4}); err != nil {
		t.Fatal(err)
	}

	m.WaitLists()
	write(t, m.LogFile("blog"), home+"/a.php: OK\n")

	m2 := &Manager{Dir: m.Dir, UnitDir: m.UnitDir, LogDir: m.LogDir, ListDir: m.ListDir, QuarantineDir: m.QuarantineDir, DatabaseDir: m.DatabaseDir, Site: m.Site, Ctl: ctl}
	m2.now = time.Now

	st, _ := m2.Status(ctx, agentapi.ScanStatusRequest{Site: "blog", ID: 4})
	if st.State != agentapi.ScanRunning || st.Progress == nil || *st.Progress != (agentapi.ScanProgress{Scanned: 1, Total: 2}) {
		t.Fatalf("after restart: %+v %+v", st, st.Progress)
	}
}
