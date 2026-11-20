// SPDX-License-Identifier: AGPL-3.0-or-later

package auth

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/maki-it/hostashi/internal/store"
)

func (e *env) admin(t *testing.T) store.User {
	t.Helper()

	e.setup(t)

	u, err := e.st.UserByUsername(context.Background(), "kim")
	if err != nil {
		t.Fatal(err)
	}

	return u
}

func TestInvitationFlow(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	kim := e.admin(t)

	a, inv, err := e.svc.CreateAccount(ctx, kim, "anna", "manager", "10.0.0.1")
	if err != nil {
		t.Fatal(err)
	}

	if a.Role != "manager" || !a.Invited || inv.Secret == "" || !inv.Expires.Equal(e.clk.now().Add(InvitationTTL)) {
		t.Fatalf("account %+v invitation %+v", a, inv)
	}

	// No password yet, so no login.
	if _, _, err := e.svc.Login(ctx, "anna", "", "", "10.0.0.2"); !errors.Is(err, ErrInvalidCredentials) {
		t.Fatalf("login before accepting: %v", err)
	}

	if name, err := e.svc.InvitedUsername(ctx, inv.Secret, "10.0.0.2"); err != nil || name != "anna" {
		t.Fatalf("invited %q %v", name, err)
	}

	if _, _, err := e.svc.AcceptInvitation(ctx, inv.Secret, "short", "10.0.0.2"); !errors.Is(err, ErrWeakPassword) {
		t.Fatalf("weak password: %v", err)
	}

	session, u, err := e.svc.AcceptInvitation(ctx, inv.Secret, testPassword, "10.0.0.2")
	if err != nil || session == "" || u.Username != "anna" || u.PasswordHash != "" {
		t.Fatalf("accept %q %+v %v", session, u, err)
	}

	if got, err := e.svc.Authenticate(ctx, session); err != nil || got.ID != u.ID {
		t.Fatalf("session after accept %v", err)
	}

	if _, _, err := e.svc.AcceptInvitation(ctx, inv.Secret, testPassword, "10.0.0.2"); !errors.Is(err, ErrInvitationInvalid) {
		t.Fatalf("second use: %v", err)
	}

	if _, _, err := e.svc.Login(ctx, "anna", testPassword, "", "10.0.0.2"); err != nil {
		t.Fatalf("login after accepting: %v", err)
	}

	var n int
	if err := e.st.RawCount(ctx, `SELECT count(*) FROM audit_log WHERE detail LIKE ?`, "%"+inv.Secret+"%", &n); err != nil || n != 0 {
		t.Fatalf("secret in audit log: %d %v", n, err)
	}
}

func TestInvitationExpiresAndIsRateLimited(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	kim := e.admin(t)

	_, inv, err := e.svc.CreateAccount(ctx, kim, "anna", "user", "10.0.0.1")
	if err != nil {
		t.Fatal(err)
	}

	e.clk.t = e.clk.t.Add(InvitationTTL + time.Second)

	if _, _, err := e.svc.AcceptInvitation(ctx, inv.Secret, testPassword, "10.0.0.3"); !errors.Is(err, ErrInvitationInvalid) {
		t.Fatalf("expired: %v", err)
	}

	for range 20 {
		_, _ = e.svc.InvitedUsername(ctx, "wrong", "10.0.0.4")
	}

	if _, err := e.svc.InvitedUsername(ctx, "wrong", "10.0.0.4"); !errors.Is(err, ErrRateLimited) {
		t.Fatalf("not rate limited: %v", err)
	}
}

func TestAccountRules(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	kim := e.admin(t)
	role := func(r string) *string { return &r }
	yes := true

	for _, c := range []struct{ name, role string }{{"Bad Name", "user"}, {"anna", "root"}, {"anna", ""}} {
		if _, _, err := e.svc.CreateAccount(ctx, kim, c.name, c.role, ""); err == nil {
			t.Errorf("create %q %q accepted", c.name, c.role)
		}
	}

	if _, _, err := e.svc.CreateAccount(ctx, kim, "kim", "user", ""); !errors.Is(err, store.ErrUserExists) {
		t.Fatalf("duplicate: %v", err)
	}

	// Nobody changes, disables, deletes or resets their own account.
	if _, err := e.svc.UpdateAccount(ctx, kim, kim.ID, store.AccountChange{Role: role("user")}, ""); !errors.Is(err, ErrSelf) {
		t.Fatalf("self update: %v", err)
	}

	if err := e.svc.DeleteAccount(ctx, kim, kim.ID, ""); !errors.Is(err, ErrSelf) {
		t.Fatalf("self delete: %v", err)
	}

	if _, err := e.svc.ResetAccount(ctx, kim, kim.ID, ""); !errors.Is(err, ErrSelf) {
		t.Fatalf("self reset: %v", err)
	}

	anna, annaInv, err := e.svc.CreateAccount(ctx, kim, "anna", "admin", "")
	if err != nil {
		t.Fatal(err)
	}

	// Until anna has a password, Kim is still the only admin who can log in.
	if _, err := e.svc.UpdateAccount(ctx, anna0(t, e, anna.ID), kim.ID, store.AccountChange{Role: role("manager")}, ""); !errors.Is(err, store.ErrLastAdmin) {
		t.Fatalf("demote before anna accepted: %v", err)
	}

	if _, _, err := e.svc.AcceptInvitation(ctx, annaInv.Secret, testPassword, ""); err != nil {
		t.Fatal(err)
	}

	annaUser, _ := e.st.UserByID(ctx, anna.ID)

	// Anna may demote Kim while both are admins, but then not herself.
	if _, err := e.svc.UpdateAccount(ctx, annaUser, kim.ID, store.AccountChange{Role: role("manager")}, ""); err != nil {
		t.Fatal(err)
	}

	if _, err := e.svc.UpdateAccount(ctx, kim, anna.ID, store.AccountChange{Disabled: &yes}, ""); !errors.Is(err, store.ErrLastAdmin) {
		t.Fatalf("last admin: %v", err)
	}

	if _, err := e.svc.UpdateAccount(ctx, kim, 999, store.AccountChange{Role: role("user")}, ""); !errors.Is(err, ErrUserNotFound) {
		t.Fatalf("unknown: %v", err)
	}

	if _, err := e.svc.UpdateAccount(ctx, annaUser, kim.ID, store.AccountChange{Role: role("owner")}, ""); !errors.Is(err, ErrInvalidRole) {
		t.Fatalf("bad role: %v", err)
	}

	var n int
	if err := e.st.RawCount(ctx, `SELECT count(*) FROM audit_log WHERE action = ? AND actor = 'anna'`, "user.update", &n); err != nil || n != 1 {
		t.Fatalf("audit %d %v", n, err)
	}
}

func TestResetEndsAccess(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	kim := e.admin(t)

	a, inv, err := e.svc.CreateAccount(ctx, kim, "anna", "user", "")
	if err != nil {
		t.Fatal(err)
	}

	session, _, err := e.svc.AcceptInvitation(ctx, inv.Secret, testPassword, "")
	if err != nil {
		t.Fatal(err)
	}

	inv2, err := e.svc.ResetAccount(ctx, kim, a.ID, "")
	if err != nil {
		t.Fatal(err)
	}

	if _, err := e.svc.Authenticate(ctx, session); !errors.Is(err, ErrUnauthenticated) {
		t.Fatalf("session survived the reset: %v", err)
	}

	if _, _, err := e.svc.Login(ctx, "anna", testPassword, "", ""); !errors.Is(err, ErrInvalidCredentials) {
		t.Fatalf("old password works: %v", err)
	}

	if _, _, err := e.svc.AcceptInvitation(ctx, inv2.Secret, testPassword+"!", ""); err != nil {
		t.Fatal(err)
	}
}

func TestGrantsThroughService(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	kim := e.admin(t)

	a, _, err := e.svc.CreateAccount(ctx, kim, "anna", "user", "")
	if err != nil {
		t.Fatal(err)
	}

	if _, err := e.st.CreateSite(ctx, "alpha", "default", "8.4", []string{"alpha.example"}, time.Now()); err != nil {
		t.Fatal(err)
	}

	got, err := e.svc.SetSiteGrants(ctx, kim, a.ID, []string{"alpha"}, "")
	if err != nil || len(got) != 1 || got[0] != "alpha" {
		t.Fatalf("grants %v %v", got, err)
	}

	if _, err := e.svc.SetSiteGrants(ctx, kim, kim.ID, []string{"alpha"}, ""); !errors.Is(err, store.ErrNotGrantee) {
		t.Fatalf("admin grant: %v", err)
	}

	if _, err := e.svc.SiteGrants(ctx, 999); !errors.Is(err, ErrUserNotFound) {
		t.Fatalf("unknown: %v", err)
	}
}

func anna0(t *testing.T, e *env, id int64) store.User {
	t.Helper()

	u, err := e.st.UserByID(context.Background(), id)
	if err != nil {
		t.Fatal(err)
	}

	return u
}
