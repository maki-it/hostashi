// SPDX-License-Identifier: AGPL-3.0-or-later

package auth

import (
	"strings"
	"testing"
	"time"
)

// RFC 6238 appendix B, SHA-1 vectors. The RFC lists 8 digits; we use the
// last 6 of them.
func TestTOTPRFC6238Vectors(t *testing.T) {
	secret := []byte("12345678901234567890")

	for unix, want := range map[int64]string{
		59:          "287082",
		1111111109:  "081804",
		1111111111:  "050471",
		1234567890:  "005924",
		2000000000:  "279037",
		20000000000: "353130",
	} {
		if got := TOTPCode(secret, unix/30); got != want {
			t.Errorf("t=%d: got %s, want %s", unix, got, want)
		}
	}
}

func TestVerifyTOTPWindow(t *testing.T) {
	secret := []byte("12345678901234567890")
	now := time.Unix(1111111111, 0)
	cur := TOTPStep(now)

	for _, tc := range []struct {
		name string
		step int64
		ok   bool
	}{
		{"current", cur, true},
		{"previous", cur - 1, true},
		{"next", cur + 1, true},
		{"two back", cur - 2, false},
		{"two ahead", cur + 2, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			step, ok := VerifyTOTP(secret, TOTPCode(secret, tc.step), now)
			if ok != tc.ok || (ok && step != tc.step) {
				t.Fatalf("ok=%v step=%d", ok, step)
			}
		})
	}

	good := TOTPCode(secret, cur)
	if _, ok := VerifyTOTP(secret, good[:3]+" "+good[3:], now); !ok {
		t.Fatal("code with a space rejected")
	}

	for _, bad := range []string{"", "12345", "1234567", "abcdef", "00000a", good + "0", "-12345"} {
		if _, ok := VerifyTOTP(secret, bad, now); ok {
			t.Errorf("%q accepted", bad)
		}
	}
}

func TestTOTPURI(t *testing.T) {
	uri := TOTPURI("Hostashi", "kim", []byte("12345678901234567890"))

	for _, want := range []string{
		"otpauth://totp/Hostashi:kim?",
		"secret=GEZDGNBVGY3TQOJQGEZDGNBVGY3TQOJQ",
		"issuer=Hostashi",
		"digits=6",
		"period=30",
	} {
		if !strings.Contains(uri, want) {
			t.Errorf("%q missing in %s", want, uri)
		}
	}
}

func TestRecoveryCodeFormat(t *testing.T) {
	codes, hashes, err := newRecoveryCodes(8)
	if err != nil || len(codes) != 8 || len(hashes) != 8 {
		t.Fatalf("%v", err)
	}

	seen := map[string]bool{}

	for i, c := range codes {
		if len(c) != 19 || seen[c] {
			t.Fatalf("bad or duplicate code %q", c)
		}

		seen[c] = true

		h, ok := recoveryHash(strings.ToUpper(c))
		if !ok || string(h) != string(hashes[i]) {
			t.Fatalf("hash mismatch for %q", c)
		}
	}

	for _, bad := range []string{"", "1234", "zzzz-zzzz-zzzz-zzzz", "0000-0000-0000-00000"} {
		if _, ok := recoveryHash(bad); ok {
			t.Errorf("%q accepted", bad)
		}
	}
}
