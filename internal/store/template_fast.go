// SPDX-License-Identifier: AGPL-3.0-or-later

//go:build hostashi_fasthash

package store

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sync"
	"testing"
)

// Migrating a fresh database runs every migration, which takes about two
// seconds under the race detector because the SQLite driver is C translated
// to Go. Most tests open a fresh database, so with the hostashi_fasthash tag
// a new database file starts as a copy of one migrated database per test
// binary. Open still runs migrate on it (a no-op) and all its checks.
// TestFreshMigration runs the real migrations without the copy.
func init() {
	if !testing.Testing() {
		panic("store: built with the hostashi_fasthash tag, which is for tests only")
	}

	seedFile = seedFromTemplate
}

var template struct {
	once sync.Once
	data []byte
	err  error
}

// seedFromTemplate writes the migrated template to path if no file is
// there. The file appears whole or not at all (hard link of a finished
// temporary file), so concurrent opens of the same path never see a
// partial copy.
func seedFromTemplate(path string) error {
	// Tests that swap in older migrations get exactly those.
	if migrations != embedded {
		return nil
	}

	if _, err := os.Lstat(path); !errors.Is(err, fs.ErrNotExist) {
		return nil // an existing path, or one prepareFile reports on
	}

	template.once.Do(buildTemplate)

	if template.err != nil {
		return template.err
	}

	tmp, err := os.CreateTemp(filepath.Dir(path), ".seed-*")
	if err != nil {
		return fmt.Errorf("store: seed: %w", err)
	}

	defer func() { _ = os.Remove(tmp.Name()) }()

	_, err = tmp.Write(template.data)
	if cerr := tmp.Close(); err == nil {
		err = cerr
	}

	if err != nil {
		return fmt.Errorf("store: seed: %w", err)
	}

	if err := os.Link(tmp.Name(), path); err != nil && !errors.Is(err, fs.ErrExist) {
		return fmt.Errorf("store: seed: %w", err)
	}

	return nil
}

func buildTemplate() {
	dir, err := os.MkdirTemp("", "hostashi-store-template-")
	if err != nil {
		template.err = fmt.Errorf("store: template: %w", err)

		return
	}

	defer func() { _ = os.RemoveAll(dir) }()

	p := filepath.Join(dir, "template.db")

	s, err := openSeeded(context.Background(), p, nil)
	if err != nil {
		template.err = fmt.Errorf("store: template: %w", err)

		return
	}

	// Everything into the main file, so copying that one file is enough.
	_, err = s.w.ExecContext(context.Background(), "PRAGMA wal_checkpoint(TRUNCATE)")
	if cerr := s.Close(); err == nil {
		err = cerr
	}

	if err == nil {
		template.data, err = os.ReadFile(p)
	}

	if err != nil {
		template.err = fmt.Errorf("store: template: %w", err)
	}
}
