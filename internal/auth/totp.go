// SPDX-License-Identifier: AGPL-3.0-or-later

package auth

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha1" //nolint:gosec // RFC 6238 default; every authenticator app supports it
	"crypto/subtle"
	"encoding/base32"
	"encoding/binary"
	"fmt"
	"net/url"
	"strings"
	"time"
)

const (
	totpPeriod = 30
	totpDigits = 6
	totpWindow = 1 // accept one step before and after, for clock drift
)

// NewTOTPSecret returns a random 160-bit seed.
func NewTOTPSecret() ([]byte, error) {
	b := make([]byte, 20)
	if _, err := rand.Read(b); err != nil {
		return nil, fmt.Errorf("auth: %w", err)
	}

	return b, nil
}

// TOTPSecretString is the base32 form shown to the user.
func TOTPSecretString(secret []byte) string {
	return base32.StdEncoding.WithPadding(base32.NoPadding).EncodeToString(secret)
}

// TOTPURI is the otpauth URI for authenticator apps.
func TOTPURI(issuer, account string, secret []byte) string {
	label := url.PathEscape(issuer + ":" + account)
	q := url.Values{
		"secret": {TOTPSecretString(secret)},
		"issuer": {issuer},
		"digits": {"6"},
		"period": {"30"},
	}

	return "otpauth://totp/" + label + "?" + q.Encode()
}

func hotp(secret []byte, counter uint64) string {
	var msg [8]byte
	binary.BigEndian.PutUint64(msg[:], counter)

	mac := hmac.New(sha1.New, secret)
	mac.Write(msg[:])
	sum := mac.Sum(nil)

	off := sum[len(sum)-1] & 0x0f
	code := (uint32(sum[off]&0x7f)<<24 | uint32(sum[off+1])<<16 | uint32(sum[off+2])<<8 | uint32(sum[off+3])) % 1_000_000

	return fmt.Sprintf("%0*d", totpDigits, code)
}

// TOTPStep returns the 30-second step number of t.
func TOTPStep(t time.Time) int64 { return t.Unix() / totpPeriod }

// TOTPCode computes the code for a step (used by tests and enrollment checks).
func TOTPCode(secret []byte, step int64) string {
	return hotp(secret, uint64(step)) //nolint:gosec // step is a positive time-derived value
}

// VerifyTOTP checks code against the steps around now. It returns the
// matching step so the caller can refuse to accept it a second time.
func VerifyTOTP(secret []byte, code string, now time.Time) (int64, bool) {
	code = strings.ReplaceAll(code, " ", "")
	if len(code) != totpDigits {
		return 0, false
	}

	cur := TOTPStep(now)
	found := int64(-1)

	// Check every step in the window without early exit.
	for d := int64(-totpWindow); d <= totpWindow; d++ {
		if subtle.ConstantTimeCompare([]byte(hotp(secret, uint64(cur+d))), []byte(code)) == 1 { //nolint:gosec // cur+d is positive
			found = cur + d
		}
	}

	return found, found >= 0
}
