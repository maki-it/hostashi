// SPDX-License-Identifier: AGPL-3.0-or-later

package auth

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/maki-it/hostashi/internal/store"
)

// SetupTokenTTL is how long a setup token stays valid.
const SetupTokenTTL = 24 * time.Hour

// ErrNoSetupToken is returned when there is no valid setup token.
var ErrNoSetupToken = errors.New("auth: no valid setup token (setup is already done, or the token expired: restart hostashi serve for a new one)")

// EnsureSetupToken creates the one-time setup token file (mode 0600) if no
// user exists yet and there is no unexpired token. It does nothing once the
// first user exists.
func EnsureSetupToken(ctx context.Context, st *store.Store, path string, now time.Time) error {
	n, err := st.CountUsers(ctx)
	if err != nil {
		return fmt.Errorf("auth: %w", err)
	}

	if n > 0 {
		return nil
	}

	if _, err := ReadSetupToken(path, now); err == nil {
		return nil
	}

	raw := make([]byte, 24)
	if _, err := rand.Read(raw); err != nil {
		return fmt.Errorf("auth: %w", err)
	}

	_ = os.Remove(path) // expired or unreadable leftovers

	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return fmt.Errorf("auth: setup token: %w", err)
	}

	if _, err := f.WriteString(hex.EncodeToString(raw) + "\n"); err != nil {
		_ = f.Close()
		return fmt.Errorf("auth: setup token: %w", err)
	}

	if err := f.Close(); err != nil {
		return fmt.Errorf("auth: setup token: %w", err)
	}

	return nil
}

// ReadSetupToken returns the token if the file exists, is a regular file
// readable only by its owner, and is younger than SetupTokenTTL.
func ReadSetupToken(path string, now time.Time) (string, error) {
	fi, err := os.Lstat(path)
	if err != nil || !fi.Mode().IsRegular() || fi.Mode().Perm()&0o077 != 0 {
		return "", ErrNoSetupToken
	}

	if now.Sub(fi.ModTime()) >= SetupTokenTTL {
		return "", ErrNoSetupToken
	}

	b, err := os.ReadFile(path)
	if err != nil {
		return "", ErrNoSetupToken
	}

	tok := strings.TrimSpace(string(b))
	if len(tok) != 48 {
		return "", ErrNoSetupToken
	}

	return tok, nil
}

// tokenMatches compares in constant time via equal-length digests.
func tokenMatches(want, got string) bool {
	a := sha256.Sum256([]byte(want))
	b := sha256.Sum256([]byte(got))

	return subtle.ConstantTimeCompare(a[:], b[:]) == 1
}
