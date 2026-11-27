// SPDX-License-Identifier: AGPL-3.0-or-later

package auth

import (
	"context"
	"errors"
	"testing"

	"github.com/maki-it/hostashi/internal/store"
)

func TestSecondFactorMissing(t *testing.T) {
	e := newEnv(t)
	e.setup(t)

	ctx := context.Background()

	missing := func(want bool) {
		t.Helper()

		u, err := e.st.UserByUsername(ctx, "kim")
		if err != nil {
			t.Fatal(err)
		}

		got, err := e.svc.SecondFactorMissing(ctx, u)
		if err != nil || got != want {
			t.Fatalf("%s/%v: missing = %v, %v; want %v", u.Role, u.RequireSecondFactor, got, err, want)
		}
	}

	set := func(q string) {
		t.Helper()

		if err := e.st.RawExec(ctx, q); err != nil {
			t.Fatal(err)
		}
	}

	missing(true) // a new admin

	set(`UPDATE users SET role = 'manager'`)
	missing(true)

	set(`UPDATE users SET role = 'user'`)
	missing(false)

	set(`UPDATE users SET require_second_factor = 1`)
	missing(true)

	e.enableTOTP(t)
	missing(false)

	set(`UPDATE users SET totp_enabled = 0`)
	set(`INSERT INTO passkeys (user_id, credential_id, name, credential, created_at) VALUES (1, x'01', 'k', '{}', 0)`)
	missing(false)

	// The password alone does not log in an account that must have a
	// second factor and has only a passkey.
	if _, _, err := e.svc.Login(ctx, "kim", testPassword, "", "10.0.0.1"); !errors.Is(err, ErrPasskeyRequired) {
		t.Fatalf("password login with only a passkey: %v", err)
	}

	set(`UPDATE users SET require_second_factor = 0`)

	if _, _, err := e.svc.Login(ctx, "kim", testPassword, "", "10.0.0.1"); err != nil {
		t.Fatalf("user without the demand: %v", err)
	}
}

func TestSecondFactorForAll(t *testing.T) {
	e := newEnv(t)
	e.setup(t)

	ctx := context.Background()

	kim := func() store.User {
		t.Helper()

		u, err := e.st.UserByUsername(ctx, "kim")
		if err != nil {
			t.Fatal(err)
		}

		return u
	}

	set := func(q string) {
		t.Helper()

		if err := e.st.RawExec(ctx, q); err != nil {
			t.Fatal(err)
		}
	}

	if on, err := e.svc.SecondFactorForAll(ctx); err != nil || on {
		t.Fatalf("default = %v, %v; want off", on, err)
	}

	// An actor without a second factor cannot turn it on: it would be
	// locked out of everything but the setup at once.
	if err := e.svc.SetSecondFactorForAll(ctx, kim(), true, "10.0.0.1"); !errors.Is(err, ErrSecondFactorRequired) {
		t.Fatalf("on without own second factor: %v", err)
	}

	e.enableTOTP(t)

	if err := e.svc.SetSecondFactorForAll(ctx, kim(), true, "10.0.0.1"); err != nil {
		t.Fatal(err)
	}

	if on, err := e.svc.SecondFactorForAll(ctx); err != nil || !on {
		t.Fatalf("after on = %v, %v", on, err)
	}

	// A user without the per-account demand now needs a second factor.
	set(`UPDATE users SET role = 'user', totp_enabled = 0`)

	if got, err := e.svc.SecondFactorMissing(ctx, kim()); err != nil || !got {
		t.Fatalf("user under the policy: missing = %v, %v", got, err)
	}

	// The account still logs in, to set one up.
	if _, _, err := e.svc.Login(ctx, "kim", testPassword, "", "10.0.0.1"); err != nil {
		t.Fatalf("login without a second factor under the policy: %v", err)
	}

	// With a passkey only, the last one cannot be removed under the policy.
	set(`INSERT INTO passkeys (user_id, credential_id, name, credential, created_at) VALUES (1, x'01', 'k', '{}', 0)`)

	if err := e.svc.keepsSecondFactor(ctx, kim(), false, 1); !errors.Is(err, ErrSecondFactorRequired) {
		t.Fatalf("drop last passkey under the policy: %v", err)
	}

	// Turning it off needs no second factor and makes it optional again.
	if err := e.svc.SetSecondFactorForAll(ctx, kim(), false, "10.0.0.1"); err != nil {
		t.Fatal(err)
	}

	if err := e.svc.keepsSecondFactor(ctx, kim(), false, 1); err != nil {
		t.Fatalf("drop last passkey after off: %v", err)
	}

	// A broken value fails closed for the check, not open.
	set(`UPDATE settings SET value = '"x"' WHERE key = 'auth.require_second_factor'`)

	if _, err := e.svc.SecondFactorRequired(ctx, kim()); err == nil {
		t.Fatal("broken setting: no error")
	}

	// Admins are never affected and can repair the value.
	set(`UPDATE users SET role = 'admin'`)

	if req, err := e.svc.SecondFactorRequired(ctx, kim()); err != nil || !req {
		t.Fatalf("admin with a broken setting: %v, %v", req, err)
	}

	if err := e.svc.SetSecondFactorForAll(ctx, kim(), false, "10.0.0.1"); err != nil {
		t.Fatalf("repair: %v", err)
	}

	if on, err := e.svc.SecondFactorForAll(ctx); err != nil || on {
		t.Fatalf("after repair = %v, %v", on, err)
	}
}
