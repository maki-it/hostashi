// SPDX-License-Identifier: AGPL-3.0-or-later

package auth

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"
)

func TestDeviceLogin(t *testing.T) {
	e := tokenEnv(t)
	ctx := context.Background()

	st, err := e.svc.StartDevice(ctx, "hostashi CLI on laptop", "198.51.100.7")
	if err != nil {
		t.Fatal(err)
	}

	if len(st.UserCode) != 9 || st.UserCode[4] != '-' || strings.ContainsAny(st.UserCode, "AEIOU01") {
		t.Errorf("user code %q", st.UserCode)
	}

	if len(st.DeviceCode) < 40 || st.ExpiresIn != DeviceCodeTTL || st.Interval != DevicePollEvery {
		t.Errorf("start = %+v", st)
	}

	if _, err := e.svc.PollDevice(ctx, st.DeviceCode); !errors.Is(err, ErrAuthorizationPending) {
		t.Errorf("first poll: %v", err)
	}

	if _, err := e.svc.PollDevice(ctx, st.DeviceCode); !errors.Is(err, ErrSlowDown) {
		t.Errorf("fast poll: %v", err)
	}

	// The page finds the code typed in lower case and without the dash.
	req, err := e.svc.DeviceRequestOf(ctx, 1, strings.ToLower(strings.ReplaceAll(st.UserCode, "-", "")))
	if err != nil || req.Client != "hostashi CLI on laptop" || req.IP != "198.51.100.7" {
		t.Fatalf("request = %+v, %v", req, err)
	}

	if err := e.svc.ApproveDevice(ctx, 1, st.UserCode, true, TokenRequest{Scopes: []string{"sites:read"}, Days: 90}, "192.0.2.1"); err != nil {
		t.Fatal(err)
	}

	// Approved once: the code is gone for the page.
	if err := e.svc.ApproveDevice(ctx, 1, st.UserCode, true, TokenRequest{Scopes: []string{"sites:read"}}, "192.0.2.1"); !errors.Is(err, ErrDeviceNotFound) {
		t.Errorf("second approval: %v", err)
	}

	e.clk.t = e.clk.t.Add(DevicePollEvery)

	nt, err := e.svc.PollDevice(ctx, st.DeviceCode)
	if err != nil {
		t.Fatal(err)
	}

	if !strings.HasPrefix(nt.Secret, TokenPrefix) || nt.Token.Name != "hostashi CLI on laptop" || nt.Token.ExpiresAt == nil {
		t.Errorf("token = %+v", nt.Token)
	}

	if _, err := e.svc.AuthenticateToken(ctx, nt.Secret, "198.51.100.7"); err != nil {
		t.Errorf("token does not work: %v", err)
	}

	if _, err := e.svc.PollDevice(ctx, st.DeviceCode); !errors.Is(err, ErrExpiredToken) {
		t.Errorf("poll after the token: %v", err)
	}
}

func TestDeviceDenyAndExpiry(t *testing.T) {
	e := tokenEnv(t)
	ctx := context.Background()

	st, err := e.svc.StartDevice(ctx, "cli", "198.51.100.7")
	if err != nil {
		t.Fatal(err)
	}

	if err := e.svc.ApproveDevice(ctx, 1, st.UserCode, false, TokenRequest{}, "192.0.2.1"); err != nil {
		t.Fatal(err)
	}

	if _, err := e.svc.PollDevice(ctx, st.DeviceCode); !errors.Is(err, ErrAccessDenied) {
		t.Errorf("denied poll: %v", err)
	}

	st, err = e.svc.StartDevice(ctx, "cli", "198.51.100.7")
	if err != nil {
		t.Fatal(err)
	}

	e.clk.t = e.clk.t.Add(DeviceCodeTTL)

	if _, err := e.svc.PollDevice(ctx, st.DeviceCode); !errors.Is(err, ErrExpiredToken) {
		t.Errorf("expired poll: %v", err)
	}

	if _, err := e.svc.PollDevice(ctx, "nope"); !errors.Is(err, ErrExpiredToken) {
		t.Errorf("unknown device code: %v", err)
	}
}

func TestDeviceTokenRules(t *testing.T) {
	e := tokenEnv(t)
	ctx := context.Background()

	st, err := e.svc.StartDevice(ctx, "cli", "198.51.100.7")
	if err != nil {
		t.Fatal(err)
	}

	// A scope the role lacks or a bad lifetime fails like on the token
	// page, and the code stays open for a corrected approval.
	if err := e.svc.ApproveDevice(ctx, 1, st.UserCode, true, TokenRequest{Scopes: []string{"nope:read"}}, ""); !errors.Is(err, ErrInvalidToken) {
		t.Errorf("bad scope: %v", err)
	}

	if err := e.svc.ApproveDevice(ctx, 1, st.UserCode, true, TokenRequest{Scopes: []string{"sites:read"}}, ""); err != nil {
		t.Errorf("corrected approval: %v", err)
	}
}

func TestDeviceThrottle(t *testing.T) {
	e := tokenEnv(t)
	ctx := context.Background()

	for _, bad := range []string{"", " lead", strings.Repeat("x", 65), "a\nb"} {
		if _, err := e.svc.StartDevice(ctx, bad, "198.51.100.7"); !errors.Is(err, ErrInvalidClient) {
			t.Errorf("client %q: %v", bad, err)
		}
	}

	var err error
	for range 11 {
		_, err = e.svc.StartDevice(ctx, "cli", "198.51.100.8")
	}

	if !errors.Is(err, ErrRateLimited) {
		t.Errorf("11th start: %v", err)
	}

	// Wrong user codes are throttled per account.
	for range 10 {
		if _, err := e.svc.DeviceRequestOf(ctx, 1, "BCDF-GHJK"); !errors.Is(err, ErrDeviceNotFound) {
			t.Fatalf("wrong code: %v", err)
		}
	}

	if _, err := e.svc.DeviceRequestOf(ctx, 1, "BCDF-GHJK"); !errors.Is(err, ErrRateLimited) {
		t.Errorf("11th wrong code: %v", err)
	}

	e.clk.t = e.clk.t.Add(15 * time.Minute)

	if _, err := e.svc.StartDevice(ctx, "cli", "198.51.100.8"); err != nil {
		t.Errorf("after the window: %v", err)
	}
}

func TestUserCode(t *testing.T) {
	seen := map[string]bool{}

	for range 200 {
		c, err := newUserCode()
		if err != nil {
			t.Fatal(err)
		}

		if normalizeUserCode(formatUserCode(c)) != c {
			t.Fatalf("round trip of %q", c)
		}

		seen[c] = true
	}

	if len(seen) < 199 {
		t.Errorf("only %d distinct codes", len(seen))
	}

	for _, bad := range []string{"", "BCDF-GHJ", "BCDF-GHJKL", "ABCD-EFGH", "BCDF_GHJK"} {
		if normalizeUserCode(bad) != "" {
			t.Errorf("%q accepted", bad)
		}
	}
}
