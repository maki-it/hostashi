// SPDX-License-Identifier: AGPL-3.0-or-later

package php

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func (f *fakeCtl) DaemonReload(context.Context) error {
	f.log("daemon-reload")

	return nil
}

func (f *fakeCtl) Restart(_ context.Context, unit string) error {
	f.log("restart " + unit)
	f.running[unit] = true

	return nil
}

func sliceOf(user string) string { return "hostashi-site-" + user + ".slice" }

func TestSliceDropIn(t *testing.T) {
	ctx := context.Background()
	m, ctl := newManager(t)
	m.Units, m.UnitDir, m.Slice = ctl, t.TempDir(), sliceOf

	home := t.TempDir()
	p := Pool{User: "shop", Version: "8.4", Home: home}
	dropIn := filepath.Join(m.UnitDir, "hostashi-php-8.4@shop.service.d", DropInName)

	if err := m.Ensure(ctx, []Pool{p}); err != nil {
		t.Fatal(err)
	}

	got, err := os.ReadFile(dropIn)
	if err != nil {
		t.Fatal(err)
	}

	if want := "[Service]\nSlice=hostashi-site-shop.slice\nBindPaths=" + home + "\nReadWritePaths=" + home + "\n"; !strings.HasSuffix(string(got), want) {
		t.Errorf("drop-in:\n%s", got)
	}

	if st, _ := os.Stat(dropIn); st.Mode().Perm() != 0o644 {
		t.Errorf("drop-in mode %v", st.Mode().Perm())
	}

	if calls := ctl.take(); calls != "test 8.4 shop.next.conf; daemon-reload; start hostashi-php-8.4@shop.service" {
		t.Errorf("new pool: %s", calls)
	}

	// Unchanged: nothing to do.
	if err := m.Ensure(ctx, []Pool{p}); err != nil {
		t.Fatal(err)
	}

	if calls := ctl.take(); calls != "" {
		t.Errorf("unchanged pool: %s", calls)
	}

	// A pool from before ADR-0015 (no drop-in) moves into its slice.
	if err := os.Remove(dropIn); err != nil {
		t.Fatal(err)
	}

	if err := m.Ensure(ctx, []Pool{p}); err != nil {
		t.Fatal(err)
	}

	if calls := ctl.take(); calls != "daemon-reload; restart hostashi-php-8.4@shop.service" {
		t.Errorf("old pool: %s", calls)
	}

	// Version switch: the old unit's drop-in goes, the new one gets one.
	p.Version = "8.3"
	if err := m.Ensure(ctx, []Pool{p}); err != nil {
		t.Fatal(err)
	}

	if _, err := os.Stat(filepath.Dir(dropIn)); !os.IsNotExist(err) {
		t.Errorf("old drop-in directory left: %v", err)
	}

	newDropIn := filepath.Join(m.UnitDir, "hostashi-php-8.3@shop.service.d", DropInName)
	if _, err := os.Stat(newDropIn); err != nil {
		t.Errorf("no drop-in for 8.3: %v", err)
	}

	if calls := ctl.take(); calls != "test 8.3 shop.next.conf; stop hostashi-php-8.4@shop.service; daemon-reload; start hostashi-php-8.3@shop.service" {
		t.Errorf("switch: %s", calls)
	}

	// Recover writes a missing drop-in and restarts that unit only.
	if err := os.Remove(newDropIn); err != nil {
		t.Fatal(err)
	}

	if err := m.Recover(ctx); err != nil {
		t.Fatal(err)
	}

	if calls := ctl.take(); calls != "daemon-reload; restart hostashi-php-8.3@shop.service" {
		t.Errorf("recover: %s", calls)
	}

	// Prune removes the drop-in with the pool.
	if err := m.Prune(ctx, map[string]bool{}); err != nil {
		t.Fatal(err)
	}

	if _, err := os.Stat(filepath.Dir(newDropIn)); !os.IsNotExist(err) {
		t.Errorf("drop-in directory left after prune: %v", err)
	}
}
