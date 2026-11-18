// SPDX-License-Identifier: AGPL-3.0-or-later

package acme

import (
	"crypto"
	"errors"
	"os"
	"path/filepath"
	"syscall"
	"testing"

	"github.com/maki-it/hostashi/internal/store"
)

func TestChallengeFiles(t *testing.T) {
	dir := t.TempDir()
	f := &files{dir: dir}

	old := syscall.Umask(0o077)
	defer syscall.Umask(old)

	token := "abcdefghijklmnop_-0123"
	if err := f.Present("a.example.com", token, token+".thumb"); err != nil {
		t.Fatal(err)
	}

	st, err := os.Stat(filepath.Join(dir, token))
	if err != nil || st.Mode().Perm() != 0o640 {
		t.Fatalf("file %v %v", st, err)
	}

	b, _ := os.ReadFile(filepath.Join(dir, token))
	if string(b) != token+".thumb" {
		t.Fatalf("content %q", b)
	}

	if err := f.CleanUp("a.example.com", token, ""); err != nil {
		t.Fatal(err)
	}

	if _, err := os.Stat(filepath.Join(dir, token)); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("file stayed")
	}

	for _, bad := range []string{"", "short", "../../../../etc/passwdxxxxx", "abcdefghijklmnop/q", "abcdefghijklmnop q"} {
		if err := f.Present("a.example.com", bad, "x"); err == nil {
			t.Errorf("token %q accepted", bad)
		}
	}
}

func TestAccountKeyIsKeptPerDirectory(t *testing.T) {
	// The real settings table: it only takes JSON values.
	st, err := store.Open(t.Context(), filepath.Join(t.TempDir(), "panel.db"))
	if err != nil {
		t.Fatal(err)
	}

	t.Cleanup(func() { _ = st.Close() })

	keys := SettingsKeys{
		Get:  st.Setting,
		Set:  st.SetSetting,
		Seal: func(p []byte, c string) ([]byte, error) { return append([]byte(c+":"), p...), nil },
		Open: func(s []byte, c string) ([]byte, error) { return s[len(c)+1:], nil },
	}

	a := &Client{Directory: "https://ca.example/dir", Keys: keys}

	k1, err := a.accountKey(t.Context())
	if err != nil {
		t.Fatal(err)
	}

	k2, err := a.accountKey(t.Context())
	if err != nil {
		t.Fatal(err)
	}

	type eq interface {
		Equal(x crypto.PrivateKey) bool
	}

	k1e, ok := k1.(eq)
	if !ok || !k1e.Equal(k2) {
		t.Fatal("second load gave another key")
	}

	b := &Client{Directory: "https://other.example/dir", Keys: keys}
	if k3, err := b.accountKey(t.Context()); err != nil || k1e.Equal(k3) {
		t.Fatalf("other directory shares the key: %v", err)
	}

	for _, c := range []*Client{a, b} {
		if _, ok, err := st.Setting(t.Context(), c.keyName()); err != nil || !ok {
			t.Fatalf("key of %s not stored: %v", c.Directory, err)
		}
	}
}
