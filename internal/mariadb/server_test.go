// SPDX-License-Identifier: AGPL-3.0-or-later

package mariadb

import (
	"context"
	"database/sql"
	"errors"
	"os"
	"testing"

	"github.com/go-sql-driver/mysql"
)

// TestServer runs against a real MariaDB server as root over its socket,
// the way the agent does on a server. It needs HOSTASHI_TEST_MARIADB_SOCKET
// and runs in CI in a Debian container (see .github/workflows/go.yml).
func TestServer(t *testing.T) {
	socket := os.Getenv("HOSTASHI_TEST_MARIADB_SOCKET")
	if socket == "" {
		t.Skip("HOSTASHI_TEST_MARIADB_SOCKET not set")
	}

	ctx := t.Context()
	root := Connect(socket)

	t.Cleanup(func() { _ = root.Close() })

	m := newManager(t, stateDir(t), root)

	// A database and a user that Hostashi did not create.
	mustExec(t, root, "CREATE DATABASE IF NOT EXISTS `legacy`")
	mustExec(t, root, "CREATE USER IF NOT EXISTS 'legacy'@'localhost' IDENTIFIED BY 'legacy-pass-123'")

	if _, err := m.Create(ctx, "shop", "legacy"); !errors.Is(err, ErrExists) {
		t.Fatalf("existing database: %v", err)
	}

	if _, err := m.Create(ctx, "shop", "a_b"); err != nil {
		t.Fatal(err)
	}

	if _, err := m.Create(ctx, "blog", "axb"); err != nil {
		t.Fatal(err)
	}

	if err := m.CreateUser(ctx, "a_b", "legacy", testPW); !errors.Is(err, ErrExists) {
		t.Fatalf("existing user: %v", err)
	}

	if err := m.CreateUser(ctx, "a_b", "shopuser", testPW); err != nil {
		t.Fatal(err)
	}

	if err := m.CreateUser(ctx, "axb", "bloguser", testPW); err != nil {
		t.Fatal(err)
	}

	shop := login(t, socket, "shopuser", testPW)
	mustExec(t, shop, "CREATE TABLE `a_b`.t (i int)")

	// "_" is a wildcard in GRANT. Unescaped, shopuser could use axb.
	if _, err := shop.ExecContext(ctx, "CREATE TABLE `axb`.t (i int)"); !isDenied(err) {
		t.Fatalf("shopuser reached the database of another site: %v", err)
	}

	if _, err := shop.ExecContext(ctx, "SELECT 1 FROM mysql.user"); !isDenied(err) {
		t.Fatalf("shopuser read mysql.user: %v", err)
	}

	if _, err := shop.ExecContext(ctx, "CREATE DATABASE `a_b2`"); !isDenied(err) {
		t.Fatalf("shopuser created a database: %v", err)
	}

	const newPW = "Changed-pass_98765"
	if err := m.SetPassword(ctx, "shopuser", newPW); err != nil {
		t.Fatal(err)
	}

	if err := tryLogin(socket, "shopuser", testPW); err == nil {
		t.Fatal("old password still works")
	}

	_ = login(t, socket, "shopuser", newPW)

	if err := m.Delete(ctx, "a_b"); err != nil {
		t.Fatal(err)
	}

	if err := tryLogin(socket, "shopuser", newPW); err == nil {
		t.Fatal("user of a deleted database can still log in")
	}

	if err := m.DeleteUser(ctx, "bloguser"); err != nil {
		t.Fatal(err)
	}

	if err := m.Delete(ctx, "axb"); err != nil {
		t.Fatal(err)
	}

	// The foreign objects are untouched.
	var n int
	if err := root.QueryRowContext(ctx, "SELECT COUNT(*) FROM information_schema.SCHEMATA WHERE SCHEMA_NAME IN ('legacy','a_b','axb')").Scan(&n); err != nil || n != 1 {
		t.Fatalf("databases left: %d, %v", n, err)
	}

	_ = login(t, socket, "legacy", "legacy-pass-123")
}

func mustExec(t *testing.T, db *sql.DB, q string) {
	t.Helper()

	if _, err := db.ExecContext(t.Context(), q); err != nil {
		t.Fatalf("%s: %v", q, err)
	}
}

func connectAs(socket, user, pw string) *sql.DB {
	c := mysql.NewConfig()
	c.Net, c.Addr, c.User, c.Passwd = "unix", socket, user, pw

	conn, err := mysql.NewConnector(c)
	if err != nil {
		panic(err)
	}

	return sql.OpenDB(conn)
}

func tryLogin(socket, user, pw string) error {
	db := connectAs(socket, user, pw)
	defer db.Close()

	return db.PingContext(context.Background())
}

func login(t *testing.T, socket, user, pw string) *sql.DB {
	t.Helper()

	db := connectAs(socket, user, pw)

	t.Cleanup(func() { _ = db.Close() })

	if err := db.PingContext(t.Context()); err != nil {
		t.Fatalf("login as %s: %v", user, err)
	}

	return db
}

func isDenied(err error) bool {
	var me *mysql.MySQLError

	return errors.As(err, &me) && (me.Number == 1142 || me.Number == 1044 || me.Number == 1045)
}
