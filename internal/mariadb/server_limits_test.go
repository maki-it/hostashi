// SPDX-License-Identifier: AGPL-3.0-or-later

package mariadb

import (
	"context"
	"database/sql"
	"errors"
	"os"
	"slices"
	"strings"
	"testing"

	"github.com/go-sql-driver/mysql"
)

// TestServerSizes measures registered databases on a real server.
func TestServerSizes(t *testing.T) {
	socket := os.Getenv("HOSTASHI_TEST_MARIADB_SOCKET")
	if socket == "" {
		t.Skip("HOSTASHI_TEST_MARIADB_SOCKET not set")
	}

	ctx := t.Context()
	root := Connect(socket)

	t.Cleanup(func() { _ = root.Close() })

	m := newManager(t, stateDir(t), root)

	for _, n := range []string{"size_full", "size_empty"} {
		if _, err := m.Create(ctx, "shop", n); err != nil {
			t.Fatal(err)
		}

		t.Cleanup(func() { _ = m.Delete(context.Background(), n) })
	}

	// Not registered: never reported.
	mustExec(t, root, "CREATE DATABASE IF NOT EXISTS `size_other`")
	mustExec(t, root, "CREATE TABLE IF NOT EXISTS `size_other`.t (i int)")

	t.Cleanup(func() { _, _ = root.ExecContext(context.Background(), "DROP DATABASE IF EXISTS `size_other`") })

	mustExec(t, root, "CREATE TABLE `size_full`.t (i int primary key, s varchar(200)) ENGINE=InnoDB")
	mustExec(t, root, "INSERT INTO `size_full`.t SELECT seq, REPEAT('x', 200) FROM `size_full`.seq_1_to_20000")
	mustExec(t, root, "ANALYZE TABLE `size_full`.t")

	got, err := m.Sizes(ctx)
	if err != nil {
		t.Fatal(err)
	}

	if len(got) != 2 {
		t.Fatalf("sizes: %v", got)
	}

	// 20,000 rows of about 200 bytes: at least 2 MB.
	if got["size_full"] < 2<<20 {
		t.Fatalf("size_full: %d", got["size_full"])
	}

	if s, ok := got["size_empty"]; !ok || s != 0 {
		t.Fatalf("size_empty: %d %v", s, ok)
	}
}

func TestSizesNeedsQuery(t *testing.T) {
	m := newManager(t, stateDir(t), &fakeDB{})
	if _, err := m.Create(t.Context(), "shop", "shop_wp"); err != nil {
		t.Fatal(err)
	}

	if _, err := m.Sizes(t.Context()); !errors.Is(err, ErrNoQuery) {
		t.Fatalf("got %v", err)
	}
}

func TestSizesEmptyRegistry(t *testing.T) {
	m := newManager(t, stateDir(t), &fakeQueryDB{})

	got, err := m.Sizes(t.Context())
	if err != nil || len(got) != 0 {
		t.Fatalf("got %v, %v", got, err)
	}
}

// fakeQueryDB can read rows but fails every query: an empty registry must
// not need one.
type fakeQueryDB struct{ fakeDB }

func (*fakeQueryDB) QueryContext(context.Context, string, ...any) (*sql.Rows, error) {
	return nil, errors.New("unexpected query")
}

func TestSiteConnections(t *testing.T) {
	ctx := t.Context()
	db := &fakeDB{}
	m := newManager(t, stateDir(t), db)

	for _, c := range [][2]string{{"shop", "shop_wp"}, {"shop", "shop_log"}, {"blog", "blog_wp"}} {
		if _, err := m.Create(ctx, c[0], c[1]); err != nil {
			t.Fatal(err)
		}
	}

	if err := m.CreateUserLimited(ctx, "shop_wp", "shopuser", testPW, 20); err != nil {
		t.Fatal(err)
	}

	if err := m.CreateUser(ctx, "shop_log", "shoplog", testPW); err != nil {
		t.Fatal(err)
	}

	if err := m.CreateUser(ctx, "blog_wp", "bloguser", testPW); err != nil {
		t.Fatal(err)
	}

	if err := m.CreateUserLimited(ctx, "shop_wp", "toomany", testPW, 10001); !errors.Is(err, ErrInvalid) {
		t.Fatalf("too many connections: %v", err)
	}

	db.stmts = nil

	users, err := m.SetSiteConnections(ctx, "shop", 40)
	if err != nil || !slices.Equal(users, []string{"shoplog", "shopuser"}) {
		t.Fatalf("users %v %v", users, err)
	}

	want := []string{
		"ALTER USER 'shoplog'@'localhost' WITH MAX_USER_CONNECTIONS 40",
		"ALTER USER 'shopuser'@'localhost' WITH MAX_USER_CONNECTIONS 40",
	}
	if !slices.Equal(db.stmts, want) {
		t.Fatalf("statements %q", db.stmts)
	}

	// A site without databases has nothing to change.
	if users, err := m.SetSiteConnections(ctx, "empty", 5); err != nil || len(users) != 0 {
		t.Fatalf("empty site: %v %v", users, err)
	}

	if _, err := m.SetSiteConnections(ctx, "Bad Site", 5); !errors.Is(err, ErrInvalid) {
		t.Fatalf("bad site: %v", err)
	}

	db.failOn, db.err = "shopuser", &mysql.MySQLError{Number: 1045}
	if _, err := m.SetSiteConnections(ctx, "shop", 0); err == nil || strings.Contains(err.Error(), "ALTER") {
		t.Fatalf("failure: %v", err)
	}
}

// TestServerSiteConnections checks the limit on a real server.
func TestServerSiteConnections(t *testing.T) {
	socket := os.Getenv("HOSTASHI_TEST_MARIADB_SOCKET")
	if socket == "" {
		t.Skip("HOSTASHI_TEST_MARIADB_SOCKET not set")
	}

	ctx := t.Context()
	root := Connect(socket)

	t.Cleanup(func() { _ = root.Close() })

	m := newManager(t, stateDir(t), root)

	if _, err := m.Create(ctx, "conns", "conn_db"); err != nil {
		t.Fatal(err)
	}

	t.Cleanup(func() { _ = m.Delete(context.Background(), "conn_db") })

	if err := m.CreateUserLimited(ctx, "conn_db", "connuser", testPW, 1); err != nil {
		t.Fatal(err)
	}

	c1 := login(t, socket, "connuser", testPW)
	c1.SetMaxOpenConns(1)

	if err := tryLogin(socket, "connuser", testPW); err == nil {
		t.Fatal("second connection accepted with limit 1")
	}

	if _, err := m.SetSiteConnections(ctx, "conns", 2); err != nil {
		t.Fatal(err)
	}

	c2 := login(t, socket, "connuser", testPW)
	c2.SetMaxOpenConns(1)

	if err := tryLogin(socket, "connuser", testPW); err == nil {
		t.Fatal("third connection accepted with limit 2")
	}

	// 0 lifts the limit.
	if _, err := m.SetSiteConnections(ctx, "conns", 0); err != nil {
		t.Fatal(err)
	}

	if err := tryLogin(socket, "connuser", testPW); err != nil {
		t.Fatalf("unlimited: %v", err)
	}
}
