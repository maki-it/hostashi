// SPDX-License-Identifier: AGPL-3.0-or-later

package scan_test

import (
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"syscall"
	"testing"
	"time"

	"golang.org/x/crypto/ssh"

	"github.com/maki-it/hostashi/internal/cloudpanel/fixture"
	"github.com/maki-it/hostashi/internal/cloudpanel/scan"
)

var now = time.Date(2026, 11, 18, 12, 0, 0, 0, time.UTC)

func build(t *testing.T) (string, []fixture.Case) {
	t.Helper()
	dir := filepath.Join(t.TempDir(), "srv")

	cases, err := fixture.Build(dir, now)
	if err != nil {
		t.Fatal(err)
	}

	return dir, cases
}

func run(t *testing.T, dir string) *scan.Report {
	t.Helper()

	rep, err := scan.Run(scan.Options{Root: dir, RequireRootOwned: os.Geteuid() == 0, Now: now})
	if err != nil {
		t.Fatal(err)
	}

	return rep
}

func byVhost(rep *scan.Report) map[string]scan.Site {
	m := map[string]scan.Site{}
	for _, s := range rep.Sites {
		m[filepath.Base(s.Sources[0])] = s
	}

	return m
}

func hasCode(s scan.Site, code string) bool {
	for _, f := range s.Findings {
		if f.Code == code {
			return true
		}
	}

	return false
}

func TestFixtureCorpus(t *testing.T) {
	dir, cases := build(t)
	rep := run(t, dir)

	got := byVhost(rep)
	if len(cases) != 23 || rep.Summary.Sites != 23 {
		t.Fatalf("cases %d, sites %d, want 23", len(cases), rep.Summary.Sites)
	}

	for _, c := range cases {
		s, ok := got[c.Vhost]
		if !ok {
			t.Errorf("%s: no site in report", c.Vhost)
			continue
		}

		if string(s.Status) != c.WantStatus {
			t.Errorf("%s (%s): status %s, want %s; findings %+v", c.Domain, c.Note, s.Status, c.WantStatus, s.Findings)
		}

		if c.WantType != "" && s.Type != c.WantType {
			t.Errorf("%s: type %q, want %q", c.Domain, s.Type, c.WantType)
		}
	}

	t.Logf("summary: %+v", rep.Summary)
}

func TestMappedDetails(t *testing.T) {
	dir, _ := build(t)
	rep := run(t, dir)
	got := byVhost(rep)

	if !slices.ContainsFunc(rep.Global, func(f scan.Finding) bool { return f.Code == "redis_detected" }) {
		t.Errorf("shared Redis not noted: %+v", rep.Global)
	}

	a := got["alpha-shop.de.conf"]
	if a.User != "alpha" || a.DocRoot != "/home/alpha/htdocs/alpha-shop.de" {
		t.Errorf("alpha user/root: %q %q", a.User, a.DocRoot)
	}

	if len(a.Domains) != 1 || a.Domains[0] != "alpha-shop.de" || len(a.RedirectFrom) != 1 || a.RedirectFrom[0] != "www.alpha-shop.de" {
		t.Errorf("alpha domains %v redirect_from %v", a.Domains, a.RedirectFrom)
	}

	if a.Pool == nil || a.Pool.Version != "8.1" || a.Pool.User != "alpha" || a.Pool.MaxChldren != 10 || a.Pool.Settings["php_admin_value[memory_limit]"] != "256M" {
		t.Errorf("alpha pool %+v", a.Pool)
	}

	if !hasCode(a, "fpm_tcp_migrate") || !hasCode(a, "https_redirect") || !hasCode(a, "tls_reissue") {
		t.Errorf("alpha expected info findings, got %+v", a.Findings)
	}

	if a.TLS.Kind != "letsencrypt" || len(a.Cron) != 1 || a.Cron[0].Schedule != "*/5 * * * *" || a.ClientMaxBody != "64m" {
		t.Errorf("alpha tls %+v cron %+v", a.TLS, a.Cron)
	}

	if !strings.HasPrefix(a.Cron[0].Command, "php /home/alpha/") || !strings.Contains(a.Cron[0].Command, "> /dev/null 2>&1") {
		t.Errorf("cron command not kept verbatim: %q", a.Cron[0].Command)
	}

	if s := got["ups.ilon-test.de.conf"]; s.TLS.Kind != "selfsigned" {
		t.Errorf("self-signed detection: %+v", s.TLS)
	}

	if s := got["lambda-kunde.de.conf"]; s.TLS.Kind != "custom" || !hasCode(s, "tls_custom") {
		t.Errorf("custom cert: %+v", s.TLS)
	}

	if s := got["tau-reisen.de.conf"]; len(s.Custom) != 1 || !strings.HasPrefix(s.Custom[0].Raw, "rewrite ") || s.Custom[0].Source != "/etc/nginx/snippets/tau-rewrites.conf" {
		t.Errorf("include not inlined: %+v", s.Custom)
	}

	if s := got["nu-magazin.de.conf"]; len(s.Custom) != 3 {
		t.Errorf("nu custom: want 2 add_header + 1 location, got %+v", s.Custom)
	}

	if s := got["zeta-handwerk.de.conf"]; s.Pool == nil || s.Pool.Version != "8.4" || !hasCode(s, "vhost_two_tier") || s.DocRoot != "/home/zeta/htdocs/zeta-handwerk.de" {
		t.Errorf("two-tier: %+v", s)
	}

	if s := got["theta-app.de.conf"]; !hasCode(s, "vhost_cache_tier") || s.Type != "wordpress" {
		t.Errorf("cache tier: %+v", s.Findings)
	}

	if s := got["theta-app.de.conf"]; !s.ObjectCache || !hasCode(s, "object_cache") {
		t.Errorf("object cache drop-in not found: %+v", s.Findings)
	}

	if s := got["alpha-shop.de.conf"]; s.ObjectCache {
		t.Errorf("object cache on a site without a drop-in")
	}

	if s := got["omikron-api.de.conf"]; s.Type != "python" || s.Project != "requirements.txt" || !hasCode(s, "app_process") {
		t.Errorf("python site: %+v", s)
	}

	if s := got["kappa-portal.de.conf"]; s.Upstream != "https://10.0.0.5:8080" {
		t.Errorf("proxy upstream %q", s.Upstream)
	}
}

func TestSecretsDoNotLeak(t *testing.T) {
	dir, _ := build(t)

	b, err := json.Marshal(run(t, dir))
	if err != nil {
		t.Fatal(err)
	}

	for _, bad := range []string{"s3cr3t-synthetic-value", "PRIVATE KEY", "<?php"} {
		if strings.Contains(string(b), bad) {
			t.Errorf("report contains %q", bad)
		}
	}

	if !strings.Contains(string(b), "DB_PASSWORD") {
		t.Error("env key names should be listed")
	}
}

func treeHash(t *testing.T, dir string) string {
	t.Helper()

	var lines []string

	err := filepath.WalkDir(dir, func(p string, _ fs.DirEntry, err error) error {
		if err != nil {
			return err
		}

		st, err := os.Lstat(p)
		if err != nil {
			return err
		}

		line := fmt.Sprintf("%s %v %d %d", p, st.Mode(), st.Size(), st.ModTime().UnixNano())
		if st.Mode().IsRegular() {
			b, _ := os.ReadFile(p)
			line += fmt.Sprintf(" %x", sha256.Sum256(b))
		}

		lines = append(lines, line)

		return nil
	})
	if err != nil {
		t.Fatal(err)
	}

	sort.Strings(lines)
	h := sha256.Sum256([]byte(strings.Join(lines, "\n")))

	return hex.EncodeToString(h[:])
}

func TestScanIsReadOnly(t *testing.T) {
	dir, _ := build(t)
	before := treeHash(t, dir)
	run(t, dir)

	if after := treeHash(t, dir); after != before {
		t.Fatal("scan changed the tree")
	}
}

func TestScanIsDeterministic(t *testing.T) {
	dir, _ := build(t)
	a, _ := json.Marshal(run(t, dir))

	b, _ := json.Marshal(run(t, dir))
	if string(a) != string(b) {
		t.Fatal("two scans of the same tree differ")
	}
}

func writeVhost(t *testing.T, dir, name, content string, mode os.FileMode) string {
	t.Helper()

	p := filepath.Join(dir, "etc/nginx/sites-enabled", name)
	if err := os.WriteFile(p, []byte(content), mode); err != nil {
		t.Fatal(err)
	}

	if err := os.Chmod(p, mode); err != nil {
		t.Fatal(err)
	}

	return p
}

func status(t *testing.T, dir, vhost string) (scan.Site, bool) {
	t.Helper()
	s, ok := byVhost(run(t, dir))[vhost]

	return s, ok
}

func TestHostileTrees(t *testing.T) {
	t.Run("symlink out of the root", func(t *testing.T) {
		dir, _ := build(t)
		outside := filepath.Join(filepath.Dir(dir), "outside.conf")
		must(t, os.WriteFile(outside, []byte("server { listen 80; server_name evil.de; root /home/alpha/htdocs/alpha-shop.de; }"), 0o644))

		for i, target := range []string{outside, "../../../../outside.conf"} {
			name := fmt.Sprintf("evil%d.conf", i)
			if err := os.Symlink(target, filepath.Join(dir, "etc/nginx/sites-enabled", name)); err != nil {
				t.Fatal(err)
			}

			s, ok := status(t, dir, name)
			if !ok || s.Status != scan.StatusBlocked || !hasCode(s, "vhost_unreadable") {
				t.Errorf("%s: want blocked/vhost_unreadable, got %+v", target, s)
			}
		}
	})
	t.Run("relative symlink inside the root is followed", func(t *testing.T) {
		dir, _ := build(t)
		en := filepath.Join(dir, "etc/nginx/sites-enabled")
		av := filepath.Join(dir, "etc/nginx/sites-available")
		must(t, os.MkdirAll(av, 0o755))

		if err := os.Rename(filepath.Join(en, "iota-landing.de.conf"), filepath.Join(av, "iota-landing.de.conf")); err != nil {
			t.Fatal(err)
		}

		must(t, os.Symlink("../sites-available/iota-landing.de.conf", filepath.Join(en, "iota-landing.de.conf")))

		if s, _ := status(t, dir, "iota-landing.de.conf"); s.Status != scan.StatusClean {
			t.Errorf("want clean, got %+v", s)
		}
	})
	t.Run("absolute symlink resolves inside the root", func(t *testing.T) {
		dir, _ := build(t)
		en := filepath.Join(dir, "etc/nginx/sites-enabled")
		av := filepath.Join(dir, "etc/nginx/sites-available")
		must(t, os.MkdirAll(av, 0o755))
		must(t, os.Rename(filepath.Join(en, "iota-landing.de.conf"), filepath.Join(av, "iota-landing.de.conf")))
		// Absolute target as on a live Debian system; must resolve to
		// <root>/etc/nginx/sites-available/..., never to the host's /etc.
		must(t, os.Symlink("/etc/nginx/sites-available/iota-landing.de.conf", filepath.Join(en, "iota-landing.de.conf")))

		if s, _ := status(t, dir, "iota-landing.de.conf"); s.Status != scan.StatusClean {
			t.Errorf("want clean, got %+v", s)
		}
		// A symlinked home directory with an absolute target also stays inside.
		home := filepath.Join(dir, "home/iota")
		must(t, os.Rename(home, filepath.Join(dir, "home/iota-real")))
		must(t, os.Symlink("/home/iota-real", home))

		if s, _ := status(t, dir, "iota-landing.de.conf"); s.Status != scan.StatusBlocked || !hasCode(s, "home_missing") {
			t.Errorf("symlinked home must be refused, got %+v", s.Findings)
		}
	})
	t.Run("FIFO and directory in sites-enabled", func(t *testing.T) {
		dir, _ := build(t)

		en := filepath.Join(dir, "etc/nginx/sites-enabled")
		if err := syscallMkfifo(filepath.Join(en, "fifo.conf")); err != nil {
			t.Fatal(err)
		}

		must(t, os.Mkdir(filepath.Join(en, "dir.conf"), 0o755))

		done := make(chan *scan.Report, 1)
		go func() { done <- run(t, dir) }()

		select {
		case rep := <-done:
			got := byVhost(rep)
			for _, n := range []string{"fifo.conf", "dir.conf"} {
				if got[n].Status != scan.StatusBlocked {
					t.Errorf("%s: want blocked, got %+v", n, got[n])
				}
			}
		case <-time.After(5 * time.Second):
			t.Fatal("scan hangs on a FIFO")
		}
	})
	t.Run("private key mode and its directory", func(t *testing.T) {
		dir, _ := build(t)
		key := filepath.Join(dir, "etc/nginx/ssl-certificates/alpha-shop.de.key")
		must(t, os.Chmod(key, 0o644))
		must(t, os.Chmod(filepath.Dir(key), 0o755))

		if s, _ := status(t, dir, "alpha-shop.de.conf"); !hasCode(s, "tls_key_world_readable") {
			t.Errorf("open dir: %+v", s.Findings)
		}

		must(t, os.Chmod(filepath.Dir(key), 0o700))

		if s, _ := status(t, dir, "alpha-shop.de.conf"); !hasCode(s, "tls_key_mode") || s.Status != scan.StatusClean {
			t.Errorf("closed dir: %+v", s.Findings)
		}
	})
	t.Run("world-writable vhost", func(t *testing.T) {
		if os.Geteuid() != 0 {
			t.Skip("ownership checks need root")
		}

		dir, _ := build(t)
		p := filepath.Join(dir, "etc/nginx/sites-enabled/iota-landing.de.conf")
		must(t, os.Chmod(p, 0o666))

		if s, _ := status(t, dir, "iota-landing.de.conf"); s.Status != scan.StatusBlocked {
			t.Errorf("want blocked, got %+v", s)
		}
	})
	t.Run("vhost owned by a site user", func(t *testing.T) {
		if os.Geteuid() != 0 {
			t.Skip("ownership checks need root")
		}

		dir, _ := build(t)
		must(t, os.Lchown(filepath.Join(dir, "etc/nginx/sites-enabled/iota-landing.de.conf"), 1009, 1009))

		if s, _ := status(t, dir, "iota-landing.de.conf"); s.Status != scan.StatusBlocked {
			t.Errorf("want blocked, got %+v", s)
		}
	})
	t.Run("crontab owned by another user", func(t *testing.T) {
		if os.Geteuid() != 0 {
			t.Skip("ownership checks need root")
		}

		dir, _ := build(t)
		must(t, os.Lchown(filepath.Join(dir, "var/spool/cron/crontabs/alpha"), 0, 0))

		s, _ := status(t, dir, "alpha-shop.de.conf")
		if !hasCode(s, "cron_unreadable") || len(s.Cron) != 0 {
			t.Errorf("want cron_unreadable, got %+v", s.Findings)
		}
	})
	t.Run("authorized_keys of the site user", func(t *testing.T) {
		if os.Geteuid() != 0 {
			t.Skip("ownership checks need root")
		}

		dir, _ := build(t)
		home := filepath.Join(dir, "home/alpha")

		st, err := os.Stat(home)
		must(t, err)

		sys, ok := st.Sys().(*syscall.Stat_t)
		if !ok {
			t.Fatal("no stat_t")
		}

		uid := int(sys.Uid)

		must(t, os.Mkdir(filepath.Join(home, ".ssh"), 0o700))
		must(t, os.Lchown(filepath.Join(home, ".ssh"), uid, uid))

		pub, _, err := ed25519.GenerateKey(rand.Reader)
		must(t, err)

		pk, err := ssh.NewPublicKey(pub)
		must(t, err)

		line := strings.TrimSuffix(string(ssh.MarshalAuthorizedKey(pk)), "\n")
		keys := filepath.Join(home, ".ssh/authorized_keys")
		must(t, os.WriteFile(keys, []byte("no-pty "+line+" kim@laptop\n"), 0o600))
		must(t, os.Lchown(keys, uid, uid))

		s, _ := status(t, dir, "alpha-shop.de.conf")
		if len(s.SSHKeys) != 1 || s.SSHKeys[0].Public != line || s.SSHKeys[0].Comment != "kim@laptop" || !hasCode(s, "ssh_key_options") {
			t.Fatalf("keys %+v findings %+v", s.SSHKeys, s.Findings)
		}

		// A key file of another owner, or a symlink, is not read.
		must(t, os.Lchown(keys, 0, 0))

		if s, _ := status(t, dir, "alpha-shop.de.conf"); len(s.SSHKeys) != 0 || !hasCode(s, "ssh_keys_unreadable") {
			t.Fatalf("root-owned: %+v %+v", s.SSHKeys, s.Findings)
		}

		must(t, os.Remove(keys))
		must(t, os.Symlink("/etc/passwd", keys))

		if s, _ := status(t, dir, "alpha-shop.de.conf"); len(s.SSHKeys) != 0 || !hasCode(s, "ssh_keys_unreadable") {
			t.Fatalf("symlink: %+v %+v", s.SSHKeys, s.Findings)
		}
	})
	t.Run("additional SSH users of a site", func(t *testing.T) {
		if os.Geteuid() != 0 {
			t.Skip("ownership checks need root")
		}

		dir, _ := build(t)

		st, err := os.Stat(filepath.Join(dir, "home/alpha"))
		must(t, err)

		sys, ok := st.Sys().(*syscall.Stat_t)
		if !ok {
			t.Fatal("no stat_t")
		}

		gid := int(sys.Gid)
		dev, ftp := 1950, 1951

		pw := filepath.Join(dir, "etc/passwd")
		b, err := os.ReadFile(pw)
		must(t, err)

		b = append(b, fmt.Sprintf("alpha-dev:x:%d:%d::/home/alpha-dev:/bin/bash\nalpha-ftp:x:%d:%d::/home/alpha/htdocs/alpha-shop.de/:/bin/false\n", dev, gid, ftp, gid)...)
		must(t, os.WriteFile(pw, b, 0o644))

		sshDir := filepath.Join(dir, "home/alpha-dev/.ssh")
		must(t, os.MkdirAll(sshDir, 0o700))
		must(t, os.Lchown(filepath.Dir(sshDir), dev, gid))
		must(t, os.Lchown(sshDir, dev, gid))

		pub, _, err := ed25519.GenerateKey(rand.Reader)
		must(t, err)

		pk, err := ssh.NewPublicKey(pub)
		must(t, err)

		line := strings.TrimSuffix(string(ssh.MarshalAuthorizedKey(pk)), "\n")
		keys := filepath.Join(sshDir, "authorized_keys")
		must(t, os.WriteFile(keys, []byte(line+" rafael@pc\n"), 0o600))
		must(t, os.Lchown(keys, dev, gid))

		rep := run(t, dir)
		s := byVhost(rep)["alpha-shop.de.conf"]

		if len(s.SSHKeys) != 1 || s.SSHKeys[0].Public != line || s.SSHKeys[0].User != "alpha-dev" ||
			len(s.SSHUsers) != 2 || s.SSHUsers[0] != "alpha-dev" || s.SSHUsers[1] != "alpha-ftp" || !hasCode(s, "ssh_user_no_keys") {
			t.Fatalf("keys %+v users %v findings %+v", s.SSHKeys, s.SSHUsers, s.Findings)
		}

		for _, o := range rep.Orphans {
			if strings.Contains(o.Msg, "alpha-dev") || strings.Contains(o.Msg, "alpha-ftp") {
				t.Errorf("additional user reported as orphan: %+v", o)
			}
		}

		// A key file the additional user does not own is not read.
		must(t, os.Lchown(keys, 0, 0))

		if s, _ := status(t, dir, "alpha-shop.de.conf"); len(s.SSHKeys) != 0 || !hasCode(s, "ssh_keys_unreadable") {
			t.Fatalf("root-owned: %+v %+v", s.SSHKeys, s.Findings)
		}
	})
	t.Run("oversized and deeply nested vhosts", func(t *testing.T) {
		dir, _ := build(t)
		writeVhost(t, dir, "big.conf", strings.Repeat("#", 2<<20), 0o644)
		writeVhost(t, dir, "deep.conf", strings.Repeat("a {", 100)+strings.Repeat("}", 100), 0o644)

		got := byVhost(run(t, dir))
		for _, n := range []string{"big.conf", "deep.conf"} {
			if got[n].Status != scan.StatusBlocked {
				t.Errorf("%s: want blocked, got %+v", n, got[n])
			}
		}
	})
	t.Run("include outside /etc/nginx and missing include", func(t *testing.T) {
		dir, _ := build(t)
		writeVhost(t, dir, "inc.conf", "server { listen 80; server_name inc-a.de; root /home/alpha/htdocs/alpha-shop.de; include /etc/passwd; }\n", 0o644)
		writeVhost(t, dir, "inc2.conf", "server { listen 80; server_name inc-b.de; root /home/alpha/htdocs/alpha-shop.de; include snippets/missing.conf; }\n", 0o644)

		got := byVhost(run(t, dir))
		if !hasCode(got["inc.conf"], "include_outside") || got["inc.conf"].Status != scan.StatusBlocked {
			t.Errorf("inc: %+v", got["inc.conf"])
		}

		if !hasCode(got["inc2.conf"], "include_unreadable") {
			t.Errorf("inc2: %+v", got["inc2.conf"])
		}
	})
	t.Run("document root tricks", func(t *testing.T) {
		dir, _ := build(t)
		for name, root := range map[string]string{
			"r1.conf": "/home/alpha/../beta/htdocs/beta-praxis.de",
			"r2.conf": "/home/$user/htdocs",
			"r3.conf": "/home",
			"r4.conf": "/home/root/htdocs",
			"r5.conf": "home/alpha/htdocs",
		} {
			writeVhost(t, dir, name, "server { listen 80; server_name "+strings.TrimSuffix(name, ".conf")+"-x.de; root "+root+"; }\n", 0o644)
		}

		got := byVhost(run(t, dir))
		for _, n := range []string{"r1.conf", "r2.conf", "r3.conf", "r4.conf", "r5.conf"} {
			if got[n].Status != scan.StatusBlocked {
				t.Errorf("%s: want blocked, got %+v", n, got[n].Findings)
			}
		}
	})
	t.Run("domain names", func(t *testing.T) {
		dir, _ := build(t)
		writeVhost(t, dir, "d1.conf", "server { listen 80; server_name Upper-Case.DE *.wild.de; root /home/alpha/htdocs/alpha-shop.de; }\n", 0o644)
		writeVhost(t, dir, "d2.conf", "server { listen 80; server_name münchen.de; root /home/alpha/htdocs/alpha-shop.de; }\n", 0o644)
		writeVhost(t, dir, "d3.conf", "server { listen 80; server_name 1.2.3.4; root /home/alpha/htdocs/alpha-shop.de; }\n", 0o644)

		got := byVhost(run(t, dir))
		if d := got["d1.conf"]; d.Name != "upper-case.de" || !hasCode(d, "domain_pattern") || d.Status != scan.StatusReview {
			t.Errorf("d1: %+v", d)
		}

		if d := got["d2.conf"]; !hasCode(d, "domain_not_punycode") || d.Status != scan.StatusBlocked {
			t.Errorf("d2: %+v", d)
		}

		if d := got["d3.conf"]; d.Status != scan.StatusBlocked {
			t.Errorf("d3: %+v", d)
		}
	})
	t.Run("two different apps in one file", func(t *testing.T) {
		dir, _ := build(t)
		writeVhost(t, dir, "multi.conf", "server { listen 443 ssl; server_name m1.de; root /home/alpha/htdocs/a; ssl_certificate /x; ssl_certificate_key /y; }\nserver { listen 80; server_name m2.de; root /home/beta/htdocs/b; }\n", 0o644)

		if s, _ := status(t, dir, "multi.conf"); !hasCode(s, "vhost_multi_app") {
			t.Errorf("%+v", s)
		}
	})
}

func syscallMkfifo(p string) error { return syscall.Mkfifo(p, 0o644) }

func must(t *testing.T, err error) {
	t.Helper()

	if err != nil {
		t.Fatal(err)
	}
}

func TestRedact(t *testing.T) {
	rep := &scan.Report{Sites: []scan.Site{{
		Custom: []scan.Custom{{Raw: "proxy_set_header Authorization \"Bearer secret\";"}},
		Cron:   []scan.CronJob{{Schedule: "* * * * *", Command: "curl -H 'X-Token: secret' https://example.com"}},
	}}}
	rep.Redact()

	b, err := json.Marshal(rep)
	if err != nil {
		t.Fatal(err)
	}

	if strings.Contains(string(b), "secret") {
		t.Fatalf("secret left in %s", b)
	}

	if rep.Sites[0].Cron[0].Schedule != "* * * * *" {
		t.Fatal("schedule must stay")
	}
}

func TestSiteUsage(t *testing.T) {
	dir, _ := build(t)

	measured := 0

	for _, s := range run(t, dir).Sites {
		if s.Usage != nil {
			measured++

			if s.Usage.Inodes < 1 || s.Usage.Bytes < 0 {
				t.Errorf("%s: usage %+v", s.Name, s.Usage)
			}
		}
	}

	if measured == 0 {
		t.Fatal("no site has a usage")
	}

	rep, err := scan.Run(scan.Options{Root: dir, RequireRootOwned: os.Geteuid() == 0, Now: now, SkipUsage: true})
	if err != nil {
		t.Fatal(err)
	}

	for _, s := range rep.Sites {
		if s.Usage != nil {
			t.Fatalf("%s measured with SkipUsage", s.Name)
		}
	}
}
