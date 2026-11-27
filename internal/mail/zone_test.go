// SPDX-License-Identifier: AGPL-3.0-or-later

package mail

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestMain(m *testing.M) {
	ServerZone = func() *time.Location { return time.UTC }

	os.Exit(m.Run())
}

func TestFormatTimeServerZone(t *testing.T) {
	berlin, err := time.LoadLocation("Europe/Berlin")
	if err != nil {
		t.Skip("no tzdata")
	}

	old := ServerZone
	ServerZone = func() *time.Location { return berlin }

	t.Cleanup(func() { ServerZone = old })

	if got := FormatTime(at, German); got != "5. März 2026, 14:30 CET" {
		t.Errorf("de %q", got)
	}

	if got := FormatTime(at.AddDate(0, 4, 0), English); got != "5 July 2026, 15:30 CEST" {
		t.Errorf("en summer %q", got)
	}
}

func TestZoneAt(t *testing.T) {
	if _, err := time.LoadLocation("Europe/Berlin"); err != nil {
		t.Skip("no tzdata")
	}

	root := t.TempDir()
	etc := filepath.Join(root, "etc")

	if err := os.Mkdir(etc, 0o755); err != nil {
		t.Fatal(err)
	}

	// Nothing there: the process's zone.
	if got := zoneAt(root); got != time.Local {
		t.Errorf("empty: %v", got)
	}

	// The link timedated writes.
	if err := os.Symlink("/usr/share/zoneinfo/Europe/Berlin", filepath.Join(etc, "localtime")); err != nil {
		t.Fatal(err)
	}

	if got := zoneAt(root).String(); got != "Europe/Berlin" {
		t.Errorf("link: %s", got)
	}

	// /etc/timezone wins; an unknown name falls back.
	if err := os.WriteFile(filepath.Join(etc, "timezone"), []byte("Europe/Vienna\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	if got := zoneAt(root).String(); got != "Europe/Vienna" {
		t.Errorf("file: %s", got)
	}

	if err := os.WriteFile(filepath.Join(etc, "timezone"), []byte("Mars/Olympus\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	if got := zoneAt(root); got != time.Local {
		t.Errorf("unknown: %v", got)
	}
}
