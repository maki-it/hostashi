// SPDX-License-Identifier: AGPL-3.0-or-later

package store

import (
	"context"
	"errors"
	"testing"
	"time"
)

func inv(b byte, now time.Time) Invitation {
	return Invitation{TokenHash: make32(b), Created: now, Expires: now.Add(24 * time.Hour)}
}

func ptr[T any](v T) *T { return &v }

func TestCreateAndAcceptInvitation(t *testing.T) {
	s := open(t, dbPath(t))
	ctx := context.Background()
	now := time.Unix(1_800_000_000, 0)

	if _, err := s.CreateFirstUser(ctx, "kim", "hash"); err != nil {
		t.Fatal(err)
	}

	a, err := s.CreateAccount(ctx, "anna", "manager", inv(7, now))
	if err != nil {
		t.Fatal(err)
	}

	if a.Role != "manager" || !a.Invited || a.InviteEnd == nil || !a.InviteEnd.Equal(now.Add(24*time.Hour)) {
		t.Fatalf("account %+v", a)
	}

	if _, err := s.CreateAccount(ctx, "anna", "user", inv(8, now)); !errors.Is(err, ErrUserExists) {
		t.Fatalf("duplicate: %v", err)
	}

	if _, err := s.CreateAccount(ctx, "bob", "root", inv(9, now)); err == nil {
		t.Fatal("unknown role accepted")
	}

	// The failed create left nothing behind.
	if all, _ := s.Accounts(ctx); len(all) != 2 {
		t.Fatalf("accounts %+v", all)
	}

	if _, err := s.InvitedUser(ctx, make32(7), now.Add(25*time.Hour)); !errors.Is(err, ErrNotFound) {
		t.Fatalf("expired link: %v", err)
	}

	if u, err := s.InvitedUser(ctx, make32(7), now); err != nil || u.Username != "anna" {
		t.Fatalf("invited %v %v", u.Username, err)
	}

	if _, err := s.AcceptInvitation(ctx, make32(6), "h2", now); !errors.Is(err, ErrNotFound) {
		t.Fatalf("wrong link: %v", err)
	}

	u, err := s.AcceptInvitation(ctx, make32(7), "h2", now)
	if err != nil || u.PasswordHash != "h2" {
		t.Fatalf("accept %+v %v", u, err)
	}

	// A link works once.
	if _, err := s.AcceptInvitation(ctx, make32(7), "h3", now); !errors.Is(err, ErrNotFound) {
		t.Fatalf("second use: %v", err)
	}

	if a, _ := s.AccountByID(ctx, u.ID); a.Invited {
		t.Fatalf("still invited %+v", a)
	}
}

func TestDisabledAccountCannotAccept(t *testing.T) {
	s := open(t, dbPath(t))
	ctx := context.Background()
	now := time.Now()

	if _, err := s.CreateFirstUser(ctx, "kim", "hash"); err != nil {
		t.Fatal(err)
	}

	a, err := s.CreateAccount(ctx, "anna", "user", inv(7, now))
	if err != nil {
		t.Fatal(err)
	}

	if _, _, err := s.UpdateAccount(ctx, a.ID, AccountChange{Disabled: ptr(true)}); err != nil {
		t.Fatal(err)
	}

	if _, err := s.AcceptInvitation(ctx, make32(7), "h", now); !errors.Is(err, ErrNotFound) {
		t.Fatalf("disabled accepted: %v", err)
	}
}

func TestLastAdminGuard(t *testing.T) {
	s := open(t, dbPath(t))
	ctx := context.Background()
	now := time.Now()

	kim, err := s.CreateFirstUser(ctx, "kim", "hash")
	if err != nil {
		t.Fatal(err)
	}

	for name, c := range map[string]AccountChange{
		"demote":  {Role: ptr("manager")},
		"disable": {Disabled: ptr(true)},
	} {
		if _, _, err := s.UpdateAccount(ctx, kim.ID, c); !errors.Is(err, ErrLastAdmin) {
			t.Errorf("%s: %v", name, err)
		}
	}

	if _, err := s.DeleteAccount(ctx, kim.ID); !errors.Is(err, ErrLastAdmin) {
		t.Fatalf("delete: %v", err)
	}

	// Keeping the role is not a demotion.
	if _, _, err := s.UpdateAccount(ctx, kim.ID, AccountChange{Role: ptr("admin"), Disabled: ptr(false)}); err != nil {
		t.Fatal(err)
	}

	// A second admin that is disabled does not count.
	anna, err := s.CreateAccount(ctx, "anna", "admin", inv(1, now))
	if err != nil {
		t.Fatal(err)
	}

	if _, _, err := s.UpdateAccount(ctx, anna.ID, AccountChange{Disabled: ptr(true)}); err != nil {
		t.Fatal(err)
	}

	if _, _, err := s.UpdateAccount(ctx, kim.ID, AccountChange{Role: ptr("user")}); !errors.Is(err, ErrLastAdmin) {
		t.Fatalf("demote with only a disabled second admin: %v", err)
	}

	if _, _, err := s.UpdateAccount(ctx, anna.ID, AccountChange{Disabled: ptr(false)}); err != nil {
		t.Fatal(err)
	}

	// Nor does one who has not accepted the invitation yet.
	if _, _, err := s.UpdateAccount(ctx, kim.ID, AccountChange{Role: ptr("user")}); !errors.Is(err, ErrLastAdmin) {
		t.Fatalf("demote with only an invited second admin: %v", err)
	}

	if _, err := s.AcceptInvitation(ctx, make32(1), "h", now); err != nil {
		t.Fatal(err)
	}

	before, after, err := s.UpdateAccount(ctx, kim.ID, AccountChange{Role: ptr("manager")})
	if err != nil || before.Role != "admin" || after.Role != "manager" {
		t.Fatalf("demote with a second admin: %v %v %v", before.Role, after.Role, err)
	}

	if _, err := s.DeleteAccount(ctx, anna.ID); !errors.Is(err, ErrLastAdmin) {
		t.Fatalf("delete the remaining admin: %v", err)
	}

	if _, _, err := s.UpdateAccount(ctx, 999, AccountChange{Role: ptr("user")}); !errors.Is(err, ErrNotFound) {
		t.Fatalf("unknown: %v", err)
	}
}

func TestRoleChangeEndsSessions(t *testing.T) {
	s := open(t, dbPath(t))
	ctx := context.Background()
	now := time.Now()

	if _, err := s.CreateFirstUser(ctx, "kim", "hash"); err != nil {
		t.Fatal(err)
	}

	a, err := s.CreateAccount(ctx, "anna", "manager", inv(1, now))
	if err != nil {
		t.Fatal(err)
	}

	session := func(b byte) {
		if err := s.CreateSession(ctx, a.ID, make32(b), now, now.Add(time.Hour)); err != nil {
			t.Fatal(err)
		}
	}

	count := func() int {
		var n int
		if err := s.RawCount(ctx, `SELECT count(*) FROM sessions WHERE user_id = ?`, a.ID, &n); err != nil {
			t.Fatal(err)
		}

		return n
	}

	session(10)

	// No real change keeps the session.
	if _, _, err := s.UpdateAccount(ctx, a.ID, AccountChange{Role: ptr("manager"), Disabled: ptr(false)}); err != nil || count() != 1 {
		t.Fatalf("no-op change: %d %v", count(), err)
	}

	if _, _, err := s.UpdateAccount(ctx, a.ID, AccountChange{Role: ptr("user")}); err != nil || count() != 0 {
		t.Fatalf("role change: %d %v", count(), err)
	}

	session(11)

	if _, _, err := s.UpdateAccount(ctx, a.ID, AccountChange{Disabled: ptr(true)}); err != nil || count() != 0 {
		t.Fatalf("disable: %d %v", count(), err)
	}
}

func TestResetAccount(t *testing.T) {
	s := open(t, dbPath(t))
	ctx := context.Background()
	now := time.Now()

	u, err := s.CreateFirstUser(ctx, "kim", "hash")
	if err != nil {
		t.Fatal(err)
	}

	if err := s.CreateSession(ctx, u.ID, make32(1), now, now.Add(time.Hour)); err != nil {
		t.Fatal(err)
	}

	if err := s.RawExec(ctx, `UPDATE users SET totp_enabled = 1, totp_secret = x'01' WHERE id = ?`, u.ID); err != nil {
		t.Fatal(err)
	}

	if err := s.RawExec(ctx, `INSERT INTO recovery_codes (user_id, code_hash) VALUES (?, ?)`, u.ID, make32(2)); err != nil {
		t.Fatal(err)
	}

	if err := s.RawExec(ctx, `INSERT INTO passkeys (user_id, credential_id, name, credential, created_at) VALUES (?, x'01', 'k', '{}', 0)`, u.ID); err != nil {
		t.Fatal(err)
	}

	if err := s.ResetAccount(ctx, u.ID, inv(3, now)); err != nil {
		t.Fatal(err)
	}

	got, _ := s.UserByID(ctx, u.ID)
	if got.PasswordHash != "" || got.TOTPEnabled || got.TOTPSecret != nil {
		t.Fatalf("after reset %+v", got)
	}

	var n int
	for _, q := range []string{
		`SELECT count(*) FROM sessions WHERE user_id = ?`,
		`SELECT count(*) FROM recovery_codes WHERE user_id = ?`,
		`SELECT count(*) FROM passkeys WHERE user_id = ?`,
	} {
		if err := s.RawCount(ctx, q, u.ID, &n); err != nil || n != 0 {
			t.Errorf("%s: %d %v", q, n, err)
		}
	}

	if a, _ := s.AccountByID(ctx, u.ID); !a.Invited || a.InviteEnd == nil {
		t.Fatalf("no invitation %+v", a)
	}

	// A second reset replaces the link.
	if err := s.ResetAccount(ctx, u.ID, inv(4, now)); err != nil {
		t.Fatal(err)
	}

	if _, err := s.InvitedUser(ctx, make32(3), now); !errors.Is(err, ErrNotFound) {
		t.Fatalf("old link still works: %v", err)
	}

	if err := s.ResetAccount(ctx, 999, inv(5, now)); !errors.Is(err, ErrNotFound) {
		t.Fatalf("unknown: %v", err)
	}

	if err := s.DeleteExpiredInvitations(ctx, now.Add(48*time.Hour)); err != nil {
		t.Fatal(err)
	}

	if a, _ := s.AccountByID(ctx, u.ID); a.InviteEnd != nil {
		t.Fatalf("expired invitation kept %+v", a)
	}
}
