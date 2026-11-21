// SPDX-License-Identifier: AGPL-3.0-or-later

package web

import (
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestSiteLogDirectives(t *testing.T) {
	n := &Nginx{
		Ctl: &testCtl{}, Listen: []string{"80"}, ListenTLS: []string{"443"},
		ControlSocket: "/run/x/ctl.sock", PIDFile: "/run/nginx.pid", ErrorLog: "/var/log/nginx/error.log",
		MimeTypes: "/etc/nginx/mime.types", SiteLogDir: "/var/log/nginx/sites",
	}

	d := Desired{Sites: []Site{{User: "alpha", Domains: []string{"alpha.example.com"}, Root: "/home/alpha/htdocs", Cert: "/c.pem", Key: "/k.pem"}}}

	f, err := n.Render(d, 1)
	if err != nil {
		t.Fatal(err)
	}

	conf := string(f.Content["sites/alpha.conf"])

	// Both server blocks, port 80 and 443, write to the site's own files.
	for _, want := range []string{
		"access_log /var/log/nginx/sites/alpha/access.log;",
		"error_log /var/log/nginx/sites/alpha/error.log warn;",
	} {
		if c := strings.Count(conf, want); c != 2 {
			t.Errorf("%q appears %d times, want 2:\n%s", want, c, conf)
		}
	}

	n.SiteLogDir = ""

	f, err = n.Render(d, 1)
	if err != nil {
		t.Fatal(err)
	}

	if strings.Contains(string(f.Content["sites/alpha.conf"]), "/sites/alpha/") {
		t.Error("site logs without SiteLogDir")
	}

	n.SiteLogDir = "/var/log/x; include /etc/passwd"
	if _, err := n.Render(d, 1); err == nil {
		t.Error("SiteLogDir with nginx syntax accepted")
	}
}

func TestPrepareAndPruneLogs(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "sites")
	n := &Nginx{SiteLogDir: dir}

	if err := n.PrepareLogs(map[string]bool{"alpha": true, "beta": true}); err != nil {
		t.Fatal(err)
	}

	for _, p := range []string{dir, filepath.Join(dir, "alpha"), filepath.Join(dir, "beta")} {
		fi, err := os.Lstat(p)
		if err != nil || !fi.IsDir() || fi.Mode().Perm() != 0o750 {
			t.Fatalf("%s: %v %v", p, fi, err)
		}
	}

	// A wider mode is narrowed again.
	if err := os.Chmod(filepath.Join(dir, "alpha"), 0o777); err != nil {
		t.Fatal(err)
	}

	if err := n.PrepareLogs(map[string]bool{"alpha": true}); err != nil {
		t.Fatal(err)
	}

	if fi, _ := os.Lstat(filepath.Join(dir, "alpha")); fi.Mode().Perm() != 0o750 {
		t.Fatalf("mode %v", fi.Mode())
	}

	// Things that are not site log directories stay.
	outside := t.TempDir()
	if err := os.WriteFile(filepath.Join(outside, "keep"), []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}

	if err := os.Symlink(outside, filepath.Join(dir, "gamma")); err != nil {
		t.Fatal(err)
	}

	if err := os.WriteFile(filepath.Join(dir, "note.txt"), []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}

	if err := os.Mkdir(filepath.Join(dir, "Not_A_Site"), 0o750); err != nil {
		t.Fatal(err)
	}

	if err := os.WriteFile(filepath.Join(dir, "beta", "access.log"), []byte("line\n"), 0o640); err != nil {
		t.Fatal(err)
	}

	if err := n.PruneLogs(map[string]bool{"alpha": true}); err != nil {
		t.Fatal(err)
	}

	if _, err := os.Lstat(filepath.Join(dir, "beta")); !os.IsNotExist(err) {
		t.Errorf("logs of deleted site beta kept: %v", err)
	}

	for _, p := range []string{"alpha", "gamma", "note.txt", "Not_A_Site"} {
		if _, err := os.Lstat(filepath.Join(dir, p)); err != nil {
			t.Errorf("%s removed: %v", p, err)
		}
	}

	if _, err := os.Stat(filepath.Join(outside, "keep")); err != nil {
		t.Errorf("prune followed a symlink: %v", err)
	}

	// A symlink in place of a site's directory is refused, never followed.
	if err := n.PrepareLogs(map[string]bool{"gamma": true}); err == nil {
		t.Error("symlink accepted as log directory")
	}

	// Invalid names never become paths.
	if err := n.PrepareLogs(map[string]bool{"../etc": true}); err == nil {
		t.Error("invalid site name accepted")
	}

	// Without SiteLogDir nothing happens.
	if err := (&Nginx{}).PrepareLogs(map[string]bool{"alpha": true}); err != nil {
		t.Error(err)
	}

	if err := (&Nginx{SiteLogDir: filepath.Join(dir, "missing")}).PruneLogs(nil); err != nil {
		t.Error(err)
	}
}

func TestSiteLogsWithNginx(t *testing.T) {
	e := newEnv(t)
	ctx := t.Context()
	e.r.Driver.SiteLogDir = filepath.Join(e.dir, "logs")

	d := Desired{Sites: []Site{
		e.site(t, "alpha", "hello alpha", "alpha.example.com"),
		e.site(t, "beta", "hello beta", "beta.example.org"),
	}}

	if _, err := e.r.Apply(ctx, d); err != nil {
		t.Fatal(err)
	}

	if code, _ := e.fetchPath(t, "alpha.example.com", "/only-alpha"); code != http.StatusNotFound {
		t.Fatalf("status %d", code)
	}

	alphaLog := filepath.Join(e.dir, "logs", "alpha", "access.log")

	var b []byte

	for range 50 {
		b, _ = os.ReadFile(alphaLog)
		if strings.Contains(string(b), "/only-alpha") {
			break
		}

		time.Sleep(20 * time.Millisecond)
	}

	if !strings.Contains(string(b), "GET /only-alpha") {
		t.Fatalf("access log of alpha: %q", b)
	}

	if b, _ := os.ReadFile(filepath.Join(e.dir, "logs", "beta", "access.log")); strings.Contains(string(b), "/only-alpha") {
		t.Fatalf("alpha's request in beta's log: %q", b)
	}

	if _, err := os.Stat(filepath.Join(e.dir, "logs", "alpha", "error.log")); err != nil {
		t.Fatalf("error log: %v", err)
	}

	// Deleting a site removes its logs once nginx no longer writes them.
	if _, err := e.r.Apply(ctx, Desired{Sites: d.Sites[1:]}); err != nil {
		t.Fatal(err)
	}

	if _, err := os.Lstat(filepath.Join(e.dir, "logs", "alpha")); !os.IsNotExist(err) {
		t.Fatalf("logs of deleted site kept: %v", err)
	}

	// A missing directory (wiped /var/log) is created again before a restore.
	if err := os.RemoveAll(filepath.Join(e.dir, "logs")); err != nil {
		t.Fatal(err)
	}

	if _, err := e.r.Recover(ctx); err != nil {
		t.Fatal(err)
	}

	if _, err := os.Stat(filepath.Join(e.dir, "logs", "beta")); err != nil {
		t.Fatalf("recover did not recreate the log directory: %v", err)
	}
}
