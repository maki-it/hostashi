// SPDX-License-Identifier: AGPL-3.0-or-later

package notify

import (
	"strings"
	"testing"
	"time"

	"github.com/maki-it/hostashi/internal/agentapi"
	"github.com/maki-it/hostashi/internal/doctor"
)

func TestGermanCheck(t *testing.T) {
	now := time.Date(2026, 11, 21, 12, 0, 0, 0, time.UTC)

	for _, c := range []struct {
		check         agentapi.DoctorCheck
		summary, hint string
	}{
		{doctor.Usage("shop", "disk space", 95, 100, func(n uint64) string { return strings.Repeat("x", int(n%3)) + "B" }), "Speicherplatz: xxB von xB (95 %)", "Fast voll"},
		{doctor.Usage("shop", "files", 85, 100, func(uint64) string { return "n" }), "Dateien: n von n (85 %)", "Füllt sich"},
		{doctor.DatabaseUsage("shop", 95, 100), "Datenbanken: 95 B von 100 B (95 %)", "Räum alte Daten auf"},
		{doctor.PHPSupport("shop", "8.1", now), "PHP 8.1 bekommt seit 2025-12-31 keine Sicherheitsupdates mehr", "nie selbst um"},
		{doctor.PHPSupport("shop", "4.0", now), "Für PHP 4.0 sind keine Support-Daten bekannt", "unterstützte PHP-Version"},
	} {
		if c.check.Code == "" {
			t.Errorf("no code: %+v", c.check)
		}

		s, h := germanCheck(c.check)
		if s != c.summary || !strings.Contains(h, c.hint) {
			t.Errorf("%s: %q %q", c.check.Code, s, h)
		}
	}

	// Without a code, with an unknown code or a missing value: English.
	for _, c := range []agentapi.DoctorCheck{
		{ID: "site.home", Summary: "home: 0755", Hint: "Fix it."},
		{ID: "x", Code: "x.new", Summary: "s", Hint: "h"},
		{ID: "site.disk", Code: "site.disk.warn", Params: map[string]string{"what": "files"}, Summary: "s", Hint: "h"},
	} {
		if s, h := germanCheck(c); s != c.Summary || h != c.Hint {
			t.Errorf("%+v: %q %q", c, s, h)
		}
	}

	c := doctor.DatabaseUsage("shop", 9_900_000_000, 10_737_418_240)
	if s, _ := germanCheck(c); s != "Datenbanken: 9,2 GiB von 10,0 GiB (92 %)" {
		t.Errorf("comma: %q", s)
	}

	if germanCheckTitle("site.tls") != "Zertifikat" || germanCheckTitle("new.check") != "new.check" {
		t.Error("titles")
	}
}

func TestDoctorMailsInGerman(t *testing.T) {
	w := &Watcher{Now: time.Now}
	rep := agentapi.DoctorReport{Checks: []agentapi.DoctorCheck{
		doctor.Usage("shop", "disk space", 95, 100, doctor.Bytes),
		{ID: "site.home", Site: "shop", Status: agentapi.DoctorFail, Summary: "home: 0755"},
		{
			ID: "hostashi.update", Status: agentapi.DoctorWarn, Summary: "hostashi 0.2.0 is available (installed: 0.1.0)", Hint: "Install it.",
			Code: "hostashi.update.available", Params: map[string]string{"available": "0.2.0", "installed": "0.1.0"},
		},
	}}

	got := map[string]string{}
	for _, ev := range w.doctorEvents(rep) {
		got[ev.Mail.Template] = ev.Mail.German.Subject + "\n" + strings.Join(ev.Mail.German.Lines, "\n")
	}

	for tpl, want := range map[string]string{
		"disk-full":       "die Site shop, Speicherplatz: 95 B von 100 B (95 %).\nFast voll",
		"hostashi-update": "Hostashi 0.2.0 ist verfügbar (installiert: 0.1.0).\nInstalliere es mit",
		"doctor-digest":   "- Site shop, Home-Verzeichnis: home: 0755",
	} {
		if !strings.Contains(got[tpl], want) {
			t.Errorf("%s: %q", tpl, got[tpl])
		}
	}
}
