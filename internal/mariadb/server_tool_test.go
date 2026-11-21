// SPDX-License-Identifier: AGPL-3.0-or-later

package mariadb

import (
	"context"
	"database/sql"
	"os"
	"testing"
	"time"
)

// TestServerTool checks the temporary users of the web database tool
// (ADR-0038) on a real server: access to one database only, the connection
// limit, and that closing the session ends open connections.
func TestServerTool(t *testing.T) {
	socket := os.Getenv("HOSTASHI_TEST_MARIADB_SOCKET")
	if socket == "" {
		t.Skip("HOSTASHI_TEST_MARIADB_SOCKET not set")
	}

	ctx := t.Context()
	root := Connect(socket)

	t.Cleanup(func() { _ = root.Close() })

	m := newManager(t, stateDir(t), root)

	for _, n := range []string{"tool_a", "toolxa"} {
		mustExec(t, root, "DROP DATABASE IF EXISTS `"+n+"`")
	}

	if _, err := m.Create(ctx, "shop", "tool_a"); err != nil {
		t.Fatal(err)
	}

	if _, err := m.Create(ctx, "blog", "toolxa"); err != nil {
		t.Fatal(err)
	}

	l, err := m.OpenTool(ctx, "tool_a", time.Hour)
	if err != nil {
		t.Fatal(err)
	}

	c1 := connectAs(socket, l.User, l.Password)

	t.Cleanup(func() { _ = c1.Close() })
	c1.SetMaxOpenConns(1)

	mustExec(t, c1, "CREATE TABLE `tool_a`.t (i int)")
	mustExec(t, c1, "INSERT INTO `tool_a`.t VALUES (1)")

	// "_" in the grant is escaped: the other site's database stays closed.
	if _, err := c1.ExecContext(ctx, "CREATE TABLE `toolxa`.t (i int)"); !isDenied(err) {
		t.Fatalf("other database: %v", err)
	}

	if _, err := c1.ExecContext(ctx, "GRANT SELECT ON `tool_a`.* TO 'root'@'localhost'"); !isDenied(err) {
		t.Fatalf("grant option: %v", err)
	}

	// At most two connections at once.
	c2 := login(t, socket, l.User, l.Password)
	c2.SetMaxOpenConns(1)

	if err := tryLogin(socket, l.User, l.Password); err == nil {
		t.Fatal("third connection accepted")
	}

	// A leftover user of an earlier run goes, the live session stays.
	mustExec(t, root, "CREATE USER 'hostashi_tool_00000000000000aa'@'localhost' IDENTIFIED BY 'leftover-pass-123'")

	if err := m.RemoveToolUsers(ctx, root); err != nil {
		t.Fatal(err)
	}

	if n := countUsers(t, root, "hostashi_tool_00000000000000aa"); n != 0 {
		t.Fatal("leftover user kept")
	}

	if n := countUsers(t, root, l.User); n != 1 {
		t.Fatal("live session user dropped")
	}

	if err := m.CloseTool(ctx, l.User); err != nil {
		t.Fatal(err)
	}

	// The open connection is gone and no new login works.
	if err := pingFresh(ctx, c1); err == nil {
		t.Fatal("connection survived the close")
	}

	if err := tryLogin(socket, l.User, l.Password); err == nil {
		t.Fatal("login after close")
	}

	if n := countUsers(t, root, l.User); n != 0 {
		t.Fatal("user kept after close")
	}

	// Deleting a database ends its sessions.
	l2, err := m.OpenTool(ctx, "toolxa", time.Hour)
	if err != nil {
		t.Fatal(err)
	}

	if err := m.Delete(ctx, "toolxa"); err != nil {
		t.Fatal(err)
	}

	if n := countUsers(t, root, l2.User); n != 0 {
		t.Fatal("user kept after database delete")
	}

	if err := m.Delete(ctx, "tool_a"); err != nil {
		t.Fatal(err)
	}
}

// pingFresh runs a statement on the pool's one connection; a killed
// connection fails it (the driver may retry once on a new connection,
// which the dropped user cannot open).
func pingFresh(ctx context.Context, db *sql.DB) error {
	_, err := db.ExecContext(ctx, "DO 1")

	return err
}

func countUsers(t *testing.T, root *sql.DB, user string) int {
	t.Helper()

	var n int
	if err := root.QueryRowContext(t.Context(), "SELECT COUNT(*) FROM mysql.user WHERE User = ?", user).Scan(&n); err != nil {
		t.Fatal(err)
	}

	return n
}
