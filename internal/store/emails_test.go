// SPDX-License-Identifier: AGPL-3.0-or-later

package store

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestEmailConfirmation(t *testing.T) {
	s := open(t, dbPath(t))
	ctx := context.Background()
	now := time.Unix(1_800_000_000, 0)

	u, err := s.CreateFirstUser(ctx, "kim", "hash")
	if err != nil {
		t.Fatal(err)
	}

	if err := s.SetPendingEmail(ctx, u.ID, "kim@example.com", make32(1), now, now.Add(time.Hour)); err != nil {
		t.Fatal(err)
	}

	if err := s.SetPendingEmail(ctx, 999, "x@example.com", make32(2), now, now.Add(time.Hour)); !errors.Is(err, ErrNotFound) {
		t.Fatalf("unknown user: %v", err)
	}

	e, err := s.UserEmail(ctx, u.ID, now)
	if err != nil || e.Confirmed != "" || e.Pending != "kim@example.com" {
		t.Fatalf("%+v %v", e, err)
	}

	// Expired links neither show nor confirm.
	if e, _ := s.UserEmail(ctx, u.ID, now.Add(2*time.Hour)); e.Pending != "" {
		t.Fatalf("expired shown: %+v", e)
	}

	if _, _, _, err := s.ConfirmEmail(ctx, make32(1), now.Add(2*time.Hour)); !errors.Is(err, ErrNotFound) {
		t.Fatalf("expired confirmed: %v", err)
	}

	got, old, addr, err := s.ConfirmEmail(ctx, make32(1), now)
	if err != nil || got.ID != u.ID || old != "" || addr != "kim@example.com" {
		t.Fatalf("%+v %q %q %v", got, old, addr, err)
	}

	if _, _, _, err := s.ConfirmEmail(ctx, make32(1), now); !errors.Is(err, ErrNotFound) {
		t.Fatalf("second use: %v", err)
	}

	if old, err := s.RemoveEmail(ctx, u.ID); err != nil || old != "kim@example.com" {
		t.Fatal(old, err)
	}

	if e, _ := s.UserEmail(ctx, u.ID, now); e.Confirmed != "" {
		t.Fatalf("not removed: %+v", e)
	}
}

func TestInvitationEmail(t *testing.T) {
	s := open(t, dbPath(t))
	ctx := context.Background()
	now := time.Unix(1_800_000_000, 0)

	if _, err := s.CreateFirstUser(ctx, "kim", "hash"); err != nil {
		t.Fatal(err)
	}

	in := inv(7, now)
	in.Email = "anna@example.com"

	a, err := s.CreateAccount(ctx, "anna", "user", in)
	if err != nil {
		t.Fatal(err)
	}

	if _, err := s.AcceptInvitation(ctx, make32(7), "hash", now); err != nil {
		t.Fatal(err)
	}

	if e, _ := s.UserEmail(ctx, a.ID, now); e.Confirmed != "anna@example.com" {
		t.Fatalf("emailed invitation did not confirm: %+v", e)
	}

	// A link that was not emailed confirms nothing and keeps the address.
	in = inv(8, now)
	in.Email = "other@example.com"

	if err := s.ResetAccount(ctx, a.ID, in); err != nil {
		t.Fatal(err)
	}

	if err := s.ClearInvitationEmail(ctx, a.ID); err != nil {
		t.Fatal(err)
	}

	if _, err := s.AcceptInvitation(ctx, make32(8), "hash2", now); err != nil {
		t.Fatal(err)
	}

	if e, _ := s.UserEmail(ctx, a.ID, now); e.Confirmed != "anna@example.com" {
		t.Fatalf("cleared address used: %+v", e)
	}
}
