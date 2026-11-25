// SPDX-License-Identifier: AGPL-3.0-or-later

package scanunit

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"time"

	"golang.org/x/sys/unix"

	"github.com/maki-it/hostashi/internal/agentapi"
)

type fakeCtl struct {
	props   map[string]string
	started []string
	stopped []string
	reloads int
}

func (f *fakeCtl) DaemonReload(context.Context) error        { f.reloads++; return nil }
func (f *fakeCtl) ResetFailed(context.Context, string) error { return nil }
func (f *fakeCtl) Stop(_ context.Context, u string) error {
	f.stopped = append(f.stopped, u)
	return nil
}

func (f *fakeCtl) StartNoBlock(_ context.Context, u string) error {
	f.started = append(f.started, u)
	f.props["ActiveState"] = "activating"

	return nil
}

func (f *fakeCtl) Show(_ context.Context, _ string, props ...string) (map[string]string, error) {
	out := map[string]string{}
	for _, p := range props {
		out[p] = f.props[p]
	}

	return out, nil
}

func newManager(t *testing.T) (*Manager, *fakeCtl, string) {
	t.Helper()

	base := t.TempDir()
	home := filepath.Join(base, "home", "blog")

	if err := os.MkdirAll(home, 0o750); err != nil {
		t.Fatal(err)
	}

	ctl := &fakeCtl{props: map[string]string{"ActiveState": "inactive"}}
	m := &Manager{
		Dir: filepath.Join(base, "state"), UnitDir: filepath.Join(base, "units"), LogDir: filepath.Join(base, "log"),
		QuarantineDir: filepath.Join(base, "q"), DatabaseDir: "/var/lib/clamav",
		Site: func(s string) (Site, error) {
			if s != "blog" {
				return Site{}, errors.New("unknown")
			}

			return Site{Home: home, UID: uint32(os.Getuid()), GID: uint32(os.Getgid())}, nil
		},
		Ctl: ctl,
	}

	return m, ctl, home
}

func sum(b string) string {
	h := sha256.Sum256([]byte(b))

	return hex.EncodeToString(h[:])
}

func TestDropInContent(t *testing.T) {
	b, err := DropInContent("blog", Site{Home: "/home/blog.example"}, []string{"/srv/pool2"}, "/var/lib/clamav", "", nil)
	if err != nil {
		t.Fatal(err)
	}

	s := string(b)
	for _, want := range []string{
		"RequiresMountsFor=/home/blog.example\n",
		"TemporaryFileSystem=/srv/pool2:ro\n",
		"BindReadOnlyPaths=/home/blog.example\n",
		"ExecStart=\nExecStart=/usr/bin/clamscan --recursive --infected --no-summary --stdout --database=/var/lib/clamav \"--exclude-dir=^/home/blog\\\\.example/\\\\.cache$$\" \"/home/blog.example\"\n",
	} {
		if !strings.Contains(s, want) {
			t.Errorf("drop-in lacks %q:\n%s", want, s)
		}
	}

	if strings.Contains(s, "ReadWritePaths") || strings.Contains(s, "BindPaths=") {
		t.Errorf("home must be read only:\n%s", s)
	}

	for _, bad := range []string{"/home/a b", "home/x", "/home/x/../y", "/home/$x", "/home/%i"} {
		if _, err := DropInContent("blog", Site{Home: bad}, nil, "/var/lib/clamav", "", nil); err == nil {
			t.Errorf("home %q accepted", bad)
		}
	}

	if _, err := DropInContent("Bad", Site{Home: "/home/x"}, nil, "/var/lib/clamav", "", nil); err == nil {
		t.Error("bad site accepted")
	}

	// With the real-time mode's clamd, clamdscan hands it the open files.
	b, err = DropInContent("blog", Site{Home: "/home/blog"}, nil, "/var/lib/clamav", "/etc/hostashi-scan/blog.conf", nil)
	if err != nil || !strings.Contains(string(b), "ExecStart=\nExecStart=/usr/bin/clamdscan --config-file=/etc/hostashi-scan/blog.conf --fdpass --infected --no-summary --stdout \"/home/blog\"\n") {
		t.Errorf("clamdscan drop-in: %v\n%s", err, b)
	}
}

func TestStartAndStatus(t *testing.T) {
	ctx := context.Background()
	m, ctl, home := newManager(t)

	st, err := m.Start(ctx, agentapi.ScanStartRequest{Site: "blog", ID: 7})
	if err != nil || st.State != agentapi.ScanRunning {
		t.Fatalf("start: %+v %v", st, err)
	}

	if len(ctl.started) != 1 || ctl.started[0] != "hostashi-scan@blog.service" {
		t.Fatalf("started %v", ctl.started)
	}

	if _, err := os.Stat(filepath.Join(m.UnitDir, "hostashi-scan@blog.service.d", DropIn)); err != nil {
		t.Fatal(err)
	}

	if _, err := m.Start(ctx, agentapi.ScanStartRequest{Site: "blog", ID: 8}); !errors.Is(err, ErrBusy) {
		t.Fatalf("second start: %v", err)
	}

	if st, _ := m.Status(ctx, agentapi.ScanStatusRequest{Site: "blog", ID: 7}); st.State != agentapi.ScanRunning {
		t.Fatalf("running: %+v", st)
	}

	// The run found one file.
	if err := os.WriteFile(filepath.Join(home, "shell.php"), []byte("bad"), 0o640); err != nil {
		t.Fatal(err)
	}

	if err := os.WriteFile(m.LogFile("blog"), []byte(home+"/shell.php: Php.Webshell-1 FOUND\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	ctl.props["ActiveState"], ctl.props["ExecMainStatus"] = "active", "1"

	st, err = m.Status(ctx, agentapi.ScanStatusRequest{Site: "blog", ID: 7})
	if err != nil || st.State != agentapi.ScanSucceeded || len(st.Findings) != 1 {
		t.Fatalf("finished: %+v %v", st, err)
	}

	if f := st.Findings[0]; f.Path != "shell.php" || f.Signature != "Php.Webshell-1" || f.SHA256 != sum("bad") || f.Size != 3 {
		t.Fatalf("finding %+v", f)
	}

	// Another id is not this run.
	if st, _ := m.Status(ctx, agentapi.ScanStatusRequest{Site: "blog", ID: 6}); st.State != agentapi.ScanUnknown {
		t.Fatalf("other id: %+v", st)
	}

	// Some files could not be read: still the findings, with a note.
	ctl.props["ExecMainStatus"] = "2"

	if st, _ := m.Status(ctx, agentapi.ScanStatusRequest{Site: "blog", ID: 7}); st.State != agentapi.ScanSucceeded || st.Error != "some files could not be scanned" || len(st.Findings) != 1 {
		t.Fatalf("partial: %+v", st)
	}

	// The first error line of the log says which file and why.
	partial := home + "/shell.php: Php.Webshell-1 FOUND\n" +
		home + "/private: Permission denied ERROR\n" + home + "/other: Permission denied ERROR\n"
	if err := os.WriteFile(m.LogFile("blog"), []byte(partial), 0o600); err != nil {
		t.Fatal(err)
	}

	if st, _ := m.Status(ctx, agentapi.ScanStatusRequest{Site: "blog", ID: 7}); st.Error != "some files could not be scanned: "+home+"/private: Permission denied ERROR" || len(st.Findings) != 1 {
		t.Fatalf("partial with reason: %+v", st)
	}

	// Exit 1 with no usable line is not "clean".
	if err := os.WriteFile(m.LogFile("blog"), []byte("/etc/passwd: X FOUND\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	ctl.props["ExecMainStatus"] = "1"

	if st, _ := m.Status(ctx, agentapi.ScanStatusRequest{Site: "blog", ID: 7}); st.State != agentapi.ScanSucceeded || st.Dropped != 1 {
		t.Fatalf("dropped: %+v", st)
	}

	if err := os.WriteFile(m.LogFile("blog"), []byte("LibClamAV Warning: something\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	if st, _ := m.Status(ctx, agentapi.ScanStatusRequest{Site: "blog", ID: 7}); st.State != agentapi.ScanFailed {
		t.Fatalf("exit 1 without findings: %+v", st)
	}

	ctl.props["ActiveState"], ctl.props["Result"] = "failed", "oom-kill"

	if st, _ := m.Status(ctx, agentapi.ScanStatusRequest{Site: "blog", ID: 7}); st.State != agentapi.ScanFailed || !strings.Contains(st.Error, "memory") {
		t.Fatalf("failed: %+v", st)
	}

	// A queued start that never ran.
	ctl.props["ActiveState"] = "inactive"
	m.now = func() time.Time { return time.Now().Add(time.Hour) }

	if st, _ := m.Status(ctx, agentapi.ScanStatusRequest{Site: "blog", ID: 7}); st.State != agentapi.ScanFailed {
		t.Fatalf("never ran: %+v", st)
	}

	if err := m.Remove(ctx, "blog"); err != nil {
		t.Fatal(err)
	}

	if _, err := os.Stat(filepath.Join(m.UnitDir, "hostashi-scan@blog.service.d")); !os.IsNotExist(err) {
		t.Fatalf("drop-in left: %v", err)
	}
}

func TestParseLogUntrusted(t *testing.T) {
	_, _, home := func() (*Manager, *fakeCtl, string) { return newManager(t) }()

	if err := os.MkdirAll(filepath.Join(home, "wp", "up"), 0o750); err != nil {
		t.Fatal(err)
	}

	for n, c := range map[string]string{"wp/a.php": "aaa", "wp/up/b: c.php": "bb"} {
		if err := os.WriteFile(filepath.Join(home, n), []byte(c), 0o640); err != nil {
			t.Fatal(err)
		}
	}

	if err := os.Symlink("/etc/passwd", filepath.Join(home, "link.php")); err != nil {
		t.Fatal(err)
	}

	if err := os.Symlink("wp", filepath.Join(home, "dirlink")); err != nil {
		t.Fatal(err)
	}

	log := strings.Join([]string{
		home + "/wp/a.php: Php.Shell-1 FOUND",
		home + "/wp/a.php: Php.Shell-1 FOUND",       // duplicate
		home + "/wp/up/b: c.php: Js.Inject-2 FOUND", // ": " in the name
		home + "/link.php: X FOUND",                 // symlink out
		home + "/dirlink/a.php: X FOUND",            // through a link: inside, allowed
		home + "/../other/x.php: X FOUND",           // outside
		"/etc/shadow: X FOUND",                      // outside
		home + "/wp: X FOUND",                       // a directory
		home + "/missing.php: X FOUND",              // gone
		home + "/wp/a.php: bad sig$ FOUND",          // bad signature
		"LibClamAV Warning: Bytecode run timed out",
		"",
	}, "\n")

	f, d := ParseLog(strings.NewReader(log), home)

	got := map[string]string{}
	for _, x := range f {
		got[x.Path] = x.Signature
	}

	want := map[string]string{"wp/a.php": "Php.Shell-1", "wp/up/b: c.php": "Js.Inject-2", "dirlink/a.php": "X"}
	if len(got) != len(want) {
		t.Fatalf("findings %v", f)
	}

	for k, v := range want {
		if got[k] != v {
			t.Errorf("%s: got %q want %q", k, got[k], v)
		}
	}

	if d != 7 {
		t.Errorf("dropped %d, want 7", d)
	}
}

func TestQuarantineRestore(t *testing.T) {
	ctx := context.Background()
	m, _, home := newManager(t)

	if err := os.MkdirAll(filepath.Join(home, "wp"), 0o750); err != nil {
		t.Fatal(err)
	}

	p := filepath.Join(home, "wp", "shell.php")
	if err := os.WriteFile(p, []byte("evil"), 0o640); err != nil {
		t.Fatal(err)
	}

	if _, err := m.Quarantine(ctx, agentapi.ScanQuarantineRequest{Site: "blog", Path: "wp/shell.php", SHA256: sum("other")}); !errors.Is(err, ErrChanged) {
		t.Fatalf("wrong hash: %v", err)
	}

	if _, err := os.Stat(p); err != nil {
		t.Fatal("file removed on a hash mismatch")
	}

	it, err := m.Quarantine(ctx, agentapi.ScanQuarantineRequest{Site: "blog", Path: "wp/shell.php", SHA256: sum("evil")})
	if err != nil {
		t.Fatal(err)
	}

	if _, err := os.Stat(p); !os.IsNotExist(err) {
		t.Fatal("file still there")
	}

	fi, err := os.Stat(filepath.Join(m.QuarantineDir, "blog", it.ID+".bin"))
	if err != nil || fi.Mode().Perm() != 0o600 {
		t.Fatalf("quarantined copy: %v %v", fi, err)
	}

	if _, err := m.Quarantine(ctx, agentapi.ScanQuarantineRequest{Site: "blog", Path: "wp/shell.php", SHA256: sum("evil")}); !errors.Is(err, ErrChanged) {
		t.Fatalf("gone: %v", err)
	}

	// Something in the way.
	if err := os.WriteFile(p, []byte("new"), 0o640); err != nil {
		t.Fatal(err)
	}

	if _, err := m.Restore(ctx, agentapi.ScanRestoreRequest{Site: "blog", ID: it.ID}); !errors.Is(err, ErrExists) {
		t.Fatalf("restore over a file: %v", err)
	}

	if err := os.Remove(p); err != nil {
		t.Fatal(err)
	}

	res, err := m.Restore(ctx, agentapi.ScanRestoreRequest{Site: "blog", ID: it.ID})
	if err != nil || res.Path != "wp/shell.php" {
		t.Fatalf("restore: %+v %v", res, err)
	}

	b, err := os.ReadFile(p)
	if err != nil || string(b) != "evil" {
		t.Fatalf("restored %q %v", b, err)
	}

	if fi, _ := os.Stat(p); fi.Mode().Perm() != 0o640 {
		t.Errorf("mode %v", fi.Mode())
	}

	if _, err := m.Restore(ctx, agentapi.ScanRestoreRequest{Site: "blog", ID: it.ID}); !errors.Is(err, ErrUnknownItem) {
		t.Fatalf("restore twice: %v", err)
	}

	// A symlink is never followed out of the home.
	if err := os.Symlink("/etc/hostname", filepath.Join(home, "l.php")); err != nil {
		t.Fatal(err)
	}

	if _, err := m.Quarantine(ctx, agentapi.ScanQuarantineRequest{Site: "blog", Path: "l.php", SHA256: sum("x")}); !errors.Is(err, ErrChanged) {
		t.Fatalf("symlink: %v", err)
	}

	for _, bad := range []string{"../x", "/etc/passwd", "a/../b", "a\nb"} {
		if _, err := m.Quarantine(ctx, agentapi.ScanQuarantineRequest{Site: "blog", Path: bad, SHA256: sum("x")}); err == nil {
			t.Errorf("path %q accepted", bad)
		}
	}
}

type fakeEngine struct{ states map[string]string }

func (f *fakeEngine) Start(_ context.Context, inst string) error {
	f.states[inst] = "activating"

	return nil
}

func (f *fakeEngine) State(_ context.Context, inst string) (string, error) {
	return f.states[inst], nil
}

func TestEngine(t *testing.T) {
	ctx := context.Background()
	dir, db := t.TempDir(), t.TempDir()
	ctl := &fakeEngine{states: map[string]string{"install": "inactive", "remove": "inactive"}}
	e := &Engine{Dir: dir, DatabaseDir: db, Ctl: ctl, DpkgStatus: filepath.Join(dir, "dpkg-status")}

	if st, _ := e.Status(ctx); st.State != agentapi.ScanEngineOff {
		t.Fatalf("off: %+v", st)
	}

	if st, _ := e.Set(ctx, agentapi.ScanEngineSetRequest{On: false}); st.State != agentapi.ScanEngineOff {
		t.Fatalf("off again: %+v", st)
	}

	if st, err := e.Set(ctx, agentapi.ScanEngineSetRequest{On: true}); err != nil || st.State != agentapi.ScanEngineInstalling {
		t.Fatalf("install: %+v %v", st, err)
	}

	if _, err := e.Set(ctx, agentapi.ScanEngineSetRequest{On: false}); !errors.Is(err, ErrEngineBusy) {
		t.Fatalf("busy: %v", err)
	}

	ctl.states["install"] = "failed"

	if err := os.WriteFile(filepath.Join(dir, "install.log"), []byte("E: Unable to locate package clamav\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	if st, _ := e.Status(ctx); st.State != agentapi.ScanEngineFailed || !strings.Contains(st.Error, "clamav") {
		t.Fatalf("failed: %+v", st)
	}

	ctl.states["install"] = "inactive"

	for n, c := range map[string]string{"state": "on\n", "version": "1.4.6+dfsg-1~deb13u1\n"} {
		if err := os.WriteFile(filepath.Join(dir, n), []byte(c), 0o600); err != nil {
			t.Fatal(err)
		}
	}

	if err := os.WriteFile(filepath.Join(db, "daily.cld"), nil, 0o644); err != nil {
		t.Fatal(err)
	}

	st, _ := e.Status(ctx)
	if st.State != agentapi.ScanEngineOn || st.Version != "1.4.6+dfsg-1~deb13u1" || st.Signatures == "" || !e.On() {
		t.Fatalf("on: %+v", st)
	}
	// After apt updated ClamAV the installed version wins over the one
	// recorded at install time.
	dpkg := "Package: clamav-base\nStatus: install ok installed\nVersion: 9\n\n" +
		"Package: clamav\nStatus: deinstall ok config-files\nVersion: 1.0\n\n" +
		"Package: clamav\nStatus: install ok installed\nVersion: 1.4.7+dfsg-1~deb13u1\n"
	if err := os.WriteFile(e.DpkgStatus, []byte(dpkg), 0o600); err != nil {
		t.Fatal(err)
	}

	if st, _ := e.Status(ctx); st.Version != "1.4.7+dfsg-1~deb13u1" {
		t.Fatalf("dpkg version: %+v", st)
	}
}

func TestFirstError(t *testing.T) {
	dir := t.TempDir()

	for _, tc := range []struct{ log, want string }{
		{"", ""},
		{"/home/blog/a.php: X FOUND\nLibClamAV Warning: old\n", ""},
		{"ERROR: Can't access file /home/blog/x\n", "ERROR: Can't access file /home/blog/x"},
		{"LibClamAV Error: cli_loaddbdir(): no db\nERROR: later\n", "LibClamAV Error: cli_loaddbdir(): no db"},
		{"/home/blog/\x1b[31mx: Excluded\n/home/blog/\x1bsock: Not supported file type ERROR\n", "/home/blog/ sock: Not supported file type ERROR"},
		{"/home/blog/" + strings.Repeat("a", 600) + ": Permission denied ERROR\n", "/home/blog/" + strings.Repeat("a", maxErrorLine-len("/home/blog/")) + "..."},
	} {
		name := filepath.Join(dir, "log")
		if err := os.WriteFile(name, []byte(tc.log), 0o600); err != nil {
			t.Fatal(err)
		}

		if got := firstError(name); got != tc.want {
			t.Errorf("firstError(%q) = %q, want %q", tc.log, got, tc.want)
		}
	}

	if got := firstError(filepath.Join(dir, "missing")); got != "" {
		t.Errorf("missing log: %q", got)
	}
}

// While the real-time mode is on, the scan reads its own clamdscan
// configuration: the site user cannot read the one below /etc/hostashi.
func TestStartThroughClamd(t *testing.T) {
	ctx := context.Background()
	m, _, home := newManager(t)
	m.ClientDir = filepath.Join(t.TempDir(), "client")
	m.Clamd = func(context.Context) bool { return true }

	// The agent's umask is 0077; the modes must not depend on it.
	old := unix.Umask(0o077)
	_, err := m.Start(ctx, agentapi.ScanStartRequest{Site: "blog", ID: 1})

	unix.Umask(old)

	if err != nil {
		t.Fatal(err)
	}

	conf := m.ClientConfig("blog")

	b, err := os.ReadFile(conf)
	if err != nil {
		t.Fatal(err)
	}

	want := "LocalSocket /run/clamav/clamd.ctl\nExcludePath ^" + regexp.QuoteMeta(home+"/.cache") + "/\n"
	if !strings.HasSuffix(string(b), want) {
		t.Fatalf("client config:\n%s\nwant suffix\n%s", b, want)
	}

	for p, mode := range map[string]os.FileMode{conf: 0o644, m.ClientDir: 0o711 | os.ModeDir} {
		if fi, err := os.Stat(p); err != nil || fi.Mode() != mode {
			t.Errorf("%s: %v %v", p, fi.Mode(), err)
		}
	}

	d, err := os.ReadFile(filepath.Join(m.UnitDir, "hostashi-scan@blog.service.d", DropIn))
	if err != nil || !strings.Contains(string(d), "/usr/bin/clamdscan --config-file="+conf+" ") {
		t.Fatalf("drop-in %v\n%s", err, d)
	}

	if err := m.Remove(ctx, "blog"); err != nil {
		t.Fatal(err)
	}

	if _, err := os.Stat(conf); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("client config left: %v", err)
	}
}

func TestClientConfigContent(t *testing.T) {
	for _, tc := range []struct{ site, home string }{{"Bad", "/home/x"}, {"blog", "home/x"}, {"blog", "/home/a b"}} {
		if _, err := ClientConfigContent(tc.site, tc.home); err == nil {
			t.Errorf("%+v accepted", tc)
		}
	}
}

func TestDropInContentTargets(t *testing.T) {
	b, err := DropInContent("blog", Site{Home: "/home/blog"}, nil, "/var/lib/clamav", "", []string{"htdocs/wp-content", "100% \"odd\" $name.php"})
	if err != nil {
		t.Fatal(err)
	}

	want := "\"/home/blog/htdocs/wp-content\" \"/home/blog/100%% \\\"odd\\\" $$name.php\"\n"
	if !strings.Contains(string(b), want) || strings.Contains(string(b), "\"/home/blog\" ") || strings.Contains(string(b), "\"/home/blog\"\n") {
		t.Errorf("targets: want %q in\n%s", want, b)
	}

	b, err = DropInContent("blog", Site{Home: "/home/blog"}, nil, "/var/lib/clamav", "/etc/hostashi-scan/blog.conf", []string{"htdocs"})
	if err != nil || !strings.HasSuffix(string(b), "--stdout \"/home/blog/htdocs\"\n") {
		t.Errorf("clamdscan targets: %v\n%s", err, b)
	}

	for _, bad := range [][]string{{"../other"}, {"/etc"}, {"a/../b"}, {"a", "a"}, {"x\n"}, {"\xff"}, {""}} {
		if _, err := DropInContent("blog", Site{Home: "/home/blog"}, nil, "/var/lib/clamav", "", bad); err == nil {
			t.Errorf("targets %q accepted", bad)
		}
	}
}

func TestStartTargets(t *testing.T) {
	ctx := context.Background()
	m, ctl, home := newManager(t)

	if err := os.MkdirAll(filepath.Join(home, "htdocs", "up"), 0o750); err != nil {
		t.Fatal(err)
	}

	if err := os.WriteFile(filepath.Join(home, "htdocs", "index.php"), []byte("x"), 0o640); err != nil {
		t.Fatal(err)
	}

	if err := os.Symlink("/etc", filepath.Join(home, "etc")); err != nil {
		t.Fatal(err)
	}

	for _, bad := range []string{"missing", "etc", "etc/passwd"} {
		if _, err := m.Start(ctx, agentapi.ScanStartRequest{Site: "blog", ID: 3, Paths: []string{"htdocs", bad}}); !errors.Is(err, ErrTarget) {
			t.Errorf("%s: %v", bad, err)
		}
	}

	if len(ctl.started) != 0 {
		t.Fatalf("started %v", ctl.started)
	}

	if _, err := m.Start(ctx, agentapi.ScanStartRequest{Site: "blog", ID: 3, Paths: []string{"htdocs/up", "htdocs/index.php"}}); err != nil {
		t.Fatal(err)
	}

	b, err := os.ReadFile(filepath.Join(m.UnitDir, "hostashi-scan@blog.service.d", DropIn))
	if err != nil || !strings.Contains(string(b), appQuote(home+"/htdocs/up")+" "+appQuote(home+"/htdocs/index.php")+"\n") {
		t.Fatalf("drop-in %v\n%s", err, b)
	}
}

func appQuote(s string) string { return "\"" + strings.ReplaceAll(s, "%", "%%") + "\"" }

func TestValidateScanTargetsLimit(t *testing.T) {
	many := make([]string, agentapi.MaxScanTargets+1)
	for i := range many {
		many[i] = "f" + strconv.Itoa(i)
	}

	if agentapi.ValidateScanTargets(many) == nil || agentapi.ValidateScanTargets(many[:agentapi.MaxScanTargets]) != nil {
		t.Fatal("limit")
	}
}
