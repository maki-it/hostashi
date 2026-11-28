// SPDX-License-Identifier: AGPL-3.0-or-later

package serve

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"sync"
	"testing"

	"github.com/maki-it/hostashi/internal/hosting"
	"github.com/maki-it/hostashi/internal/secrets"
)

// stubDNS seals with a real box and fails checks on request.
type stubDNS struct {
	hosting.DNSProviders

	mu       sync.Mutex
	checkErr error
}

func (s *stubDNS) Check(context.Context, string, map[string]string, string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	return s.checkErr
}

func TestDNSAccountsOverHTTP(t *testing.T) {
	e := newAPIWithAgent(t, &stubAgent{})

	box, err := secrets.New(bytes.Repeat([]byte{3}, 32))
	if err != nil {
		t.Fatal(err)
	}

	dns := &stubDNS{DNSProviders: hosting.DNSProviders{Box: box}}
	e.sites.EnableDNSAccounts(dns)
	c := e.adminCookie(t)

	rec := e.do(t, reqOpts{method: "GET", path: "/api/v1/dns-providers", cookie: c})
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"id":"inwx"`) || strings.Contains(rec.Body.String(), "exec") {
		t.Fatalf("providers: %d %s", rec.Code, rec.Body)
	}

	add := `{"label":"INWX","provider":"inwx","zones":["example.com"],
		"credentials":{"username":"kim","password":"Top-secret-dns","sharedSecret":"Totp-seed-dns"}}`

	rec = e.do(t, reqOpts{method: "POST", path: "/api/v1/dns-accounts", cookie: c, body: add})
	if rec.Code != http.StatusCreated || strings.Contains(rec.Body.String(), "Top-secret") || strings.Contains(rec.Body.String(), "Totp-seed") {
		t.Fatalf("add: %d %s", rec.Code, rec.Body)
	}

	var got dnsAccountJSON
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil || got.State != "ok" || got.Settings["username"] != "kim" ||
		len(got.Settings) != 1 || got.CheckedAt == nil {
		t.Fatalf("%+v %v", got, err)
	}

	id := "/api/v1/dns-accounts/" + itoa(got.ID)

	// The list and the single read never carry a secret either.
	for _, p := range []string{"/api/v1/dns-accounts", id} {
		rec := e.do(t, reqOpts{method: "GET", path: p, cookie: c})
		if rec.Code != http.StatusOK || strings.Contains(rec.Body.String(), "Top-secret") || !strings.Contains(rec.Body.String(), `"label":"INWX"`) {
			t.Fatalf("GET %s: %d %s", p, rec.Code, rec.Body)
		}
	}

	// A failed check is recorded, not an error.
	dns.mu.Lock()
	dns.checkErr = errors.New("inwx: login failed for Top-secret-dns")
	dns.mu.Unlock()

	rec = e.do(t, reqOpts{method: "POST", path: id + "/check", cookie: c})
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"state":"error"`) || strings.Contains(rec.Body.String(), "Top-secret") {
		t.Fatalf("failed check: %d %s", rec.Code, rec.Body)
	}

	cases := []struct {
		method, path, body string
		code               int
	}{
		{"POST", "/api/v1/dns-accounts", add, http.StatusConflict},
		{"POST", "/api/v1/dns-accounts", `{"label":"x","provider":"exec","zones":["a.example"],"credentials":{"command":"sh"}}`, http.StatusUnprocessableEntity},
		{"POST", "/api/v1/dns-accounts", `{"label":"x","provider":"hetzner","zones":["example.com"],"credentials":{"apiToken":"t"}}`, http.StatusConflict},
		{"POST", "/api/v1/dns-accounts", `{"label":"x","nope":1}`, http.StatusBadRequest},
		{"PUT", id, `{"label":"INWX","zones":[]}`, http.StatusUnprocessableEntity},
		{"PUT", "/api/v1/dns-accounts/99", `{"label":"x","zones":["a.example"]}`, http.StatusNotFound},
		{"PUT", "/api/v1/dns-accounts/abc", `{"label":"x","zones":["a.example"]}`, http.StatusNotFound},
		{"POST", "/api/v1/dns-accounts/99/check", ``, http.StatusNotFound},
		{"DELETE", "/api/v1/dns-accounts/0", ``, http.StatusNotFound},
	}
	for _, tc := range cases {
		if rec := e.do(t, reqOpts{method: tc.method, path: tc.path, cookie: c, body: tc.body}); rec.Code != tc.code {
			t.Errorf("%s %s %s: %d, want %d: %s", tc.method, tc.path, tc.body, rec.Code, tc.code, rec.Body)
		}
	}

	rec = e.do(t, reqOpts{method: "PUT", path: id, cookie: c, body: `{"label":"INWX main","zones":["example.com","example.org"]}`})
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"example.org"`) {
		t.Fatalf("update: %d %s", rec.Code, rec.Body)
	}

	if rec := e.do(t, reqOpts{method: "DELETE", path: id, cookie: c}); rec.Code != http.StatusNoContent {
		t.Fatalf("delete: %d %s", rec.Code, rec.Body)
	}

	if rec := e.do(t, reqOpts{method: "GET", path: id, cookie: c}); rec.Code != http.StatusNotFound {
		t.Fatalf("after delete: %d %s", rec.Code, rec.Body)
	}
}
