// SPDX-License-Identifier: AGPL-3.0-or-later

package phpinstall

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/maki-it/hostashi/internal/agentapi"
)

type fakeUnits struct {
	active  map[string]bool
	started []string
	err     error
}

func (f *fakeUnits) Start(_ context.Context, v string) error {
	if f.err != nil {
		return f.err
	}

	f.started = append(f.started, v)

	return nil
}

func (f *fakeUnits) Active(_ context.Context, v string) (bool, error) { return f.active[v], nil }

func write(t *testing.T, root, name, content string, mod time.Time) {
	t.Helper()

	p := filepath.Join(root, name)
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}

	if err := os.WriteFile(p, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}

	if !mod.IsZero() {
		if err := os.Chtimes(p, mod, mod); err != nil {
			t.Fatal(err)
		}
	}
}

func TestStart(t *testing.T) {
	ctx := context.Background()
	u := &fakeUnits{active: map[string]bool{}}
	m := &Manager{Units: u, Installed: func() []string { return []string{"8.4"} }, Root: t.TempDir()}

	st, err := m.Start(ctx, "8.1")
	if err != nil || st.State != agentapi.PHPInstallRunning || st.Version != "8.1" {
		t.Fatalf("start: %+v %v", st, err)
	}

	if !slices.Equal(u.started, []string{"8.1"}) {
		t.Fatalf("started %v", u.started)
	}

	for _, v := range []string{"8.0", "8.6", "", "8.1 ", "../8.1", "8.1;reboot"} {
		if _, err := m.Start(ctx, v); err == nil {
			t.Errorf("%q accepted", v)
		}
	}

	if _, err := m.Start(ctx, "8.4"); !errors.Is(err, ErrInstalled) {
		t.Fatalf("installed version: %v", err)
	}

	u.active["7.4"] = true

	if _, err := m.Start(ctx, "8.2"); !errors.Is(err, ErrBusy) {
		t.Fatalf("second installation: %v", err)
	}

	if len(u.started) != 1 {
		t.Fatalf("a refused start reached systemd: %v", u.started)
	}
}

func TestStatus(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	u := &fakeUnits{active: map[string]bool{}}
	m := &Manager{Units: u, Root: root}

	if st, err := m.Status(ctx); err != nil || st.State != agentapi.PHPInstallIdle {
		t.Fatalf("no run: %+v %v", st, err)
	}

	old := time.Now().Add(-time.Hour)
	write(t, root, LogDir+"/7.4.log", "php-install: installing PHP 7.4\nE: Unable to locate package\n", old)

	st, err := m.Status(ctx)
	if err != nil || st.Version != "7.4" || st.State != agentapi.PHPInstallFailed || st.Log[len(st.Log)-1] != "E: Unable to locate package" {
		t.Fatalf("failed run: %+v %v", st, err)
	}

	write(t, root, LogDir+"/8.1.log", "php-install: installing PHP 8.1\n\x1b[31mcolour\x1b[0m\n\n"+DoneMarker+" PHP 8.1 installed\n", time.Time{})

	st, err = m.Status(ctx)
	if err != nil || st.Version != "8.1" || st.State != agentapi.PHPInstallSucceeded {
		t.Fatalf("newest run: %+v %v", st, err)
	}

	if slices.ContainsFunc(st.Log, func(l string) bool { return strings.ContainsRune(l, 0x1b) || l == "" }) {
		t.Fatalf("control characters or empty lines in the log: %q", st.Log)
	}

	// A running unit wins over the newest log.
	u.active["7.4"] = true

	st, err = m.Status(ctx)
	if err != nil || st.Version != "7.4" || st.State != agentapi.PHPInstallRunning {
		t.Fatalf("running: %+v %v", st, err)
	}
}

func TestStatusTailsLongLogs(t *testing.T) {
	root := t.TempDir()
	m := &Manager{Units: &fakeUnits{}, Root: root}

	var b strings.Builder
	for range 500 {
		b.WriteString("Unpacking something ...\n")
	}

	b.WriteString(DoneMarker + " PHP 8.2 installed\n")
	write(t, root, LogDir+"/8.2.log", b.String(), time.Time{})

	st, err := m.Status(context.Background())
	if err != nil || len(st.Log) != LogLines || st.State != agentapi.PHPInstallSucceeded {
		t.Fatalf("%d lines, %+v, %v", len(st.Log), st.State, err)
	}
}

func TestNewer(t *testing.T) {
	root := t.TempDir()
	m := &Manager{Root: root}
	lists := "Package: php8.6-fpm\nVersion: 8.6.0-1\n\nPackage: php8.5-fpm\n\nPackage: php9.0-fpm\n\nPackage: php8.10-fpm\n\nPackage: php8.6-fpm-dbg\n\nPackage: php5.6-fpm\n"
	write(t, root, AptLists+"/packages.sury.org_php_dists_trixie_main_binary-amd64_Packages", lists, time.Time{})

	if got := m.Newer(); len(got) != 0 {
		t.Fatalf("without the source: %v", got)
	}

	write(t, root, SourceFile, "Types: deb\n", time.Time{})

	if !m.Repository() {
		t.Fatal("source not seen")
	}

	if got, want := m.Newer(), []string{"8.6", "8.10", "9.0"}; !slices.Equal(got, want) {
		t.Fatalf("newer: %v, want %v", got, want)
	}
}
