// SPDX-License-Identifier: AGPL-3.0-or-later

// Package sshkeys checks the public SSH keys of a site and renders the
// site's key file (ADR-0032). A key comes in as one authorized_keys line
// without options; it leaves as a line whose options this package chooses
// from the key's access level, so nothing a user typed can widen access.
// This package only computes; the agent writes the file.
package sshkeys

import (
	"crypto/rsa"
	"errors"
	"fmt"
	"strings"

	"golang.org/x/crypto/ssh"
)

// ErrInvalid marks a key, label or key list that is not accepted.
var ErrInvalid = errors.New("sshkeys: invalid")

// Access is what a key may do.
type Access string

const (
	// SFTP allows SFTP (and scp) only. sshd runs its internal SFTP server
	// for every session, whatever the client asks to run.
	SFTP Access = "sftp"
	// Shell allows a login shell with a terminal. No forwarding of any kind.
	Shell Access = "shell"
)

const (
	// MaxKeys is the most keys one site may have.
	MaxKeys = 50
	// MaxLabel is the longest label in bytes.
	MaxLabel = 64
	// maxLine is the longest key line accepted as input. An RSA key of
	// 16384 bits is about 2.8 KiB in authorized_keys form.
	maxLine = 8192
	// MinRSABits is the smallest RSA modulus accepted.
	MinRSABits = 3072
)

// allowedTypes are the key types accepted. Certificates and DSA are not.
var allowedTypes = map[string]bool{
	ssh.KeyAlgoED25519:    true,
	ssh.KeyAlgoSKED25519:  true,
	ssh.KeyAlgoECDSA256:   true,
	ssh.KeyAlgoECDSA384:   true,
	ssh.KeyAlgoECDSA521:   true,
	ssh.KeyAlgoSKECDSA256: true,
	ssh.KeyAlgoRSA:        true,
}

// Key is one accepted public key.
type Key struct {
	Label  string
	Access Access
	// Public is the key in canonical authorized_keys form: type, a space,
	// base64. No options, no comment.
	Public string
}

// Parse checks one public key line as a user pastes it: type, base64 and
// an optional comment, no options. It returns the canonical form and the
// SHA256 fingerprint.
func Parse(line string) (public, fingerprint string, err error) {
	if len(line) > maxLine {
		return "", "", fmt.Errorf("%w: key line longer than %d bytes", ErrInvalid, maxLine)
	}

	line = strings.TrimRight(line, "\r\n")
	if strings.ContainsAny(line, "\x00\r\n") {
		return "", "", fmt.Errorf("%w: key must be a single line", ErrInvalid)
	}

	pk, _, opts, rest, err := ssh.ParseAuthorizedKey([]byte(line))
	if err != nil {
		return "", "", fmt.Errorf("%w: not a public key", ErrInvalid)
	}

	if len(opts) > 0 {
		return "", "", fmt.Errorf("%w: key options are not accepted, the panel sets them", ErrInvalid)
	}

	if len(rest) > 0 {
		return "", "", fmt.Errorf("%w: one key per line", ErrInvalid)
	}

	if err := checkType(pk); err != nil {
		return "", "", err
	}

	return canonical(pk), ssh.FingerprintSHA256(pk), nil
}

func checkType(pk ssh.PublicKey) error {
	if _, ok := pk.(*ssh.Certificate); ok {
		return fmt.Errorf("%w: certificates are not accepted", ErrInvalid)
	}

	if !allowedTypes[pk.Type()] {
		return fmt.Errorf("%w: key type %q is not accepted", ErrInvalid, pk.Type())
	}

	if pk.Type() == ssh.KeyAlgoRSA {
		ck, ok := pk.(ssh.CryptoPublicKey)
		if !ok {
			return fmt.Errorf("%w: unreadable RSA key", ErrInvalid)
		}

		rk, ok := ck.CryptoPublicKey().(*rsa.PublicKey)
		if !ok || rk.N.BitLen() < MinRSABits {
			return fmt.Errorf("%w: RSA keys need at least %d bits", ErrInvalid, MinRSABits)
		}
	}

	return nil
}

// canonical is "type base64" without a trailing newline or comment.
func canonical(pk ssh.PublicKey) string {
	return strings.TrimSuffix(string(ssh.MarshalAuthorizedKey(pk)), "\n")
}

// CheckLabel accepts 1 to MaxLabel bytes of letters, digits, space and
// ". _ - @ +", not starting or ending with a space. The label ends up as the
// key's comment in the key file, so it must not carry anything sshd parses.
func CheckLabel(s string) error {
	if s == "" || len(s) > MaxLabel || s[0] == ' ' || s[len(s)-1] == ' ' {
		return fmt.Errorf("%w: label must be 1 to %d characters without leading or trailing spaces", ErrInvalid, MaxLabel)
	}

	for i := range len(s) {
		c := s[i]
		switch {
		case c >= 'a' && c <= 'z', c >= 'A' && c <= 'Z', c >= '0' && c <= '9':
		case strings.IndexByte(" ._-@+", c) >= 0:
		default:
			return fmt.Errorf("%w: label may contain letters, digits, space and . _ - @ +", ErrInvalid)
		}
	}

	return nil
}

// CheckAccess accepts the two access levels.
func CheckAccess(a Access) error {
	if a != SFTP && a != Shell {
		return fmt.Errorf("%w: access must be %q or %q", ErrInvalid, SFTP, Shell)
	}

	return nil
}

// Check validates a site's complete key list as the agent receives it:
// every key parses again, labels and access are valid, no key appears
// twice, and there are at most MaxKeys.
func Check(keys []Key) error {
	if len(keys) > MaxKeys {
		return fmt.Errorf("%w: at most %d keys per site", ErrInvalid, MaxKeys)
	}

	seen := make(map[string]bool, len(keys))

	for i, k := range keys {
		if err := CheckLabel(k.Label); err != nil {
			return fmt.Errorf("key %d: %w", i, err)
		}

		if err := CheckAccess(k.Access); err != nil {
			return fmt.Errorf("key %d: %w", i, err)
		}

		pub, _, err := Parse(k.Public)
		if err != nil {
			return fmt.Errorf("key %d: %w", i, err)
		}

		if pub != k.Public {
			return fmt.Errorf("key %d: %w: key is not in canonical form", i, ErrInvalid)
		}

		if seen[pub] {
			return fmt.Errorf("key %d: %w: key appears twice", i, ErrInvalid)
		}

		seen[pub] = true
	}

	return nil
}

// options are the authorized_keys options per access level. restrict turns
// off every forwarding, the agent, X11, user rc and the terminal; shell
// keys get the terminal back, SFTP keys are forced into internal-sftp.
var options = map[Access]string{
	SFTP:  `restrict,command="internal-sftp"`,
	Shell: `restrict,pty`,
}

// Render returns the key file for a checked key list. An empty list gives
// an empty file: nobody can log in.
func Render(keys []Key) ([]byte, error) {
	if err := Check(keys); err != nil {
		return nil, err
	}

	var b strings.Builder

	b.WriteString("# Managed by Hostashi. Changes here are overwritten.\n")

	for _, k := range keys {
		b.WriteString(options[k.Access] + " " + k.Public + " " + k.Label + "\n")
	}

	return []byte(b.String()), nil
}

// NeedsShell reports whether any key has shell access, so the site user
// needs a real login shell.
func NeedsShell(keys []Key) bool {
	for _, k := range keys {
		if k.Access == Shell {
			return true
		}
	}

	return false
}
