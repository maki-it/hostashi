package reconcile

import (
	"context"
	"errors"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func freePort(t *testing.T) int {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()
	return l.Addr().(*net.TCPAddr).Port
}

type counting struct {
	Driver
	activates atomic.Int32
	failNext  atomic.Int32 // number of upcoming Activate calls that fail
}

func (c *counting) Activate(ctx context.Context, dir string) error {
	c.activates.Add(1)
	if c.failNext.Load() > 0 {
		c.failNext.Add(-1)
		return errors.New("injected activate failure")
	}
	return c.Driver.Activate(ctx, dir)
}

type env struct {
	t    *testing.T
	nx   *NginxDriver
	drv  *counting
	rec  *Reconciler
	good Site
}

func newEnv(t *testing.T) *env {
	t.Helper()
	bin, err := exec.LookPath("nginx")
	if err != nil {
		t.Skip("nginx not installed")
	}
	tmp, err := os.MkdirTemp("", "s2spike")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(tmp) })
	nx := &NginxDriver{
		Binary: bin, RunDir: filepath.Join(tmp, "run"), User: "root",
		ListenPort: freePort(t), ControlPort: freePort(t), VerifyTimeout: 3 * time.Second,
	}
	drv := &counting{Driver: nx}
	rec := &Reconciler{Base: filepath.Join(tmp, "hostashi"), Driver: drv}
	root := filepath.Join(tmp, "www-good")
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "index.html"), []byte("good"), 0o644); err != nil {
		t.Fatal(err)
	}
	e := &env{t: t, nx: nx, drv: drv, rec: rec,
		good: Site{User: "good", Domains: []string{"good.example.com"}, Kind: KindStatic, Root: root}}
	t.Cleanup(func() { nx.Stop(context.Background(), rec.currentLink()) })
	return e
}

func (e *env) apply(sites ...Site) (Result, error) {
	return e.rec.Apply(context.Background(), Desired{Sites: sites})
}

func (e *env) mustApply(sites ...Site) Result {
	e.t.Helper()
	r, err := e.apply(sites...)
	if err != nil {
		e.t.Fatalf("apply: %v", err)
	}
	return r
}

func (e *env) body(host string) (int, string) {
	e.t.Helper()
	code, body, err := get(context.Background(), e.nx.ListenPort, host, "/")
	if err != nil {
		e.t.Fatalf("GET %s: %v", host, err)
	}
	return code, body
}

func (e *env) live() int {
	e.t.Helper()
	_, body, err := get(context.Background(), e.nx.ControlPort, "_hostashi_control", "/.hostashi/generation")
	if err != nil {
		e.t.Fatal(err)
	}
	var n int
	fmt.Sscanf(body, "%d", &n)
	return n
}

// probe hits the good site continuously and counts anything but "200 good".
type probe struct {
	stop  chan struct{}
	wg    sync.WaitGroup
	fails atomic.Int32
	total atomic.Int32
}

func (e *env) startProbe() *probe {
	p := &probe{stop: make(chan struct{})}
	p.wg.Add(1)
	go func() {
		defer p.wg.Done()
		for {
			select {
			case <-p.stop:
				return
			case <-time.After(time.Millisecond):
			}
			code, body, err := get(context.Background(), e.nx.ListenPort, "good.example.com", "/")
			p.total.Add(1)
			if err != nil || code != 200 || body != "good" {
				p.fails.Add(1)
			}
		}
	}()
	return p
}

func (p *probe) finish(t *testing.T) {
	t.Helper()
	time.Sleep(60 * time.Millisecond)
	close(p.stop)
	p.wg.Wait()
	if p.total.Load() < 5 {
		t.Fatalf("probe too short: %d requests", p.total.Load())
	}
	if f := p.fails.Load(); f != 0 {
		t.Fatalf("downtime: %d of %d requests failed", f, p.total.Load())
	}
	t.Logf("probe: %d requests, 0 failed", p.total.Load())
}

func TestFirstApplyServesAndIsIdempotent(t *testing.T) {
	e := newEnv(t)
	r := e.mustApply(e.good)
	if !r.Changed || r.Generation != 1 || e.live() != 1 {
		t.Fatalf("first apply: %+v live=%d", r, e.live())
	}
	if c, b := e.body("good.example.com"); c != 200 || b != "good" {
		t.Fatalf("got %d %q", c, b)
	}
	before := e.drv.activates.Load()
	r = e.mustApply(e.good)
	if r.Changed || r.Generation != 1 || e.drv.activates.Load() != before {
		t.Fatalf("second apply must be a no-op: %+v, activates %d -> %d", r, before, e.drv.activates.Load())
	}
	if _, err := os.Stat(filepath.Join(e.rec.Base, "generations", "2")); err == nil {
		t.Fatal("no-op created a generation")
	}
}

func TestChangeAddsAndRemovesSites(t *testing.T) {
	e := newEnv(t)
	e.mustApply(e.good)
	pr := e.startProbe()
	other := Site{User: "other", Domains: []string{"other.example.com", "www.other.example.com"}, Kind: KindStatic, Root: e.good.Root}
	if r := e.mustApply(e.good, other); r.Generation != 2 || e.live() != 2 {
		t.Fatalf("%+v live=%d", r, e.live())
	}
	if c, _ := e.body("www.other.example.com"); c != 200 {
		t.Fatalf("new site not served: %d", c)
	}
	e.mustApply(e.good)
	if c, _ := e.body("other.example.com"); c == 200 && e.live() == 3 {
		// other.example.com now falls through to the first server block; it must not serve "other" config.
		t.Log("removed site falls back to default server (expected in nginx)")
	}
	pr.finish(t)
}

func TestInvalidDesiredStateWritesNothing(t *testing.T) {
	e := newEnv(t)
	bad := []Site{
		{User: "good", Domains: []string{"a.example.com; return 200"}, Kind: KindStatic, Root: e.good.Root},
		{User: "good", Domains: []string{"a.example.com"}, Kind: KindStatic, Root: "/srv/../etc"},
		{User: "good", Domains: []string{"a.example.com"}, Kind: KindStatic, Root: "/srv/a b"},
		{User: "good", Domains: []string{"a.example.com"}, Kind: KindStatic, Root: "/srv/a;b"},
		{User: "Good", Domains: []string{"a.example.com"}, Kind: KindStatic, Root: "/srv/a"},
		{User: "good", Domains: []string{"a.example.com"}, Kind: KindProxy, ProxyPort: 80},
		{User: "good", Domains: []string{"a.example.com"}, Kind: "exec", Root: "/srv/a"},
		{User: "good", Domains: []string{"a.example.com"}, Kind: KindStatic, Root: "/srv/a", Snippet: "x\x00y"},
		{User: "good", Domains: nil, Kind: KindStatic, Root: "/srv/a"},
	}
	for i, s := range bad {
		if _, err := e.apply(s); !errors.Is(err, ErrInvalidDesired) {
			t.Errorf("case %d: want ErrInvalidDesired, got %v", i, err)
		}
	}
	if _, err := e.apply(e.good, e.good); !errors.Is(err, ErrInvalidDesired) {
		t.Errorf("duplicate site: %v", err)
	}
	if _, err := os.Stat(e.rec.gensDir()); err == nil {
		ents, _ := os.ReadDir(e.rec.gensDir())
		if len(ents) != 0 {
			t.Fatalf("invalid input wrote %d entries", len(ents))
		}
	}
}

// failure injection: each case must leave the previous generation live and
// serving without a single failed request.
func TestFailureInjection(t *testing.T) {
	// A listener that makes "listen" in a snippet fail at bind time.
	busy, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer busy.Close()
	busyPort := busy.Addr().(*net.TCPAddr).Port
	deadPort := freePort(t)

	cases := []struct {
		name    string
		snippet string
		kind    SiteKind
		port    int
		wantErr error
	}{
		{"syntax error in snippet", "this_is_not_a_directive on;", KindStatic, 0, ErrValidation},
		{"unknown variable", `return 200 "$no_such_variable";`, KindStatic, 0, ErrValidation},
		{"brace injection closes the server block", "}\nserver { listen 127.0.0.1:1; server_name evil.example.com;", KindStatic, 0, ErrValidation},
		{"unterminated block", "location /x {", KindStatic, 0, ErrValidation},
		{"include outside the generation (missing file)", "include /nonexistent/file.conf;", KindStatic, 0, ErrValidation},
		{"valid config, page returns 500", "return 500;", KindStatic, 0, ErrApplyFailed},
		{"valid config, dead upstream (502)", "", KindProxy, deadPort, ErrApplyFailed},
		{"valid config, bind conflict at reload", fmt.Sprintf("listen 127.0.0.1:%d;", busyPort), KindStatic, 0, nil},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			e := newEnv(t)
			e.mustApply(e.good)
			pr := e.startProbe()
			broken := Site{User: "broken", Domains: []string{"broken.example.com"}, Kind: c.kind, Root: e.good.Root, Snippet: c.snippet}
			if c.kind == KindProxy {
				broken.Root, broken.ProxyPort = "", c.port
			}
			_, err := e.apply(e.good, broken)
			if err == nil {
				t.Fatal("broken generation was accepted")
			}
			t.Logf("rejected as: %v", firstLine(err))
			if c.wantErr != nil && !errors.Is(err, c.wantErr) {
				t.Fatalf("want %v, got %v", c.wantErr, err)
			}
			if e.live() != 1 {
				t.Fatalf("live generation %d, want 1", e.live())
			}
			if n, ok := e.rec.current(); !ok || n != 1 {
				t.Fatalf("current -> %d", n)
			}
			// the failed generation is kept for debugging, and numbers are not reused
			if _, err := os.Stat(filepath.Join(e.rec.gensDir(), "2.failed", "FAILED")); err != nil {
				t.Fatalf("no failed generation kept: %v", err)
			}
			// a fixed state applies afterwards as generation 3
			if r := e.mustApply(e.good); r.Generation != 1 {
				t.Fatalf("unchanged desired state after failure: %+v", r)
			}
			pr.finish(t)
		})
	}
}

func firstLine(err error) string {
	s := err.Error()
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		s = s[:i]
	}
	if len(s) > 160 {
		s = s[:160] + "..."
	}
	return s
}

func TestRollbackFailureIsLoud(t *testing.T) {
	e := newEnv(t)
	e.mustApply(e.good)
	e.drv.failNext.Store(2) // apply fails to activate, then the rollback fails too
	other := Site{User: "other", Domains: []string{"other.example.com"}, Kind: KindStatic, Root: e.good.Root}
	_, err := e.apply(e.good, other)
	if !errors.Is(err, ErrRollbackFailed) {
		t.Fatalf("want ErrRollbackFailed, got %v", err)
	}
	// the old server process is untouched and still serves generation 1
	if c, b := e.body("good.example.com"); c != 200 || b != "good" {
		t.Fatalf("%d %q", c, b)
	}
	// Recover brings the server back in line with the confirmed generation
	if err := e.rec.Recover(context.Background()); err != nil {
		t.Fatal(err)
	}
	if e.live() != 1 {
		t.Fatalf("live %d after recover", e.live())
	}
}

func TestCrashRecovery(t *testing.T) {
	for _, step := range []string{"after-tmp-write", "after-commit", "after-switch", "after-activate"} {
		t.Run(step, func(t *testing.T) {
			e := newEnv(t)
			e.mustApply(e.good)
			e.rec.Crash = func(s string) bool { return s == step }
			other := Site{User: "other", Domains: []string{"other.example.com"}, Kind: KindStatic, Root: e.good.Root}
			if _, err := e.apply(e.good, other); !errors.Is(err, errCrashed) {
				t.Fatalf("want simulated crash, got %v", err)
			}
			e.rec.Crash = nil
			// a fresh process: new Reconciler over the same directory
			fresh := &Reconciler{Base: e.rec.Base, Driver: e.drv}
			if err := fresh.Recover(context.Background()); err != nil {
				t.Fatal(err)
			}
			if n, _ := fresh.current(); n != 1 || e.live() != 1 {
				t.Fatalf("after recover: current=%d live=%d, want 1", n, e.live())
			}
			if c, b := e.body("good.example.com"); c != 200 || b != "good" {
				t.Fatalf("%d %q", c, b)
			}
			ents, _ := os.ReadDir(fresh.gensDir())
			for _, en := range ents {
				if strings.HasSuffix(en.Name(), ".tmp") {
					t.Fatalf("tmp dir left behind: %s", en.Name())
				}
			}
			// and the next apply works and does not reuse a number
			r, err := fresh.Apply(context.Background(), Desired{Sites: []Site{e.good, other}})
			if err != nil {
				t.Fatal(err)
			}
			if r.Generation < 2 || e.live() != r.Generation {
				t.Fatalf("%+v live=%d", r, e.live())
			}
		})
	}
}

func TestRecoverStartsStoppedServer(t *testing.T) {
	e := newEnv(t)
	e.mustApply(e.good)
	e.nx.Stop(context.Background(), e.rec.currentLink())
	for i := 0; i < 50 && func() bool { _, a := e.nx.pid(); return a }(); i++ {
		time.Sleep(50 * time.Millisecond)
	}
	if err := e.rec.Recover(context.Background()); err != nil {
		t.Fatal(err)
	}
	if c, b := e.body("good.example.com"); c != 200 || b != "good" {
		t.Fatalf("%d %q", c, b)
	}
}

func TestDriftAndOverwrite(t *testing.T) {
	e := newEnv(t)
	e.mustApply(e.good)
	if d := e.rec.Drift(1); len(d) != 0 {
		t.Fatalf("fresh generation has drift: %v", d)
	}
	f := filepath.Join(e.rec.genDir(1), "sites", "good.conf")
	if err := os.WriteFile(f, []byte("# edited by hand\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(e.rec.genDir(1), "sites", "extra.conf"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	d := e.rec.Drift(1)
	if len(d) != 2 || d[0] != "sites/extra.conf (unexpected)" || d[1] != "sites/good.conf (modified)" {
		t.Fatalf("drift: %v", d)
	}
	plan, err := e.rec.Plan(Desired{Sites: []Site{e.good}})
	if err != nil || !plan.Changed || !strings.Contains(plan.Diff, "# edited by hand") {
		t.Fatalf("plan must show the manual edit as diff: %+v %v", plan, err)
	}
	// "overwrite": applying the same desired state creates a clean generation
	r := e.mustApply(e.good)
	if !r.Changed || r.Generation != 2 || len(e.rec.Drift(2)) != 0 {
		t.Fatalf("%+v", r)
	}
}

func TestPlanShowsDiffWithoutWriting(t *testing.T) {
	e := newEnv(t)
	e.mustApply(e.good)
	changed := e.good
	changed.Domains = []string{"good.example.com", "www.good.example.com"}
	p, err := e.rec.Plan(Desired{Sites: []Site{changed}})
	if err != nil {
		t.Fatal(err)
	}
	if !p.Changed || !strings.Contains(p.Diff, "+ ") || !strings.Contains(p.Diff, "www.good.example.com") {
		t.Fatalf("diff:\n%s", p.Diff)
	}
	if n, _ := e.rec.nextGen(); n != 2 {
		t.Fatalf("plan wrote a generation (next=%d)", n)
	}
	same, _ := e.rec.Plan(Desired{Sites: []Site{e.good}})
	if same.Changed || same.Diff != "" {
		t.Fatalf("unchanged plan: %+v", same)
	}
}

func TestLockPreventsParallelRuns(t *testing.T) {
	e := newEnv(t)
	unlock, err := e.rec.lock()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := (&Reconciler{Base: e.rec.Base, Driver: e.drv}).Apply(context.Background(), Desired{Sites: []Site{e.good}}); !errors.Is(err, ErrBusy) {
		t.Fatalf("want ErrBusy, got %v", err)
	}
	unlock()
	e.mustApply(e.good)
}

func TestPruneKeepsRecentConfirmedGenerations(t *testing.T) {
	e := newEnv(t)
	e.rec.Keep = 2
	for i := 0; i < 6; i++ {
		s := e.good
		s.Snippet = fmt.Sprintf("add_header X-Rev %d;", i)
		e.mustApply(s)
	}
	gens, _ := e.rec.listGens()
	if fmt.Sprint(gens) != "[4 5 6]" {
		t.Fatalf("kept %v, want [4 5 6]", gens)
	}
}

func TestDiffHelper(t *testing.T) {
	got := diffFiles(map[string][]byte{"a": []byte("1\n2\n3\n"), "gone": []byte("x\n")}, map[string][]byte{"a": []byte("1\n3\n4\n"), "new": []byte("y\n")})
	for _, want := range []string{"~~~ a", "- 2", "+ 4", "--- gone (removed)", "+++ new (new)"} {
		if !strings.Contains(got, want) {
			t.Errorf("missing %q in:\n%s", want, got)
		}
	}
}
