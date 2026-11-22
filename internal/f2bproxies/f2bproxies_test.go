// SPDX-License-Identifier: AGPL-3.0-or-later

package f2bproxies

import (
	"context"
	"errors"
	"net/netip"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func pfx(ss ...string) []netip.Prefix {
	out := make([]netip.Prefix, 0, len(ss))
	for _, s := range ss {
		out = append(out, netip.MustParsePrefix(s))
	}

	return out
}

func TestRender(t *testing.T) {
	if Render(nil) != nil {
		t.Error("a file without proxies")
	}

	got := string(Render(pfx("2400:cb00::/32", "173.245.48.0/20", "10.0.0.2/32", "173.245.48.0/20", "10.0.0.7/24")))
	want := "[DEFAULT]\nignoreip = 127.0.0.0/8 ::1\n           10.0.0.0/24\n           10.0.0.2/32\n           173.245.48.0/20\n           2400:cb00::/32\n"

	if !strings.HasSuffix(got, want) || !strings.HasPrefix(got, "# Managed by Hostashi") {
		t.Errorf("file:\n%s", got)
	}

	// The order of the input does not change the file.
	if string(Render(pfx("10.0.0.2/32", "2400:cb00::/32", "173.245.48.0/20", "10.0.0.0/24"))) != got {
		t.Error("the file depends on the order")
	}
}

func TestSync(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "hostashi-proxies.local")
	reloads := 0
	f := &File{Path: path, Reload: func(context.Context) error { reloads++; return nil }}

	if changed, err := f.Sync(ctx, nil); err != nil || changed || reloads != 0 {
		t.Fatalf("nothing to do: %v %v %d", changed, err, reloads)
	}

	if changed, err := f.Sync(ctx, pfx("173.245.48.0/20")); err != nil || !changed || reloads != 1 {
		t.Fatalf("first write: %v %v %d", changed, err, reloads)
	}

	if fi, err := os.Stat(path); err != nil || fi.Mode().Perm() != 0o644 {
		t.Fatalf("file: %v %v", fi, err)
	}

	if changed, err := f.Sync(ctx, pfx("173.245.48.0/20")); err != nil || changed || reloads != 1 {
		t.Fatalf("same set reloaded: %v %v %d", changed, err, reloads)
	}

	if changed, err := f.Sync(ctx, nil); err != nil || !changed || reloads != 2 {
		t.Fatalf("removal: %v %v %d", changed, err, reloads)
	}

	if _, err := os.Stat(path); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("file kept: %v", err)
	}

	// A failed reload is reported; the file is written, so the next sync
	// does not reload again for the same set.
	f.Reload = func(context.Context) error { return errors.New("fail2ban is not running") }
	if changed, err := f.Sync(ctx, pfx("10.0.0.2/32")); err == nil || !changed {
		t.Fatalf("reload error lost: %v %v", changed, err)
	}

	if b, _ := os.ReadFile(path); !strings.Contains(string(b), "10.0.0.2/32") {
		t.Errorf("file:\n%s", b)
	}

	// A file that cannot be written leaves the old one.
	f = &File{Path: filepath.Join(t.TempDir(), "missing", "x.local")}
	if _, err := f.Sync(ctx, pfx("10.0.0.2/32")); err == nil {
		t.Error("write into a missing directory succeeded")
	}
}
