// SPDX-License-Identifier: AGPL-3.0-or-later

package certs

import (
	"testing"
	"time"
)

// A wildcard name of a redirect or proxy site goes into the CSR and comes
// back in the live certificate (ADR-0075).
func TestCSRInstallWildcard(t *testing.T) {
	s := &Store{Dir: t.TempDir()}
	ca := newCA(t)
	now := time.Now()

	csr, err := s.CSR("fwd", []string{"example.com", "*.example.com"})
	if err != nil {
		t.Fatal(err)
	}

	info, err := s.Install("fwd", ca.sign(t, csr, now.Add(-time.Minute), now.Add(90*24*time.Hour)))
	if err != nil {
		t.Fatal(err)
	}

	if len(info.Domains) != 2 || info.Domains[1] != "*.example.com" {
		t.Fatalf("info %+v", info)
	}

	for domains, want := range map[string]bool{"*.example.com": true, "a.example.com": true, "example.com": true, "a.b.example.com": false} {
		if ok, err := s.Covers("fwd", []string{domains}); err != nil || ok != want {
			t.Errorf("covers %s: %v %v", domains, ok, err)
		}
	}

	for _, d := range []string{"*.com", "a.*.example.com", "*"} {
		if _, err := s.CSR("fwd", []string{"example.com", d}); err == nil {
			t.Errorf("CSR for %q", d)
		}
	}
}
