// SPDX-License-Identifier: AGPL-3.0-or-later

package dnsprov

import (
	"context"
	"errors"
	"strings"
	"testing"
)

func TestAllSortedAndKnown(t *testing.T) {
	all := All()
	if len(all) != 3 {
		t.Fatalf("providers = %d, want 3", len(all))
	}

	for i := 1; i < len(all); i++ {
		if all[i-1].Name > all[i].Name {
			t.Fatalf("not sorted: %s before %s", all[i-1].Name, all[i].Name)
		}
	}

	for _, id := range []string{INWX, Hetzner, Cloudflare} {
		if _, ok := Lookup(id); !ok {
			t.Errorf("Lookup(%q) failed", id)
		}
	}

	for _, id := range []string{"exec", "httpreq", "acmedns", ""} {
		if _, ok := Lookup(id); ok {
			t.Errorf("Lookup(%q) must fail", id)
		}
	}
}

func TestValidate(t *testing.T) {
	tests := []struct {
		name  string
		id    string
		creds map[string]string
		ok    bool
	}{
		{"cloudflare", Cloudflare, map[string]string{"apiToken": "tok"}, true},
		{"hetzner", Hetzner, map[string]string{"apiToken": "tok"}, true},
		{"inwx without 2fa", INWX, map[string]string{"username": "u", "password": "p"}, true},
		{"inwx with 2fa", INWX, map[string]string{"username": "u", "password": "p", "sharedSecret": "s"}, true},
		{"missing token", Cloudflare, map[string]string{}, false},
		{"empty token", Hetzner, map[string]string{"apiToken": ""}, false},
		{"missing password", INWX, map[string]string{"username": "u"}, false},
		{"unknown field", Cloudflare, map[string]string{"apiToken": "t", "apiKey": "k"}, false},
		{"unknown provider", "exec", map[string]string{"command": "/bin/sh"}, false},
		{"newline", Hetzner, map[string]string{"apiToken": "a\nb"}, false},
		{"too long", Hetzner, map[string]string{"apiToken": strings.Repeat("a", maxValue+1)}, false},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			err := Validate(tc.id, tc.creds)
			if (err == nil) != tc.ok {
				t.Fatalf("Validate = %v, want ok=%v", err, tc.ok)
			}
		})
	}
}

func TestValidateNeverNamesValue(t *testing.T) {
	err := Validate(Hetzner, map[string]string{"apiToken": "secret-value\n"})
	if err == nil || strings.Contains(err.Error(), "secret-value") {
		t.Fatalf("err = %v", err)
	}
}

func TestNewBuildsEveryProvider(t *testing.T) {
	for id, creds := range map[string]map[string]string{
		Cloudflare: {"apiToken": "tok"},
		Hetzner:    {"apiToken": "tok"},
		INWX:       {"username": "u", "password": "p"},
	} {
		p, err := New(id, creds)
		if err != nil {
			t.Fatalf("New(%s): %v", id, err)
		}

		if timeout, _ := p.Timeout(); timeout <= 0 {
			t.Errorf("%s: timeout %v", id, timeout)
		}
	}

	if _, err := New("exec", map[string]string{}); !errors.Is(err, ErrUnknown) {
		t.Fatalf("exec: %v", err)
	}

	if _, err := New(Cloudflare, map[string]string{}); err == nil {
		t.Fatal("missing token accepted")
	}
}

func TestRedact(t *testing.T) {
	creds := map[string]string{"username": "u", "password": "hunter22", "sharedSecret": "hunter22xyz"}

	got := Redact("login u with hunter22xyz and hunter22 failed", creds)
	if strings.Contains(got, "hunter22") {
		t.Fatalf("secret left: %q", got)
	}

	if !strings.Contains(got, "login u with") {
		t.Fatalf("short value redacted: %q", got)
	}
}

func TestMatchZone(t *testing.T) {
	zones := []string{"example.com", "dev.example.com.", "ACME.example.net"}

	tests := []struct {
		fqdn, want string
		ok         bool
	}{
		{"_acme-challenge.www.example.com.", "example.com", true},
		{"_acme-challenge.example.com", "example.com", true},
		{"_acme-challenge.a.dev.example.com.", "dev.example.com", true},
		{"example.com", "example.com", true},
		{"www.example.com.acme.example.net.", "acme.example.net", true},
		{"_acme-challenge.badexample.com.", "", false},
		{"_acme-challenge.example.org.", "", false},
		{"com", "", false},
	}

	for _, tc := range tests {
		got, ok := MatchZone(tc.fqdn, zones)
		if got != tc.want || ok != tc.ok {
			t.Errorf("MatchZone(%q) = %q, %v; want %q, %v", tc.fqdn, got, ok, tc.want, tc.ok)
		}
	}

	if _, ok := MatchZone("a.example.com", nil); ok {
		t.Error("match without zones")
	}
}

type fakeProvider struct {
	presentErr error
	calls      []string
}

func (f *fakeProvider) Present(_ context.Context, domain, token, _ string) error {
	f.calls = append(f.calls, "present "+domain+" "+token)

	return f.presentErr
}

func (f *fakeProvider) CleanUp(_ context.Context, domain, token, _ string) error {
	f.calls = append(f.calls, "cleanup "+domain+" "+token)

	return nil
}

func TestCheck(t *testing.T) {
	f := &fakeProvider{}
	if err := Check(t.Context(), f, "example.com"); err != nil {
		t.Fatal(err)
	}

	if len(f.calls) != 2 || f.calls[0] != "present example.com "+checkToken || f.calls[1] != "cleanup example.com "+checkToken {
		t.Fatalf("calls = %v", f.calls)
	}

	f = &fakeProvider{presentErr: errors.New("403")}
	if err := Check(t.Context(), f, "example.com"); err == nil || len(f.calls) != 1 {
		t.Fatalf("err = %v, calls = %v", err, f.calls)
	}
}
