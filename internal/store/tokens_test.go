// SPDX-License-Identifier: AGPL-3.0-or-later

package store

import (
	"bytes"
	"context"
	"errors"
	"testing"
	"time"
)

func tokenStore(t *testing.T) (*Store, User) {
	t.Helper()

	s := open(t, dbPath(t))

	u, err := s.CreateFirstUser(context.Background(), "kim", "hash")
	if err != nil {
		t.Fatal(err)
	}

	return s, u
}

func hashOf(b byte) []byte { return bytes.Repeat([]byte{b}, 32) }

func TestAPITokenStoreRoundTrip(t *testing.T) {
	s, u := tokenStore(t)
	ctx := context.Background()
	now := time.Unix(2_000_000, 0)
	exp := now.Add(time.Hour)

	tok, err := s.CreateAPIToken(ctx, APIToken{UserID: u.ID, Name: "a", Prefix: "abcdef", Scopes: []string{"sites:read"}, CreatedAt: now, ExpiresAt: &exp}, hashOf(1))
	if err != nil || tok.ID == 0 {
		t.Fatalf("%+v %v", tok, err)
	}

	got, owner, err := s.APITokenByHash(ctx, hashOf(1), now)
	if err != nil || owner.ID != u.ID || got.Name != "a" || got.ExpiresAt == nil || got.ExpiresAt.Unix() != exp.Unix() {
		t.Fatalf("%+v %+v %v", got, owner, err)
	}

	// The owner comes without credentials.
	if owner.PasswordHash != "" || owner.TOTPSecret != nil {
		t.Fatalf("credentials leaked: %+v", owner)
	}

	// Expired at the expiry second, unknown hash, and revoked.
	if _, _, err := s.APITokenByHash(ctx, hashOf(1), exp); !errors.Is(err, ErrNotFound) {
		t.Fatalf("at expiry: %v", err)
	}

	if _, _, err := s.APITokenByHash(ctx, hashOf(2), now); !errors.Is(err, ErrNotFound) {
		t.Fatalf("unknown: %v", err)
	}

	if _, err := s.RevokeAPIToken(ctx, u.ID, tok.ID, now); err != nil {
		t.Fatal(err)
	}

	if _, _, err := s.APITokenByHash(ctx, hashOf(1), now); !errors.Is(err, ErrNotFound) {
		t.Fatalf("revoked: %v", err)
	}
}

func TestAPITokenConstraints(t *testing.T) {
	s, u := tokenStore(t)
	ctx := context.Background()
	now := time.Unix(2_000_000, 0)

	good := APIToken{UserID: u.ID, Name: "a", Prefix: "abcdef", Scopes: []string{"sites:read"}, CreatedAt: now}

	if _, err := s.CreateAPIToken(ctx, good, hashOf(1)); err != nil {
		t.Fatal(err)
	}

	// The same hash twice, a short hash, an empty name and an unknown owner are refused by the schema.
	if _, err := s.CreateAPIToken(ctx, good, hashOf(1)); err == nil {
		t.Error("duplicate hash accepted")
	}

	if _, err := s.CreateAPIToken(ctx, good, []byte{1, 2, 3}); err == nil {
		t.Error("short hash accepted")
	}

	bad := good
	bad.Name = ""

	if _, err := s.CreateAPIToken(ctx, bad, hashOf(3)); err == nil {
		t.Error("empty name accepted")
	}

	bad = good
	bad.UserID = 999

	if _, err := s.CreateAPIToken(ctx, bad, hashOf(4)); err == nil {
		t.Error("unknown owner accepted")
	}

	// Scopes must be a JSON array.
	if err := s.RawExec(ctx, `UPDATE api_tokens SET scopes = '{"a":1}'`); err == nil {
		t.Error("non-array scopes accepted")
	}
}

func TestAPITokensGoWithTheirUser(t *testing.T) {
	s, u := tokenStore(t)
	ctx := context.Background()

	if _, err := s.CreateAPIToken(ctx, APIToken{UserID: u.ID, Name: "a", Prefix: "abcdef", Scopes: []string{"sites:read"}, CreatedAt: time.Now()}, hashOf(1)); err != nil {
		t.Fatal(err)
	}

	if err := s.RawExec(ctx, `DELETE FROM users WHERE id = `+"1"); err != nil {
		t.Fatal(err)
	}

	var n int
	if err := s.RawCount(ctx, `SELECT count(*) FROM api_tokens WHERE user_id = ?`, u.ID, &n); err != nil || n != 0 {
		t.Fatalf("%d tokens left, %v", n, err)
	}
}

func TestSettings(t *testing.T) {
	s, _ := tokenStore(t)
	ctx := context.Background()

	if _, ok, err := s.Setting(ctx, "k"); err != nil || ok {
		t.Fatalf("%v %v", ok, err)
	}

	for _, v := range []string{`["a"]`, `["b","c"]`} {
		if err := s.SetSetting(ctx, "k", v); err != nil {
			t.Fatal(err)
		}

		if got, ok, err := s.Setting(ctx, "k"); err != nil || !ok || got != v {
			t.Fatalf("%q %v %v", got, ok, err)
		}
	}

	// Only valid JSON is stored.
	if err := s.SetSetting(ctx, "k", `not json`); err == nil {
		t.Fatal("invalid JSON accepted")
	}
}
