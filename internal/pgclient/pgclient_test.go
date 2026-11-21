// SPDX-License-Identifier: AGPL-3.0-or-later

package pgclient

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"testing"
	"time"

	"github.com/maki-it/hostashi/internal/agentapi"
)

type fakeUnits struct {
	active  map[string]bool
	started []string
	err     error
}

func (f *fakeUnits) Start(_ context.Context, m string) error {
	if f.err != nil {
		return f.err
	}

	f.started = append(f.started, m)

	return nil
}

func (f *fakeUnits) Active(_ context.Context, m string) (bool, error) { return f.active[m], nil }

func write(t *testing.T, root, name, content string, mode os.FileMode, mod time.Time) {
	t.Helper()

	p := filepath.Join(root, name)
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}

	if err := os.WriteFile(p, []byte(content), mode); err != nil {
		t.Fatal(err)
	}

	if !mod.IsZero() {
		if err := os.Chtimes(p, mod, mod); err != nil {
			t.Fatal(err)
		}
	}
}

func TestInstalled(t *testing.T) {
	root := t.TempDir()
	m := &Manager{Root: root}

	if got := m.Installed(); len(got) != 0 {
		t.Fatalf("nothing installed: %v", got)
	}

	write(t, root, LibDir+"/17/bin/pg_dump", "x", 0o755, time.Time{})
	write(t, root, LibDir+"/9/bin/pg_dump", "x", 0o755, time.Time{})
	write(t, root, LibDir+"/18/bin/pg_dump", "x", 0o644, time.Time{}) // not executable
	write(t, root, LibDir+"/19/bin/psql", "x", 0o755, time.Time{})    // no pg_dump
	write(t, root, LibDir+"/019/bin/pg_dump", "x", 0o755, time.Time{})
	write(t, root, LibDir+"/x/bin/pg_dump", "x", 0o755, time.Time{})
	write(t, root, "elsewhere/pg_dump", "x", 0o755, time.Time{})

	if err := os.MkdirAll(filepath.Join(root, LibDir, "20", "bin"), 0o755); err != nil {
		t.Fatal(err)
	}

	if err := os.Symlink(filepath.Join(root, "elsewhere/pg_dump"), filepath.Join(root, LibDir, "20", "bin", "pg_dump")); err != nil {
		t.Fatal(err)
	}

	if got := m.Installed(); !slices.Equal(got, []string{"9", "17"}) {
		t.Fatalf("installed %v", got)
	}
}

func TestStart(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	u := &fakeUnits{active: map[string]bool{}}
	m := &Manager{Units: u, Root: root}
	newest := agentapi.PGClientNewest()

	st, err := m.Start(ctx, "")
	if err != nil || st.State != agentapi.PHPInstallRunning || st.Major != newest {
		t.Fatalf("start: %+v %v", st, err)
	}

	if !slices.Equal(u.started, []string{newest}) {
		t.Fatalf("started %v", u.started)
	}

	for _, v := range []string{"13", "99", "19 ", "../19", "19;reboot", "19.0"} {
		if _, err := m.Start(ctx, v); err == nil {
			t.Errorf("%q accepted", v)
		}
	}

	u.active[newest] = true

	if _, err := m.Start(ctx, newest); !errors.Is(err, ErrBusy) {
		t.Fatalf("second installation: %v", err)
	}

	u.active[newest] = false

	write(t, root, LibDir+"/"+newest+"/bin/pg_dump", "x", 0o755, time.Time{})

	if _, err := m.Start(ctx, newest); !errors.Is(err, ErrInstalled) {
		t.Fatalf("installed major: %v", err)
	}

	if len(u.started) != 1 {
		t.Fatalf("a refused start reached systemd: %v", u.started)
	}

	u.err = errors.New("systemd down")
	m2 := &Manager{Units: u, Root: t.TempDir()}

	if _, err := m2.Start(ctx, ""); err == nil {
		t.Fatal("start error swallowed")
	}
}

func TestStatus(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	u := &fakeUnits{active: map[string]bool{}}
	m := &Manager{Units: u, Root: root}
	newest := agentapi.PGClientNewest()

	st, err := m.Status(ctx)
	if err != nil || st.State != agentapi.PHPInstallIdle || st.Installed == nil || len(st.Installed) != 0 {
		t.Fatalf("no run: %+v %v", st, err)
	}

	write(t, root, LogDir+"/"+newest+".log", "pg-client-install: installing\nE: Unable to locate package\n", 0o600, time.Time{})

	if st, _ := m.Status(ctx); st.State != agentapi.PHPInstallFailed || st.Major != newest || len(st.Log) != 2 {
		t.Fatalf("failed run: %+v", st)
	}

	u.active[newest] = true

	if st, _ := m.Status(ctx); st.State != agentapi.PHPInstallRunning {
		t.Fatalf("running: %+v", st)
	}

	u.active[newest] = false

	write(t, root, LogDir+"/"+newest+".log", "x\n\x1b[31mcolour\x1b[0m\n"+DoneMarker+" PostgreSQL client "+newest+" installed\n", 0o600, time.Time{})
	write(t, root, LibDir+"/"+newest+"/bin/pg_dump", "x", 0o755, time.Time{})

	st, err = m.Status(ctx)
	if err != nil || st.State != agentapi.PHPInstallSucceeded || !slices.Equal(st.Installed, []string{newest}) {
		t.Fatalf("done: %+v %v", st, err)
	}

	if st.Log[1] != "[31mcolour[0m" {
		t.Errorf("control characters kept: %q", st.Log[1])
	}
}
