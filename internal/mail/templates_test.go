// SPDX-License-Identifier: AGPL-3.0-or-later

package mail

import (
	"maps"
	"slices"
	"strings"
	"testing"
	"time"
)

var at = time.Date(2026, 3, 5, 14, 30, 0, 0, time.FixedZone("CET", 3600))

func TestLanguage(t *testing.T) {
	for in, want := range map[string]Lang{"de": German, "en": English, "auto": English, "": English, "fr": English, "DE": English} {
		if got := Language(in); got != want {
			t.Errorf("%q: %q", in, got)
		}
	}
}

func TestFormatTime(t *testing.T) {
	if got := FormatTime(at, English); got != "5 March 2026, 13:30 UTC" {
		t.Errorf("en %q", got)
	}

	if got := FormatTime(at, German); got != "5. März 2026, 13:30 UTC" {
		t.Errorf("de %q", got)
	}

	if got := FormatTime(at.AddDate(0, 9, 0), German); got != "5. Dezember 2026, 13:30 UTC" {
		t.Errorf("de december %q", got)
	}
}

// accountMails builds every account email in a language.
func accountMails(l Lang) map[string]Message {
	out := map[string]Message{}
	out["confirm"], _ = ConfirmEmail(l, "kim@example.com", "kim", "https://panel.example.com/#/confirm-email/x", "panel.example.com", at)
	out["changed"], _ = EmailChanged(l, "old@example.com", "kim", "new@example.com", "panel.example.com", at)
	out["invite"], _ = Invitation(l, "ada@example.com", "kim", "ada", "user", "https://panel.example.com/#/invite/x", "panel.example.com", at)
	out["reset"], _ = ResetLink(l, "ada@example.com", "kim", "ada", "https://panel.example.com/#/invite/x", "panel.example.com", at)
	out["security"], _, _ = AccountSecurity(l, "kim@example.com", "kim", "passkey-added", "Laptop", "192.0.2.1", "panel.example.com", at)

	return out
}

func TestAccountMailsEnglish(t *testing.T) {
	for name, m := range accountMails(English) {
		if !strings.HasPrefix(m.Body, "Hello") || !strings.Contains(m.Body, "Sent by the Hostashi panel at panel.example.com.") ||
			!strings.Contains(m.Body, "5 March 2026, 13:30 UTC") || strings.Contains(m.Body, "Hallo") {
			t.Errorf("%s: %q\n%s", name, m.Subject, m.Body)
		}
	}
}

func TestAccountMailsGerman(t *testing.T) {
	en := accountMails(English)

	for name, m := range accountMails(German) {
		if m.Subject == en[name].Subject || m.To != en[name].To {
			t.Errorf("%s: subject %q to %q", name, m.Subject, m.To)
		}

		if !strings.HasPrefix(m.Body, "Hallo") || !strings.Contains(m.Body, "Gesendet vom Hostashi-Panel auf panel.example.com.") ||
			!strings.Contains(m.Body, "5. März 2026, 13:30 UTC") {
			t.Errorf("%s:\n%s", name, m.Body)
		}

		for _, english := range []string{"Hello", "Sent by", "The link", "If you", " the "} {
			if strings.Contains(m.Body, english) {
				t.Errorf("%s has English %q:\n%s", name, english, m.Body)
			}
		}
	}

	if m := accountMails(German)["invite"]; !strings.Contains(m.Body, "Dein Benutzername: ada (Rolle Benutzer)") || !strings.Contains(m.Body, "kim hat dich") {
		t.Errorf("invite:\n%s", m.Body)
	}

	if m := accountMails(German)["security"]; !strings.Contains(m.Body, "Ein Passkey wurde hinzugefügt: Laptop.") || !strings.Contains(m.Body, "Von der Adresse 192.0.2.1.") {
		t.Errorf("security:\n%s", m.Body)
	}
}

func TestUnknownLanguageIsEnglish(t *testing.T) {
	en := accountMails(English)

	for name, m := range accountMails(Lang("fr")) {
		if m.Subject != en[name].Subject || m.Body != en[name].Body {
			t.Errorf("%s: %q", name, m.Subject)
		}
	}
}

func TestSecurityTextsInBothLanguages(t *testing.T) {
	en := slices.Sorted(maps.Keys(securityText[English]))
	if de := slices.Sorted(maps.Keys(securityText[German])); !slices.Equal(en, de) {
		t.Fatalf("en %v de %v", en, de)
	}

	for _, kind := range en {
		for _, l := range []Lang{English, German} {
			if _, _, ok := AccountSecurity(l, "a@example.com", "kim", kind, "", "", "p", at); !ok {
				t.Errorf("%s %s", l, kind)
			}
		}
	}

	if _, _, ok := AccountSecurity(German, "a@example.com", "kim", "other", "", "", "p", at); ok {
		t.Error("unknown kind sent")
	}
}

func TestOperationsMailLanguages(t *testing.T) {
	ev := OpsEvent{
		Template: "backup-failed", Subject: "Backup failed", Lines: []string{"the backup did not save everything."},
		German: OpsText{Subject: "Backup fehlgeschlagen", Lines: []string{"das Backup hat nicht alles gesichert."}},
	}

	m, env := OperationsMail(German, "kim@example.com", "kim", "Backups", ev, "https://panel.example.com/#/backups", "panel.example.com")
	if m.Subject != "Backup fehlgeschlagen" || env.Template != "backup-failed" || env.Category != Operations ||
		m.Body != "Hallo kim,\n\ndas Backup hat nicht alles gesichert.\n\nDetails im Panel: https://panel.example.com/#/backups\n"+
			"\n-- \nGesendet vom Hostashi-Panel auf panel.example.com. Du bekommst diese E-Mail, weil Benachrichtigungen zu Backups in deinem Konto eingeschaltet sind. Du kannst sie unter Konto, Benachrichtigungen ausschalten.\n" {
		t.Errorf("de %q\n%s", m.Subject, m.Body)
	}

	m, _ = OperationsMail(English, "kim@example.com", "kim", "backups", ev, "", "panel.example.com")
	if m.Subject != "Backup failed" || !strings.HasPrefix(m.Body, "Hello kim,\n\nthe backup") || !strings.Contains(m.Body, "notifications about backups are on") {
		t.Errorf("en %q\n%s", m.Subject, m.Body)
	}

	// Without a German text the whole email is English.
	ev.German = OpsText{}
	if ev.Lang(German) != English {
		t.Fatal("lang without German text")
	}

	m, _ = OperationsMail(German, "kim@example.com", "kim", "backups", ev, "", "panel.example.com")
	if m.Subject != "Backup failed" || !strings.HasPrefix(m.Body, "Hello kim,") || strings.Contains(m.Body, "Gesendet") {
		t.Errorf("fallback %q\n%s", m.Subject, m.Body)
	}
}
