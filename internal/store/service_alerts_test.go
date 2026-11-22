// SPDX-License-Identifier: AGPL-3.0-or-later

package store

import (
	"context"
	"database/sql"
	"io/fs"
	"testing"
	"testing/fstest"
	"time"
)

// TestServiceAlertsMigration: 0046 rebuilds two tables (ADR-0055) and must
// keep their rows.
func TestServiceAlertsMigration(t *testing.T) {
	ctx := context.Background()
	old := fstest.MapFS{}

	entries, err := fs.ReadDir(embedded, "migrations")
	if err != nil {
		t.Fatal(err)
	}

	for _, e := range entries {
		if e.Name() < "0046" {
			b, err := fs.ReadFile(embedded, "migrations/"+e.Name())
			if err != nil {
				t.Fatal(err)
			}

			old["migrations/"+e.Name()] = &fstest.MapFile{Data: b}
		}
	}

	db, err := sql.Open("sqlite", dsn(dbPath(t), false))
	if err != nil {
		t.Fatal(err)
	}

	t.Cleanup(func() { _ = db.Close() })

	if err := migrate(ctx, db, old); err != nil {
		t.Fatal(err)
	}

	for _, q := range []string{
		`INSERT INTO users (id, username, password_hash, role) VALUES (7, 'kim', 'x', 'admin')`,
		`INSERT INTO notification_prefs (user_id, category, enabled) VALUES (7, 'backups', 0)`,
		`INSERT INTO service_events (at, service, site, kind, detail, user_id) VALUES (100, 'mariadb', '', 'action', 'stop', 7)`,
	} {
		if _, err := db.ExecContext(ctx, q); err != nil {
			t.Fatalf("%s: %v", q, err)
		}
	}

	if _, err := db.ExecContext(ctx, `INSERT INTO service_events (at, service, kind) VALUES (1, 'nginx', 'recovered')`); err == nil {
		t.Fatal("recovered accepted before 0046")
	}

	if err := migrate(ctx, db, embedded); err != nil {
		t.Fatal(err)
	}

	var n int
	if err := db.QueryRowContext(ctx, `SELECT count(*) FROM notification_prefs WHERE user_id = 7 AND category = 'backups' AND enabled = 0`).Scan(&n); err != nil || n != 1 {
		t.Fatalf("prefs kept: %d %v", n, err)
	}

	if err := db.QueryRowContext(ctx, `SELECT count(*) FROM service_events WHERE service = 'mariadb' AND user_id = 7`).Scan(&n); err != nil || n != 1 {
		t.Fatalf("events kept: %d %v", n, err)
	}

	for _, q := range []string{
		`INSERT INTO notification_prefs (user_id, category, enabled) VALUES (7, 'services', 1)`,
		`INSERT INTO service_events (at, service, kind) VALUES (200, 'nginx', 'recovered')`,
	} {
		if _, err := db.ExecContext(ctx, q); err != nil {
			t.Fatalf("%s: %v", q, err)
		}
	}

	// The foreign keys still work after the rebuild.
	if _, err := db.ExecContext(ctx, `DELETE FROM users WHERE id = 7`); err != nil {
		t.Fatal(err)
	}

	if err := db.QueryRowContext(ctx, `SELECT count(*) FROM notification_prefs`).Scan(&n); err != nil || n != 0 {
		t.Fatalf("prefs cascade: %d %v", n, err)
	}

	var uid sql.NullInt64
	if err := db.QueryRowContext(ctx, `SELECT user_id FROM service_events WHERE service = 'mariadb'`).Scan(&uid); err != nil || uid.Valid {
		t.Fatalf("events set null: %v %v", uid, err)
	}
}

func TestRecoveredEvent(t *testing.T) {
	s := open(t, dbPath(t))
	ctx := context.Background()

	if err := s.AddServiceEvent(ctx, ServiceEvent{At: time.Unix(300, 0), Service: "nginx", Kind: ServiceEventRecovered}); err != nil {
		t.Fatal(err)
	}

	got, err := s.ServiceEvents(ctx, ServiceEventFilter{})
	if err != nil || len(got) != 1 || got[0].Kind != ServiceEventRecovered {
		t.Fatalf("%+v %v", got, err)
	}
}
