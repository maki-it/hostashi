// SPDX-License-Identifier: AGPL-3.0-or-later

package store

import (
	"errors"
	"slices"
	"testing"
	"time"
)

func TestDNSAccountsRoundTrip(t *testing.T) {
	s := open(t, dbPath(t))
	ctx := t.Context()
	now := time.Unix(1_000_000, 0)

	list, err := s.DNSAccounts(ctx)
	if err != nil || len(list) != 0 {
		t.Fatalf("empty list: %v %v", list, err)
	}

	a, err := s.CreateDNSAccount(ctx, NewDNSAccount{Label: "inwx main", Provider: "inwx", Zones: []string{"example.com"}, Seal: sealID}, now)
	if err != nil {
		t.Fatal(err)
	}

	if string(a.SealedCredentials) != "sealed-1" || a.Provider != "inwx" || !slices.Equal(a.Zones, []string{"example.com"}) ||
		!a.CreatedAt.Equal(now) || !a.CheckedAt.IsZero() {
		t.Fatalf("created: %+v", a)
	}

	if _, err := s.CreateDNSAccount(ctx, NewDNSAccount{Label: "inwx main", Provider: "hetzner", Seal: sealID}, now); !errors.Is(err, ErrDNSAccountExists) {
		t.Fatalf("duplicate label: %v", err)
	}

	if _, err := s.CreateDNSAccount(ctx, NewDNSAccount{Label: "x", Provider: "exec", Seal: sealID}, now); err == nil {
		t.Fatal("unknown provider accepted")
	}

	if _, err := s.CreateDNSAccount(ctx, NewDNSAccount{Label: "x", Provider: "hetzner"}, now); err == nil {
		t.Fatal("account without seal accepted")
	}

	b, err := s.CreateDNSAccount(ctx, NewDNSAccount{Label: "cf", Provider: "cloudflare", Seal: sealID}, now)
	if err != nil {
		t.Fatal(err)
	}

	if b.Zones == nil || len(b.Zones) != 0 {
		t.Fatalf("zones = %#v", b.Zones)
	}

	if err := s.SetDNSAccountCheck(ctx, a.ID, "403 forbidden", now.Add(time.Minute)); err != nil {
		t.Fatal(err)
	}

	got, err := s.DNSAccount(ctx, a.ID)
	if err != nil || got.LastError != "403 forbidden" || !got.CheckedAt.Equal(now.Add(time.Minute)) {
		t.Fatalf("after check: %+v %v", got, err)
	}

	// Label and zones only: the credentials and the check stay.
	later := now.Add(time.Hour)

	got, err = s.UpdateDNSAccount(ctx, a.ID, DNSAccountUpdate{Label: "inwx", Zones: []string{"example.com", "example.net"}}, later)
	if err != nil || got.Label != "inwx" || len(got.Zones) != 2 || string(got.SealedCredentials) != "sealed-1" || got.LastError == "" ||
		!got.UpdatedAt.Equal(later) {
		t.Fatalf("update: %+v %v", got, err)
	}

	// New credentials clear the check.
	got, err = s.UpdateDNSAccount(ctx, a.ID, DNSAccountUpdate{
		Label: "inwx", Zones: got.Zones,
		Seal: func(int64) ([]byte, error) { return []byte("new"), nil },
	}, later)
	if err != nil || string(got.SealedCredentials) != "new" || got.LastError != "" || !got.CheckedAt.IsZero() {
		t.Fatalf("replace credentials: %+v %v", got, err)
	}

	if _, err := s.UpdateDNSAccount(ctx, b.ID, DNSAccountUpdate{Label: "inwx"}, later); !errors.Is(err, ErrDNSAccountExists) {
		t.Fatalf("rename onto another label: %v", err)
	}

	if _, err := s.UpdateDNSAccount(ctx, 99, DNSAccountUpdate{Label: "z"}, later); !errors.Is(err, ErrNotFound) {
		t.Fatalf("update missing: %v", err)
	}

	list, err = s.DNSAccounts(ctx)
	if err != nil || len(list) != 2 || list[0].Label != "cf" || list[1].Label != "inwx" {
		t.Fatalf("list: %+v %v", list, err)
	}

	if err := s.DeleteDNSAccount(ctx, a.ID); err != nil {
		t.Fatal(err)
	}

	if _, err := s.DNSAccount(ctx, a.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("after delete: %v", err)
	}

	if err := s.DeleteDNSAccount(ctx, a.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("delete twice: %v", err)
	}
}
