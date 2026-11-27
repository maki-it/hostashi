// SPDX-License-Identifier: AGPL-3.0-or-later

package notify

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/maki-it/hostashi/internal/agentapi"
	"github.com/maki-it/hostashi/internal/store"
)

// everyEvent builds one event of every template.
func everyEvent(t *testing.T) []Event {
	t.Helper()

	now := time.Date(2026, 11, 21, 12, 0, 0, 0, time.UTC)
	w := &Watcher{Now: func() time.Time { return now }}

	var out []Event

	ev, ok := w.backupEvent(agentapi.BackupStatus{Finished: now.Add(-time.Hour), Error: "exit 1", Items: []agentapi.BackupItemResult{
		{Kind: "files", Site: "shop", Partial: true}, {Kind: "database", Database: "shop_db", Error: "exit 2"},
	}})
	if !ok {
		t.Fatal("no backup event")
	}

	out = append(out, ev)

	settings := agentapi.DefaultBackupSettings()
	settings.IntervalHours = 1

	ev, ok = w.overdueEvent(agentapi.BackupInfo{Settings: settings, Ready: true}, agentapi.BackupStatus{OK: true, Finished: now.Add(-4 * time.Hour)})
	if !ok {
		t.Fatal("no overdue event")
	}

	out = append(out, ev)
	out = append(out, w.certEvents(Cert{Site: "shop", Name: "shop.example", NotAfter: now.Add(48 * time.Hour), Error: "rate limited", FailedAt: now})...)
	out = append(out, w.certEvents(Cert{Name: "panel.example", NotAfter: now.Add(-time.Hour)})...)
	out = append(out, w.doctorEvents(agentapi.DoctorReport{Checks: []agentapi.DoctorCheck{
		{ID: "system.disk", Status: agentapi.DoctorFail, Summary: "/: 2 GB / 38 GB (5 %)"},
		{ID: "site.disk", Site: "shop", Status: agentapi.DoctorWarn, Summary: "files: 9.2 GB / 10 GB (92 %)"},
		{ID: "site.database", Site: "shop", Status: agentapi.DoctorFail, Summary: "databases: 2.0 GiB / 2.0 GiB (100 %)"},
		{ID: "site.php", Site: "shop", Status: agentapi.DoctorWarn, Summary: "PHP 8.1: EOL"},
		{ID: "hostashi.update", Status: agentapi.DoctorWarn, Summary: "hostashi 0.2.0"},
		{ID: "site.home", Site: "shop", Status: agentapi.DoctorFail, Summary: "home: 0755"},
	}})...)

	se := store.ServiceEvent{Service: "php", Site: "shop", At: now, Detail: "exit-code"}
	out = append(out, downEvent(se), crashingEvent(se, 3), backEvent(&outage{id: "nginx", since: now}, now.Add(time.Hour)))
	out = append(out, MalwareFound("shop", store.ScanRun{ID: 1}, []store.ScanFinding{{Path: "x.php", Signature: "Php.Webshell-1"}}))
	out = append(out, DeployFailed("shop", store.SiteDeploy{ID: 7, Commit: "abc", Source: store.DeploySourceWebhook, Error: "exit 3"}))

	return out
}

func TestEveryEventHasGerman(t *testing.T) {
	seen := map[string]bool{}

	for _, ev := range everyEvent(t) {
		seen[ev.Mail.Template] = true

		de := ev.Mail.German
		if de.Subject == "" || len(de.Lines) == 0 || de.Subject == ev.Mail.Subject {
			t.Errorf("%s: no German text: %+v", ev.Mail.Template, de)
		}

		// The agent's summaries stay English; the test data avoids the words.
		for _, english := range []string{" the ", " of ", " failed", " and ", "Error:"} {
			if strings.Contains(de.Subject+"\n"+strings.Join(de.Lines, "\n"), english) {
				t.Errorf("%s has English %q: %q %q", ev.Mail.Template, english, de.Subject, de.Lines)
			}
		}
	}

	for _, tpl := range []string{"backup-failed", "backup-overdue", "cert-renewal-failed", "cert-expiry", "disk-full", "php-eol", "hostashi-update", "doctor-digest", "service-down", "service-crashing", "service-back", "malware-found", "deploy-failed"} {
		if !seen[tpl] {
			t.Errorf("not built: %s", tpl)
		}
	}
}

func TestEveryCategoryHasGermanTitle(t *testing.T) {
	for _, c := range Categories {
		if germanTitles[c] == "" || titles[c] == "" {
			t.Errorf("%s", c)
		}
	}
}

func TestNotifyInRecipientLanguage(t *testing.T) {
	n, st, ob := newNotifier(t)
	st.people[0].Language = "de"   // kim
	st.people[1].Language = "auto" // ada
	st.people[1].Prefs = map[string]bool{}

	for _, ev := range everyEvent(t)[:1] {
		if _, err := n.Notify(t.Context(), ev); err != nil {
			t.Fatal(err)
		}
	}

	if len(ob.sent) != 2 {
		t.Fatalf("sent %v", ob.to())
	}

	de, en := ob.sent[0], ob.sent[1]
	if de.To != "kim@example.com" || de.Subject != "Backup fehlgeschlagen" || !strings.HasPrefix(de.Body, "Hallo kim,\n\ndas Backup, das am 21. November 2026, 11:00 UTC fertig wurde") ||
		!strings.Contains(de.Body, "- Datenbank shop_db: exit 2") || !strings.Contains(de.Body, "Benachrichtigungen zu Backups") {
		t.Errorf("de %q\n%s", de.Subject, de.Body)
	}

	if en.To != "ada@example.com" || en.Subject != "Backup failed" || !strings.Contains(en.Body, "notifications about backups") {
		t.Errorf("en %q\n%s", en.Subject, en.Body)
	}
}

func TestAgentDownInGerman(t *testing.T) {
	n, st, ob := newNotifier(t)
	st.people[0].Language = "de"
	a := &ServiceAlerts{Notifier: n}
	at := time.Date(2026, 11, 21, 12, 0, 0, 0, time.UTC)

	for i := range agentPolls {
		a.AgentUnreachable(t.Context(), at.Add(time.Duration(i)*time.Minute), errors.New("connection refused"))
	}

	if len(ob.sent) == 0 || ob.sent[0].Subject != "Der Hostashi-Agent antwortet nicht" ||
		!strings.Contains(ob.sent[0].Body, "seit 21. November 2026, 12:00 UTC nicht mehr") || !strings.Contains(ob.sent[0].Body, "Benachrichtigungen zu Diensten") {
		t.Fatalf("%+v", ob.sent)
	}
}
