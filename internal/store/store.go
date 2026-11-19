// SPDX-License-Identifier: AGPL-3.0-or-later

// Package store holds the panel state in SQLite (WAL mode) and applies the
// embedded schema migrations. Only hostashi serve opens the database.
package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"time"

	"modernc.org/sqlite" // pure Go driver, no cgo (ADR-0003)
	sqlite3 "modernc.org/sqlite/lib"
)

// DefaultPath is where the database lives on a production server.
const DefaultPath = "/var/lib/hostashi/hostashi.db"

// ErrInsecurePermissions is returned when the database file or its directory
// is accessible to other users.
var ErrInsecurePermissions = errors.New("store: database file must not be accessible to group or others")

// Store wraps the database handle.
type Store struct {
	db *sql.DB
}

// Open opens or creates the database at path and applies all pending
// migrations. A new file is created with mode 0600. An existing file that
// group or others can access is refused, because the database holds audit
// data and encrypted secrets.
func Open(ctx context.Context, path string) (*Store, error) {
	if err := prepareFile(path); err != nil {
		return nil, err
	}

	q := url.Values{}
	// Pragmas run in this order on every new connection. busy_timeout must
	// come first: switching a fresh file to WAL needs a lock, and without the
	// timeout a second process opening at the same moment fails at once
	// with SQLITE_BUSY.
	for _, p := range []string{
		"busy_timeout(5000)",
		"journal_mode(WAL)",
		"foreign_keys(ON)",
		"synchronous(NORMAL)",
	} {
		q.Add("_pragma", p)
	}

	q.Set("_txlock", "immediate")

	db, err := sql.Open("sqlite", "file:"+path+"?"+q.Encode())
	if err != nil {
		return nil, fmt.Errorf("store: open: %w", err)
	}

	s := &Store{db: db}
	if err := s.prepare(ctx); err != nil {
		_ = db.Close()
		return nil, err
	}

	return s, nil
}

// openAttempts bounds the retries while another process creates the file.
const openAttempts = 20

// prepare checks the connection and migrates. Switching a new file to WAL
// takes an exclusive lock, and SQLite answers SQLITE_BUSY at once instead of
// waiting (deadlock avoidance), so two processes opening a fresh database at
// the same moment retry here for up to about five seconds.
func (s *Store) prepare(ctx context.Context) error {
	for attempt := 1; ; attempt++ {
		err := s.check(ctx)
		if err == nil {
			err = migrate(ctx, s.db, migrations)
		}

		if err == nil || !isBusy(err) || attempt == openAttempts {
			return err
		}

		select {
		case <-ctx.Done():
			return fmt.Errorf("store: %w", ctx.Err())
		case <-time.After(time.Duration(attempt) * 25 * time.Millisecond):
		}
	}
}

func isBusy(err error) bool {
	var e *sqlite.Error

	return errors.As(err, &e) && e.Code()&0xff == sqlite3.SQLITE_BUSY
}

// prepareFile creates the file with mode 0600 if it does not exist and
// refuses existing files or directories that leak access.
func prepareFile(path string) error {
	if path == "" {
		return errors.New("store: empty database path")
	}

	dir := filepath.Dir(path)

	fi, err := os.Stat(dir)
	if err != nil {
		return fmt.Errorf("store: database directory: %w", err)
	}

	if !fi.IsDir() || fi.Mode().Perm()&0o022 != 0 {
		return fmt.Errorf("%w: directory %s has mode %v", ErrInsecurePermissions, dir, fi.Mode().Perm())
	}

	f, err := os.OpenFile(path, os.O_RDWR|os.O_CREATE, 0o600)
	if err != nil {
		return fmt.Errorf("store: %w", err)
	}

	fi, err = f.Stat()
	_ = f.Close()

	if err != nil {
		return fmt.Errorf("store: %w", err)
	}

	if !fi.Mode().IsRegular() {
		return fmt.Errorf("store: %s is not a regular file", path)
	}

	if fi.Mode().Perm()&0o077 != 0 {
		return fmt.Errorf("%w: %s has mode %v", ErrInsecurePermissions, path, fi.Mode().Perm())
	}

	return nil
}

// check verifies that the connection works and the pragmas took effect.
func (s *Store) check(ctx context.Context) error {
	var mode string
	if err := s.db.QueryRowContext(ctx, "PRAGMA journal_mode").Scan(&mode); err != nil {
		return fmt.Errorf("store: %w", err)
	}

	if mode != "wal" {
		return fmt.Errorf("store: journal_mode is %q, want wal", mode)
	}

	var fk int
	if err := s.db.QueryRowContext(ctx, "PRAGMA foreign_keys").Scan(&fk); err != nil {
		return fmt.Errorf("store: %w", err)
	}

	if fk != 1 {
		return errors.New("store: foreign keys are not enabled")
	}

	return nil
}

// Ping checks that the database answers.
func (s *Store) Ping(ctx context.Context) error {
	return s.db.PingContext(ctx)
}

// SchemaVersion returns the applied schema version.
func (s *Store) SchemaVersion(ctx context.Context) (int, error) {
	return userVersion(ctx, s.db)
}

// Close closes the database.
func (s *Store) Close() error {
	return s.db.Close()
}

// CopyTo writes a consistent copy of the database to path with VACUUM INTO
// (ADR-0008: the backup reads this copy, never the live file). The copy is
// made next to path and renamed over it, so a reader sees the old or the
// new copy, never half of one.
func (s *Store) CopyTo(ctx context.Context, path string) error {
	if !filepath.IsAbs(path) {
		return fmt.Errorf("store: copy path %q", path)
	}

	tmp := path + ".tmp"
	_ = os.Remove(tmp)

	if _, err := s.db.ExecContext(ctx, "VACUUM INTO ?", tmp); err != nil {
		_ = os.Remove(tmp)

		return fmt.Errorf("store: copy: %w", err)
	}

	if err := os.Chmod(tmp, 0o600); err != nil {
		_ = os.Remove(tmp)

		return fmt.Errorf("store: copy: %w", err)
	}

	if err := os.Rename(tmp, path); err != nil {
		_ = os.Remove(tmp)

		return fmt.Errorf("store: copy: %w", err)
	}

	return nil
}
