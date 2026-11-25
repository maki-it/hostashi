// SPDX-License-Identifier: AGPL-3.0-or-later

package store

import (
	"context"
	"testing"
)

// TestFreshMigration migrates an empty file with every migration, also in
// test builds where Open starts new files from a migrated copy
// (template_fast.go).
func TestFreshMigration(t *testing.T) {
	ctx := context.Background()

	s, err := openSeeded(ctx, dbPath(t), nil)
	if err != nil {
		t.Fatal(err)
	}

	defer func() { _ = s.Close() }()

	ms, err := loadMigrations(embedded)
	if err != nil {
		t.Fatal(err)
	}

	got, err := userVersion(ctx, s.w)
	if err != nil {
		t.Fatal(err)
	}

	if want := ms[len(ms)-1].version; got != want {
		t.Fatalf("user_version = %d, want %d", got, want)
	}
}
