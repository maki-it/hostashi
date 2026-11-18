// SPDX-License-Identifier: AGPL-3.0-or-later

// Package auth implements password login, server-side sessions, rate
// limiting and the one-time setup token. It never touches the agent.
package auth

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"unicode/utf8"

	"golang.org/x/crypto/argon2"
)

// Argon2id parameters: 64 MiB, 3 passes, 1 lane (above the OWASP minimum).
const (
	argonMemoryKiB = 64 * 1024
	argonTime      = 3
	argonThreads   = 1
	argonKeyLen    = 32
	argonSaltLen   = 16

	// Upper bounds when parsing a stored hash, so a corrupted value cannot
	// make the server allocate gigabytes.
	maxArgonMemoryKiB = 256 * 1024
	maxArgonTime      = 10
	maxArgonThreads   = 16

	// MinPasswordRunes is a length rule instead of character class rules.
	MinPasswordRunes = 12
	// MaxPasswordBytes bounds the hashing work per request.
	MaxPasswordBytes = 256
)

// ErrWeakPassword is returned for passwords outside the allowed length.
var ErrWeakPassword = fmt.Errorf("password must be %d to %d bytes long and at least %d characters",
	MinPasswordRunes, MaxPasswordBytes, MinPasswordRunes)

var usernameRe = regexp.MustCompile(`^[a-z][a-z0-9_.-]{2,31}$`)

// ErrInvalidUsername is returned for usernames outside the allowed form.
var ErrInvalidUsername = errors.New("username must be 3 to 32 characters: lower-case letters, digits, '_', '.', '-', starting with a letter")

// ValidateUsername accepts 3 to 32 lower-case characters, starting with a letter.
func ValidateUsername(u string) error {
	if !usernameRe.MatchString(u) {
		return ErrInvalidUsername
	}

	return nil
}

// ValidatePassword checks the length rules.
func ValidatePassword(p string) error {
	if len(p) > MaxPasswordBytes || utf8.RuneCountInString(p) < MinPasswordRunes || !utf8.ValidString(p) || strings.ContainsRune(p, 0) {
		return ErrWeakPassword
	}

	return nil
}

// HashPassword returns an Argon2id hash in PHC string format.
func HashPassword(password string) (string, error) {
	if err := ValidatePassword(password); err != nil {
		return "", err
	}

	salt := make([]byte, argonSaltLen)
	if _, err := rand.Read(salt); err != nil {
		return "", fmt.Errorf("auth: %w", err)
	}

	key := argon2.IDKey([]byte(password), salt, argonTime, argonMemoryKiB, argonThreads, argonKeyLen)
	enc := base64.RawStdEncoding

	return fmt.Sprintf("$argon2id$v=%d$m=%d,t=%d,p=%d$%s$%s",
		argon2.Version, argonMemoryKiB, argonTime, argonThreads, enc.EncodeToString(salt), enc.EncodeToString(key)), nil
}

// ErrBadHash is returned when a stored hash cannot be parsed.
var ErrBadHash = errors.New("auth: malformed password hash")

// VerifyPassword reports whether password matches the stored hash. An empty
// or malformed hash never matches.
func VerifyPassword(hash, password string) (bool, error) {
	if len(password) > MaxPasswordBytes {
		return false, nil
	}

	var (
		version int
		m, t    uint32
		p       uint8
	)

	parts := strings.Split(hash, "$")
	if len(parts) != 6 || parts[0] != "" || parts[1] != "argon2id" {
		return false, ErrBadHash
	}

	if _, err := fmt.Sscanf(parts[2], "v=%d", &version); err != nil || version != argon2.Version {
		return false, ErrBadHash
	}

	if _, err := fmt.Sscanf(parts[3], "m=%d,t=%d,p=%d", &m, &t, &p); err != nil {
		return false, ErrBadHash
	}

	if m == 0 || m > maxArgonMemoryKiB || t == 0 || t > maxArgonTime || p == 0 || p > maxArgonThreads {
		return false, ErrBadHash
	}

	enc := base64.RawStdEncoding

	salt, err := enc.DecodeString(parts[4])
	if err != nil || len(salt) < 8 {
		return false, ErrBadHash
	}

	want, err := enc.DecodeString(parts[5])
	if err != nil || len(want) < 16 || len(want) > 64 {
		return false, ErrBadHash
	}

	got := argon2.IDKey([]byte(password), salt, t, m, p, uint32(len(want))) //nolint:gosec // len(want) <= 64

	return subtle.ConstantTimeCompare(got, want) == 1, nil
}
