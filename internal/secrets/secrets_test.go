// SPDX-License-Identifier: AGPL-3.0-or-later

package secrets

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func testBox(t *testing.T) *Box {
	t.Helper()

	b, err := New(bytes.Repeat([]byte{7}, 32))
	if err != nil {
		t.Fatal(err)
	}

	return b
}

func TestSealOpen(t *testing.T) {
	b := testBox(t)

	sealed, err := b.Seal([]byte("seed"), "totp:user:1")
	if err != nil {
		t.Fatal(err)
	}

	if bytes.Contains(sealed, []byte("seed")) {
		t.Fatal("plaintext visible in ciphertext")
	}

	got, err := b.Open(sealed, "totp:user:1")
	if err != nil || string(got) != "seed" {
		t.Fatalf("%q %v", got, err)
	}

	// Same input, new nonce.
	again, _ := b.Seal([]byte("seed"), "totp:user:1")
	if bytes.Equal(sealed, again) {
		t.Fatal("nonce reused")
	}
}

func TestOpenRejects(t *testing.T) {
	b := testBox(t)
	sealed, _ := b.Seal([]byte("seed"), "totp:user:1")

	tampered := bytes.Clone(sealed)
	tampered[len(tampered)-1] ^= 1

	other, _ := New(bytes.Repeat([]byte{8}, 32))

	for name, tc := range map[string]struct {
		box  *Box
		data []byte
		ctx  string
	}{
		"wrong context": {b, sealed, "totp:user:2"},
		"tampered":      {b, tampered, "totp:user:1"},
		"wrong key":     {other, sealed, "totp:user:1"},
		"empty":         {b, nil, "totp:user:1"},
		"too short":     {b, []byte("short"), "totp:user:1"},
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := tc.box.Open(tc.data, tc.ctx); !errors.Is(err, ErrDecrypt) {
				t.Fatalf("got %v", err)
			}
		})
	}
}

func TestNewRejectsBadKeyLength(t *testing.T) {
	for _, n := range []int{0, 16, 31, 33, 64} {
		if _, err := New(make([]byte, n)); err == nil {
			t.Errorf("%d byte key accepted", n)
		}
	}
}

func TestLoadOrCreate(t *testing.T) {
	path := filepath.Join(t.TempDir(), "secret.key")

	b1, err := LoadOrCreate(path)
	if err != nil {
		t.Fatal(err)
	}

	fi, err := os.Stat(path)
	if err != nil || fi.Mode().Perm() != 0o400 {
		t.Fatalf("mode %v %v", fi.Mode(), err)
	}

	sealed, _ := b1.Seal([]byte("x"), "c")

	// Loading again yields the same key.
	b2, err := LoadOrCreate(path)
	if err != nil {
		t.Fatal(err)
	}

	if got, err := b2.Open(sealed, "c"); err != nil || string(got) != "x" {
		t.Fatalf("key changed: %v", err)
	}
}

func TestLoadRejects(t *testing.T) {
	dir := t.TempDir()

	write := func(name string, content []byte, mode os.FileMode) string {
		p := filepath.Join(dir, name)
		if err := os.WriteFile(p, content, mode); err != nil {
			t.Fatal(err)
		}

		if err := os.Chmod(p, mode); err != nil {
			t.Fatal(err)
		}

		return p
	}

	key := bytes.Repeat([]byte{1}, 32)

	if _, err := LoadOrCreate(write("world", key, 0o644)); !errors.Is(err, ErrInsecureKeyFile) {
		t.Fatalf("world-readable key accepted: %v", err)
	}

	if _, err := LoadOrCreate(write("group", key, 0o640)); !errors.Is(err, ErrInsecureKeyFile) {
		t.Fatalf("group-readable key accepted: %v", err)
	}

	if _, err := LoadOrCreate(write("short", []byte("too short"), 0o600)); err == nil {
		t.Fatal("short key accepted")
	}

	if _, err := LoadOrCreate(write("ok", key, 0o600)); err != nil {
		t.Fatalf("0600 key rejected: %v", err)
	}

	link := filepath.Join(dir, "link")
	if err := os.Symlink(filepath.Join(dir, "ok"), link); err != nil {
		t.Fatal(err)
	}

	if _, err := LoadOrCreate(link); !errors.Is(err, ErrInsecureKeyFile) {
		t.Fatalf("symlink followed: %v", err)
	}

	if _, err := LoadOrCreate(dir); !errors.Is(err, ErrInsecureKeyFile) {
		t.Fatalf("directory accepted: %v", err)
	}

	if _, err := LoadOrCreate(filepath.Join(dir, "missing-dir", "k")); err == nil {
		t.Fatal("key created in a missing directory")
	}
}
