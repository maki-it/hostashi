// SPDX-License-Identifier: AGPL-3.0-or-later

// Package secrets encrypts small secrets (for example TOTP seeds) before they
// are stored in the database. The key lives in a separate file, so a stolen
// database or backup alone does not reveal them.
package secrets

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"errors"
	"fmt"
	"os"
)

// DefaultKeyPath is where the key lives on a production server.
const DefaultKeyPath = "/etc/hostashi/secret.key"

const keyLen = 32

var (
	// ErrInsecureKeyFile is returned when the key file is accessible to others.
	ErrInsecureKeyFile = errors.New("secrets: key file must be a regular file with mode 0400 or 0600")
	// ErrDecrypt is returned for tampered data, a wrong key or a wrong context.
	ErrDecrypt = errors.New("secrets: cannot decrypt")
)

// Box encrypts and decrypts with AES-256-GCM.
type Box struct {
	aead cipher.AEAD
}

// New builds a Box from a 32-byte key.
func New(key []byte) (*Box, error) {
	if len(key) != keyLen {
		return nil, fmt.Errorf("secrets: key must be %d bytes", keyLen)
	}

	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, fmt.Errorf("secrets: %w", err)
	}

	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, fmt.Errorf("secrets: %w", err)
	}

	return &Box{aead: aead}, nil
}

// LoadOrCreate reads the key file. If it does not exist it is created with
// a random key and mode 0400. A file that group or others can access, or
// that is not a regular file, is refused.
func LoadOrCreate(path string) (*Box, error) {
	fi, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		if err := createKey(path); err != nil {
			return nil, err
		}

		fi, err = os.Lstat(path)
	}

	if err != nil {
		return nil, fmt.Errorf("secrets: %w", err)
	}

	if !fi.Mode().IsRegular() || fi.Mode().Perm()&0o177 != 0 {
		return nil, fmt.Errorf("%w: %s", ErrInsecureKeyFile, path)
	}

	key, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("secrets: %w", err)
	}

	return New(key)
}

func createKey(path string) error {
	key := make([]byte, keyLen)
	if _, err := rand.Read(key); err != nil {
		return fmt.Errorf("secrets: %w", err)
	}

	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o400)
	if err != nil {
		return fmt.Errorf("secrets: create key: %w", err)
	}

	if _, err := f.Write(key); err != nil {
		_ = f.Close()
		_ = os.Remove(path)

		return fmt.Errorf("secrets: %w", err)
	}

	if err := f.Close(); err != nil {
		_ = os.Remove(path)

		return fmt.Errorf("secrets: %w", err)
	}

	return nil
}

// Seal encrypts plaintext. context binds the ciphertext to its purpose, for
// example "totp:user:7", so it cannot be moved to another row.
func (b *Box) Seal(plaintext []byte, context string) ([]byte, error) {
	nonce := make([]byte, b.aead.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return nil, fmt.Errorf("secrets: %w", err)
	}

	return b.aead.Seal(nonce, nonce, plaintext, []byte(context)), nil
}

// Open decrypts data produced by Seal with the same context.
func (b *Box) Open(data []byte, context string) ([]byte, error) {
	n := b.aead.NonceSize()
	if len(data) < n+b.aead.Overhead() {
		return nil, ErrDecrypt
	}

	pt, err := b.aead.Open(nil, data[:n], data[n:], []byte(context))
	if err != nil {
		return nil, ErrDecrypt
	}

	return pt, nil
}
