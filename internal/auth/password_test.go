// SPDX-License-Identifier: AGPL-3.0-or-later

package auth

import (
	"errors"
	"strings"
	"testing"
)

func TestHashAndVerify(t *testing.T) {
	const pw = "correct horse battery staple"

	h, err := HashPassword(pw)
	if err != nil {
		t.Fatal(err)
	}

	if !strings.HasPrefix(h, "$argon2id$v=19$m=65536,t=3,p=1$") {
		t.Fatalf("unexpected format %q", h)
	}

	if ok, err := VerifyPassword(h, pw); err != nil || !ok {
		t.Fatalf("right password: %v %v", ok, err)
	}

	for _, wrong := range []string{"correct horse battery stapl", pw + " ", "", strings.Repeat("x", MaxPasswordBytes+1)} {
		if ok, err := VerifyPassword(h, wrong); err != nil || ok {
			t.Fatalf("wrong password %q accepted: %v %v", wrong, ok, err)
		}
	}

	// A second hash of the same password uses a new salt.
	h2, err := HashPassword(pw)
	if err != nil || h2 == h {
		t.Fatalf("salt reused: %v", err)
	}
}

func TestVerifyRejectsMalformedHashes(t *testing.T) {
	for name, h := range map[string]string{
		"empty":        "",
		"plain":        "secret",
		"wrong alg":    "$argon2i$v=19$m=65536,t=3,p=1$c2FsdHNhbHQ$aGFzaGhhc2hoYXNoaGFzaA",
		"wrong ver":    "$argon2id$v=18$m=65536,t=3,p=1$c2FsdHNhbHQ$aGFzaGhhc2hoYXNoaGFzaA",
		"huge memory":  "$argon2id$v=19$m=4194304,t=3,p=1$c2FsdHNhbHQ$aGFzaGhhc2hoYXNoaGFzaA",
		"zero time":    "$argon2id$v=19$m=65536,t=0,p=1$c2FsdHNhbHQ$aGFzaGhhc2hoYXNoaGFzaA",
		"huge threads": "$argon2id$v=19$m=65536,t=3,p=200$c2FsdHNhbHQ$aGFzaGhhc2hoYXNoaGFzaA",
		"short salt":   "$argon2id$v=19$m=1024,t=1,p=1$YQ$aGFzaGhhc2hoYXNoaGFzaA",
		"bad base64":   "$argon2id$v=19$m=1024,t=1,p=1$!!!$aGFzaGhhc2hoYXNoaGFzaA",
		"short hash":   "$argon2id$v=19$m=1024,t=1,p=1$c2FsdHNhbHQ$YQ",
		"missing part": "$argon2id$v=19$m=1024,t=1,p=1$c2FsdHNhbHQ",
	} {
		t.Run(name, func(t *testing.T) {
			ok, err := VerifyPassword(h, "whatever password")
			if ok || !errors.Is(err, ErrBadHash) {
				t.Fatalf("ok=%v err=%v", ok, err)
			}
		})
	}
}

func TestValidatePassword(t *testing.T) {
	good := []string{"twelve chars", "ünïcödé-pässwörd", strings.Repeat("a", MaxPasswordBytes)}
	bad := []string{"", "short", "elevenchars", strings.Repeat("a", MaxPasswordBytes+1), "has\x00nul-byte-here", "invalid\xffutf8-string"}

	for _, p := range good {
		if err := ValidatePassword(p); err != nil {
			t.Errorf("%q rejected: %v", p, err)
		}
	}

	for _, p := range bad {
		if err := ValidatePassword(p); !errors.Is(err, ErrWeakPassword) {
			t.Errorf("%q accepted", p)
		}
	}

	if _, err := HashPassword("short"); !errors.Is(err, ErrWeakPassword) {
		t.Fatalf("HashPassword accepted a short password: %v", err)
	}
}

func TestValidateUsername(t *testing.T) {
	for _, u := range []string{"kim", "admin", "a.b-c_d", "user123", strings.Repeat("a", 32)} {
		if err := ValidateUsername(u); err != nil {
			t.Errorf("%q rejected", u)
		}
	}

	for _, u := range []string{"", "ab", "Kim", "1abc", "-abc", "a b c", "a;rm", "root\n", "ünï", strings.Repeat("a", 33), "a/../b"} {
		if err := ValidateUsername(u); !errors.Is(err, ErrInvalidUsername) {
			t.Errorf("%q accepted", u)
		}
	}
}
