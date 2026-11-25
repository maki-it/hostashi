// SPDX-License-Identifier: AGPL-3.0-or-later

package resticctl

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/maki-it/hostashi/internal/dbserver"
)

func validRepo() *Repo {
	return &Repo{
		Repository:   "sftp://u123456-sub1@u123456.your-storagebox.de:23/hostashi",
		PasswordFile: "/etc/hostashi-agent/backup/password",
		CacheDir:     "/var/cache/hostashi-backup",
		SSHKey:       "/etc/hostashi-agent/backup/id_ed25519",
		KnownHosts:   "/etc/hostashi-agent/backup/known_hosts",
		Host:         "web3.example.com",
	}
}

func TestValidate(t *testing.T) {
	if err := validRepo().validate(); err != nil {
		t.Fatalf("valid repo refused: %v", err)
	}

	bad := map[string]func(r *Repo){
		"shell in repository":  func(r *Repo) { r.Repository = "sftp://u1@host:23/x;rm -rf /" },
		"dotdot in repository": func(r *Repo) { r.Repository = "sftp://u1@host:23/../etc" },
		"triple slash path":    func(r *Repo) { r.Repository = "sftp://u1@host:23///etc" },
		"unbracketed ipv6":     func(r *Repo) { r.Repository = "sftp://u1@::1:22/x" },
		"user with colon":      func(r *Repo) { r.Repository = "sftp://u:p@host:23/x" },
		"port zero":            func(r *Repo) { r.Repository = "sftp://u1@host:0/x" },
		"port too large":       func(r *Repo) { r.Repository = "sftp://u1@host:70000/x" },
		"option as user":       func(r *Repo) { r.Repository = "sftp://-oProxyCommand=x@host:23/x" },
		"other backend":        func(r *Repo) { r.Repository = "s3:https://example.com/bucket" },
		"rclone":               func(r *Repo) { r.Repository = "rclone:remote:path" },
		"relative local":       func(r *Repo) { r.Repository = "repo" },
		"local dotdot":         func(r *Repo) { r.Repository = "/srv/../etc" },
		"sftp without key":     func(r *Repo) { r.SSHKey = "" },
		"key with space":       func(r *Repo) { r.SSHKey = "/etc/a b" },
		"known hosts relative": func(r *Repo) { r.KnownHosts = "known_hosts" },
		"relative password":    func(r *Repo) { r.PasswordFile = "password" },
		"cache with space":     func(r *Repo) { r.CacheDir = "/var/cache/a b" },
		"host with option":     func(r *Repo) { r.Host = "-x" },
		"host uppercase":       func(r *Repo) { r.Host = "Web" },
		"relative binary":      func(r *Repo) { r.Binary = "restic" },
	}

	for name, mutate := range bad {
		r := validRepo()
		mutate(r)

		if err := r.validate(); !errors.Is(err, ErrInvalid) {
			t.Errorf("%s: got %v, want ErrInvalid", name, err)
		}
	}

	for _, repo := range []string{
		"sftp://backup_web3@nas.example.lan:22//srv/backups/web3",
		"sftp://first.last@192.0.2.10:2222/hostashi",
		"sftp://u1@[2001:db8::10]:22//srv/restic",
	} {
		r := validRepo()
		r.Repository = repo

		if err := r.validate(); err != nil {
			t.Errorf("%s refused: %v", repo, err)
		}
	}

	local := validRepo()
	local.Repository = "/srv/backup/repo"
	local.SSHKey, local.KnownHosts = "", ""

	if err := local.validate(); err != nil {
		t.Errorf("local repo without ssh files refused: %v", err)
	}
}

func TestArgumentChecks(t *testing.T) {
	ctx := context.Background()
	r := validRepo()
	r.Binary = "/nonexistent/restic" // must never be reached

	cases := map[string]error{}

	_, cases["no tags"] = r.Backup(ctx, nil, "/home/blog")
	_, cases["bad tag"] = r.Backup(ctx, []string{"site:Blog"}, "/home/blog")
	_, cases["tag with comma"] = r.Backup(ctx, []string{"site:a,kind:b"}, "/home/blog")
	_, cases["no path"] = r.Backup(ctx, []string{"site:blog"})
	_, cases["relative path"] = r.Backup(ctx, []string{"site:blog"}, "home/blog")
	_, cases["option path"] = r.Backup(ctx, []string{"site:blog"}, "--files-from=/etc/shadow")
	_, cases["unclean path"] = r.Backup(ctx, []string{"site:blog"}, "/home//blog")
	_, cases["bad database"] = r.BackupDatabase(ctx, []string{"site:blog"}, "shop; DROP")
	_, cases["reserved database"] = r.BackupDatabase(ctx, []string{"site:blog"}, "mysql")
	_, cases["option database"] = r.BackupDatabase(ctx, []string{"site:blog"}, "--all-databases")
	_, cases["snapshots bad tag"] = r.Snapshots(ctx, []string{"x"})
	cases["keep no daily"] = r.Forget(ctx, Keep{Daily: 0, Weekly: 4, Monthly: 6})
	cases["keep negative"] = r.Forget(ctx, Keep{Daily: 7, Weekly: -1})
	cases["check part zero"] = r.Check(ctx, 0, 12)
	cases["check part too big"] = r.Check(ctx, 13, 12)
	cases["check negative"] = r.Check(ctx, 1, -1)

	for name, err := range cases {
		if !errors.Is(err, ErrInvalid) {
			t.Errorf("%s: got %v, want ErrInvalid", name, err)
		}
	}
}

func TestParseSummary(t *testing.T) {
	out := []byte(`{"message_type":"error","error":{"message":"open /x: permission denied"},"during":"archival","item":"/x"}
{"message_type":"summary","files_new":2,"data_added":1397,"total_files_processed":3,"snapshot_id":"54cba39114cdcd35516ed1ec5b6b26843cbe641de448c682f13ae2b695ad1d10"}
`)

	s, err := parseSummary(out)
	if err != nil || s.FilesNew != 2 || s.DataAdded != 1397 || s.TotalFiles != 3 {
		t.Fatalf("got %+v, %v", s, err)
	}

	if _, err := parseSummary([]byte(`{"message_type":"summary","snapshot_id":"../x"}`)); err == nil {
		t.Error("summary with a bad snapshot id accepted")
	}

	if _, err := parseSummary(nil); err == nil {
		t.Error("empty output accepted")
	}
}

func TestExitMessage(t *testing.T) {
	got := exitMessage([]byte(`{"message_type":"exit_error","code":12,"message":"Fatal: wrong password or no key found"}`), nil)
	if got != "Fatal: wrong password or no key found" {
		t.Errorf("got %q", got)
	}

	if got := exitMessage(nil, []byte("a\nlast line\n")); got != "last line" {
		t.Errorf("got %q", got)
	}
}

// TestRestic runs the real restic against a local repository (and an SFTP
// repository and MariaDB when configured). It needs HOSTASHI_TEST_RESTIC=1
// and restic at /usr/bin/restic; CI runs it in a Debian 13 container.
func TestRestic(t *testing.T) {
	if os.Getenv("HOSTASHI_TEST_RESTIC") != "1" {
		t.Skip("HOSTASHI_TEST_RESTIC not set")
	}

	ctx := context.Background()
	dir := t.TempDir()

	pw := filepath.Join(dir, "password")
	if err := os.WriteFile(pw, []byte("test-password-0123456789\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	repos := map[string]*Repo{
		"local": {Repository: filepath.Join(dir, "repo"), PasswordFile: pw, CacheDir: filepath.Join(dir, "cache"), Host: "test-host"},
	}

	if url := os.Getenv("HOSTASHI_TEST_RESTIC_SFTP"); url != "" {
		repos["sftp"] = &Repo{
			Repository: url, PasswordFile: pw, CacheDir: filepath.Join(dir, "cache-sftp"), Host: "test-host",
			SSHKey: os.Getenv("HOSTASHI_TEST_RESTIC_SSH_KEY"), KnownHosts: os.Getenv("HOSTASHI_TEST_RESTIC_KNOWN_HOSTS"),
		}
	}

	src := filepath.Join(dir, "site")
	if err := os.MkdirAll(filepath.Join(src, "htdocs"), 0o755); err != nil {
		t.Fatal(err)
	}

	if err := os.WriteFile(filepath.Join(src, "htdocs", "index.html"), []byte("hello"), 0o644); err != nil {
		t.Fatal(err)
	}

	for name, r := range repos {
		t.Run(name, func(t *testing.T) {
			if _, err := r.Snapshots(ctx, []string{"site:blog"}); !errors.Is(err, ErrNoRepository) {
				t.Fatalf("snapshots before init: got %v, want ErrNoRepository", err)
			}

			if err := r.Init(ctx); err != nil {
				t.Fatalf("init: %v", err)
			}

			if err := r.Init(ctx); !errors.Is(err, ErrExists) {
				t.Fatalf("second init: got %v, want ErrExists", err)
			}

			sum, err := r.Backup(ctx, []string{"site:blog", "kind:files"}, src)
			if err != nil || !ValidSnapshotID(sum.SnapshotID) || sum.FilesNew != 1 {
				t.Fatalf("backup: %+v, %v", sum, err)
			}

			snaps, err := r.Snapshots(ctx, []string{"site:blog", "kind:files"})
			if err != nil || len(snaps) != 1 || snaps[0].ID != sum.SnapshotID || snaps[0].Hostname != "test-host" {
				t.Fatalf("snapshots: %+v, %v", snaps, err)
			}

			if all, err := r.Snapshots(ctx, nil); err != nil || len(all) != 1 {
				t.Fatalf("all snapshots: %+v, %v", all, err)
			}

			if s, err := r.Snapshots(ctx, []string{"site:other"}); err != nil || len(s) != 0 {
				t.Fatalf("snapshots of another site: %+v, %v", s, err)
			}

			if err := r.Forget(ctx, Keep{Daily: 7, Weekly: 4, Monthly: 6}); err != nil {
				t.Fatalf("forget: %v", err)
			}

			if err := r.Prune(ctx); err != nil {
				t.Fatalf("prune: %v", err)
			}

			if err := r.Check(ctx, 1, 12); err != nil {
				t.Fatalf("check: %v", err)
			}

			if err := r.Unlock(ctx); err != nil {
				t.Fatalf("unlock: %v", err)
			}

			wrong := *r
			wrong.PasswordFile = filepath.Join(dir, "wrong")
			_ = os.WriteFile(wrong.PasswordFile, []byte("wrong-password\n"), 0o600)

			if _, err := wrong.Snapshots(ctx, []string{"site:blog"}); !errors.Is(err, ErrWrongPassword) {
				t.Fatalf("wrong password: got %v", err)
			}
		})
	}

	if os.Getenv("HOSTASHI_TEST_MARIADB_SOCKET") == "" {
		return
	}

	t.Run("database", func(t *testing.T) {
		r := repos["local"]

		sum, err := r.BackupDatabase(ctx, []string{"site:blog", "kind:db", "db:resticdump"}, "resticdump")
		if err != nil || sum.FilesNew != 1 {
			t.Fatalf("dump of an existing database: %+v, %v", sum, err)
		}

		if _, err := r.BackupDatabase(ctx, []string{"site:blog", "kind:db"}, "doesnotexist"); err == nil {
			t.Fatal("dump of a missing database succeeded")
		}
	})

	// The same server over TCP, as an external server (ADR-0041), with an
	// administrator whose password needs quoting in the option file. CI
	// creates the user; see .github/workflows/go.yml.
	if os.Getenv("HOSTASHI_TEST_REMOTE_DUMP") != "1" {
		return
	}

	t.Run("remote database", func(t *testing.T) {
		r := *repos["local"]
		r.TempDir = t.TempDir()
		cfg := dbserver.Config{
			Host: "127.0.0.1", Port: 3306, AdminUser: "dumpadm", AdminPassword: `a"b'c\d #e f`, TLS: dbserver.TLSNone,
		}

		sum, err := r.BackupRemoteDatabase(ctx, []string{"site:blog", "kind:db", "db:resticdump"}, "resticdump", cfg)
		if err != nil || !ValidSnapshotID(sum.SnapshotID) {
			t.Fatalf("remote dump: %+v, %v", sum, err)
		}

		if left, _ := os.ReadDir(r.TempDir); len(left) != 0 {
			t.Fatalf("option files left behind: %v", left)
		}

		wrong := cfg
		wrong.AdminPassword = "wrong"

		if _, err := r.BackupRemoteDatabase(ctx, []string{"site:blog", "kind:db", "db:resticdump"}, "resticdump", wrong); err == nil {
			t.Fatal("dump with a wrong password succeeded")
		}
	})
}
