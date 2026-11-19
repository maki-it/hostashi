// SPDX-License-Identifier: AGPL-3.0-or-later

package mariadb

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/maki-it/hostashi/internal/agentapi"
)

// A dump as mariadb-dump writes it on another server, with a view, a
// trigger and a procedure of that server's user.
const definerDump = "CREATE TABLE `t` (`a` int, `b` int);\n" +
	"DELIMITER ;;\n" +
	"/*!50003 CREATE*/ /*!50017 DEFINER=`shop`@`localhost`*/ /*!50003 TRIGGER `t_bi` BEFORE INSERT ON `t` FOR EACH ROW SET NEW.b = NEW.a * 2 */;;\n" +
	"DELIMITER ;\n" +
	"/*!50001 CREATE ALGORITHM=UNDEFINED */\n" +
	"/*!50013 DEFINER=`shop`@`localhost` SQL SECURITY DEFINER */\n" +
	"/*!50001 VIEW `v` AS select `t`.`b` AS `b` from `t` */;\n" +
	"DELIMITER ;;\n" +
	"CREATE DEFINER=`shop`@`localhost` PROCEDURE `p`()\nBEGIN SELECT COUNT(*) FROM `t`; END ;;\n" +
	"DELIMITER ;\n"

// TestServerOwnerLoad loads a dump with definers through the real client as
// uid 65534 (ADR-0030).
func TestServerOwnerLoad(t *testing.T) {
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

	for _, n := range []string{"own_target", "own_other"} {
		if _, err := m.Create(ctx, "shop", n); err != nil {
			t.Fatal(err)
		}
	}

	mustExec(t, root, "CREATE TABLE `own_other`.secret (i int)")

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

	write("defs.sql", definerDump)

	// The plain import refuses the foreign definer.
	if j := run(m.StartImport("own_target", "defs.sql", false)); j.State != agentapi.JobFailed {
		t.Fatalf("plain import of a dump with definers: %+v", j)
	}

	mustExec(t, root, "DROP TABLE IF EXISTS `own_target`.`t`")

	if j := run(m.StartImport("own_target", "defs.sql", true)); j.State != agentapi.JobDone {
		t.Fatalf("owner load: %+v", j)
	}

	owner := m.List()[1].Owner
	if m.List()[1].Name != "own_target" || !ownerUserRe.MatchString(owner) {
		t.Fatalf("list %+v", m.List())
	}

	// The objects belong to the owner and work after the load, although the
	// owner is locked.
	mustExec(t, root, "INSERT INTO `own_target`.`t` (a) VALUES (21)")

	var b int
	if err := root.QueryRowContext(ctx, "SELECT b FROM `own_target`.`v`").Scan(&b); err != nil || b != 42 {
		t.Fatalf("view after trigger: %d %v", b, err)
	}

	var n int
	if err := root.QueryRowContext(ctx, "SELECT COUNT(*) FROM information_schema.TRIGGERS WHERE TRIGGER_SCHEMA='own_target' AND DEFINER=?", owner+"@localhost").Scan(&n); err != nil || n != 1 {
		t.Fatalf("trigger definer: %d %v", n, err)
	}

	if err := root.QueryRowContext(ctx, "SELECT COUNT(*) FROM information_schema.ROUTINES WHERE ROUTINE_SCHEMA='own_target' AND DEFINER=?", owner+"@localhost").Scan(&n); err != nil || n != 1 {
		t.Fatalf("procedure definer: %d %v", n, err)
	}

	var locked string
	if err := root.QueryRowContext(ctx, "SELECT JSON_VALUE(Priv, '$.account_locked') FROM mysql.global_priv WHERE User=? AND Host='localhost'", owner).Scan(&locked); err != nil || locked != "true" {
		t.Fatalf("owner locked = %q %v", locked, err)
	}

	// The owner has no more rights than the temporary user.
	for name, dump := range map[string]string{
		"use.sql":  "USE own_other;\nDROP TABLE secret;\n",
		"user.sql": "CREATE USER 'evil'@'%' IDENTIFIED BY 'evil';\n",
		"root.sql": "CREATE DEFINER=`root`@`localhost` VIEW `vr` AS SELECT 1; GRANT ALL ON *.* TO CURRENT_USER;\n",
	} {
		write(name, dump)

		if j := run(m.StartImport("own_target", name, true)); j.State != agentapi.JobFailed {
			t.Errorf("%s: %+v", name, j)
		}
	}

	if err := root.QueryRowContext(ctx, "SELECT COUNT(*) FROM information_schema.TABLES WHERE TABLE_SCHEMA='own_other'").Scan(&n); err != nil || n != 1 {
		t.Fatalf("other database changed: %d %v", n, err)
	}

	if err := root.QueryRowContext(ctx, "SELECT COUNT(*) FROM mysql.user WHERE User='evil'").Scan(&n); err != nil || n != 0 {
		t.Fatalf("accounts left: %d %v", n, err)
	}

	for _, d := range []string{"own_target", "own_other"} {
		if err := m.Delete(ctx, d); err != nil {
			t.Fatal(err)
		}
	}

	if err := root.QueryRowContext(ctx, "SELECT COUNT(*) FROM mysql.user WHERE User LIKE 'hostashi\\_own\\_%'").Scan(&n); err != nil || n != 0 {
		t.Fatalf("owner left after delete: %d %v", n, err)
	}
}
