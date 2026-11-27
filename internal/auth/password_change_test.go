// SPDX-License-Identifier: AGPL-3.0-or-later

package auth

import (
	"context"
	"errors"
	"testing"
)

func TestChangePassword(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	kim := e.admin(t)

	keep, _, err := e.svc.Login(ctx, "kim", testPassword, "", "10.0.0.1")
	if err != nil {
		t.Fatal(err)
	}

	other, _, err := e.svc.Login(ctx, "kim", testPassword, "", "10.0.0.2")
	if err != nil {
		t.Fatal(err)
	}

	var events []AccountEvent

	e.svc.OnAccountEvent(func(_ context.Context, ev AccountEvent) { events = append(events, ev) })

	const next = "a new and long passphrase"

	if err := e.svc.ChangePassword(ctx, kim.ID, "wrong password!!", next, keep, "10.0.0.1"); !errors.Is(err, ErrInvalidCredentials) {
		t.Fatalf("wrong current password: %v", err)
	}

	if err := e.svc.ChangePassword(ctx, kim.ID, testPassword, "short", keep, "10.0.0.1"); !errors.Is(err, ErrWeakPassword) {
		t.Fatalf("weak password: %v", err)
	}

	if err := e.svc.ChangePassword(ctx, kim.ID, testPassword, next, keep, "10.0.0.1"); err != nil {
		t.Fatal(err)
	}

	if _, err := e.svc.Authenticate(ctx, keep); err != nil {
		t.Fatalf("the changing session ended: %v", err)
	}

	if _, err := e.svc.Authenticate(ctx, other); !errors.Is(err, ErrUnauthenticated) {
		t.Fatalf("the other session stays: %v", err)
	}

	if _, _, err := e.svc.Login(ctx, "kim", testPassword, "", "10.0.0.3"); !errors.Is(err, ErrInvalidCredentials) {
		t.Fatalf("old password still works: %v", err)
	}

	if _, _, err := e.svc.Login(ctx, "kim", next, "", "10.0.0.3"); err != nil {
		t.Fatalf("new password: %v", err)
	}

	if len(events) != 1 || events[0].Kind != EventPasswordChanged || events[0].IP != "10.0.0.1" {
		t.Fatalf("events %+v", events)
	}

	var n int
	if err := e.st.RawCount(ctx, `SELECT count(*) FROM audit_log WHERE action = ?`, "password.change", &n); err != nil || n != 1 {
		t.Fatalf("audit %d %v", n, err)
	}
}

func TestChangePasswordIsRateLimited(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	kim := e.admin(t)

	var err error

	for range 25 {
		err = e.svc.ChangePassword(ctx, kim.ID, "wrong password!!", "a new and long passphrase", "", "")
		if errors.Is(err, ErrRateLimited) {
			break
		}
	}

	if !errors.Is(err, ErrRateLimited) {
		t.Fatalf("never rate limited: %v", err)
	}
}
