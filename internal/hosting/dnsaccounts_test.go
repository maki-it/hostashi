// SPDX-License-Identifier: AGPL-3.0-or-later

package hosting

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"sync"
	"testing"

	"github.com/maki-it/hostashi/internal/secrets"
	"github.com/maki-it/hostashi/internal/store"
)

// fakeDNS seals with a real box and answers checks from a table.
type fakeDNS struct {
	DNSProviders

	mu     sync.Mutex
	fail   map[string]error // by zone
	checks []string         // "provider zone token"
}

func newFakeDNS(t *testing.T) *fakeDNS {
	t.Helper()

	box, err := secrets.New(bytes.Repeat([]byte{7}, 32))
	if err != nil {
		t.Fatal(err)
	}

	return &fakeDNS{DNSProviders: DNSProviders{Box: box}, fail: map[string]error{}}
}

func (f *fakeDNS) Check(_ context.Context, provider string, creds map[string]string, zone string) error {
	f.mu.Lock()
	defer f.mu.Unlock()

	f.checks = append(f.checks, provider+" "+zone+" "+creds["apiToken"]+creds["password"])

	return f.fail[zone]
}

func dnsService(t *testing.T) (*Service, *fakeDNS, *store.Store) {
	t.Helper()

	s, _, st := newService(t)
	f := newFakeDNS(t)
	s.EnableDNSAccounts(f)

	return s, f, st
}

func TestDNSAccountsOff(t *testing.T) {
	s, _, _ := newService(t)

	if _, err := s.DNSAccountList(t.Context()); !errors.Is(err, ErrDNSAccountsOff) {
		t.Fatalf("list: %v", err)
	}

	if _, err := s.AddDNSAccount(t.Context(), 1, NewDNSAccount{}); !errors.Is(err, ErrDNSAccountsOff) {
		t.Fatalf("add: %v", err)
	}
}

func TestAddDNSAccount(t *testing.T) {
	s, f, st := dnsService(t)
	ctx := t.Context()

	v, err := s.AddDNSAccount(ctx, 1, NewDNSAccount{
		Label: "INWX", Provider: "inwx", Zones: []string{"Example.COM.", " acme.example.net "},
		Credentials: map[string]string{"username": "kim", "password": "Secret-pw-1", "sharedSecret": "TOTPSEED"},
	})
	if err != nil {
		t.Fatal(err)
	}

	if strings.Join(v.Zones, ",") != "example.com,acme.example.net" || v.LastError != "" || v.CheckedAt.IsZero() {
		t.Fatalf("account %+v", v)
	}

	// Only the user name is shown, never a secret field.
	if len(v.Settings) != 1 || v.Settings["username"] != "kim" {
		t.Fatalf("settings %v", v.Settings)
	}

	if len(f.checks) != 2 || f.checks[0] != "inwx example.com Secret-pw-1" {
		t.Fatalf("checks %v", f.checks)
	}

	// Sealed with the account's purpose, not in clear text.
	a, err := st.DNSAccount(ctx, v.ID)
	if err != nil {
		t.Fatal(err)
	}

	if bytes.Contains(a.SealedCredentials, []byte("Secret-pw-1")) {
		t.Fatal("credentials stored in clear text")
	}

	creds, err := f.Open(a)
	if err != nil || creds["password"] != "Secret-pw-1" || creds["sharedSecret"] != "TOTPSEED" {
		t.Fatalf("open: %v %v", creds, err)
	}

	if _, err := f.Box.Open(a.SealedCredentials, DNSAccountPurpose(a.ID+1)); err == nil {
		t.Fatal("opened with another account's purpose")
	}

	// The audit entry names label, provider and zones, never a secret.
	es, _, err := st.AuditEntries(ctx, store.AuditFilter{Action: "dns_account.create"}, 0, 1)
	if err != nil || len(es) != 1 {
		t.Fatalf("audit %v %v", es, err)
	}

	if !bytes.Contains(es[0].Detail, []byte("acme.example.net")) || bytes.Contains(es[0].Detail, []byte("Secret-pw-1")) ||
		bytes.Contains(es[0].Detail, []byte("TOTPSEED")) {
		t.Fatalf("audit detail %s", es[0].Detail)
	}
}

func TestAddDNSAccountRefused(t *testing.T) {
	s, _, _ := dnsService(t)
	ctx := t.Context()

	if _, err := s.AddDNSAccount(ctx, 1, NewDNSAccount{
		Label: "cf", Provider: "cloudflare", Zones: []string{"example.com"}, Credentials: map[string]string{"apiToken": "t"},
	}); err != nil {
		t.Fatal(err)
	}

	tests := []struct {
		name string
		req  NewDNSAccount
		want error
	}{
		{"unknown provider", NewDNSAccount{Label: "x", Provider: "exec", Zones: []string{"a.example"}, Credentials: map[string]string{"command": "sh"}}, ErrInvalid},
		{"missing credential", NewDNSAccount{Label: "x", Provider: "hetzner", Zones: []string{"a.example"}, Credentials: map[string]string{}}, ErrInvalid},
		{"no zones", NewDNSAccount{Label: "x", Provider: "hetzner", Credentials: map[string]string{"apiToken": "t"}}, ErrInvalid},
		{"wildcard zone", NewDNSAccount{Label: "x", Provider: "hetzner", Zones: []string{"*.example.org"}, Credentials: map[string]string{"apiToken": "t"}}, ErrInvalid},
		{"one-label zone", NewDNSAccount{Label: "x", Provider: "hetzner", Zones: []string{"com"}, Credentials: map[string]string{"apiToken": "t"}}, ErrInvalid},
		{"empty label", NewDNSAccount{Label: "", Provider: "hetzner", Zones: []string{"a.example"}, Credentials: map[string]string{"apiToken": "t"}}, ErrInvalid},
		{"label with newline", NewDNSAccount{Label: "a\nb", Provider: "hetzner", Zones: []string{"a.example"}, Credentials: map[string]string{"apiToken": "t"}}, ErrInvalid},
		{"duplicate label", NewDNSAccount{Label: "cf", Provider: "hetzner", Zones: []string{"a.example"}, Credentials: map[string]string{"apiToken": "t"}}, ErrDNSAccountExists},
		{"zone of another account", NewDNSAccount{Label: "h", Provider: "hetzner", Zones: []string{"EXAMPLE.com"}, Credentials: map[string]string{"apiToken": "t"}}, ErrDNSZoneTaken},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := s.AddDNSAccount(ctx, 1, tc.req); !errors.Is(err, tc.want) {
				t.Fatalf("got %v, want %v", err, tc.want)
			}
		})
	}
}

func TestDNSAccountFailedCheckIsKeptAndRedacted(t *testing.T) {
	s, f, _ := dnsService(t)
	ctx := t.Context()

	f.fail["example.com"] = errors.New("cloudflare: 403 for token tok-12345: forbidden")

	v, err := s.AddDNSAccount(ctx, 1, NewDNSAccount{
		Label: "cf", Provider: "cloudflare", Zones: []string{"example.com"}, Credentials: map[string]string{"apiToken": "tok-12345"},
	})
	if err != nil {
		t.Fatal(err)
	}

	if !strings.Contains(v.LastError, "example.com: cloudflare: 403") || strings.Contains(v.LastError, "tok-12345") {
		t.Fatalf("last error %q", v.LastError)
	}

	delete(f.fail, "example.com")

	v, err = s.CheckDNSAccount(ctx, v.ID)
	if err != nil || v.LastError != "" {
		t.Fatalf("check again: %+v %v", v, err)
	}

	if _, err := s.CheckDNSAccount(ctx, 99); !errors.Is(err, ErrDNSAccountNotFound) {
		t.Fatalf("missing: %v", err)
	}
}

func TestUpdateDNSAccount(t *testing.T) {
	s, f, st := dnsService(t)
	ctx := t.Context()

	v, err := s.AddDNSAccount(ctx, 1, NewDNSAccount{
		Label: "hz", Provider: "hetzner", Zones: []string{"example.com"}, Credentials: map[string]string{"apiToken": "old-token"},
	})
	if err != nil {
		t.Fatal(err)
	}

	// Label and zones only: no new check, the token stays.
	v, err = s.UpdateDNSAccount(ctx, 1, v.ID, NewDNSAccount{Label: "Hetzner DNS", Zones: []string{"example.com", "example.org"}})
	if err != nil || v.Label != "Hetzner DNS" || len(v.Zones) != 2 || len(f.checks) != 1 {
		t.Fatalf("update: %+v %v %v", v, err, f.checks)
	}

	d := lastDiff(t, st, "dns_account.update")
	if !changed(d["zones"], "example.com", "example.com example.org") || !changed(d["label"], "hz", "Hetzner DNS") {
		t.Fatalf("diff %v", d)
	}

	// New credentials are checked at once.
	if _, err := s.UpdateDNSAccount(ctx, 1, v.ID, NewDNSAccount{
		Label: "Hetzner DNS", Zones: v.Zones, Credentials: map[string]string{"apiToken": "new-token"},
	}); err != nil {
		t.Fatal(err)
	}

	if len(f.checks) != 3 || f.checks[2] != "hetzner example.org new-token" {
		t.Fatalf("checks %v", f.checks)
	}

	es, _, err := st.AuditEntries(ctx, store.AuditFilter{Action: "dns_account.update"}, 0, 1)
	if err != nil || !bytes.Contains(es[0].Detail, []byte(`"credentials":"replaced"`)) || bytes.Contains(es[0].Detail, []byte("new-token")) ||
		bytes.Contains(es[0].Diff, []byte("token")) {
		t.Fatalf("audit %s %s %v", es[0].Detail, es[0].Diff, err)
	}

	if _, err := s.UpdateDNSAccount(ctx, 1, v.ID, NewDNSAccount{Label: "x", Provider: "inwx", Zones: v.Zones}); !errors.Is(err, ErrInvalid) {
		t.Fatalf("provider change: %v", err)
	}

	if _, err := s.UpdateDNSAccount(ctx, 1, v.ID, NewDNSAccount{Label: "x", Zones: v.Zones, Credentials: map[string]string{}}); !errors.Is(err, ErrInvalid) {
		t.Fatalf("empty credentials: %v", err)
	}

	if _, err := s.UpdateDNSAccount(ctx, 1, 99, NewDNSAccount{Label: "x", Zones: v.Zones}); !errors.Is(err, ErrDNSAccountNotFound) {
		t.Fatalf("missing: %v", err)
	}

	if err := s.DeleteDNSAccount(ctx, 1, v.ID); err != nil {
		t.Fatal(err)
	}

	if err := s.DeleteDNSAccount(ctx, 1, v.ID); !errors.Is(err, ErrDNSAccountNotFound) {
		t.Fatalf("delete twice: %v", err)
	}

	list, err := s.DNSAccountList(ctx)
	if err != nil || len(list) != 0 {
		t.Fatalf("list %v %v", list, err)
	}
}
