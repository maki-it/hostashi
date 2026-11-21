// SPDX-License-Identifier: AGPL-3.0-or-later

package store

import (
	"context"
	"errors"
	"testing"
)

func TestNotificationPrefs(t *testing.T) {
	s := open(t, dbPath(t))
	ctx := context.Background()

	kim, err := s.CreateFirstUser(ctx, "kim", "hash")
	if err != nil {
		t.Fatal(err)
	}

	if err := s.RawExec(ctx, `INSERT INTO users (username, password_hash, role, email) VALUES ('ada', 'h', 'manager', 'ada@example.com'), ('max', 'h', 'user', '')`); err != nil {
		t.Fatal(err)
	}

	if err := s.RawExec(ctx, `INSERT INTO users (username, password_hash, role, email, disabled) VALUES ('old', 'h', 'admin', 'old@example.com', 1)`); err != nil {
		t.Fatal(err)
	}

	if err := s.RawExec(ctx, `UPDATE users SET email = 'kim@example.com' WHERE id = ?`, kim.ID); err != nil {
		t.Fatal(err)
	}

	if p, err := s.NotificationPrefs(ctx, kim.ID); err != nil || len(p) != 0 {
		t.Fatalf("empty: %v %v", p, err)
	}

	if err := s.SetNotificationPrefs(ctx, kim.ID, map[string]bool{"backups": false, "php": true}); err != nil {
		t.Fatal(err)
	}

	if err := s.SetNotificationPrefs(ctx, kim.ID, map[string]bool{"php": false}); err != nil {
		t.Fatal(err)
	}

	p, err := s.NotificationPrefs(ctx, kim.ID)
	if err != nil || len(p) != 2 || p["backups"] || p["php"] {
		t.Fatalf("prefs: %v %v", p, err)
	}

	if err := s.SetNotificationPrefs(ctx, 999, map[string]bool{"php": true}); !errors.Is(err, ErrNotFound) {
		t.Fatalf("unknown user: %v", err)
	}

	if err := s.SetNotificationPrefs(ctx, kim.ID, map[string]bool{"mail": true}); err == nil {
		t.Fatal("unknown category stored")
	}

	list, err := s.NotificationRecipients(ctx)
	if err != nil {
		t.Fatal(err)
	}

	// Only enabled accounts with an address, each once.
	if len(list) != 2 || list[0].Username != "ada" || list[1].Username != "kim" || len(list[0].Prefs) != 0 || len(list[1].Prefs) != 2 {
		t.Fatalf("%+v", list)
	}

	if list[1].Role != "admin" || list[1].Email != "kim@example.com" {
		t.Fatalf("%+v", list[1])
	}
}
