// SPDX-License-Identifier: AGPL-3.0-or-later

package statefile

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func testDir(t *testing.T) string {
	t.Helper()

	dir := filepath.Join(t.TempDir(), "state")
	if err := os.Mkdir(dir, 0o700); err != nil {
		t.Fatal(err)
	}

	return dir
}

func TestWriteRead(t *testing.T) {
	dir := testDir(t)
	uid := uint32(os.Getuid())

	if _, err := Read(dir, "x.json", uid, 100); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("missing file: %v", err)
	}

	if err := Write(dir, "x.json", []byte("one")); err != nil {
		t.Fatal(err)
	}

	if err := Write(dir, "x.json", []byte("two")); err != nil {
		t.Fatal(err)
	}

	got, err := Read(dir, "x.json", uid, 100)
	if err != nil || string(got) != "two" {
		t.Fatalf("got %q, %v", got, err)
	}

	st, err := os.Stat(filepath.Join(dir, "x.json"))
	if err != nil || st.Mode().Perm() != 0o600 {
		t.Fatalf("mode %v, %v", st.Mode(), err)
	}

	if _, err := os.Stat(filepath.Join(dir, ".x.json.tmp")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("temp file left: %v", err)
	}
}

func TestReadRefusesUnsafe(t *testing.T) {
	uid := uint32(os.Getuid())

	t.Run("too large", func(t *testing.T) {
		dir := testDir(t)
		if err := Write(dir, "x", make([]byte, 11)); err != nil {
			t.Fatal(err)
		}

		if _, err := Read(dir, "x", uid, 10); !errors.Is(err, ErrTooLarge) {
			t.Fatalf("got %v", err)
		}
	})

	t.Run("loose file mode", func(t *testing.T) {
		dir := testDir(t)
		if err := os.WriteFile(filepath.Join(dir, "x"), nil, 0o644); err != nil {
			t.Fatal(err)
		}

		if err := os.Chmod(filepath.Join(dir, "x"), 0o644); err != nil {
			t.Fatal(err)
		}

		if _, err := Read(dir, "x", uid, 10); !errors.Is(err, ErrUnsafe) {
			t.Fatalf("got %v", err)
		}
	})

	t.Run("symlink", func(t *testing.T) {
		dir := testDir(t)
		target := filepath.Join(t.TempDir(), "t")

		if err := os.WriteFile(target, nil, 0o600); err != nil {
			t.Fatal(err)
		}

		if err := os.Symlink(target, filepath.Join(dir, "x")); err != nil {
			t.Fatal(err)
		}

		if _, err := Read(dir, "x", uid, 10); !errors.Is(err, ErrUnsafe) {
			t.Fatalf("got %v", err)
		}
	})

	t.Run("loose directory", func(t *testing.T) {
		dir := testDir(t)
		if err := os.Chmod(dir, 0o755); err != nil {
			t.Fatal(err)
		}

		if _, err := Read(dir, "x", uid, 10); !errors.Is(err, ErrUnsafe) {
			t.Fatalf("got %v", err)
		}
	})

	t.Run("foreign owner", func(t *testing.T) {
		if _, err := Read(testDir(t), "x", uid+1, 10); !errors.Is(err, ErrUnsafe) {
			t.Fatalf("got %v", err)
		}
	})

	t.Run("relative directory", func(t *testing.T) {
		if err := CheckDir("state", uid); !errors.Is(err, ErrUnsafe) {
			t.Fatalf("got %v", err)
		}
	})
}
