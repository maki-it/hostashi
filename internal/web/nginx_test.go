// SPDX-License-Identifier: AGPL-3.0-or-later

package web

import (
	"bufio"
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"errors"
	"fmt"
	"io"
	"math/big"
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

	code, _, body := e.fetchFull(t, host, p)

	return code, body
}

// fetchFull is fetchPath with the response headers.
func (e *env) fetchFull(t *testing.T, host, p string) (int, http.Header, string) {
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
		return 0, nil, ""
	}

	defer resp.Body.Close()

	b, _ := io.ReadAll(resp.Body)

	return resp.StatusCode, resp.Header, string(b)
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

// TestDisabledSite checks that a disabled site answers 503 for every path,
// serves none of its files, still answers ACME challenges and the agent's
// health check, and comes back when enabled again.
func TestDisabledSite(t *testing.T) {
	e := newEnv(t)
	ctx := t.Context()

	e.r.Driver.ACMERoot = filepath.Join(e.dir, "acme")

	challenge := filepath.Join(e.r.Driver.ACMERoot, ".well-known", "acme-challenge")
	if err := os.MkdirAll(challenge, 0o755); err != nil {
		t.Fatal(err)
	}

	if err := os.WriteFile(filepath.Join(challenge, "tok123"), []byte("tok123.thumb"), 0o644); err != nil {
		t.Fatal(err)
	}

	s := e.site(t, "alpha", "hello alpha", "alpha.example.com")
	if err := os.WriteFile(filepath.Join(s.Root, "file.txt"), []byte("data"), 0o644); err != nil {
		t.Fatal(err)
	}

	other := e.site(t, "beta", "hello beta", "beta.example.com")

	if _, err := e.r.Recover(ctx); err != nil {
		t.Fatal(err)
	}

	s.Disabled = true
	if _, err := e.r.Apply(ctx, Desired{Sites: []Site{s, other}}); err != nil {
		t.Fatal(err)
	}

	for _, path := range []string{"/", "/file.txt", "/index.html", "/missing"} {
		code, body := e.fetchPath(t, "alpha.example.com", path)
		if code != http.StatusServiceUnavailable || !strings.Contains(body, "Temporarily unavailable") || strings.Contains(body, "data") {
			t.Errorf("disabled site %s: %d %q", path, code, body)
		}
	}

	if code, body := e.fetchPath(t, "alpha.example.com", "/.well-known/acme-challenge/tok123"); code != http.StatusOK || body != "tok123.thumb" {
		t.Errorf("challenge on a disabled site: %d %q", code, body)
	}

	if code, body := e.fetch(t, "beta.example.com"); code != http.StatusOK || body != "hello beta" {
		t.Errorf("other site: %d %q", code, body)
	}

	s.Disabled = false
	if _, err := e.r.Apply(ctx, Desired{Sites: []Site{s, other}}); err != nil {
		t.Fatal(err)
	}

	if code, body := e.fetch(t, "alpha.example.com"); code != http.StatusOK || body != "hello alpha" {
		t.Errorf("enabled again: %d %q", code, body)
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
		"page dir":        {User: "alpha", Domains: []string{"a.example.com"}, Root: "/srv/a", Maintenance: &Maintenance{Page: "p", Digest: digest64, Dir: "/srv/p;x"}},
		"page id":         {User: "alpha", Domains: []string{"a.example.com"}, Root: "/srv/a", Maintenance: &Maintenance{Page: "P/..", Digest: digest64, Dir: "/srv/p"}},
		"allow ip":        {User: "alpha", Domains: []string{"a.example.com"}, Root: "/srv/a", Maintenance: &Maintenance{Page: "p", Digest: digest64, Dir: "/srv/p", AllowIPs: []string{"1.2.3.4; deny"}}},
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

// legacyHTTP2 reports whether nginx is older than 1.25.1 ("http2 on" unknown).
func legacyHTTP2(t *testing.T, bin string) bool {
	t.Helper()

	out, err := exec.Command(bin, "-v").CombinedOutput()
	if err != nil {
		t.Fatal(err)
	}

	var a, b, c int
	if _, err := fmt.Sscanf(strings.TrimSpace(string(out)[strings.Index(string(out), "/")+1:]), "%d.%d.%d", &a, &b, &c); err != nil {
		t.Fatalf("nginx -v: %q", out)
	}

	return a == 1 && (b < 25 || b == 25 && c < 1)
}

// selfSigned writes a certificate for domains and its key into dir.
func selfSigned(t *testing.T, dir string, domains ...string) (certFile, keyFile string, pool *x509.CertPool) {
	t.Helper()

	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}

	tmpl := &x509.Certificate{
		SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: domains[0]}, DNSNames: domains,
		NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(time.Hour),
		IsCA: true, BasicConstraintsValid: true, KeyUsage: x509.KeyUsageCertSign | x509.KeyUsageDigitalSignature,
		ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
	}

	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}

	keyDER, _ := x509.MarshalPKCS8PrivateKey(key)
	certFile, keyFile = filepath.Join(dir, "cert.pem"), filepath.Join(dir, "key.pem")

	if err := os.WriteFile(certFile, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}), 0o600); err != nil {
		t.Fatal(err)
	}

	if err := os.WriteFile(keyFile, pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: keyDER}), 0o600); err != nil {
		t.Fatal(err)
	}

	c, _ := x509.ParseCertificate(der)
	pool = x509.NewCertPool()
	pool.AddCert(c)

	return certFile, keyFile, pool
}

func TestTLSSite(t *testing.T) {
	e := newEnv(t)
	ctx := t.Context()

	tlsPort := freePort(t)
	e.r.Driver.ListenTLS = []string{"127.0.0.1:" + strconv.Itoa(tlsPort)}
	e.r.Driver.LegacyHTTP2 = legacyHTTP2(t, e.ctl.bin)
	e.r.Driver.ACMERoot = filepath.Join(e.dir, "acme")

	challenge := filepath.Join(e.r.Driver.ACMERoot, ".well-known", "acme-challenge")
	if err := os.MkdirAll(challenge, 0o755); err != nil {
		t.Fatal(err)
	}

	if err := os.WriteFile(filepath.Join(challenge, "tok123"), []byte("tok123.thumb"), 0o644); err != nil {
		t.Fatal(err)
	}

	secure := e.site(t, "alpha", "hello tls", "alpha.example.com")
	certDir := filepath.Join(e.dir, "certs")

	if err := os.MkdirAll(certDir, 0o700); err != nil {
		t.Fatal(err)
	}

	var pool *x509.CertPool

	secure.Cert, secure.Key, pool = selfSigned(t, certDir, "alpha.example.com")
	secure.HSTS = true
	plain := e.site(t, "beta", "hello plain", "beta.example.com")

	if _, err := e.r.Recover(ctx); err != nil {
		t.Fatal(err)
	}

	if _, err := e.r.Apply(ctx, Desired{Sites: []Site{secure, plain}}); err != nil {
		t.Fatal(err)
	}

	// HTTP: redirect for the TLS site, content for the other, challenges for both.
	if code, _ := e.fetch(t, "alpha.example.com"); code != http.StatusMovedPermanently {
		t.Fatalf("http on the TLS site answered %d, want 301", code)
	}

	if code, body := e.fetch(t, "beta.example.com"); code != http.StatusOK || body != "hello plain" {
		t.Fatalf("plain site: %d %q", code, body)
	}

	for _, host := range []string{"alpha.example.com", "beta.example.com"} {
		if code, body := e.fetchPath(t, host, "/.well-known/acme-challenge/tok123"); code != http.StatusOK || body != "tok123.thumb" {
			t.Errorf("challenge on %s: %d %q", host, code, body)
		}
	}

	addr := "127.0.0.1:" + strconv.Itoa(tlsPort)
	client := &http.Client{Timeout: 3 * time.Second, Transport: &http.Transport{
		TLSClientConfig: &tls.Config{RootCAs: pool, MinVersion: tls.VersionTLS12},
		DialContext: func(ctx context.Context, network, _ string) (net.Conn, error) {
			var d net.Dialer

			return d.DialContext(ctx, network, addr)
		},
		ForceAttemptHTTP2: true,
	}}

	resp, err := client.Get("https://alpha.example.com/")
	if err != nil {
		t.Fatal(err)
	}

	body, _ := io.ReadAll(resp.Body)
	_ = resp.Body.Close()

	if resp.StatusCode != http.StatusOK || string(body) != "hello tls" || resp.ProtoMajor != 2 {
		t.Fatalf("https: %d %q %s", resp.StatusCode, body, resp.Proto)
	}

	if h := resp.Header.Get("Strict-Transport-Security"); h != "max-age=31536000" {
		t.Fatalf("HSTS header %q", h)
	}

	// Another name over TLS: no certificate is shown at all.
	conn, err := tls.Dial("tcp", addr, &tls.Config{ServerName: "beta.example.com", InsecureSkipVerify: true})
	if err == nil {
		_ = conn.Close()

		t.Fatal("handshake for a site without TLS succeeded")
	}
}

// TestHTTP3Render checks the QUIC listeners and headers in the rendered
// files; the local nginx may lack http_v3_module, the container test runs
// them for real.
func TestHTTP3Render(t *testing.T) {
	n := &Nginx{
		Ctl: &testCtl{}, Listen: []string{"80"}, ListenTLS: []string{"443", "[::]:443"}, HTTP3: true,
		ControlSocket: "/run/x.sock", PIDFile: "/run/x.pid", ErrorLog: "/var/log/x", MimeTypes: "/etc/x",
	}

	f, err := n.Render(Desired{Sites: []Site{
		{User: "alpha", Domains: []string{"a.example.com"}, Root: "/srv/a", Cert: "/c.pem", Key: "/k.pem"},
		{User: "beta", Domains: []string{"b.example.com"}, Root: "/srv/b"},
	}}, 1)
	if err != nil {
		t.Fatal(err)
	}

	main := string(f.Content["nginx.conf"])
	for _, want := range []string{"listen 443 quic reuseport default_server;", "listen [::]:443 quic reuseport default_server;"} {
		if !strings.Contains(main, want) {
			t.Errorf("nginx.conf lacks %q", want)
		}
	}

	alpha := string(f.Content["sites/alpha.conf"])
	for _, want := range []string{"listen 443 quic;", "listen [::]:443 quic;", `add_header Alt-Svc 'h3=":443"; ma=86400' always;`} {
		if !strings.Contains(alpha, want) {
			t.Errorf("alpha.conf lacks %q", want)
		}
	}

	if strings.Contains(alpha, "Strict-Transport-Security") {
		t.Error("HSTS without the setting")
	}

	if beta := string(f.Content["sites/beta.conf"]); strings.Contains(beta, "quic") || strings.Contains(beta, "Alt-Svc") {
		t.Errorf("site without TLS got HTTP/3:\n%s", beta)
	}

	n.ListenTLS = nil
	if _, err := n.Render(Desired{}, 1); err == nil {
		t.Fatal("HTTP/3 without HTTPS listeners accepted")
	}
}

func TestCertificateNeedsTLSListener(t *testing.T) {
	n := &Nginx{Ctl: &testCtl{}, Listen: []string{"80"}, ControlSocket: "/run/x.sock", PIDFile: "/run/x.pid", ErrorLog: "/var/log/x", MimeTypes: "/etc/x"}

	_, err := n.Render(Desired{Sites: []Site{{User: "alpha", Domains: []string{"a.example.com"}, Root: "/srv/a", Cert: "/c.pem", Key: "/k.pem"}}}, 1)
	if err == nil || !strings.Contains(err.Error(), "no HTTPS listener") {
		t.Fatalf("err %v", err)
	}

	if err := (Desired{Sites: []Site{{User: "alpha", Domains: []string{"a.example.com"}, Root: "/srv/a", Cert: "/c.pem"}}}).Validate(); err == nil {
		t.Fatal("certificate without key accepted")
	}
}

// TestPanelProxy serves the panel's address: only ACME on port 80 without a
// certificate, then HTTPS on the panel port proxied to a Unix socket with the
// client address in X-Real-IP.
func TestPanelProxy(t *testing.T) {
	e := newEnv(t)
	ctx := t.Context()

	panelPort := freePort(t)
	e.r.Driver.ListenTLS = []string{"127.0.0.1:" + strconv.Itoa(freePort(t))}
	e.r.Driver.PanelListen = []string{"127.0.0.1:" + strconv.Itoa(panelPort)}
	e.r.Driver.PanelUpstream = filepath.Join(e.dir, "panel.sock")
	e.r.Driver.LegacyHTTP2 = legacyHTTP2(t, e.ctl.bin)
	e.r.Driver.ACMERoot = filepath.Join(e.dir, "acme")

	challenge := filepath.Join(e.r.Driver.ACMERoot, ".well-known", "acme-challenge")
	if err := os.MkdirAll(challenge, 0o755); err != nil {
		t.Fatal(err)
	}

	if err := os.WriteFile(filepath.Join(challenge, "tok456"), []byte("tok456.thumb"), 0o644); err != nil {
		t.Fatal(err)
	}

	// The panel: echoes what nginx passes on.
	ln, err := net.Listen("unix", e.r.Driver.PanelUpstream)
	if err != nil {
		t.Fatal(err)
	}

	if err := os.Chmod(e.r.Driver.PanelUpstream, 0o666); err != nil {
		t.Fatal(err)
	}

	srv := &http.Server{ReadHeaderTimeout: time.Second, Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = fmt.Fprintf(w, "panel host=%s ip=%s xff=%q proto=%s", r.Host, r.Header.Get("X-Real-IP"), r.Header.Get("X-Forwarded-For"), r.Header.Get("X-Forwarded-Proto"))
	})}

	go func() { _ = srv.Serve(ln) }()

	t.Cleanup(func() { _ = srv.Close() })

	if _, err := e.r.Recover(ctx); err != nil {
		t.Fatal(err)
	}

	panel := &Panel{Domain: "panel.example.com"}
	if _, err := e.r.Apply(ctx, Desired{Panel: panel}); err != nil {
		t.Fatal(err)
	}

	if code, body := e.fetchPath(t, "panel.example.com", "/.well-known/acme-challenge/tok456"); code != http.StatusOK || body != "tok456.thumb" {
		t.Fatalf("challenge: %d %q", code, body)
	}

	if code, _ := e.fetch(t, "panel.example.com"); code != http.StatusNotFound {
		t.Fatalf("panel over HTTP without a certificate answered %d, want 404", code)
	}

	certDir := filepath.Join(e.dir, "certs")
	if err := os.MkdirAll(certDir, 0o700); err != nil {
		t.Fatal(err)
	}

	var pool *x509.CertPool

	panel.Cert, panel.Key, pool = selfSigned(t, certDir, "panel.example.com")

	if _, err := e.r.Apply(ctx, Desired{Panel: panel}); err != nil {
		t.Fatal(err)
	}

	addr := "127.0.0.1:" + strconv.Itoa(panelPort)
	client := &http.Client{Timeout: 3 * time.Second, Transport: &http.Transport{
		TLSClientConfig: &tls.Config{RootCAs: pool, MinVersion: tls.VersionTLS12},
		DialContext: func(ctx context.Context, network, _ string) (net.Conn, error) {
			var d net.Dialer

			return d.DialContext(ctx, network, addr)
		},
		ForceAttemptHTTP2: true,
	}}

	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, "https://panel.example.com:"+strconv.Itoa(panelPort)+"/api/v1/health", nil)
	req.Header.Set("X-Forwarded-For", "203.0.113.9")
	req.Header.Set("X-Real-IP", "203.0.113.9")

	resp, err := client.Do(req)
	if err != nil {
		t.Fatal(err)
	}

	body, _ := io.ReadAll(resp.Body)
	_ = resp.Body.Close()

	want := fmt.Sprintf(`panel host=panel.example.com:%d ip=127.0.0.1 xff="" proto=https`, panelPort)
	if resp.StatusCode != http.StatusOK || string(body) != want || resp.Header.Get("Strict-Transport-Security") == "" {
		t.Fatalf("panel over https: %d %q %v", resp.StatusCode, body, resp.Header)
	}

	// Port 80 now redirects to the panel port.
	conn, err := net.DialTimeout("tcp", "127.0.0.1:"+strconv.Itoa(e.port), 2*time.Second)
	if err != nil {
		t.Fatal(err)
	}

	_, _ = fmt.Fprintf(conn, "GET /login?x=1 HTTP/1.1\r\nHost: panel.example.com\r\nConnection: close\r\n\r\n")

	r80, err := http.ReadResponse(bufio.NewReader(conn), nil)
	if err == nil {
		_ = r80.Body.Close()
	}

	_ = conn.Close()

	if err != nil || r80.StatusCode != http.StatusMovedPermanently ||
		r80.Header.Get("Location") != fmt.Sprintf("https://panel.example.com:%d/login?x=1", panelPort) {
		t.Fatalf("redirect: %v %v", err, r80)
	}

	// Another name on the panel port gets no certificate.
	if c, err := tls.Dial("tcp", addr, &tls.Config{ServerName: "other.example.com", InsecureSkipVerify: true}); err == nil {
		_ = c.Close()

		t.Fatal("handshake for another name on the panel port succeeded")
	}
}

// TestPanelOnSitesPort serves the panel by host name on the sites' TLS
// addresses (ADR-0042): the panel and a site answer on the same port, an
// unknown name still gets no certificate, and port 80 redirects without a
// port.
func TestPanelOnSitesPort(t *testing.T) {
	e := newEnv(t)
	ctx := t.Context()

	tlsPort := freePort(t)
	addr := "127.0.0.1:" + strconv.Itoa(tlsPort)
	e.r.Driver.ListenTLS = []string{addr}
	e.r.Driver.PanelListen = []string{addr}
	e.r.Driver.PanelUpstream = filepath.Join(e.dir, "panel.sock")
	e.r.Driver.LegacyHTTP2 = legacyHTTP2(t, e.ctl.bin)

	ln, err := net.Listen("unix", e.r.Driver.PanelUpstream)
	if err != nil {
		t.Fatal(err)
	}

	if err := os.Chmod(e.r.Driver.PanelUpstream, 0o666); err != nil {
		t.Fatal(err)
	}

	srv := &http.Server{ReadHeaderTimeout: time.Second, Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = fmt.Fprintf(w, "panel host=%s", r.Host)
	})}

	go func() { _ = srv.Serve(ln) }()

	t.Cleanup(func() { _ = srv.Close() })

	if _, err := e.r.Recover(ctx); err != nil {
		t.Fatal(err)
	}

	certDir := func(name string) string {
		d := filepath.Join(e.dir, "certs", name)
		if err := os.MkdirAll(d, 0o700); err != nil {
			t.Fatal(err)
		}

		return d
	}

	site := e.site(t, "alpha", "hello alpha", "alpha.example.com")
	site.Cert, site.Key, _ = selfSigned(t, certDir("alpha"), "alpha.example.com")
	panel := &Panel{Domain: "cp.example.com"}

	var pool *x509.CertPool

	panel.Cert, panel.Key, pool = selfSigned(t, certDir("panel"), "cp.example.com")

	sitePEM, err := os.ReadFile(site.Cert)
	if err != nil || !pool.AppendCertsFromPEM(sitePEM) {
		t.Fatalf("site certificate: %v", err)
	}

	if _, err := e.r.Apply(ctx, Desired{Sites: []Site{site}, Panel: panel}); err != nil {
		t.Fatal(err)
	}

	client := &http.Client{Timeout: 3 * time.Second, Transport: &http.Transport{
		TLSClientConfig: &tls.Config{RootCAs: pool, MinVersion: tls.VersionTLS12},
		DialContext: func(ctx context.Context, network, _ string) (net.Conn, error) {
			var d net.Dialer

			return d.DialContext(ctx, network, addr)
		},
	}}

	get := func(host string) string {
		t.Helper()

		req, _ := http.NewRequestWithContext(ctx, http.MethodGet, "https://"+host+"/", nil)

		resp, err := client.Do(req)
		if err != nil {
			t.Fatalf("%s: %v", host, err)
		}

		body, _ := io.ReadAll(resp.Body)
		_ = resp.Body.Close()

		return string(body)
	}

	if got := get("cp.example.com"); got != "panel host=cp.example.com" {
		t.Fatalf("panel: %q", got)
	}

	if got := get("alpha.example.com"); !strings.Contains(got, "hello alpha") {
		t.Fatalf("site next to the panel: %q", got)
	}

	if c, err := tls.Dial("tcp", addr, &tls.Config{ServerName: "other.example.com", InsecureSkipVerify: true}); err == nil {
		_ = c.Close()

		t.Fatal("handshake for an unknown name succeeded")
	}

	// The panel allowlist in nginx (ADR-0042): others get 403, the site
	// next to it is not affected.
	status := func(host string) int {
		t.Helper()

		req, _ := http.NewRequestWithContext(ctx, http.MethodGet, "https://"+host+"/", nil)

		resp, err := client.Do(req)
		if err != nil {
			t.Fatalf("%s: %v", host, err)
		}

		_ = resp.Body.Close()

		return resp.StatusCode
	}

	panel.Allow = []string{"192.0.2.1", "2001:db8::/32"}
	if _, err := e.r.Apply(ctx, Desired{Sites: []Site{site}, Panel: panel}); err != nil {
		t.Fatal(err)
	}

	if code := status("cp.example.com"); code != http.StatusForbidden {
		t.Fatalf("panel outside the allowlist: %d", code)
	}

	if code := status("alpha.example.com"); code != http.StatusOK {
		t.Fatalf("site with a panel allowlist: %d", code)
	}

	panel.Allow = []string{"127.0.0.0/8"}
	if _, err := e.r.Apply(ctx, Desired{Sites: []Site{site}, Panel: panel}); err != nil {
		t.Fatal(err)
	}

	if code := status("cp.example.com"); code != http.StatusOK {
		t.Fatalf("panel inside the allowlist: %d", code)
	}

	panel.Allow = nil

	conn, err := net.DialTimeout("tcp", "127.0.0.1:"+strconv.Itoa(e.port), 2*time.Second)
	if err != nil {
		t.Fatal(err)
	}

	_, _ = fmt.Fprintf(conn, "GET /login HTTP/1.1\r\nHost: cp.example.com\r\nConnection: close\r\n\r\n")

	r80, err := http.ReadResponse(bufio.NewReader(conn), nil)
	if err == nil {
		_ = r80.Body.Close()
	}

	_ = conn.Close()

	if err != nil || r80.StatusCode != http.StatusMovedPermanently || r80.Header.Get("Location") != "https://cp.example.com/login" {
		t.Fatalf("redirect: %v %v", err, r80)
	}
}

func TestPanelSharedRender(t *testing.T) {
	n := &Nginx{
		Ctl: &testCtl{}, Listen: []string{"80", "[::]:80"}, ListenTLS: []string{"443", "[::]:443"}, HTTP3: true,
		PanelListen: []string{"443", "[::]:443"}, PanelUpstream: "/run/hostashi-panel.sock",
		ControlSocket: "/run/x.sock", PIDFile: "/run/x.pid", ErrorLog: "/var/log/x", MimeTypes: "/etc/x",
	}

	f, err := n.Render(Desired{Panel: &Panel{Domain: "cp.example.com", Cert: "/c.pem", Key: "/k.pem"}}, 1)
	if err != nil {
		t.Fatal(err)
	}

	conf := string(f.Content["sites/_panel.conf"])
	for _, want := range []string{"return 301 https://$host$request_uri;", "listen 443 ssl;", "listen [::]:443 quic;", `add_header Alt-Svc 'h3=":443"; ma=86400' always;`} {
		if !strings.Contains(conf, want) {
			t.Errorf("missing %q in\n%s", want, conf)
		}
	}

	// The sites' default server already refuses unknown names; a second
	// default_server on the same port would not load.
	if strings.Contains(conf, "default_server") || strings.Contains(conf, "reuseport") {
		t.Errorf("shared panel renders its own default server:\n%s", conf)
	}

	// On its own port nothing changes.
	n.PanelListen = []string{"8443", "[::]:8443"}

	f, err = n.Render(Desired{Panel: &Panel{Domain: "cp.example.com", Cert: "/c.pem", Key: "/k.pem"}}, 1)
	if err != nil {
		t.Fatal(err)
	}

	conf = string(f.Content["sites/_panel.conf"])
	if !strings.Contains(conf, "return 301 https://$host:8443$request_uri;") || !strings.Contains(conf, "listen 8443 ssl default_server;") {
		t.Errorf("own port:\n%s", conf)
	}

	for _, bad := range []string{"10.0.0.1; deny", "0.0.0.0/0", "10.1.2.3/8", "fe80::1%eth0", "all"} {
		if err := (Desired{Panel: &Panel{Domain: "cp.example.com", Allow: []string{bad}}}).Validate(); err == nil {
			t.Errorf("allowed address %q accepted", bad)
		}
	}

	f, err = n.Render(Desired{Panel: &Panel{Domain: "cp.example.com", Cert: "/c.pem", Key: "/k.pem", Allow: []string{"192.0.2.1", "2001:db8::/32"}}}, 1)
	if err != nil {
		t.Fatal(err)
	}

	if conf = string(f.Content["sites/_panel.conf"]); !strings.Contains(conf, "allow 192.0.2.1;\n    allow 2001:db8::/32;\n    deny all;") {
		t.Errorf("allowlist:\n%s", conf)
	}

	for _, bad := range [][]string{{"443"}, {"80"}, {"443", "[::]:8443"}} {
		n.PanelListen = bad
		if _, err := n.Render(Desired{}, 1); err == nil {
			t.Errorf("panel addresses %v accepted", bad)
		}
	}
}

func TestPanelNeedsListener(t *testing.T) {
	n := &Nginx{Ctl: &testCtl{}, Listen: []string{"80"}, ControlSocket: "/run/x.sock", PIDFile: "/run/x.pid", ErrorLog: "/var/log/x", MimeTypes: "/etc/x"}

	if _, err := n.Render(Desired{Panel: &Panel{Domain: "panel.example.com"}}, 1); err == nil || !strings.Contains(err.Error(), "no panel listener") {
		t.Fatalf("err %v", err)
	}

	if err := (Desired{Sites: []Site{{User: "alpha", Domains: []string{"panel.example.com"}, Root: "/srv/a"}}, Panel: &Panel{Domain: "panel.example.com"}}).Validate(); err == nil {
		t.Fatal("site domain equal to the panel's accepted")
	}
}

const digest64 = "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"

// page writes a committed page directory for maintenance tests.
func (e *env) page(t *testing.T, files map[string]string) *Maintenance {
	t.Helper()

	dir := filepath.Join(e.dir, "pages", "maint."+digest64)
	for name, body := range files {
		p := filepath.Join(dir, name)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}

		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	return &Maintenance{Page: "maint", Digest: digest64, Dir: dir}
}

func TestMaintenanceRender(t *testing.T) {
	n := &Nginx{
		Ctl: &testCtl{}, Listen: []string{"80"}, ListenTLS: []string{"443"}, HTTP3: true,
		ControlSocket: "/run/x.sock", PIDFile: "/run/x.pid", ErrorLog: "/var/log/x", MimeTypes: "/etc/x",
	}

	m := &Maintenance{Page: "maint", Digest: digest64, Dir: "/var/lib/hostashi-pages/maint." + digest64, AllowIPs: []string{"203.0.113.7", "2001:db8::/32"}}

	f, err := n.Render(Desired{Sites: []Site{
		{User: "my-site", Domains: []string{"a.example.com"}, Root: "/srv/a", Cert: "/c.pem", Key: "/k.pem", HSTS: true, PHPSocket: "/run/php.sock", Maintenance: m},
		{User: "off", Domains: []string{"b.example.com"}, Root: "/srv/b", Disabled: true, Maintenance: m},
	}}, 1)
	if err != nil {
		t.Fatal(err)
	}

	conf := string(f.Content["sites/my-site.conf"])
	for _, want := range []string{
		"geo $hostashi_open_6d792d73697465 {", "203.0.113.7 1;", "2001:db8::/32 1;",
		"error_page 503 /.hostashi/page/maint/index.html;",
		"location ^~ /.hostashi/page/maint/ {", "alias /var/lib/hostashi-pages/maint." + digest64 + "/;",
		"text/html html;", "image/jpeg jpeg jpg;",
		`add_header Content-Security-Policy "` + PageCSP + `" always;`,
		"add_header Strict-Transport-Security", "add_header Alt-Svc",
	} {
		if !strings.Contains(conf, want) {
			t.Errorf("site conf lacks %q", want)
		}
	}

	if got := strings.Count(conf, "if ($hostashi_open_6d792d73697465 = 0)"); got != 2 {
		t.Errorf("%d allowlist checks, want 2 (location / and PHP):\n%s", got, conf)
	}

	if off := string(f.Content["sites/off.conf"]); strings.Contains(off, "geo ") || strings.Contains(off, "/.hostashi/page/") {
		t.Errorf("disabled site got the maintenance page:\n%s", off)
	}
}

func TestMaintenanceSite(t *testing.T) {
	e := newEnv(t)
	ctx := t.Context()

	e.r.Driver.ACMERoot = filepath.Join(e.dir, "acme")

	challenge := filepath.Join(e.r.Driver.ACMERoot, ".well-known", "acme-challenge")
	if err := os.MkdirAll(challenge, 0o755); err != nil {
		t.Fatal(err)
	}

	if err := os.WriteFile(filepath.Join(challenge, "tok123"), []byte("tok123.thumb"), 0o644); err != nil {
		t.Fatal(err)
	}

	s := e.site(t, "alpha", "hello alpha", "alpha.example.com")
	if err := os.WriteFile(filepath.Join(s.Root, "file.txt"), []byte("data"), 0o644); err != nil {
		t.Fatal(err)
	}

	s.Maintenance = e.page(t, map[string]string{
		"index.html":       `<!doctype html><link rel="stylesheet" href="css/site.css"><h1>Back soon</h1>`,
		"css/site.css":     "h1 { color: teal }",
		"img/logo.svg":     `<svg xmlns="http://www.w3.org/2000/svg"/>`,
		"js/countdown.js":  "console.log(1)",
		"fonts/sans.woff2": "wOF2",
	})
	s.Maintenance.AllowIPs = []string{"10.0.0.0/8"}

	if _, err := e.r.Recover(ctx); err != nil {
		t.Fatal(err)
	}

	if _, err := e.r.Apply(ctx, Desired{Sites: []Site{s}}); err != nil {
		t.Fatal(err)
	}

	for _, p := range []string{"/", "/file.txt", "/index.html", "/missing"} {
		code, h, body := e.fetchFull(t, "alpha.example.com", p)
		if code != http.StatusServiceUnavailable || !strings.Contains(body, "Back soon") || strings.Contains(body, "data") {
			t.Errorf("maintenance %s: %d %q", p, code, body)
		}

		if h.Get("Retry-After") != "3600" || h.Get("Content-Security-Policy") != PageCSP || h.Get("Cache-Control") != "no-store" {
			t.Errorf("maintenance %s headers: %v", p, h)
		}
	}

	for p, ct := range map[string]string{
		"/.hostashi/page/maint/css/site.css":     "text/css; charset=utf-8",
		"/.hostashi/page/maint/img/logo.svg":     "image/svg+xml; charset=utf-8",
		"/.hostashi/page/maint/js/countdown.js":  "text/javascript; charset=utf-8",
		"/.hostashi/page/maint/fonts/sans.woff2": "font/woff2",
		"/.hostashi/page/maint/":                 "text/html; charset=utf-8",
	} {
		code, h, _ := e.fetchFull(t, "alpha.example.com", p)
		if code != http.StatusOK || h.Get("Content-Type") != ct || h.Get("X-Content-Type-Options") != "nosniff" || h.Get("Retry-After") != "" {
			t.Errorf("page file %s: %d %v", p, code, h)
		}
	}

	if code, _, _ := e.fetchFull(t, "alpha.example.com", "/.hostashi/page/maint/missing.css"); code != http.StatusNotFound {
		t.Errorf("missing page file: %d", code)
	}

	if code, body := e.fetchPath(t, "alpha.example.com", "/.well-known/acme-challenge/tok123"); code != http.StatusOK || body != "tok123.thumb" {
		t.Errorf("challenge during maintenance: %d %q", code, body)
	}

	// The test client connects from 127.0.0.1.
	s.Maintenance.AllowIPs = []string{"10.0.0.0/8", "127.0.0.1"}
	if _, err := e.r.Apply(ctx, Desired{Sites: []Site{s}}); err != nil {
		t.Fatal(err)
	}

	if code, body := e.fetch(t, "alpha.example.com"); code != http.StatusOK || body != "hello alpha" {
		t.Errorf("allowed address: %d %q", code, body)
	}

	if code, body := e.fetchPath(t, "alpha.example.com", "/file.txt"); code != http.StatusOK || body != "data" {
		t.Errorf("allowed address file: %d %q", code, body)
	}

	s.Maintenance = nil
	if _, err := e.r.Apply(ctx, Desired{Sites: []Site{s}}); err != nil {
		t.Fatal(err)
	}

	// Hidden paths are denied again.
	if code, _ := e.fetchPath(t, "alpha.example.com", "/.hostashi/page/maint/css/site.css"); code != http.StatusForbidden {
		t.Errorf("page served after maintenance: %d", code)
	}
}

func TestParkedDomain(t *testing.T) {
	e := newEnv(t)
	ctx := t.Context()
	tlsPort := freePort(t)
	e.r.Driver.ListenTLS = []string{"127.0.0.1:" + strconv.Itoa(tlsPort)}
	e.r.Driver.LegacyHTTP2 = legacyHTTP2(t, e.ctl.bin)
	e.r.Driver.ACMERoot = filepath.Join(e.dir, "acme")

	challenge := filepath.Join(e.r.Driver.ACMERoot, ".well-known", "acme-challenge")
	if err := os.MkdirAll(challenge, 0o755); err != nil {
		t.Fatal(err)
	}

	if err := os.WriteFile(filepath.Join(challenge, "tok123"), []byte("tok123.thumb"), 0o644); err != nil {
		t.Fatal(err)
	}

	m := e.page(t, map[string]string{"index.html": "<h1>Coming soon</h1>", "css/a.css": "h1{}"})
	plain := Parked{Name: "_park-1", Domain: "parked.example.net", Page: m.Page, Digest: m.Digest, Dir: m.Dir}
	secure := Parked{Name: "_park-2", Domain: "secure.example.net", Page: m.Page, Digest: m.Digest, Dir: m.Dir}

	certDir := filepath.Join(e.dir, "certs")
	if err := os.MkdirAll(certDir, 0o700); err != nil {
		t.Fatal(err)
	}

	var pool *x509.CertPool

	secure.Cert, secure.Key, pool = selfSigned(t, certDir, "secure.example.net")
	site := e.site(t, "alpha", "hello alpha", "alpha.example.com")

	if _, err := e.r.Recover(ctx); err != nil {
		t.Fatal(err)
	}

	if _, err := e.r.Apply(ctx, Desired{Sites: []Site{site}, Parked: []Parked{plain, secure}}); err != nil {
		t.Fatal(err)
	}

	for _, p := range []string{"/", "/some/deep/path?x=1", "/index.php"} {
		code, h, body := e.fetchFull(t, "parked.example.net", p)
		if code != http.StatusOK || body != "<h1>Coming soon</h1>" || h.Get("Content-Security-Policy") != PageCSP || h.Get("Retry-After") != "" {
			t.Errorf("parked %s: %d %q %v", p, code, body, h)
		}
	}

	if code, h, body := e.fetchFull(t, "parked.example.net", "/.hostashi/page/maint/css/a.css"); code != http.StatusOK || body != "h1{}" || h.Get("Content-Type") != "text/css; charset=utf-8" {
		t.Errorf("page file: %d %q %v", code, body, h)
	}

	for _, host := range []string{"parked.example.net", "secure.example.net"} {
		if code, body := e.fetchPath(t, host, "/.well-known/acme-challenge/tok123"); code != http.StatusOK || body != "tok123.thumb" {
			t.Errorf("challenge on %s: %d %q", host, code, body)
		}
	}

	if code, _ := e.fetch(t, "secure.example.net"); code != http.StatusMovedPermanently {
		t.Errorf("http on the TLS parked domain: %d", code)
	}

	if code, body := e.fetch(t, "alpha.example.com"); code != http.StatusOK || body != "hello alpha" {
		t.Errorf("site: %d %q", code, body)
	}

	addr := "127.0.0.1:" + strconv.Itoa(tlsPort)
	client := &http.Client{Timeout: 3 * time.Second, Transport: &http.Transport{
		TLSClientConfig: &tls.Config{RootCAs: pool, MinVersion: tls.VersionTLS12},
		DialContext: func(ctx context.Context, network, _ string) (net.Conn, error) {
			var d net.Dialer

			return d.DialContext(ctx, network, addr)
		},
	}}

	resp, err := client.Get("https://secure.example.net/anything")
	if err != nil {
		t.Fatal(err)
	}

	body, _ := io.ReadAll(resp.Body)
	_ = resp.Body.Close()

	if resp.StatusCode != http.StatusOK || string(body) != "<h1>Coming soon</h1>" {
		t.Fatalf("https: %d %q", resp.StatusCode, body)
	}

	// A parked domain cannot also belong to a site.
	plain.Domain = "alpha.example.com"
	if _, err := e.r.Apply(ctx, Desired{Sites: []Site{site}, Parked: []Parked{plain}}); !errors.Is(err, ErrInvalidDesired) {
		t.Fatalf("duplicate domain: %v", err)
	}
}

func TestDBToolServer(t *testing.T) {
	e := newEnv(t)
	ctx := t.Context()

	toolPort := freePort(t)
	e.r.Driver.ListenTLS = []string{"127.0.0.1:" + strconv.Itoa(freePort(t))}
	e.r.Driver.PanelListen = []string{"127.0.0.1:" + strconv.Itoa(freePort(t))}
	e.r.Driver.PanelUpstream = filepath.Join(e.dir, "panel.sock")
	e.r.Driver.DBToolListen = []string{"127.0.0.1:" + strconv.Itoa(toolPort)}
	e.r.Driver.DBToolSocket = filepath.Join(e.dir, "tool.sock")
	e.r.Driver.DBToolRoot = filepath.Join(e.dir, "adminer")
	e.r.Driver.LegacyHTTP2 = legacyHTTP2(t, e.ctl.bin)

	if err := os.MkdirAll(e.r.Driver.DBToolRoot, 0o755); err != nil {
		t.Fatal(err)
	}

	// The tool's PHP-FPM: echoes what nginx passes on.
	ln, err := net.Listen("unix", e.r.Driver.DBToolSocket)
	if err != nil {
		t.Fatal(err)
	}

	if err := os.Chmod(e.r.Driver.DBToolSocket, 0o666); err != nil {
		t.Fatal(err)
	}

	go func() {
		_ = fcgi.Serve(ln, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			env := fcgi.ProcessEnv(r)
			_, _ = fmt.Fprintf(w, "script=%s uri=%s https=%t prefix=%q", env["SCRIPT_FILENAME"], r.URL.RequestURI(), r.TLS != nil, r.Header.Get("X-Forwarded-Prefix"))
		}))
	}()

	t.Cleanup(func() { _ = ln.Close() })

	if _, err := e.r.Recover(ctx); err != nil {
		t.Fatal(err)
	}

	certDir := filepath.Join(e.dir, "certs")
	if err := os.MkdirAll(certDir, 0o700); err != nil {
		t.Fatal(err)
	}

	panel := &Panel{Domain: "panel.example.com"}

	var pool *x509.CertPool

	panel.Cert, panel.Key, pool = selfSigned(t, certDir, "panel.example.com")

	addr := "127.0.0.1:" + strconv.Itoa(toolPort)
	client := &http.Client{Timeout: 3 * time.Second, Transport: &http.Transport{
		TLSClientConfig: &tls.Config{RootCAs: pool, MinVersion: tls.VersionTLS12},
		DialContext: func(ctx context.Context, network, _ string) (net.Conn, error) {
			var d net.Dialer

			return d.DialContext(ctx, network, addr)
		},
	}}
	// reply is what the tests read of a response; its body is closed.
	type reply struct {
		StatusCode int
		Header     http.Header
	}

	get := func(path string, hdr map[string]string) (reply, string, error) {
		req, _ := http.NewRequestWithContext(ctx, http.MethodGet, "https://panel.example.com:"+strconv.Itoa(toolPort)+path, nil)
		for k, v := range hdr {
			req.Header.Set(k, v)
		}

		resp, err := client.Do(req)
		if err != nil {
			return reply{}, "", err
		}

		body, _ := io.ReadAll(resp.Body)
		_ = resp.Body.Close()

		return reply{resp.StatusCode, resp.Header}, string(body), nil
	}

	// Without DBTool the port stays closed.
	if _, err := e.r.Apply(ctx, Desired{Panel: panel}); err != nil {
		t.Fatal(err)
	}

	if _, _, err := get("/", nil); err == nil {
		t.Fatal("tool served without DBTool")
	}

	panel.DBTool = true
	if _, err := e.r.Apply(ctx, Desired{Panel: panel}); err != nil {
		t.Fatal(err)
	}

	resp, body, err := get("/?ticket=abc", map[string]string{"X-Forwarded-Prefix": "/evil"})
	if err != nil {
		t.Fatal(err)
	}

	want := fmt.Sprintf(`script=%s/index.php uri=/?ticket=abc https=true prefix=""`, e.r.Driver.DBToolRoot)
	if resp.StatusCode != http.StatusOK || body != want {
		t.Fatalf("tool: %d %q", resp.StatusCode, body)
	}

	for _, h := range []string{"Strict-Transport-Security", "Referrer-Policy", "X-Frame-Options", "X-Content-Type-Options"} {
		if resp.Header.Get(h) == "" {
			t.Errorf("header %s missing", h)
		}
	}

	// Only "/" reaches PHP.
	for _, p := range []string{"/adminer.php", "/index.php", "/tool.php", "/x/"} {
		if resp, _, err := get(p, nil); err != nil || resp.StatusCode != http.StatusNotFound {
			t.Errorf("%s: %v %v", p, resp, err)
		}
	}

	// Another name on the tool port gets no certificate.
	if c, err := tls.Dial("tcp", addr, &tls.Config{ServerName: "other.example.com", InsecureSkipVerify: true}); err == nil {
		_ = c.Close()

		t.Fatal("handshake for another name on the tool port succeeded")
	}

	// The panel allowlist covers the tool too.
	panel.Allow = []string{"192.0.2.1"}
	if _, err := e.r.Apply(ctx, Desired{Panel: panel}); err != nil {
		t.Fatal(err)
	}

	if resp, _, err := get("/", nil); err != nil || resp.StatusCode != http.StatusForbidden {
		t.Errorf("outside the allowlist: %v %v", resp, err)
	}

	panel.Allow = []string{"127.0.0.0/8"}
	if _, err := e.r.Apply(ctx, Desired{Panel: panel}); err != nil {
		t.Fatal(err)
	}

	if resp, _, err := get("/", nil); err != nil || resp.StatusCode != http.StatusOK {
		t.Errorf("on the allowlist: %v %v", resp, err)
	}
}
