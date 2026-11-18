package helper

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"golang.org/x/sys/unix"

	"github.com/maki-it/hostashi/spike/s4-site-helper/confine"
)

// The test binary doubles as the helper binary (re-exec of the same binary, as planned for the agent)
// and as a probe that runs *inside* a dropped-privilege process.
func TestMain(m *testing.M) {
	if len(os.Args) >= 2 {
		switch os.Args[1] {
		case Arg0:
			os.Exit(Main())
		case "__introspect":
			os.Exit(introspect())
		case "__probe":
			os.Exit(probe(os.Args[2:]))
		case "__sleep":
			time.Sleep(time.Minute)
			os.Exit(0)
		}
	}
	os.Exit(m.Run())
}

func introspect() int {
	if err := Harden(); err != nil {
		fmt.Println(err)
		return 70
	}
	out := map[string]any{}
	b, _ := os.ReadFile("/proc/self/status")
	for _, l := range strings.Split(string(b), "\n") {
		for _, k := range []string{"Uid:", "Gid:", "Groups:", "CapInh:", "CapPrm:", "CapEff:", "CapBnd:", "CapAmb:", "NoNewPrivs:"} {
			if strings.HasPrefix(l, k) {
				out[strings.TrimSuffix(k, ":")] = strings.Join(strings.Fields(strings.TrimPrefix(l, k)), " ")
			}
		}
	}
	ents, _ := os.ReadDir("/proc/self/fd")
	fds := map[string]string{}
	for _, e := range ents {
		if t, err := os.Readlink("/proc/self/fd/" + e.Name()); err == nil && !strings.HasPrefix(t, "/proc") {
			fds[e.Name()] = t
		}
	}
	out["fds"] = fds
	out["env"] = os.Environ()
	wd, _ := os.Getwd()
	out["cwd"] = wd
	var rl unix.Rlimit
	unix.Getrlimit(unix.RLIMIT_CORE, &rl)
	out["rlimitCore"] = rl.Cur
	umask := unix.Umask(0)
	out["umask"] = umask
	pg, _ := unix.Getpgid(0)
	out["pgidIsPid"] = pg == os.Getpid()
	_ = json.NewEncoder(os.Stdout).Encode(out)
	return 0
}

// probe tries to open each path for reading and for writing and tries to get root back.
func probe(paths []string) int {
	if err := Harden(); err != nil {
		fmt.Println(err)
		return 70
	}
	res := map[string]string{}
	for _, p := range paths {
		if f, err := os.Open(p); err != nil {
			res["read "+p] = errName(err)
		} else {
			f.Close()
			res["read "+p] = "ok"
		}
		if f, err := os.OpenFile(p, os.O_WRONLY|os.O_APPEND, 0); err != nil {
			res["write "+p] = errName(err)
		} else {
			f.Close()
			res["write "+p] = "ok"
		}
	}
	res["setuid(0)"] = errName(syscall.Setuid(0))
	res["setresuid(0,0,0)"] = errName(unix.Setresuid(0, 0, 0))
	res["setgid(0)"] = errName(syscall.Setgid(0))
	res["mount tmpfs"] = errName(unix.Mount("tmpfs", "/mnt", "tmpfs", 0, ""))
	res["chroot"] = errName(syscall.Chroot("/tmp"))
	res["kill 1"] = errName(syscall.Kill(1, 0)) // signal 0 only checks permission
	_ = json.NewEncoder(os.Stdout).Encode(res)
	return 0
}

func errName(err error) string {
	if err == nil {
		return "ok"
	}
	if en, ok := err.(syscall.Errno); ok {
		return en.Error()
	}
	if pe, ok := err.(*os.PathError); ok {
		return errName(pe.Err)
	}
	return err.Error()
}

var (
	exeOnce sync.Once
	exePath string
)

// installedExe copies the test binary to a root-owned 0755 location, like the installed agent.
func installedExe(t *testing.T) string {
	t.Helper()
	exeOnce.Do(func() {
		dir, err := os.MkdirTemp("", "s4exe")
		if err != nil {
			t.Fatal(err)
		}
		os.Chmod(dir, 0o755)
		self, _ := os.Executable()
		src, err := os.ReadFile(self)
		if err != nil {
			t.Fatal(err)
		}
		exePath = filepath.Join(dir, "agent")
		if err := os.WriteFile(exePath, src, 0o755); err != nil {
			t.Fatal(err)
		}
	})
	return exePath
}

const (
	uidA = 20001
	uidB = 20002
)

func needRoot(t *testing.T) {
	t.Helper()
	if os.Geteuid() != 0 {
		t.Skip("needs root to start a helper as another uid")
	}
}

// base returns a 0755 directory (so a dropped helper may traverse it) for site homes.
func base(t *testing.T) string {
	t.Helper()
	d, err := os.MkdirTemp("", "s4base")
	if err != nil {
		t.Fatal(err)
	}
	os.Chmod(d, 0o755)
	t.Cleanup(func() { os.RemoveAll(d) })
	return d
}

func site(t *testing.T, base, name string, uid int, mode os.FileMode) (string, *os.File) {
	t.Helper()
	p := filepath.Join(base, name)
	if err := os.Mkdir(p, mode); err != nil {
		t.Fatal(err)
	}
	if err := os.Chown(p, uid, uid); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(p, mode); err != nil {
		t.Fatal(err)
	}
	f, err := os.Open(p)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { f.Close() })
	return p, f
}

func runner(t *testing.T) *Runner { return &Runner{Exe: installedExe(t), MinID: 10000} }

func call(t *testing.T, r *Runner, uid uint32, root *os.File, req Request, body string) (Response, string, error) {
	t.Helper()
	var out bytes.Buffer
	var in io.Reader
	if body != "" {
		in = strings.NewReader(body)
	}
	resp, err := r.Run(context.Background(), Identity{uid, uid}, root, req, in, &out)
	return resp, out.String(), err
}

func TestRoundTripAsSiteUser(t *testing.T) {
	needRoot(t)
	b := base(t)
	home, root := site(t, b, "alpha", uidA, 0o750)
	r := runner(t)

	resp, _, err := call(t, r, uidA, root, Request{Op: OpMkdir, Path: "htdocs/example.com", Mode: 0o750, Parents: true}, "")
	if err != nil || !resp.OK {
		t.Fatalf("%v %+v", err, resp)
	}
	resp, _, err = call(t, r, uidA, root, Request{Op: OpWrite, Path: "htdocs/example.com/index.html", Mode: 0o640, Size: 5}, "hello")
	if err != nil || !resp.OK {
		t.Fatalf("%v %+v", err, resp)
	}
	// The kernel, not our code, decided the owner: no chown needed, no race window.
	var st unix.Stat_t
	must(t, unix.Stat(filepath.Join(home, "htdocs/example.com/index.html"), &st))
	if st.Uid != uidA || st.Gid != uidA || st.Mode&0o7777 != 0o640 {
		t.Fatalf("owner %d:%d mode %o", st.Uid, st.Gid, st.Mode&0o7777)
	}
	must(t, unix.Stat(filepath.Join(home, "htdocs/example.com"), &st))
	if st.Uid != uidA || st.Mode&0o7777 != 0o750 {
		t.Fatalf("dir owner %d mode %o", st.Uid, st.Mode&0o7777)
	}
	resp, body, err := call(t, r, uidA, root, Request{Op: OpRead, Path: "htdocs/example.com/index.html"}, "")
	if err != nil || !resp.OK || body != "hello" {
		t.Fatalf("%v %+v %q", err, resp, body)
	}
	resp, _, _ = call(t, r, uidA, root, Request{Op: OpList, Path: "htdocs"}, "")
	if !resp.OK || len(resp.Entries) != 1 || resp.Entries[0].Name != "example.com" {
		t.Fatalf("%+v", resp)
	}
	resp, _, _ = call(t, r, uidA, root, Request{Op: OpRemove, Path: "htdocs", Recursive: true}, "")
	if !resp.OK {
		t.Fatalf("%+v", resp)
	}
	// Op-level failures are responses, not Go errors.
	resp, _, err = call(t, r, uidA, root, Request{Op: OpRead, Path: "nope"}, "")
	if err != nil || resp.OK || resp.Code != confine.CodeNotFound {
		t.Fatalf("%v %+v", err, resp)
	}
}

func must(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}

func TestLargeBodyBothWays(t *testing.T) {
	needRoot(t)
	_, root := site(t, base(t), "alpha", uidA, 0o750)
	r := runner(t)
	data := strings.Repeat("0123456789abcdef", 1<<20) // 16 MiB
	start := time.Now()
	resp, _, err := call(t, r, uidA, root, Request{Op: OpWrite, Path: "big.bin", Mode: 0o600, Size: int64(len(data))}, data)
	if err != nil || !resp.OK {
		t.Fatalf("%v %+v", err, resp)
	}
	resp, body, err := call(t, r, uidA, root, Request{Op: OpRead, Path: "big.bin"}, "")
	if err != nil || !resp.OK || body != data {
		t.Fatalf("%v %+v len=%d", err, resp, len(body))
	}
	t.Logf("16 MiB write + read through the helper: %v", time.Since(start))
	// Declared size and body disagree.
	resp, _, err = call(t, r, uidA, root, Request{Op: OpWrite, Path: "short", Mode: 0o600, Size: 10}, "abc")
	if err != nil || resp.OK || resp.Code != confine.CodeInvalid {
		t.Fatalf("%v %+v", err, resp)
	}
	resp, _, _ = call(t, r, uidA, root, Request{Op: OpStat, Path: "short"}, "")
	if resp.OK {
		t.Fatal("partial file exists")
	}
}

type status struct {
	Uid, Gid, Groups, CapInh, CapPrm, CapEff, CapBnd, CapAmb, NoNewPrivs string
	Fds                                                                  map[string]string `json:"fds"`
	Env                                                                  []string          `json:"env"`
	Cwd                                                                  string            `json:"cwd"`
	RlimitCore                                                           uint64            `json:"rlimitCore"`
	Umask                                                                int               `json:"umask"`
	PgidIsPid                                                            bool              `json:"pgidIsPid"`
}

func TestHelperProcessIsDeprivileged(t *testing.T) {
	needRoot(t)
	_, root := site(t, base(t), "alpha", uidA, 0o750)
	// Agent-side noise that must not leak: an extra open fd and environment.
	leak, _ := os.Open("/etc/hostname")
	defer leak.Close()
	t.Setenv("SECRET_TOKEN", "hunter2")

	r := runner(t)
	var out bytes.Buffer
	cmd, err := r.Command(context.Background(), Identity{uidA, uidA}, root, "__introspect")
	must(t, err)
	cmd.Stdout = &out
	cmd.Stderr = os.Stderr
	must(t, cmd.Run())
	var s status
	must(t, json.Unmarshal(out.Bytes(), &s))
	t.Logf("helper status: uid=%q gid=%q groups=%q capEff=%s capPrm=%s capBnd=%s nnp=%s fds=%v env=%v cwd=%s umask=%d",
		s.Uid, s.Gid, s.Groups, s.CapEff, s.CapPrm, s.CapBnd, s.NoNewPrivs, s.Fds, s.Env, s.Cwd, s.Umask)
	if s.Uid != "20001 20001 20001 20001" || s.Gid != "20001 20001 20001 20001" {
		t.Errorf("ids %q %q", s.Uid, s.Gid)
	}
	if s.Groups != "" {
		t.Errorf("supplementary groups: %q", s.Groups)
	}
	zero := "0000000000000000"
	for name, v := range map[string]string{"CapEff": s.CapEff, "CapPrm": s.CapPrm, "CapAmb": s.CapAmb, "CapInh": s.CapInh} {
		if v != zero {
			t.Errorf("%s = %s", name, v)
		}
	}
	if s.NoNewPrivs != "1" {
		t.Errorf("no_new_privs = %s", s.NoNewPrivs)
	}
	if len(s.Env) != 0 {
		t.Errorf("environment leaked: %v", s.Env)
	}
	if s.Cwd != "/" {
		t.Errorf("cwd %s", s.Cwd)
	}
	for k, v := range s.Fds {
		// 5 and 6 are the Go runtime's own epoll and eventfd, not inherited files.
		if k != "0" && k != "1" && k != "2" && k != "3" && !strings.HasPrefix(v, "anon_inode:") {
			t.Errorf("unexpected fd %s -> %s", k, v)
		}
		if strings.Contains(v, "hostname") {
			t.Errorf("agent fd leaked into helper: %s -> %s", k, v)
		}
	}
	if s.RlimitCore != 0 || !s.PgidIsPid {
		t.Errorf("rlimit core %d, own process group %v", s.RlimitCore, s.PgidIsPid)
	}
}

func runProbe(t *testing.T, uid uint32, args ...string) map[string]string {
	t.Helper()
	cmd, err := (&Runner{Exe: installedExe(t), MinID: 10000}).Command(context.Background(), Identity{uid, uid}, nil, append([]string{"__probe"}, args...)...)
	must(t, err)
	var out bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = os.Stderr
	must(t, cmd.Run())
	m := map[string]string{}
	must(t, json.Unmarshal(out.Bytes(), &m))
	return m
}

// Second layer: even if the path logic had a bug, the kernel's DAC still applies to the helper.
func TestDACStillHoldsInsideHelper(t *testing.T) {
	needRoot(t)
	b := base(t)
	homeA, _ := site(t, b, "alpha", uidA, 0o750)
	homeB, _ := site(t, b, "beta", uidB, 0o750)
	must(t, os.WriteFile(filepath.Join(homeA, "mine"), []byte("a"), 0o640))
	must(t, os.Chown(filepath.Join(homeA, "mine"), uidA, uidA))
	must(t, os.WriteFile(filepath.Join(homeB, "secret"), []byte("b"), 0o640))
	must(t, os.Chown(filepath.Join(homeB, "secret"), uidB, uidB))
	rootOnly := filepath.Join(b, "root-only")
	must(t, os.WriteFile(rootOnly, []byte("r"), 0o600))

	res := runProbe(t, uidA, filepath.Join(homeA, "mine"), filepath.Join(homeB, "secret"), rootOnly, "/etc/passwd", "/etc")
	for k, v := range res {
		t.Logf("as uid %d: %-44s %s", uidA, k, v)
	}
	want := map[string]string{
		"read " + filepath.Join(homeA, "mine"):    "ok",
		"read " + filepath.Join(homeB, "secret"):  "permission denied",
		"write " + filepath.Join(homeB, "secret"): "permission denied",
		"read " + rootOnly:                        "permission denied",
		"write " + rootOnly:                       "permission denied",
		"write /etc/passwd":                       "permission denied",
		"setuid(0)":                               "operation not permitted",
		"setresuid(0,0,0)":                        "operation not permitted",
		"setgid(0)":                               "operation not permitted",
		"mount tmpfs":                             "operation not permitted",
		"chroot":                                  "operation not permitted",
	}
	for k, v := range want {
		if res[k] != v {
			t.Errorf("%s: got %q want %q", k, res[k], v)
		}
	}
	// The honest counterpart: world-readable system files remain readable. DAC alone is not a sandbox.
	if res["read /etc/passwd"] != "ok" {
		t.Logf("note: /etc/passwd not readable here: %s", res["read /etc/passwd"])
	}
}

func TestIdentityGuards(t *testing.T) {
	r := runner(t)
	root, _ := os.Open("/")
	defer root.Close()
	for _, id := range []Identity{{0, 0}, {0, 20001}, {20001, 0}, {999, 999}, {9999, 20001}} {
		_, err := r.Run(context.Background(), id, root, Request{Op: OpStat}, nil, nil)
		if err == nil || !strings.Contains(err.Error(), "refusing") {
			t.Errorf("%+v: %v", id, err)
		}
	}
	rel := &Runner{Exe: "agent", MinID: 10000}
	if _, err := rel.Run(context.Background(), Identity{20001, 20001}, root, Request{Op: OpStat}, nil, nil); err == nil {
		t.Error("relative exe accepted")
	}
	// Invalid requests never start a process.
	for _, bad := range []Request{
		{Op: "exec", Path: "x"}, {Op: OpRead, Path: "../x"}, {Op: OpRead, Path: "/etc/passwd"},
		{Op: OpRead, Path: "a", Mode: 0o4755}, {Op: OpWrite, Path: "a", Size: -1}, {Op: OpStat, Recursive: true},
		{Op: OpRename, Path: "a"}, {Op: OpList, Limit: 100000}, {Op: OpRemove, Path: "a", To: "b"},
	} {
		_, err := r.Run(context.Background(), Identity{20001, 20001}, root, bad, nil, nil)
		if err == nil || strings.Contains(err.Error(), "helper failed") || strings.Contains(err.Error(), "helper start") {
			t.Errorf("%+v: %v", bad, err)
		}
	}
}

func TestHelperRefusesWrongRootOwnerAndRootUser(t *testing.T) {
	needRoot(t)
	b := base(t)
	_, rootB := site(t, b, "beta", uidB, 0o750)
	r := runner(t)
	// Agent bug simulation: right identity A, root directory of B.
	_, err := r.Run(context.Background(), Identity{uidA, uidA}, rootB, Request{Op: OpList}, nil, nil)
	if err == nil || !strings.Contains(err.Error(), "owned by uid") {
		t.Fatalf("helper accepted foreign root: %v", err)
	}
	// Helper started with root credentials (agent bug or misuse) refuses to serve.
	cmd := exec.Command(r.Exe, Arg0)
	cmd.Env = []string{}
	cmd.ExtraFiles = []*os.File{rootB}
	cmd.Stdin = strings.NewReader(`{"op":"stat"}` + "\n")
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	err = cmd.Run()
	ee, ok := err.(*exec.ExitError)
	if !ok || ee.ExitCode() != 70 {
		t.Fatalf("root helper: %v stderr=%s", err, stderr.String())
	}
}

func TestMalformedHelperInput(t *testing.T) {
	needRoot(t)
	home, root := site(t, base(t), "alpha", uidA, 0o750)
	r := runner(t)
	inputs := map[string]string{
		"empty":          "",
		"not json":       "hello\n",
		"unknown field":  `{"op":"stat","path":"","shell":"id"}` + "\n",
		"unknown op":     `{"op":"exec"}` + "\n",
		"trailing":       `{"op":"stat"} {"op":"stat"}` + "\n",
		"giant header":   `{"op":"stat","path":"` + strings.Repeat("a", 64<<10) + `"}` + "\n",
		"no newline":     `{"op":"stat"}`,
		"dotdot":         `{"op":"read","path":"../x"}` + "\n",
		"write w/o body": `{"op":"write","path":"f","mode":420,"size":4}` + "\n",
		"nul in path":    `{"op":"stat","path":"a\u0000b"}` + "\n",
		"huge size":      `{"op":"write","path":"f","mode":420,"size":99999999999}` + "\n",
		"setuid mode":    `{"op":"mkdir","path":"d","mode":2541}` + "\n", // 0o4755
	}
	for name, in := range inputs {
		cmd, err := r.Command(context.Background(), Identity{uidA, uidA}, root)
		must(t, err)
		cmd.Stdin = strings.NewReader(in)
		var out, errb bytes.Buffer
		cmd.Stdout, cmd.Stderr = &out, &errb
		done := make(chan error, 1)
		go func() { done <- cmd.Run() }()
		select {
		case <-done:
		case <-time.After(5 * time.Second):
			cmd.Process.Kill()
			t.Errorf("%s: helper hung", name)
			continue
		}
		if strings.Contains(errb.String(), "panic") || strings.Contains(errb.String(), "goroutine") {
			t.Errorf("%s: panic: %s", name, errb.String())
		}
		var resp Response
		if line, _, _ := strings.Cut(out.String(), "\n"); line != "" {
			_ = json.Unmarshal([]byte(line), &resp)
		}
		if resp.OK && name != "no newline" && name != "trailing" {
			t.Errorf("%s: accepted: %s", name, out.String())
		}
	}
	if ents, _ := os.ReadDir(home); len(ents) != 0 {
		t.Errorf("malformed input left files behind: %v", ents)
	}
}

func TestTimeoutKillsHelper(t *testing.T) {
	needRoot(t)
	_, root := site(t, base(t), "alpha", uidA, 0o750)
	r := runner(t)
	r.Args = []string{"__sleep"}
	r.Timeout = 300 * time.Millisecond
	start := time.Now()
	_, err := r.Run(context.Background(), Identity{uidA, uidA}, root, Request{Op: OpStat}, nil, nil)
	if err == nil {
		t.Fatal("no error")
	}
	if d := time.Since(start); d > 3*time.Second {
		t.Fatalf("took %v", d)
	}
}

// Misconfiguration that the confinement must still survive: site B is world-readable, A links to it.
func TestForeignSiteViaSymlinkIsRefusedEvenWhenDACWouldAllow(t *testing.T) {
	needRoot(t)
	b := base(t)
	_, rootA := site(t, b, "alpha", uidA, 0o750)
	homeB, _ := site(t, b, "beta", uidB, 0o755) // world-readable on purpose
	must(t, os.WriteFile(filepath.Join(homeB, "data"), []byte("B-DATA"), 0o644))
	must(t, os.Symlink(homeB, filepath.Join(b, "alpha", "to-b")))
	must(t, os.Chown(filepath.Join(b, "alpha", "to-b"), uidA, uidA)) // as if created by site A
	r := runner(t)

	if res := runProbe(t, uidA, filepath.Join(b, "alpha", "to-b", "data")); res["read "+filepath.Join(b, "alpha", "to-b", "data")] != "ok" {
		t.Fatalf("precondition: plain process as A should read B's world-readable data, got %v", res)
	}
	resp, body, err := call(t, r, uidA, rootA, Request{Op: OpRead, Path: "to-b/data"}, "")
	if err != nil || resp.OK || body != "" || resp.Code != confine.CodeEscape {
		t.Fatalf("%v %+v %q", err, resp, body)
	}
	resp, _, _ = call(t, r, uidA, rootA, Request{Op: OpWrite, Path: "to-b/evil", Mode: 0o644, Size: 1}, "x")
	if resp.OK {
		t.Fatal("wrote into B")
	}
	if _, err := os.Stat(filepath.Join(homeB, "evil")); err == nil {
		t.Fatal("file appeared in B")
	}
}

func TestSpawnLatency(t *testing.T) {
	needRoot(t)
	_, root := site(t, base(t), "alpha", uidA, 0o750)
	r := runner(t)
	n := 200
	if testing.Short() {
		n = 20 // the race detector makes every helper start slow
	}
	var ds []time.Duration
	for i := 0; i < n; i++ {
		start := time.Now()
		resp, _, err := call(t, r, uidA, root, Request{Op: OpList}, "")
		if err != nil || !resp.OK {
			t.Fatalf("%v %+v", err, resp)
		}
		ds = append(ds, time.Since(start))
	}
	sort.Slice(ds, func(i, j int) bool { return ds[i] < ds[j] })
	t.Logf("spawn-per-request list: n=%d median=%v p95=%v max=%v", n, ds[n/2], ds[n*95/100], ds[n-1])
	// Parallel: many sites at once.
	var wg sync.WaitGroup
	start := time.Now()
	for i := 0; i < 32 && !testing.Short(); i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 10; j++ {
				call(t, r, uidA, root, Request{Op: OpList}, "")
			}
		}()
	}
	wg.Wait()
	if !testing.Short() {
		t.Logf("320 requests, 32 in parallel: %v total (%.0f req/s)", time.Since(start), 320/time.Since(start).Seconds())
	}
}
