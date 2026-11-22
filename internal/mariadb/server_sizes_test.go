// SPDX-License-Identifier: AGPL-3.0-or-later

package mariadb

import (
	"context"
	"database/sql"
	"errors"
	"os"
	"testing"
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
