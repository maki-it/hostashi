// SPDX-License-Identifier: AGPL-3.0-or-later

package mariadb

import (
	"compress/gzip"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/maki-it/hostashi/internal/agentapi"
)

// TestServerDumps imports and exports through the real mariadb and
// mariadb-dump as uid 65534, like the agent does as root on a server.
func TestServerDumps(t *testing.T) {
	socket := os.Getenv("HOSTASHI_TEST_MARIADB_SOCKET")
	if socket == "" || os.Getuid() != 0 {
		t.Skip("needs root and HOSTASHI_TEST_MARIADB_SOCKET")
	}

	ctx := t.Context()
	root := Connect(socket)

	t.Cleanup(func() { _ = root.Close() })

	home := filepath.Join(t.TempDir(), "shop")
	if err := os.Mkdir(home, 0o750); err != nil {
		t.Fatal(err)
	}

	const uid, gid = 65534, 65534

	if err := os.Chown(home, uid, gid); err != nil {
		t.Fatal(err)
	}

	if err := os.Chmod(filepath.Dir(home), 0o755); err != nil {
		t.Fatal(err)
	}

	m := newManager(t, stateDir(t), root)
	m.EnableDumps(Dumps{Tools: RealTools{}, Socket: socket, HomeOf: func(string) (SiteHome, bool) {
		return SiteHome{Home: home, UID: uid, GID: gid}, true
	}})

	for _, n := range []string{"dump_target", "dump_other"} {
		if _, err := m.Create(ctx, "shop", n); err != nil {
			t.Fatal(err)
		}
	}

	mustExec(t, root, "CREATE TABLE `dump_other`.secret (i int)")

	write := func(name, content string) {
		t.Helper()

		p := filepath.Join(home, name)
		if err := os.WriteFile(p, []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}

		if err := os.Chown(p, uid, gid); err != nil {
			t.Fatal(err)
		}
	}

	run := func(j agentapi.DBJob, err error) agentapi.DBJob {
		t.Helper()

		if err != nil {
			t.Fatal(err)
		}

		m.wait(j.ID)

		j, err = m.Job(j.ID)
		if err != nil {
			t.Fatal(err)
		}

		return j
	}

	// A MySQL 8 dump of WordPress imports.
	write("wp.sql", "CREATE TABLE `wp_posts` (`ID` bigint unsigned NOT NULL AUTO_INCREMENT, `t` text, PRIMARY KEY (`ID`)) "+
		"ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_ai_ci;\nINSERT INTO `wp_posts` VALUES (1,'hällo');\n")

	if j := run(m.StartImport("dump_target", "wp.sql", false)); j.State != agentapi.JobDone {
		t.Fatalf("import: %+v", j)
	}

	// Hostile dumps: a shell escape, another database, a new account.
	marker := filepath.Join(t.TempDir(), "pwned")
	for name, dump := range map[string]string{
		"shell.sql": "system touch " + marker + "\n",
		"bang.sql":  "\\! touch " + marker + "\n",
		"use.sql":   "USE dump_other;\nDROP TABLE secret;\n",
		"user.sql":  "CREATE USER 'evil'@'%' IDENTIFIED BY 'evil';\n",
		"file.sql":  "SELECT LOAD_FILE('/etc/shadow') INTO OUTFILE '/tmp/shadow';\n",
	} {
		write(name, dump)

		if j := run(m.StartImport("dump_target", name, false)); j.State != agentapi.JobFailed {
			t.Errorf("%s: %+v", name, j)
		}
	}

	if _, err := os.Stat(marker); err == nil {
		t.Fatal("a dump ran a shell command")
	}

	var n int
	if err := root.QueryRowContext(ctx, "SELECT COUNT(*) FROM information_schema.TABLES WHERE TABLE_SCHEMA='dump_other'").Scan(&n); err != nil || n != 1 {
		t.Fatalf("other database changed: %d %v", n, err)
	}

	if err := root.QueryRowContext(ctx, "SELECT COUNT(*) FROM mysql.user WHERE User='evil' OR User LIKE 'hostashi\\_job\\_%'").Scan(&n); err != nil || n != 0 {
		t.Fatalf("accounts left: %d %v", n, err)
	}

	// Export, then import the export into the other database.
	if j := run(m.StartExport("dump_target", "out.sql.gz")); j.State != agentapi.JobDone {
		t.Fatalf("export: %+v", j)
	}

	st, err := os.Stat(filepath.Join(home, "out.sql.gz"))
	if err != nil || st.Mode().Perm() != 0o600 {
		t.Fatalf("export file: %v %v", st, err)
	}

	f, err := os.Open(filepath.Join(home, "out.sql.gz"))
	if err != nil {
		t.Fatal(err)
	}

	zr, err := gzip.NewReader(f)
	if err != nil {
		t.Fatal(err)
	}

	b, _ := io.ReadAll(zr)
	_ = f.Close()

	if !strings.Contains(string(b), "INSERT INTO `wp_posts` VALUES") || strings.Contains(string(b), "CREATE DATABASE") {
		t.Fatalf("export content: %.300s", b)
	}

	if j := run(m.StartImport("dump_other", "out.sql.gz", false)); j.State != agentapi.JobDone {
		t.Fatalf("re-import: %+v", j)
	}

	var title string
	if err := root.QueryRowContext(ctx, "SELECT t FROM `dump_other`.wp_posts WHERE ID=1").Scan(&title); err != nil || title != "hällo" {
		t.Fatalf("re-imported %q %v", title, err)
	}

	if err := m.RemoveJobUsers(ctx, root); err != nil {
		t.Fatal(err)
	}

	for _, d := range []string{"dump_target", "dump_other"} {
		if err := m.Delete(ctx, d); err != nil {
			t.Fatal(err)
		}
	}
}
