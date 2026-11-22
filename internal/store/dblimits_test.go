// SPDX-License-Identifier: AGPL-3.0-or-later

package store

import (
	"context"
	"io/fs"
	"strconv"
	"strings"
	"testing"
	"testing/fstest"
	"time"
)

// migrationsBefore returns the embedded migrations older than the one
// whose file name contains name.
func migrationsBefore(t *testing.T, name string) fstest.MapFS {
	t.Helper()

	old := fstest.MapFS{}
	found := false

	err := fs.WalkDir(embedded, "migrations", func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}

		if strings.Contains(p, name) {
			found = true
		}

		if found {
			return nil
		}

		b, err := fs.ReadFile(embedded, p)
		if err != nil {
			return err
		}

		old[p] = &fstest.MapFile{Data: b}

		return nil
	})
	if err != nil || !found {
		t.Fatalf("migrations before %s: %v (found %v)", name, err, found)
	}

	return old
}

// The database limits migration (ADR-0057) fills the seeded presets and
// the sites that follow them, and marks those for the next apply.
func TestDatabaseLimitsMigration(t *testing.T) {
	migrations = migrationsBefore(t, "database_limits")

	t.Cleanup(func() { migrations = embedded })

	ctx := context.Background()
	p := dbPath(t)
	now := time.Unix(2_000_000, 0)

	s, err := Open(ctx, p)
	if err != nil {
		t.Fatal(err)
	}

	// The code of this version knows the new columns, so the old state is
	// written with SQL.
	pre := []string{
		`INSERT INTO limit_presets (name, disk_bytes, disk_inodes, memory_bytes, php_workers, tasks, cpu_priority, created_at, updated_at)
			VALUES ('own', 1, 1, 0, 5, 0, 'normal', 1, 1)`,
	}

	presetOf := map[string]string{"follows": "(SELECT id FROM limit_presets WHERE name = 'small')", "custom": "NULL", "ownpreset": "(SELECT id FROM limit_presets WHERE name = 'own')"}
	ids := map[string]int64{}

	for name := range presetOf {
		site, err := s.CreateSite(ctx, name, "default", "", []string{name + ".example.com"}, now)
		if err != nil {
			t.Fatal(err)
		}

		ids[name] = site.ID
		pre = append(pre, `INSERT INTO site_limits (site_id, preset_id, disk_bytes, disk_inodes, memory_bytes, php_workers, tasks, cpu_priority, applied_at, updated_at)
			VALUES (`+strconv.FormatInt(site.ID, 10)+`, `+presetOf[name]+`, 1, 1, 0, 5, 0, 'normal', 1000, 1)`)
	}

	for _, q := range pre {
		if err := s.RawExec(ctx, q); err != nil {
			t.Fatal(err)
		}
	}

	_ = s.Close()

	migrations = embedded

	s = open(t, p)

	for name, want := range map[string][2]int64{"small": {2 << 30, 20}, "medium": {5 << 30, 40}, "large": {10 << 30, 80}, "own": {0, 0}} {
		got, err := s.LimitPreset(ctx, name)
		if err != nil || got.Limits.DatabaseBytes != want[0] || got.Limits.DatabaseConnections != want[1] {
			t.Errorf("preset %s: %+v %v", name, got.Limits, err)
		}
	}

	follows, _, _ := s.SiteLimits(ctx, ids["follows"])
	if follows.Limits.DatabaseBytes != 2<<30 || follows.Limits.DatabaseConnections != 20 || !follows.AppliedAt.IsZero() {
		t.Errorf("follower: %+v", follows)
	}

	for _, name := range []string{"custom", "ownpreset"} {
		got, _, _ := s.SiteLimits(ctx, ids[name])
		if got.Limits.DatabaseBytes != 0 || got.Limits.DatabaseConnections != 0 || got.AppliedAt.IsZero() {
			t.Errorf("%s: %+v", name, got)
		}
	}
}

func TestDatabaseLimitsRoundTrip(t *testing.T) {
	s := open(t, dbPath(t))
	ctx := t.Context()
	now := time.Unix(2_000_000, 0)

	l := Limits{DiskBytes: 1 << 30, CPUPriority: "normal", PHPWorkers: 5, DatabaseBytes: 3 << 30, DatabaseConnections: 15}

	p, err := s.CreateLimitPreset(ctx, "db", l, PresetCaches{}, false, now)
	if err != nil || p.Limits != l {
		t.Fatalf("create: %+v %v", p.Limits, err)
	}

	site, err := s.CreateSite(ctx, "shop", "default", "", []string{"shop.example.com"}, now)
	if err != nil {
		t.Fatal(err)
	}

	if err := s.SetSiteLimits(ctx, site.ID, p.ID, l, now); err != nil {
		t.Fatal(err)
	}

	l.DatabaseConnections = 30
	if _, err := s.UpdateLimitPreset(ctx, p.ID, "db", l, PresetCaches{}, false, now); err != nil {
		t.Fatal(err)
	}

	if got, _ := s.LimitPreset(ctx, "db"); got.Limits.DatabaseConnections != 30 {
		t.Errorf("preset: %+v", got.Limits)
	}

	if got, _, _ := s.SiteLimits(ctx, site.ID); got.Limits != l {
		t.Errorf("follower: %+v", got.Limits)
	}
}
