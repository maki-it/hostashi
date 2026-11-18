// SPDX-License-Identifier: AGPL-3.0-or-later

package web

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/fcgi"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
)

// testCtl runs a private nginx with its own pid file, error log and temp
// paths, the way the systemd drop-in runs the real one.
type testCtl struct {
	bin, dir, conf string
	failTest       error
}

func (c *testCtl) nginx(ctx context.Context, args ...string) error {
	args = append([]string{"-e", filepath.Join(c.dir, "error.log")}, args...)

	out, err := exec.CommandContext(ctx, c.bin, args...).CombinedOutput()
	if err != nil {
		return fmt.Errorf("nginx %v: %w: %s", args, err, strings.TrimSpace(string(out)))
	}

	return nil
}

func (c *testCtl) Test(ctx context.Context, conf string) error {
	if c.failTest != nil {
		return c.failTest
	}

	return c.nginx(ctx, "-t", "-q", "-c", conf)
}

func (c *testCtl) Reload(ctx context.Context) error {
	return c.nginx(ctx, "-c", c.conf, "-s", "reload")
}

func (c *testCtl) pid() int {
	b, err := os.ReadFile(filepath.Join(c.dir, "nginx.pid"))
	if err != nil {
		return 0
	}

	pid, _ := strconv.Atoi(strings.TrimSpace(string(b)))
	if pid > 1 && syscall.Kill(pid, 0) == nil {
		return pid
	}

	return 0
}

func (c *testCtl) stop() {
	pid := c.pid()
	if pid == 0 {
		return
	}

	_ = syscall.Kill(pid, syscall.SIGQUIT)

	for range 300 {
		if syscall.Kill(pid, 0) != nil {
			break
		}

		time.Sleep(10 * time.Millisecond)
	}

	_ = os.Remove(filepath.Join(c.dir, "ctl.sock"))
}

func (c *testCtl) Restart(ctx context.Context) error {
	c.stop()

	return c.nginx(ctx, "-c", c.conf)
}

type env struct {
	r    *Reconciler
	ctl  *testCtl
	port int
	dir  string
}

func freePort(t *testing.T) int {
	t.Helper()

	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}

	defer l.Close()

	addr, ok := l.Addr().(*net.TCPAddr)
	if !ok {
		t.Fatal("not a TCP address")
	}

	return addr.Port
}

func newEnv(t *testing.T) *env {
	t.Helper()

	bin, err := exec.LookPath("nginx")
	if err != nil {
		if _, statErr := os.Stat("/usr/sbin/nginx"); statErr != nil {
			t.Skip("nginx not installed")
		}

		bin = "/usr/sbin/nginx"
	}

	if _, err := os.Stat("/etc/nginx/mime.types"); err != nil {
		t.Skip("nginx mime.types missing")
	}

	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "tmp"), 0o700); err != nil {
		t.Fatal(err)
	}

	base := filepath.Join(dir, "web")
	r := &Reconciler{Base: base}
	ctl := &testCtl{bin: bin, dir: dir, conf: r.ConfigPath()}
	port := freePort(t)

	user := ""
	if os.Geteuid() == 0 {
		user = "root" // workers must read the test's private temp dirs
	}

	r.Driver = &Nginx{
		Ctl:           ctl,
		Listen:        []string{"127.0.0.1:" + strconv.Itoa(port)},
		ControlSocket: filepath.Join(dir, "ctl.sock"),
		User:          user,
		PIDFile:       filepath.Join(dir, "nginx.pid"),
		ErrorLog:      filepath.Join(dir, "error.log"),
		TempDir:       filepath.Join(dir, "tmp"),
		MimeTypes:     "/etc/nginx/mime.types",
		VerifyTimeout: 3 * time.Second,
	}

	t.Cleanup(ctl.stop)

	return &env{r: r, ctl: ctl, port: port, dir: dir}
}

func (e *env) site(t *testing.T, user, body string, domains ...string) Site {
	t.Helper()

	root := filepath.Join(e.dir, "sites", user)
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatal(err)
	}

	if err := os.WriteFile(filepath.Join(root, "index.html"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}

	return Site{User: user, Domains: domains, Root: root}
}

// fetch requests / with the Host header over TCP. A closed connection
// without answer (nginx 444) gives status 0.
func (e *env) fetch(t *testing.T, host string) (int, string) {
	t.Helper()

	return e.fetchPath(t, host, "/")
}

// fetchPath requests p with the Host header over TCP.
func (e *env) fetchPath(t *testing.T, host, p string) (int, string) {
	t.Helper()

	conn, err := net.DialTimeout("tcp", "127.0.0.1:"+strconv.Itoa(e.port), 2*time.Second)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}

	defer conn.Close()

	_ = conn.SetDeadline(time.Now().Add(3 * time.Second))
	_, _ = fmt.Fprintf(conn, "GET %s HTTP/1.1\r\nHost: %s\r\nConnection: close\r\n\r\n", p, host)

	resp, err := http.ReadResponse(bufio.NewReader(conn), nil)
	if err != nil {
		return 0, ""
	}

	defer resp.Body.Close()

	b, _ := io.ReadAll(resp.Body)

	return resp.StatusCode, string(b)
}

func TestRecoverStartsWithEmptyState(t *testing.T) {
	e := newEnv(t)
	ctx := t.Context()

	res, err := e.r.Recover(ctx)
	if err != nil {
		t.Fatal(err)
	}

	if res.Generation != 1 || !res.Changed {
		t.Fatalf("result %+v, want generation 1 changed", res)
	}

	if code, _ := e.fetch(t, "unknown.example.com"); code != 0 {
		t.Fatalf("unknown host got status %d, want no answer", code)
	}

	// A second start restores the same generation and writes nothing new.
	res, err = e.r.Recover(ctx)
	if err != nil || res.Generation != 1 || res.Changed {
		t.Fatalf("second recover: %+v, %v", res, err)
	}
}

func TestApplyServesSitesAndIsIdempotent(t *testing.T) {
	e := newEnv(t)
	ctx := t.Context()

	if _, err := e.r.Recover(ctx); err != nil {
		t.Fatal(err)
	}

	d := Desired{Sites: []Site{
		e.site(t, "alpha", "hello alpha", "alpha.example.com", "www.alpha.example.com"),
		e.site(t, "beta", "hello beta", "beta.example.org"),
	}}

	res, err := e.r.Apply(ctx, d)
	if err != nil {
		t.Fatal(err)
	}

	if res.Generation != 2 || !res.Changed {
		t.Fatalf("result %+v", res)
	}

	for host, want := range map[string]string{
		"alpha.example.com": "hello alpha", "www.alpha.example.com": "hello alpha", "beta.example.org": "hello beta",
	} {
		if code, body := e.fetch(t, host); code != http.StatusOK || body != want {
			t.Errorf("%s: %d %q, want 200 %q", host, code, body, want)
		}
	}

	res, err = e.r.Apply(ctx, d)
	if err != nil || res.Changed || res.Generation != 2 {
		t.Fatalf("same state again: %+v, %v", res, err)
	}

	// Removing a site takes its host names off.
	res, err = e.r.Apply(ctx, Desired{Sites: d.Sites[1:]})
	if err != nil || res.Generation != 3 {
		t.Fatalf("remove site: %+v, %v", res, err)
	}

	if code, _ := e.fetch(t, "alpha.example.com"); code != 0 {
		t.Fatalf("removed site still answers %d", code)
	}

	cur, ok, err := e.r.Current()
	if err != nil || !ok || len(cur.Sites) != 1 || cur.Sites[0].User != "beta" {
		t.Fatalf("Current: %+v %v %v", cur, ok, err)
	}
}

func TestHiddenFilesAreDenied(t *testing.T) {
	e := newEnv(t)
	ctx := t.Context()

	s := e.site(t, "alpha", "x", "alpha.example.com")
	if err := os.WriteFile(filepath.Join(s.Root, ".env"), []byte("SECRET=1"), 0o644); err != nil {
		t.Fatal(err)
	}

	if err := os.WriteFile(filepath.Join(s.Root, "config.php"), []byte("<?php $pw = 'secret';"), 0o644); err != nil {
		t.Fatal(err)
	}

	if _, err := e.r.Recover(ctx); err != nil {
		t.Fatal(err)
	}

	if _, err := e.r.Apply(ctx, Desired{Sites: []Site{s}}); err != nil {
		t.Fatal(err)
	}

	conn, err := net.Dial("tcp", "127.0.0.1:"+strconv.Itoa(e.port))
	if err != nil {
		t.Fatal(err)
	}

	defer conn.Close()

	_, _ = fmt.Fprint(conn, "GET /.env HTTP/1.1\r\nHost: alpha.example.com\r\nConnection: close\r\n\r\n")

	resp, err := http.ReadResponse(bufio.NewReader(conn), nil)
	if err != nil {
		t.Fatal(err)
	}

	defer resp.Body.Close()

	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("/.env answered %d, want 403", resp.StatusCode)
	}

	// A site without PHP never hands out PHP source.
	if code, body := e.fetchPath(t, "alpha.example.com", "/config.php"); code != http.StatusForbidden {
		t.Fatalf("/config.php answered %d %q, want 403", code, body)
	}
}

func TestRejectedConfigChangesNothing(t *testing.T) {
	e := newEnv(t)
	ctx := t.Context()

	if _, err := e.r.Recover(ctx); err != nil {
		t.Fatal(err)
	}

	e.ctl.failTest = errors.New("nginx: [emerg] simulated")

	_, err := e.r.Apply(ctx, Desired{Sites: []Site{e.site(t, "alpha", "x", "alpha.example.com")}})
	if !errors.Is(err, ErrValidation) {
		t.Fatalf("err = %v, want ErrValidation", err)
	}

	if n, _ := e.r.current(); n != 1 {
		t.Fatalf("current is %d, want 1", n)
	}

	if _, err := os.Stat(e.r.genDir(2) + ".failed"); err != nil {
		t.Fatalf("failed generation not kept: %v", err)
	}
}

// A listen address that is already taken passes nginx -t, and the reload
// "succeeds" while nginx keeps the old config. Only the generation check
// catches it (spike S2, finding 1).
func TestBindConflictRollsBack(t *testing.T) {
	e := newEnv(t)
	ctx := t.Context()

	if _, err := e.r.Recover(ctx); err != nil {
		t.Fatal(err)
	}

	good := Desired{Sites: []Site{e.site(t, "alpha", "hello alpha", "alpha.example.com")}}
	if _, err := e.r.Apply(ctx, good); err != nil {
		t.Fatal(err)
	}

	busy, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}

	defer busy.Close()

	e.r.Driver.Listen = append(e.r.Driver.Listen, busy.Addr().String())
	e.r.Driver.VerifyTimeout = time.Second

	_, err = e.r.Apply(ctx, good)
	if !errors.Is(err, ErrApplyFailed) {
		t.Fatalf("err = %v, want ErrApplyFailed", err)
	}

	if n, _ := e.r.current(); n != 2 {
		t.Fatalf("current is %d, want 2", n)
	}

	if code, body := e.fetch(t, "alpha.example.com"); code != http.StatusOK || body != "hello alpha" {
		t.Fatalf("after rollback: %d %q", code, body)
	}
}

func TestRecoverAfterCrash(t *testing.T) {
	e := newEnv(t)
	ctx := t.Context()

	if _, err := e.r.Recover(ctx); err != nil {
		t.Fatal(err)
	}

	d := Desired{Sites: []Site{e.site(t, "alpha", "hello alpha", "alpha.example.com")}}
	if _, err := e.r.Apply(ctx, d); err != nil {
		t.Fatal(err)
	}

	// A crash after the switch: generation 3 is current but not confirmed,
	// and a half-written generation 4 is left behind.
	files, err := e.r.Driver.Render(Desired{}, 3)
	if err != nil {
		t.Fatal(err)
	}

	if err := e.r.writeGeneration(3, Desired{}, files); err != nil {
		t.Fatal(err)
	}

	if err := e.r.switchTo(3); err != nil {
		t.Fatal(err)
	}

	if err := os.MkdirAll(e.r.genDir(4)+".tmp", 0o700); err != nil {
		t.Fatal(err)
	}

	res, err := e.r.Recover(ctx)
	if err != nil || res.Generation != 2 {
		t.Fatalf("recover: %+v, %v", res, err)
	}

	if _, err := os.Stat(e.r.genDir(4) + ".tmp"); !os.IsNotExist(err) {
		t.Fatalf("tmp generation not removed: %v", err)
	}

	if code, body := e.fetch(t, "alpha.example.com"); code != http.StatusOK || body != "hello alpha" {
		t.Fatalf("after recover: %d %q", code, body)
	}

	// The next apply skips the failed number 3; the removed tmp number may
	// be reused, it never went live.
	res, err = e.r.Apply(ctx, Desired{})
	if err != nil || res.Generation != 4 {
		t.Fatalf("next apply: %+v, %v", res, err)
	}
}

func TestDriftForcesNewGeneration(t *testing.T) {
	e := newEnv(t)
	ctx := t.Context()

	if _, err := e.r.Recover(ctx); err != nil {
		t.Fatal(err)
	}

	if err := os.WriteFile(filepath.Join(e.r.genDir(1), "nginx.conf"), []byte("# edited by hand\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	if got := e.r.drift(1); len(got) != 1 || got[0] != "nginx.conf (modified)" {
		t.Fatalf("drift = %v", got)
	}

	res, err := e.r.Apply(ctx, Desired{})
	if err != nil || res.Generation != 2 || !res.Changed {
		t.Fatalf("apply after drift: %+v, %v", res, err)
	}
}

func TestPruneKeepsRecentGenerations(t *testing.T) {
	e := newEnv(t)
	ctx := t.Context()
	e.r.Keep = 2

	if _, err := e.r.Recover(ctx); err != nil {
		t.Fatal(err)
	}

	for i := range 5 {
		s := e.site(t, "alpha", "x", "alpha"+strconv.Itoa(i)+".example.com")
		if _, err := e.r.Apply(ctx, Desired{Sites: []Site{s}}); err != nil {
			t.Fatal(err)
		}
	}

	gens, err := e.r.generations()
	if err != nil {
		t.Fatal(err)
	}

	if want := []int{4, 5, 6}; fmt.Sprint(gens) != fmt.Sprint(want) {
		t.Fatalf("generations %v, want %v", gens, want)
	}
}

func TestInvalidDesiredStateWritesNothing(t *testing.T) {
	r := &Reconciler{Base: filepath.Join(t.TempDir(), "web"), Driver: &Nginx{}}

	for name, s := range map[string]Site{
		"shell in domain": {User: "alpha", Domains: []string{"a.example.com;rm"}, Root: "/srv/a"},
		"root with dots":  {User: "alpha", Domains: []string{"a.example.com"}, Root: "/srv/../etc"},
		"root with space": {User: "alpha", Domains: []string{"a.example.com"}, Root: "/srv/a b"},
		"root variable":   {User: "alpha", Domains: []string{"a.example.com"}, Root: "/srv/$host"},
		"relative root":   {User: "alpha", Domains: []string{"a.example.com"}, Root: "srv/a"},
		"no domain":       {User: "alpha", Root: "/srv/a"},
		"bad user":        {User: "Alpha", Domains: []string{"a.example.com"}, Root: "/srv/a"},
		"control host":    {User: "alpha", Domains: []string{controlHost}, Root: "/srv/a"},
	} {
		if _, err := r.Apply(t.Context(), Desired{Sites: []Site{s}}); !errors.Is(err, ErrInvalidDesired) {
			t.Errorf("%s: err = %v, want ErrInvalidDesired", name, err)
		}
	}

	dup := Desired{Sites: []Site{
		{User: "alpha", Domains: []string{"a.example.com"}, Root: "/srv/a"},
		{User: "beta", Domains: []string{"a.example.com"}, Root: "/srv/b"},
	}}
	if _, err := r.Apply(t.Context(), dup); !errors.Is(err, ErrInvalidDesired) {
		t.Errorf("duplicate domain: err = %v", err)
	}

	if _, err := os.Stat(r.Base); !os.IsNotExist(err) {
		t.Fatalf("something was written: %v", err)
	}
}

// fcgiServer answers every FastCGI request with the script nginx asked for,
// like a PHP-FPM pool would run it.
func fcgiServer(t *testing.T, sock string) {
	t.Helper()

	l, err := net.Listen("unix", sock)
	if err != nil {
		t.Fatal(err)
	}

	t.Cleanup(func() { _ = l.Close() })

	go func() {
		_ = fcgi.Serve(l, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			env := fcgi.ProcessEnv(r)
			_, _ = fmt.Fprintf(w, "php %s %s", filepath.Base(env["SCRIPT_FILENAME"]), r.URL.RequestURI())
		}))
	}()
}

func TestPHPSiteGoesToItsPool(t *testing.T) {
	e := newEnv(t)
	ctx := t.Context()

	s := e.site(t, "alpha", "static", "alpha.example.com")
	s.PHPSocket = filepath.Join(e.dir, "php.sock")
	fcgiServer(t, s.PHPSocket)

	for name, body := range map[string]string{"index.php": "<?php", "info.php": "<?php", "style.css": "body{}"} {
		if err := os.WriteFile(filepath.Join(s.Root, name), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	if _, err := e.r.Recover(ctx); err != nil {
		t.Fatal(err)
	}

	if _, err := e.r.Apply(ctx, Desired{Sites: []Site{s}}); err != nil {
		t.Fatal(err)
	}

	for p, want := range map[string]string{
		"/":               "php index.php /",
		"/info.php?a=1":   "php info.php /info.php?a=1",
		"/blog/post-1":    "php index.php /blog/post-1", // front controller
		"/style.css":      "body{}",
		"/missing.php":    "404",
		"/style.css/.php": "404", // no path info tricks: the script must exist
	} {
		code, body := e.fetchPath(t, "alpha.example.com", p)

		got := body
		if code == http.StatusNotFound {
			got = "404"
		} else if code != http.StatusOK {
			got = strconv.Itoa(code)
		}

		if got != want {
			t.Errorf("%s: %d %q, want %q", p, code, body, want)
		}
	}

	// The health location is only for the agent's Unix socket.
	if code, _ := e.fetchPath(t, "alpha.example.com", "/.hostashi/health"); code != http.StatusNotFound {
		t.Fatalf("health over TCP answered %d, want 404", code)
	}
}

func TestBrokenApplicationDoesNotBlockChanges(t *testing.T) {
	e := newEnv(t)
	ctx := t.Context()

	// The pool socket does not exist: every PHP request fails with 502.
	s := e.site(t, "alpha", "x", "alpha.example.com")
	s.PHPSocket = filepath.Join(e.dir, "missing.sock")

	if err := os.WriteFile(filepath.Join(s.Root, "index.php"), []byte("<?php"), 0o644); err != nil {
		t.Fatal(err)
	}

	if _, err := e.r.Recover(ctx); err != nil {
		t.Fatal(err)
	}

	if _, err := e.r.Apply(ctx, Desired{Sites: []Site{s}}); err != nil {
		t.Fatal(err)
	}

	if code, _ := e.fetch(t, "alpha.example.com"); code != http.StatusBadGateway {
		t.Fatalf("broken pool answered %d, want 502", code)
	}

	// Another site can still be added.
	if _, err := e.r.Apply(ctx, Desired{Sites: []Site{s, e.site(t, "beta", "b", "beta.example.com")}}); err != nil {
		t.Fatal(err)
	}
}
