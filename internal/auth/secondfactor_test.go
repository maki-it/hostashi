// SPDX-License-Identifier: AGPL-3.0-or-later

package auth

import (
	"context"
	"errors"
	"testing"
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
