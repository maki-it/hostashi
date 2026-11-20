// SPDX-License-Identifier: AGPL-3.0-or-later

package store

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"testing/fstest"
)

func dbPath(t *testing.T) string {
	t.Helper()

	dir := filepath.Join(t.TempDir(), "lib")
	if err := os.Mkdir(dir, 0o750); err != nil {
		t.Fatal(err)
	}

	return filepath.Join(dir, "hostashi.db")
}

func open(t *testing.T, path string) *Store {
	t.Helper()

	s, err := Open(context.Background(), path)
	if err != nil {
		t.Fatal(err)
	}

	t.Cleanup(func() { _ = s.Close() })

	return s
}

func TestOpenCreatesDatabase(t *testing.T) {
	path := dbPath(t)
	s := open(t, path)
	ctx := context.Background()

	fi, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}

	if fi.Mode().Perm() != 0o600 {
		t.Fatalf("mode %v, want 0600", fi.Mode().Perm())
	}

	all, err := loadMigrations(migrations)
	if err != nil {
		t.Fatal(err)
	}

	// A fresh database is at the newest embedded migration.
	v, err := s.SchemaVersion(ctx)
	if err != nil || v != len(all) {
		t.Fatalf("version %d, %v; want %d", v, err, len(all))
	}

	if err := s.Ping(ctx); err != nil {
		t.Fatal(err)
	}

	// WAL side files inherit the restrictive mode from the database.
	for _, side := range []string{"-wal", "-shm"} {
		if fi, err := os.Stat(path + side); err == nil && fi.Mode().Perm()&0o077 != 0 {
			t.Fatalf("%s has mode %v", side, fi.Mode().Perm())
		}
	}
}

func TestReopenIsIdempotent(t *testing.T) {
	path := dbPath(t)
	s := open(t, path)
	ctx := context.Background()

	if _, err := s.w.ExecContext(ctx, `INSERT INTO users (username) VALUES ('kim')`); err != nil {
		t.Fatal(err)
	}

	_ = s.Close()

	s2 := open(t, path)

	var n int
	if err := s2.db.QueryRowContext(ctx, `SELECT count(*) FROM users`).Scan(&n); err != nil || n != 1 {
		t.Fatalf("users %d, %v", n, err)
	}
}

func TestConcurrentOpen(t *testing.T) {
	path := dbPath(t)

	var wg sync.WaitGroup

	errs := make(chan error, 4)

	for range 4 {
		wg.Go(func() {
			s, err := Open(context.Background(), path)
			if err == nil {
				_ = s.Close()
			}

			errs <- err
		})
	}

	wg.Wait()
	close(errs)

	for err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}
}

func TestSchemaConstraints(t *testing.T) {
	s := open(t, dbPath(t))
	ctx := context.Background()

	if _, err := s.w.ExecContext(ctx, `INSERT INTO users (username) VALUES ('kim')`); err != nil {
		t.Fatal(err)
	}

	bad := map[string]string{
		"duplicate user":     `INSERT INTO users (username) VALUES ('kim')`,
		"empty user":         `INSERT INTO users (username) VALUES ('')`,
		"strict types":       `INSERT INTO users (username, disabled) VALUES ('a', 'yes')`,
		"disabled range":     `INSERT INTO users (username, disabled) VALUES ('b', 2)`,
		"audit invalid json": `INSERT INTO audit_log (action, detail) VALUES ('x', 'not json')`,
		"audit empty action": `INSERT INTO audit_log (action) VALUES ('')`,
		"audit unknown user": `INSERT INTO audit_log (action, user_id) VALUES ('x', 999)`,
	}
	for name, q := range bad {
		t.Run(name, func(t *testing.T) {
			if _, err := s.w.ExecContext(ctx, q); err == nil {
				t.Fatal("accepted")
			}
		})
	}

	// Deleting a user keeps its audit entries.
	if _, err := s.w.ExecContext(ctx, `INSERT INTO audit_log (action, user_id) VALUES ('login', 1)`); err != nil {
		t.Fatal(err)
	}

	if _, err := s.w.ExecContext(ctx, `DELETE FROM users WHERE id = 1`); err != nil {
		t.Fatal(err)
	}

	var n int
	if err := s.db.QueryRowContext(ctx, `SELECT count(*) FROM audit_log WHERE user_id IS NULL`).Scan(&n); err != nil || n != 1 {
		t.Fatalf("audit rows %d, %v", n, err)
	}
}

func TestSchemaTooNew(t *testing.T) {
	path := dbPath(t)
	s := open(t, path)

	if _, err := s.w.ExecContext(context.Background(), `PRAGMA user_version = 99`); err != nil {
		t.Fatal(err)
	}

	_ = s.Close()

	if _, err := Open(context.Background(), path); !errors.Is(err, ErrSchemaTooNew) {
		t.Fatalf("want ErrSchemaTooNew, got %v", err)
	}
}

func TestInsecurePermissions(t *testing.T) {
	t.Run("world writable directory", func(t *testing.T) {
		path := dbPath(t)
		if err := os.Chmod(filepath.Dir(path), 0o777); err != nil {
			t.Fatal(err)
		}

		if _, err := Open(context.Background(), path); !errors.Is(err, ErrInsecurePermissions) {
			t.Fatalf("got %v", err)
		}
	})

	t.Run("readable file", func(t *testing.T) {
		path := dbPath(t)
		if err := os.WriteFile(path, nil, 0o644); err != nil {
			t.Fatal(err)
		}

		if err := os.Chmod(path, 0o644); err != nil { // umask may have narrowed it
			t.Fatal(err)
		}

		if _, err := Open(context.Background(), path); !errors.Is(err, ErrInsecurePermissions) {
			t.Fatalf("got %v", err)
		}
	})
}

func TestOpenRejectsBadPaths(t *testing.T) {
	path := dbPath(t)
	if err := os.Mkdir(path, 0o700); err != nil {
		t.Fatal(err)
	}

	for name, p := range map[string]string{
		"empty":             "",
		"directory":         path,
		"missing directory": filepath.Join(t.TempDir(), "nope", "h.db"),
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := Open(context.Background(), p); err == nil {
				t.Fatal("accepted")
			}
		})
	}
}

func TestOpenRejectsGarbageFile(t *testing.T) {
	path := dbPath(t)
	if err := os.WriteFile(path, []byte(strings.Repeat("not a database ", 100)), 0o600); err != nil {
		t.Fatal(err)
	}

	if _, err := Open(context.Background(), path); err == nil {
		t.Fatal("garbage file accepted")
	}
}

func TestLoadMigrationsRejects(t *testing.T) {
	sql := &fstest.MapFile{Data: []byte("SELECT 1;")}

	cases := map[string]fstest.MapFS{
		"bad name":    {"migrations/0001_Init.sql": sql},
		"no number":   {"migrations/init.sql": sql},
		"gap":         {"migrations/0001_a.sql": sql, "migrations/0003_c.sql": sql},
		"duplicate":   {"migrations/0001_a.sql": sql, "migrations/0001_b.sql": sql},
		"start at 2":  {"migrations/0002_a.sql": sql},
		"no dir":      {"other/0001_a.sql": sql},
		"wrong ext":   {"migrations/0001_a.txt": sql},
		"subdir name": {"migrations/0001_a.sql/x": sql},
	}
	for name, fsys := range cases {
		t.Run(name, func(t *testing.T) {
			if _, err := loadMigrations(fsys); err == nil {
				t.Fatal("accepted")
			}
		})
	}
}

func TestFailedMigrationRollsBack(t *testing.T) {
	path := dbPath(t)
	s := open(t, path)
	ctx := context.Background()

	base, err := s.SchemaVersion(ctx)
	if err != nil {
		t.Fatal(err)
	}

	// The database is already at the current version, so pad the fake
	// history with no-op migrations up to it, then add one good and one bad.
	fsys := fstest.MapFS{}
	for i := 1; i <= base; i++ {
		fsys[fmt.Sprintf("migrations/%04d_noop.sql", i)] = &fstest.MapFile{Data: []byte("SELECT 1;")}
	}

	fsys[fmt.Sprintf("migrations/%04d_ok.sql", base+1)] = &fstest.MapFile{Data: []byte("CREATE TABLE t2 (x INTEGER) STRICT;")}
	fsys[fmt.Sprintf("migrations/%04d_bad.sql", base+2)] = &fstest.MapFile{Data: []byte(
		"CREATE TABLE t3 (x INTEGER) STRICT; INSERT INTO missing VALUES (1);")}

	if err := migrate(ctx, s.w, fsys); err == nil || !strings.Contains(err.Error(), "_bad.sql") {
		t.Fatalf("want error naming the bad migration, got %v", err)
	}

	v, err := s.SchemaVersion(ctx)
	if err != nil || v != base+1 {
		t.Fatalf("version %d, %v; want %d", v, err, base+1)
	}

	var n int
	if err := s.db.QueryRowContext(ctx, `SELECT count(*) FROM sqlite_schema WHERE name = 't3'`).Scan(&n); err != nil || n != 0 {
		t.Fatalf("partial migration left table t3 (%d, %v)", n, err)
	}

	if err := s.db.QueryRowContext(ctx, `SELECT count(*) FROM sqlite_schema WHERE name = 't2'`).Scan(&n); err != nil || n != 1 {
		t.Fatalf("good migration was not kept (%d, %v)", n, err)
	}
}

func TestCopyTo(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()

	st, err := Open(ctx, filepath.Join(dir, "hostashi.db"))
	if err != nil {
		t.Fatal(err)
	}

	defer st.Close()

	dst := filepath.Join(dir, "copy.db")
	for range 2 { // the second copy replaces the first
		if err := st.CopyTo(ctx, dst); err != nil {
			t.Fatal(err)
		}
	}

	fi, err := os.Stat(dst)
	if err != nil || fi.Mode().Perm() != 0o600 {
		t.Fatalf("copy: %v %v", fi, err)
	}

	cp, err := Open(ctx, dst)
	if err != nil {
		t.Fatalf("copy does not open: %v", err)
	}

	defer cp.Close()

	if v, err := cp.SchemaVersion(ctx); err != nil || v < 1 {
		t.Errorf("copy schema %d, %v", v, err)
	}

	if err := st.CopyTo(ctx, "relative.db"); err == nil {
		t.Error("relative path accepted")
	}
}
