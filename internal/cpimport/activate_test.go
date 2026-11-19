// SPDX-License-Identifier: AGPL-3.0-or-later

package cpimport

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/maki-it/hostashi/internal/agentapi"
	"github.com/maki-it/hostashi/internal/sites"
)

func activateHome(t *testing.T, a *Agent) string {
	t.Helper()

	if os.Geteuid() != 0 {
		t.Skip("needs root")
	}

	home := filepath.Join(a.StateDir, "pool", "blog")
	must(t, os.Mkdir(home, 0o750))
	must(t, os.Chown(home, 2000, 2000))

	for _, d := range []string{sites.WebRootName, agentapi.CloudPanelImportDir} {
		p := filepath.Join(home, d)
		must(t, os.Mkdir(p, 0o700))
		must(t, os.WriteFile(filepath.Join(p, "index.html"), []byte(d), 0o644))
		must(t, os.Chown(p, 2000, 2000))
	}

	return home
}

func TestActivate(t *testing.T) {
	a, _ := newAgent(t, true)
	home := activateHome(t, a)

	got, err := a.Activate("blog")
	must(t, err)

	if got.Site != "blog" || !strings.HasPrefix(got.Previous, "htdocs-before-import-") {
		t.Fatalf("%+v", got)
	}

	b, err := os.ReadFile(filepath.Join(home, sites.WebRootName, "index.html"))
	must(t, err)

	if string(b) != agentapi.CloudPanelImportDir {
		t.Fatalf("web root holds %q", b)
	}

	fi, err := os.Stat(filepath.Join(home, sites.WebRootName))
	must(t, err)

	if fi.Mode().Perm() != sites.WebRootMode {
		t.Fatalf("web root mode %v", fi.Mode())
	}

	b, err = os.ReadFile(filepath.Join(home, got.Previous, "index.html"))
	must(t, err)

	if string(b) != sites.WebRootName {
		t.Fatalf("previous web root holds %q", b)
	}

	// Nothing left to activate.
	if _, err := a.Activate("blog"); !errors.Is(err, ErrNothingImported) {
		t.Fatalf("again: %v", err)
	}
}

func TestActivateRefused(t *testing.T) {
	a, _ := newAgent(t, true)
	home := activateHome(t, a)

	if _, err := a.Activate("Blog"); !errors.Is(err, ErrInvalid) {
		t.Fatalf("bad name: %v", err)
	}

	if _, err := a.Activate("shop"); !errors.Is(err, ErrNoSite) {
		t.Fatalf("unknown site: %v", err)
	}

	// A running job holds the lock.
	must(t, ensureDir(a.Dir))

	unlock, err := lock(filepath.Join(a.Dir, lockFile))
	must(t, err)

	if _, err := a.Activate("blog"); !errors.Is(err, ErrBusy) {
		t.Fatalf("while running: %v", err)
	}

	unlock()

	// A symlink in place of the import is not followed.
	imp := filepath.Join(home, agentapi.CloudPanelImportDir)
	must(t, os.RemoveAll(imp))
	must(t, os.Symlink("/etc", imp))

	if _, err := a.Activate("blog"); !errors.Is(err, ErrNothingImported) {
		t.Fatalf("symlink: %v", err)
	}

	// A directory of another user is refused too.
	must(t, os.Remove(imp))
	must(t, os.Mkdir(imp, 0o700))

	if _, err := a.Activate("blog"); !errors.Is(err, ErrNothingImported) {
		t.Fatalf("root-owned import: %v", err)
	}

	b, err := os.ReadFile(filepath.Join(home, sites.WebRootName, "index.html"))
	if err != nil || string(b) != sites.WebRootName {
		t.Fatalf("web root changed: %q %v", b, err)
	}
}
