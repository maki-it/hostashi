// SPDX-License-Identifier: AGPL-3.0-or-later

package auth

import (
	"context"
	"encoding/base32"
	"errors"
	"testing"
	"time"
)

func TestTOTPReplace(t *testing.T) {
	e := newEnv(t)
	e.setup(t)

	ctx := context.Background()
	oldSeed, oldCodes := e.enableTOTP(t)
	u, _ := e.st.UserByUsername(ctx, "kim")

	e.clk.t = e.clk.t.Add(30 * time.Second)
	oldCode := TOTPCode(oldSeed, TOTPStep(e.clk.now()))

	// Starting needs the password and a second factor, each of them.
	if _, _, err := e.svc.TOTPReplaceStart(ctx, u.ID, "wrong password!!", oldCode); !errors.Is(err, ErrInvalidCredentials) {
		t.Fatalf("wrong password: %v", err)
	}

	if _, _, err := e.svc.TOTPReplaceStart(ctx, u.ID, testPassword, "000000"); !errors.Is(err, ErrInvalidCredentials) {
		t.Fatalf("wrong code: %v", err)
	}

	// Nothing is waiting yet.
	if _, err := e.svc.TOTPReplaceConfirm(ctx, u.ID, "123456"); !errors.Is(err, ErrTOTPState) {
		t.Fatalf("confirm without start: %v", err)
	}

	// A recovery code is enough when the old phone is gone.
	b32, uri, err := e.svc.TOTPReplaceStart(ctx, u.ID, testPassword, oldCodes[0])
	if err != nil || uri == "" {
		t.Fatalf("start: %v", err)
	}

	newSeed, err := base32.StdEncoding.WithPadding(base32.NoPadding).DecodeString(b32)
	if err != nil {
		t.Fatal(err)
	}

	// Until confirmed, the old app still logs in and the new one does not.
	e.clk.t = e.clk.t.Add(30 * time.Second)

	if _, _, err := e.svc.Login(ctx, "kim", testPassword, TOTPCode(newSeed, TOTPStep(e.clk.now())), "10.0.0.1"); !errors.Is(err, ErrInvalidCredentials) {
		t.Fatalf("unconfirmed new app logs in: %v", err)
	}

	if _, _, err := e.svc.Login(ctx, "kim", testPassword, TOTPCode(oldSeed, TOTPStep(e.clk.now())), "10.0.0.1"); err != nil {
		t.Fatalf("old app stopped before confirm: %v", err)
	}

	// A code of the old app does not confirm the new one.
	e.clk.t = e.clk.t.Add(30 * time.Second)

	if _, err := e.svc.TOTPReplaceConfirm(ctx, u.ID, TOTPCode(oldSeed, TOTPStep(e.clk.now()))); !errors.Is(err, ErrInvalidCredentials) {
		t.Fatalf("old code confirms: %v", err)
	}

	codes, err := e.svc.TOTPReplaceConfirm(ctx, u.ID, TOTPCode(newSeed, TOTPStep(e.clk.now())))
	if err != nil || len(codes) != 8 {
		t.Fatalf("confirm: %v %v", codes, err)
	}

	// The confirming step is used up, the old app and the old codes are dead.
	if _, _, err := e.svc.Login(ctx, "kim", testPassword, TOTPCode(newSeed, TOTPStep(e.clk.now())), "10.0.0.1"); !errors.Is(err, ErrInvalidCredentials) {
		t.Fatalf("confirming code replayed: %v", err)
	}

	e.clk.t = e.clk.t.Add(30 * time.Second)

	if _, _, err := e.svc.Login(ctx, "kim", testPassword, TOTPCode(oldSeed, TOTPStep(e.clk.now())), "10.0.0.1"); !errors.Is(err, ErrInvalidCredentials) {
		t.Fatalf("old app still logs in: %v", err)
	}

	if _, _, err := e.svc.Login(ctx, "kim", testPassword, oldCodes[1], "10.0.0.1"); !errors.Is(err, ErrInvalidCredentials) {
		t.Fatalf("old recovery code still works: %v", err)
	}

	if _, _, err := e.svc.Login(ctx, "kim", testPassword, TOTPCode(newSeed, TOTPStep(e.clk.now())), "10.0.0.1"); err != nil {
		t.Fatalf("new app rejected: %v", err)
	}

	if _, _, err := e.svc.Login(ctx, "kim", testPassword, codes[0], "10.0.0.1"); err != nil {
		t.Fatalf("new recovery code rejected: %v", err)
	}

	// The replacement is used up.
	if _, err := e.svc.TOTPReplaceConfirm(ctx, u.ID, "123456"); !errors.Is(err, ErrTOTPState) {
		t.Fatalf("second confirm: %v", err)
	}

	var n int
	if err := e.st.RawCount(ctx, `SELECT count(*) FROM audit_log WHERE action = ?`, "totp.replace", &n); err != nil || n != 1 {
		t.Fatalf("audit entries: %d %v", n, err)
	}
}

func TestTOTPReplaceStates(t *testing.T) {
	e := newEnv(t)
	e.setup(t)

	ctx := context.Background()
	u, _ := e.st.UserByUsername(ctx, "kim")

	// Off: there is nothing to move.
	if _, _, err := e.svc.TOTPReplaceStart(ctx, u.ID, testPassword, "123456"); !errors.Is(err, ErrTOTPState) {
		t.Fatalf("start while off: %v", err)
	}

	seed, codes := e.enableTOTP(t)

	first, _, err := e.svc.TOTPReplaceStart(ctx, u.ID, testPassword, codes[0])
	if err != nil {
		t.Fatal(err)
	}

	// Starting again replaces the waiting seed; the first one no longer confirms.
	second, _, err := e.svc.TOTPReplaceStart(ctx, u.ID, testPassword, codes[1])
	if err != nil {
		t.Fatal(err)
	}

	s1, _ := base32.StdEncoding.WithPadding(base32.NoPadding).DecodeString(first)
	s2, _ := base32.StdEncoding.WithPadding(base32.NoPadding).DecodeString(second)

	e.clk.t = e.clk.t.Add(30 * time.Second)

	if _, err := e.svc.TOTPReplaceConfirm(ctx, u.ID, TOTPCode(s1, TOTPStep(e.clk.now()))); !errors.Is(err, ErrInvalidCredentials) {
		t.Fatalf("stale replacement confirms: %v", err)
	}

	// Turning TOTP off drops the waiting seed too.
	if err := e.st.RawExec(ctx, `UPDATE users SET role = 'user' WHERE id = ?`, u.ID); err != nil {
		t.Fatal(err)
	}

	if err := e.svc.TOTPDisable(ctx, u.ID, testPassword, TOTPCode(seed, TOTPStep(e.clk.now()))); err != nil {
		t.Fatal(err)
	}

	if _, err := e.svc.TOTPReplaceConfirm(ctx, u.ID, TOTPCode(s2, TOTPStep(e.clk.now()))); !errors.Is(err, ErrTOTPState) {
		t.Fatalf("confirm after disable: %v", err)
	}

	var n int
	if err := e.st.RawCount(ctx, `SELECT count(*) FROM users WHERE totp_pending_secret IS NOT NULL AND id = ?`, u.ID, &n); err != nil || n != 0 {
		t.Fatalf("waiting seed left after disable: %d %v", n, err)
	}
}
