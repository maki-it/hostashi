package confine

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"golang.org/x/sys/unix"
)

// newTree creates <tmp>/site (the root) and <tmp>/outside (a sibling the root must never reach).
func newTree(t *testing.T, opt Options) (r *Root, site, outside string) {
	t.Helper()
	tmp := t.TempDir()
	site = filepath.Join(tmp, "site")
	outside = filepath.Join(tmp, "outside")
	for _, d := range []string{site, outside} {
		if err := os.Mkdir(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(outside, "secret"), []byte("SECRET"), 0o644); err != nil {
		t.Fatal(err)
	}
	fd, err := unix.Open(site, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_CLOEXEC, 0)
	if err != nil {
		t.Fatal(err)
	}
	r, err = NewRoot(fd, opt)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { r.Close() })
	return r, site, outside
}

func must(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}

func wantCode(t *testing.T, err error, code string) {
	t.Helper()
	if err == nil {
		t.Fatalf("expected error %s, got nil", code)
	}
	if got := CodeOf(err); got != code {
		t.Fatalf("expected code %s, got %s (%v)", code, got, err)
	}
}

func rel(t *testing.T, s string) Rel {
	t.Helper()
	r, err := ParseRel(s)
	if err != nil {
		t.Fatal(err)
	}
	return r
}

func goVersion() string { return runtime.Version() }
