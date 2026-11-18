// SPDX-License-Identifier: AGPL-3.0-or-later

package mariadb

import (
	"context"
	"database/sql"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"syscall"
	"testing"

	"github.com/go-sql-driver/mysql"
)

const testPW = "Secret-pass_12345"

// fakeDB records statements and fails the ones that contain failOn.
type fakeDB struct {
	stmts  []string
	failOn string
	err    error
}

func (f *fakeDB) ExecContext(_ context.Context, q string, _ ...any) (sql.Result, error) {
	f.stmts = append(f.stmts, q)
	if f.failOn != "" && strings.Contains(q, f.failOn) {
		return nil, f.err
	}

	return nil, nil
}

func stateDir(t *testing.T) string {
	t.Helper()

	dir := filepath.Join(t.TempDir(), "state")
	if err := os.Mkdir(dir, 0o700); err != nil {
		t.Fatal(err)
	}

	return dir
}

func newManager(t *testing.T, dir string, db Execer) *Manager {
	t.Helper()

	m, err := New(dir, db, Options{OwnerUID: uint32(os.Getuid())})
	if err != nil {
		t.Fatal(err)
	}

	return m
}

func TestLifecycle(t *testing.T) {
	ctx := t.Context()
	dir := stateDir(t)
	db := &fakeDB{}
	m := newManager(t, dir, db)

	if _, err := m.Create(ctx, "shop", "shop_wp"); err != nil {
		t.Fatal(err)
	}

	if err := m.CreateUser(ctx, "shop_wp", "shop_wp", testPW); err != nil {
		t.Fatal(err)
	}

	if err := m.SetPassword(ctx, "shop_wp", testPW+"x"); err != nil {
		t.Fatal(err)
	}

	want := []string{
		"CREATE DATABASE `shop_wp` CHARACTER SET utf8mb4",
		"CREATE USER 'shop_wp'@'localhost' IDENTIFIED BY '" + testPW + "'",
		"GRANT ALL PRIVILEGES ON `shop\\_wp`.* TO 'shop_wp'@'localhost'",
		"ALTER USER 'shop_wp'@'localhost' IDENTIFIED BY '" + testPW + "x'",
	}
	if !reflect.DeepEqual(db.stmts, want) {
		t.Fatalf("statements:\n%q\nwant\n%q", db.stmts, want)
	}

	if !m.SiteHasDatabases("shop") || m.SiteHasDatabases("blog") {
		t.Fatal("SiteHasDatabases wrong")
	}

	// The registry survives a restart.
	m2 := newManager(t, dir, db)
	if got := m2.List(); !reflect.DeepEqual(got, []Database{{Name: "shop_wp", Site: "shop", Users: []string{"shop_wp"}}}) {
		t.Fatalf("after reload: %+v", got)
	}

	db.stmts = nil

	if err := m2.Delete(ctx, "shop_wp"); err != nil {
		t.Fatal(err)
	}

	want = []string{"DROP USER IF EXISTS 'shop_wp'@'localhost'", "DROP DATABASE IF EXISTS `shop_wp`"}
	if !reflect.DeepEqual(db.stmts, want) {
		t.Fatalf("delete statements: %q", db.stmts)
	}

	if len(m2.List()) != 0 || m2.SiteHasDatabases("shop") {
		t.Fatal("database still listed")
	}
}

func TestDeleteUser(t *testing.T) {
	ctx := t.Context()
	m := newManager(t, stateDir(t), &fakeDB{})

	if _, err := m.Create(ctx, "shop", "shop"); err != nil {
		t.Fatal(err)
	}

	for _, u := range []string{"a1", "b2", "c3"} {
		if err := m.CreateUser(ctx, "shop", u, testPW); err != nil {
			t.Fatal(err)
		}
	}

	if err := m.DeleteUser(ctx, "b2"); err != nil {
		t.Fatal(err)
	}

	if got := m.List()[0].Users; !reflect.DeepEqual(got, []string{"a1", "c3"}) {
		t.Fatalf("users %v", got)
	}

	if err := m.DeleteUser(ctx, "b2"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("second delete: %v", err)
	}

	// The name is free again.
	if err := m.CreateUser(ctx, "shop", "b2", testPW); err != nil {
		t.Fatal(err)
	}
}

func TestOnlyRegisteredObjects(t *testing.T) {
	ctx := t.Context()
	db := &fakeDB{}
	m := newManager(t, stateDir(t), db)

	if _, err := m.Create(ctx, "shop", "shop"); err != nil {
		t.Fatal(err)
	}

	if err := m.CreateUser(ctx, "shop", "shopuser", testPW); err != nil {
		t.Fatal(err)
	}

	db.stmts = nil

	checks := []struct {
		name string
		err  error
		want error
	}{
		{"delete unknown database", m.Delete(ctx, "other"), ErrNotFound},
		{"delete unknown user", m.DeleteUser(ctx, "other"), ErrNotFound},
		{"password of unknown user", m.SetPassword(ctx, "other", testPW), ErrNotFound},
		{"user for unknown database", m.CreateUser(ctx, "other", "u1", testPW), ErrNotFound},
		{"duplicate database", func() error { _, err := m.Create(ctx, "blog", "shop"); return err }(), ErrExists},
		{"duplicate user", m.CreateUser(ctx, "shop", "shopuser", testPW), ErrExists},
	}
	for _, c := range checks {
		if !errors.Is(c.err, c.want) {
			t.Errorf("%s: got %v, want %v", c.name, c.err, c.want)
		}
	}

	if len(db.stmts) != 0 {
		t.Fatalf("refused calls reached the server: %q", db.stmts)
	}
}

func TestInvalidInputNeverReachesServer(t *testing.T) {
	ctx := t.Context()
	db := &fakeDB{}
	m := newManager(t, stateDir(t), db)

	if _, err := m.Create(ctx, "shop", "shop"); err != nil {
		t.Fatal(err)
	}

	db.stmts = nil

	bad := []error{
		func() error { _, err := m.Create(ctx, "shop", "a`; DROP DATABASE mysql; --"); return err }(),
		func() error { _, err := m.Create(ctx, "shop", "mysql"); return err }(),
		func() error { _, err := m.Create(ctx, "root", "x1"); return err }(),
		func() error { _, err := m.Create(ctx, "Shop", "x1"); return err }(),
		m.Delete(ctx, "information_schema"),
		m.CreateUser(ctx, "shop", "root", testPW),
		m.CreateUser(ctx, "shop", "u1'@'%", testPW),
		m.CreateUser(ctx, "shop", "u1", "short"),
		m.CreateUser(ctx, "shop", "u1", "pass' OR '1'='1xx"),
		m.CreateUser(ctx, "shop", "u1", `back\slash-pass12`),
		m.SetPassword(ctx, "u1", "with space in it 1"),
	}
	for i, err := range bad {
		if !errors.Is(err, ErrInvalid) {
			t.Errorf("case %d: got %v, want ErrInvalid", i, err)
		}
	}

	if len(db.stmts) != 0 {
		t.Fatalf("invalid input reached the server: %q", db.stmts)
	}
}

func TestServerErrors(t *testing.T) {
	ctx := t.Context()

	t.Run("database exists on the server", func(t *testing.T) {
		db := &fakeDB{failOn: "CREATE DATABASE", err: &mysql.MySQLError{Number: 1007, Message: "exists"}}
		m := newManager(t, stateDir(t), db)

		if _, err := m.Create(ctx, "shop", "legacy"); !errors.Is(err, ErrExists) {
			t.Fatalf("got %v", err)
		}

		if len(m.List()) != 0 {
			t.Fatal("foreign database adopted")
		}

		for _, s := range db.stmts {
			if strings.HasPrefix(s, "DROP") {
				t.Fatalf("dropped a database it did not create: %q", s)
			}
		}
	})

	t.Run("user exists on the server", func(t *testing.T) {
		db := &fakeDB{failOn: "CREATE USER", err: &mysql.MySQLError{Number: 1396, Message: "exists"}}
		m := newManager(t, stateDir(t), db)

		if _, err := m.Create(ctx, "shop", "shop"); err != nil {
			t.Fatal(err)
		}

		if err := m.CreateUser(ctx, "shop", "legacy", testPW); !errors.Is(err, ErrExists) {
			t.Fatalf("got %v", err)
		}

		if last := db.stmts[len(db.stmts)-1]; strings.HasPrefix(last, "DROP") {
			t.Fatalf("dropped a user it did not create: %q", last)
		}
	})

	t.Run("grant fails, user is removed again", func(t *testing.T) {
		db := &fakeDB{failOn: "GRANT", err: &mysql.MySQLError{Number: 1044, Message: "denied"}}
		m := newManager(t, stateDir(t), db)

		if _, err := m.Create(ctx, "shop", "shop"); err != nil {
			t.Fatal(err)
		}

		err := m.CreateUser(ctx, "shop", "u1", testPW)
		if err == nil || errors.Is(err, ErrExists) {
			t.Fatalf("got %v", err)
		}

		if last := db.stmts[len(db.stmts)-1]; last != "DROP USER IF EXISTS 'u1'@'localhost'" {
			t.Fatalf("no undo: %q", last)
		}

		if len(m.List()[0].Users) != 0 {
			t.Fatal("user registered")
		}
	})

	t.Run("server message and password never in the error", func(t *testing.T) {
		db := &fakeDB{failOn: "ALTER USER", err: &mysql.MySQLError{Number: 1064, Message: "syntax error near '" + testPW + "'"}}
		m := newManager(t, stateDir(t), db)

		if _, err := m.Create(ctx, "shop", "shop"); err != nil {
			t.Fatal(err)
		}

		if err := m.CreateUser(ctx, "shop", "u1", testPW); err != nil {
			t.Fatal(err)
		}

		err := m.SetPassword(ctx, "u1", testPW)
		if err == nil || strings.Contains(err.Error(), testPW) || strings.Contains(err.Error(), "syntax") {
			t.Fatalf("error leaks details: %v", err)
		}
	})

	t.Run("server not running", func(t *testing.T) {
		db := &fakeDB{failOn: "CREATE", err: &os.SyscallError{Syscall: "connect", Err: syscall.ENOENT}}
		m := newManager(t, stateDir(t), db)

		if _, err := m.Create(ctx, "shop", "shop"); !errors.Is(err, ErrUnavailable) {
			t.Fatalf("got %v", err)
		}
	})

	t.Run("delete stops at the first failure and can be retried", func(t *testing.T) {
		db := &fakeDB{}
		m := newManager(t, stateDir(t), db)

		if _, err := m.Create(ctx, "shop", "shop"); err != nil {
			t.Fatal(err)
		}

		if err := m.CreateUser(ctx, "shop", "u1", testPW); err != nil {
			t.Fatal(err)
		}

		db.failOn, db.err = "DROP DATABASE", &mysql.MySQLError{Number: 1010}
		if err := m.Delete(ctx, "shop"); err == nil {
			t.Fatal("no error")
		}

		if len(m.List()) != 1 {
			t.Fatal("database left the registry although the drop failed")
		}

		db.failOn = ""

		if err := m.Delete(ctx, "shop"); err != nil {
			t.Fatal(err)
		}
	})
}

func TestRegistryFailureUndoes(t *testing.T) {
	ctx := t.Context()
	dir := stateDir(t)
	db := &fakeDB{}
	m := newManager(t, dir, db)

	// A directory in place of the temp file makes every save fail.
	if err := os.Mkdir(filepath.Join(dir, "."+StateFile+".tmp"), 0o700); err != nil {
		t.Fatal(err)
	}

	if err := os.WriteFile(filepath.Join(dir, "."+StateFile+".tmp", "x"), nil, 0o600); err != nil {
		t.Fatal(err)
	}

	if _, err := m.Create(ctx, "shop", "shop"); err == nil {
		t.Fatal("no error")
	}

	if last := db.stmts[len(db.stmts)-1]; last != "DROP DATABASE IF EXISTS `shop`" {
		t.Fatalf("no undo: %q", db.stmts)
	}

	if len(m.List()) != 0 {
		t.Fatal("registry and memory disagree")
	}
}

func TestRegistryRejectsBadState(t *testing.T) {
	cases := map[string]string{
		"unknown field":   `{"version":1,"databases":[],"x":1}`,
		"wrong version":   `{"version":2,"databases":[]}`,
		"bad name":        `{"version":1,"databases":[{"name":"my sql","site":"shop","users":[]}]}`,
		"reserved name":   `{"version":1,"databases":[{"name":"mysql","site":"shop","users":[]}]}`,
		"duplicate db":    `{"version":1,"databases":[{"name":"a1","site":"shop","users":[]},{"name":"a1","site":"shop","users":[]}]}`,
		"duplicate user":  `{"version":1,"databases":[{"name":"a1","site":"shop","users":["u1"]},{"name":"a2","site":"shop","users":["u1"]}]}`,
		"reserved user":   `{"version":1,"databases":[{"name":"a1","site":"shop","users":["root"]}]}`,
		"two documents":   `{"version":1,"databases":[]}{}`,
		"reserved site":   `{"version":1,"databases":[{"name":"a1","site":"root","users":[]}]}`,
		"not json at all": `nope`,
	}
	for name, raw := range cases {
		t.Run(name, func(t *testing.T) {
			dir := stateDir(t)
			if err := os.WriteFile(filepath.Join(dir, StateFile), []byte(raw), 0o600); err != nil {
				t.Fatal(err)
			}

			if _, err := New(dir, &fakeDB{}, Options{OwnerUID: uint32(os.Getuid())}); !errors.Is(err, ErrStateInvalid) {
				t.Fatalf("got %v", err)
			}
		})
	}
}

func TestQuoteGrantName(t *testing.T) {
	got, err := quoteGrantName("a_b_c")
	if err != nil || got != "`a\\_b\\_c`" {
		t.Fatalf("got %q, %v", got, err)
	}

	if _, err := quoteGrantName("a%"); !errors.Is(err, ErrInvalid) {
		t.Fatalf("wildcard accepted: %v", err)
	}
}
