// SPDX-License-Identifier: AGPL-3.0-or-later

package mariadb

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/maki-it/hostashi/internal/agentapi"
)

// TestServerCopy copies a database with a view, a trigger and a procedure
// through the real mariadb-dump and mariadb, each side as its own uid
// (ADR-0070).
func TestServerCopy(t *testing.T) {
	socket := os.Getenv("HOSTASHI_TEST_MARIADB_SOCKET")
	if socket == "" || os.Getuid() != 0 {
		t.Skip("needs root and HOSTASHI_TEST_MARIADB_SOCKET")
	}

	ctx := t.Context()
	root := Connect(socket)

	t.Cleanup(func() { _ = root.Close() })

	base := t.TempDir()
	if err := os.Chmod(base, 0o755); err != nil {
		t.Fatal(err)
	}

	homes := map[string]SiteHome{}

	for site, id := range map[string]uint32{"live": 65534, "stage": 65533} {
		home := filepath.Join(base, site)
		if err := os.Mkdir(home, 0o750); err != nil {
			t.Fatal(err)
		}

		if err := os.Chown(home, int(id), int(id)); err != nil {
			t.Fatal(err)
		}

		homes[site] = SiteHome{Home: home, UID: id, GID: id}
	}

	m := newManager(t, stateDir(t), root)
	m.EnableDumps(Dumps{Tools: RealTools{}, Socket: socket, HomeOf: func(site string) (SiteHome, bool) {
		h, ok := homes[site]

		return h, ok
	}})

	if _, err := m.Create(ctx, "live", "copy_src"); err != nil {
		t.Fatal(err)
	}

	if _, err := m.Create(ctx, "stage", "copy_dst"); err != nil {
		t.Fatal(err)
	}

	// Later tests count the owner accounts on the server: drop ours.
	t.Cleanup(func() {
		for _, n := range []string{"copy_src", "copy_dst"} {
			if err := m.Delete(context.Background(), n); err != nil {
				t.Errorf("delete %s: %v", n, err)
			}
		}
	})

	p := filepath.Join(homes["live"].Home, "defs.sql")
	if err := os.WriteFile(p, []byte(definerDump), 0o600); err != nil {
		t.Fatal(err)
	}

	if err := os.Chown(p, int(homes["live"].UID), int(homes["live"].GID)); err != nil {
		t.Fatal(err)
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

	if j := run(m.StartImport("copy_src", "defs.sql", true)); j.State != agentapi.JobDone {
		t.Fatalf("load source: %+v", j)
	}

	mustExec(t, root, "INSERT INTO `copy_src`.`t` (a) VALUES (21)")

	if j := run(m.StartCopy("copy_src", "copy_dst")); j.State != agentapi.JobDone {
		t.Fatalf("copy: %+v", j)
	}

	var b int
	if err := root.QueryRowContext(ctx, "SELECT b FROM `copy_dst`.`v`").Scan(&b); err != nil || b != 42 {
		t.Fatalf("view in the copy: %d %v", b, err)
	}

	var owner string
	if err := root.QueryRowContext(ctx, "SELECT DEFINER FROM information_schema.VIEWS WHERE TABLE_SCHEMA='copy_dst'").Scan(&owner); err != nil {
		t.Fatal(err)
	}

	for _, d := range m.List() {
		if d.Name == "copy_dst" && owner != d.Owner+"@localhost" {
			t.Fatalf("view definer %q, want the target's owner %q", owner, d.Owner)
		}
	}

	// A second copy replaces the tables.
	if j := run(m.StartCopy("copy_src", "copy_dst")); j.State != agentapi.JobDone {
		t.Fatalf("second copy: %+v", j)
	}

	var n int
	if err := root.QueryRowContext(ctx, "SELECT COUNT(*) FROM `copy_dst`.`t`").Scan(&n); err != nil || n != 1 {
		t.Fatalf("rows after a second copy: %d %v", n, err)
	}

	if err := root.QueryRowContext(ctx, "SELECT COUNT(*) FROM mysql.user WHERE User LIKE 'hostashi\\_job\\_%'").Scan(&n); err != nil || n != 0 {
		t.Fatalf("job users left: %d %v", n, err)
	}
}
