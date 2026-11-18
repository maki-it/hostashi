// SPDX-License-Identifier: AGPL-3.0-or-later

package store

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"
)

func TestCreateFirstUserOnlyOnce(t *testing.T) {
	s := open(t, dbPath(t))
	ctx := context.Background()

	u, err := s.CreateFirstUser(ctx, "kim", "hash")
	if err != nil || u.ID == 0 || u.Role != "admin" {
		t.Fatalf("%+v %v", u, err)
	}

	if _, err := s.CreateFirstUser(ctx, "other", "hash"); !errors.Is(err, ErrSetupDone) {
		t.Fatalf("want ErrSetupDone, got %v", err)
	}

	got, err := s.UserByUsername(ctx, "kim")
	if err != nil || got.ID != u.ID || got.PasswordHash != "hash" || got.Disabled {
		t.Fatalf("%+v %v", got, err)
	}

	if _, err := s.UserByUsername(ctx, "missing"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("want ErrNotFound, got %v", err)
	}
}

func TestCreateFirstUserRace(t *testing.T) {
	s := open(t, dbPath(t))

	var wg sync.WaitGroup

	results := make(chan error, 8)

	for i := range 8 {
		wg.Go(func() {
			_, err := s.CreateFirstUser(context.Background(), "user"+string(rune('a'+i)), "hash")
			results <- err
		})
	}

	wg.Wait()
	close(results)

	ok := 0

	for err := range results {
		if err == nil {
			ok++
		} else if !errors.Is(err, ErrSetupDone) {
			t.Fatal(err)
		}
	}

	if ok != 1 {
		t.Fatalf("%d setups succeeded, want exactly 1", ok)
	}
}

func TestSessions(t *testing.T) {
	s := open(t, dbPath(t))
	ctx := context.Background()
	now := time.Unix(10000, 0)

	u, err := s.CreateFirstUser(ctx, "kim", "hash")
	if err != nil {
		t.Fatal(err)
	}

	hash := make([]byte, 32)
	hash[0] = 1

	if err := s.CreateSession(ctx, u.ID, hash, now, now.Add(time.Hour)); err != nil {
		t.Fatal(err)
	}

	if err := s.CreateSession(ctx, u.ID, hash, now, now.Add(time.Hour)); err == nil {
		t.Fatal("duplicate token hash accepted")
	}

	if err := s.CreateSession(ctx, u.ID, []byte("short"), now, now.Add(time.Hour)); err == nil {
		t.Fatal("short token hash accepted")
	}

	if err := s.CreateSession(ctx, 999, make([]byte, 32), now, now.Add(time.Hour)); err == nil {
		t.Fatal("session for unknown user accepted")
	}

	if got, err := s.SessionUser(ctx, hash, now.Add(time.Minute)); err != nil || got.Username != "kim" {
		t.Fatalf("%+v %v", got, err)
	}

	if _, err := s.SessionUser(ctx, hash, now.Add(time.Hour)); !errors.Is(err, ErrNotFound) {
		t.Fatalf("expired session valid: %v", err)
	}

	n, err := s.DeleteExpiredSessions(ctx, now.Add(2*time.Hour))
	if err != nil || n != 1 {
		t.Fatalf("deleted %d, %v", n, err)
	}

	// Deleting a user removes its sessions.
	if err := s.CreateSession(ctx, u.ID, hash, now, now.Add(time.Hour)); err != nil {
		t.Fatal(err)
	}

	if err := s.RawExec(ctx, `DELETE FROM users`); err != nil {
		t.Fatal(err)
	}

	if _, err := s.SessionUser(ctx, hash, now); !errors.Is(err, ErrNotFound) {
		t.Fatalf("session survived user deletion: %v", err)
	}
}

func TestAuditLogIsAppendOnly(t *testing.T) {
	s := open(t, dbPath(t))
	ctx := context.Background()

	u, err := s.CreateFirstUser(ctx, "kim", "hash")
	if err != nil {
		t.Fatal(err)
	}

	if err := s.Audit(ctx, &u.ID, "login", "kim", map[string]string{"ip": "10.0.0.1"}); err != nil {
		t.Fatal(err)
	}

	if err := s.Audit(ctx, nil, "login.failed", "x", nil); err != nil {
		t.Fatal(err)
	}

	for name, q := range map[string]string{
		"update action": `UPDATE audit_log SET action = 'x'`,
		"update detail": `UPDATE audit_log SET detail = '{}'`,
		"update at":     `UPDATE audit_log SET at = '2000-01-01'`,
		"delete":        `DELETE FROM audit_log`,
	} {
		t.Run(name, func(t *testing.T) {
			if err := s.RawExec(ctx, q); err == nil {
				t.Fatal("audit log was modified")
			}
		})
	}

	// Deleting the user still works and keeps the entry.
	if err := s.RawExec(ctx, `DELETE FROM users WHERE id = ?`, u.ID); err != nil {
		t.Fatal(err)
	}

	var n int
	if err := s.RawCount(ctx, `SELECT count(*) FROM audit_log WHERE user_id IS NULL AND 1 = ?`, 1, &n); err != nil || n != 2 {
		t.Fatalf("audit rows without user: %d %v", n, err)
	}
}
