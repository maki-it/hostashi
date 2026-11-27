// SPDX-License-Identifier: AGPL-3.0-or-later

package gitsync

import (
	"io"
	"path/filepath"
	"strings"
	"testing"
)

func TestRun(t *testing.T) {
	dir := t.TempDir()
	base := []string{"-url", "https://github.com/acme/site.git", "-branch", "main", "-cache", dir + "/repo", "-release", dir + "/r", "-result", dir + "/res.json"}

	if err := Run(t.Context(), base, 0, io.Discard); err == nil || !strings.Contains(err.Error(), "root") {
		t.Fatalf("as root: %v", err)
	}

	if err := Run(t.Context(), base[:len(base)-2], 1000, io.Discard); err == nil {
		t.Fatal("no -result accepted")
	}

	if err := Run(t.Context(), append(base, "extra"), 1000, io.Discard); err == nil {
		t.Fatal("extra argument accepted")
	}

	if err := Run(t.Context(), append(base, "-auth", "token", "-token-file", filepath.Join(dir, "missing")), 1000, io.Discard); err == nil {
		t.Fatal("missing token file accepted")
	}

	// A local path is refused before anything connects.
	local := append([]string{}, base...)
	local[1] = dir

	if err := Run(t.Context(), local, 1000, io.Discard); err == nil || !strings.Contains(err.Error(), "repository address") {
		t.Fatalf("local path: %v", err)
	}
}
