// SPDX-License-Identifier: AGPL-3.0-or-later

package mail

import (
	"strings"
	"time"
	"unicode"
)

// Templates of ADR-0044: plain text with fixed wording, only short escaped
// values from the panel. Each returns a Message and its envelope. Every
// email exists in English and German (ADR-0071); the recipient's language
// picks one.

// Lang is the language of an email.
type Lang string

// The languages of the panel. English is the default and the fallback.
const (
	English Lang = "en"
	German  Lang = "de"
)

// Language maps an account's language setting (auto, en or de) to the
// language of its emails. An email has no browser to follow, so auto and
// anything unknown are English.
func Language(setting string) Lang {
	if setting == string(German) {
		return German
	}

	return English
}

// clean makes a value safe for a subject or a line: no control characters,
// at most n characters.
func clean(s string, n int) string {
	s = strings.Map(func(r rune) rune {
		if unicode.IsControl(r) {
			return -1
		}

		return r
	}, s)

	if r := []rune(s); len(r) > n {
		s = string(r[:n]) + "…"
	}

	return s
}

var germanMonths = [...]string{
	"Januar", "Februar", "März", "April", "Mai", "Juni",
	"Juli", "August", "September", "Oktober", "November", "Dezember",
}

// FormatTime writes a time in UTC the way the emails of the language do:
// "2 January 2006, 15:04 UTC" or "2. Januar 2006, 15:04 UTC".
func FormatTime(t time.Time, l Lang) string {
	t = t.UTC()
	if l == German {
		return t.Format("2. ") + germanMonths[t.Month()-1] + t.Format(" 2006, 15:04 UTC")
	}

	return t.Format("2 January 2006, 15:04 UTC")
}

// words are the parts every email shares.
type words struct {
	hello, helloAll string // "Hello x," and the greeting without a name
	footer          string // %s: the panel's host
	opsFooter       string // %s: the panel's host, then the category
	details         string
}

var common = map[Lang]words{
	English: {
		hello: "Hello ", helloAll: "Hello,",
		footer:    "\n-- \nSent by the Hostashi panel at %s. You get this email because of your account there.\n",
		opsFooter: "\n-- \nSent by the Hostashi panel at %s. You get this email because notifications about %s are on in your account. You can turn them off under Account, Notifications.\n",
		details:   "Details in the panel: ",
	},
	German: {
		hello: "Hallo ", helloAll: "Hallo,",
		footer:    "\n-- \nGesendet vom Hostashi-Panel auf %s. Du bekommst diese E-Mail wegen deines Kontos dort.\n",
		opsFooter: "\n-- \nGesendet vom Hostashi-Panel auf %s. Du bekommst diese E-Mail, weil Benachrichtigungen zu %s in deinem Konto eingeschaltet sind. Du kannst sie unter Konto, Benachrichtigungen ausschalten.\n",
		details:   "Details im Panel: ",
	},
}

// wordsOf returns the shared parts of a language; unknown is English.
func wordsOf(l Lang) words {
	if w, ok := common[l]; ok {
		return w
	}

	return common[English]
}

func greet(l Lang, username string) string {
	return wordsOf(l).hello + clean(username, 64) + ",\n\n"
}

func withFooter(l Lang, body, panel string) string {
	return body + strings.Replace(wordsOf(l).footer, "%s", clean(panel, 253), 1)
}

// ConfirmEmail asks the user to confirm a new address.
func ConfirmEmail(l Lang, to, username, link, panel string, expires time.Time) (Message, Envelope) {
	host := clean(panel, 253)
	subject := "Confirm your email address for Hostashi"
	body := greet(l, username) +
		"please confirm that this is your email address for the Hostashi panel at " + host + ":\n\n" +
		link + "\n\n" +
		"The link works once, until " + FormatTime(expires, l) + ".\n" +
		"If you did not ask for this, ignore this email. The address will not be used.\n"

	if l == German {
		subject = "Bestätige deine E-Mail-Adresse für Hostashi"
		body = greet(l, username) +
			"bitte bestätige, dass dies deine E-Mail-Adresse für das Hostashi-Panel auf " + host + " ist:\n\n" +
			link + "\n\n" +
			"Der Link funktioniert einmal, bis " + FormatTime(expires, l) + ".\n" +
			"Wenn du das nicht angefordert hast, ignoriere diese E-Mail. Die Adresse wird dann nicht verwendet.\n"
	}

	return Message{To: to, Subject: subject, Body: withFooter(l, body, panel)},
		Envelope{Template: "email-confirm", Category: Account}
}

// EmailChanged tells the old address that the account uses another one now.
func EmailChanged(l Lang, to, username, newAddress, panel string, at time.Time) (Message, Envelope) {
	subject := "Your Hostashi email address was changed"
	body := greet(l, username) +
		"the email address of your Hostashi account was changed on " + FormatTime(at, l) + ".\n" +
		"New address: " + clean(newAddress, 254) + "\n\n" +
		"Notifications go to the new address from now on. If you did not do this, " +
		"log in, check your account and tell an administrator.\n"

	if l == German {
		subject = "Die E-Mail-Adresse deines Hostashi-Kontos wurde geändert"
		body = greet(l, username) +
			"die E-Mail-Adresse deines Hostashi-Kontos wurde am " + FormatTime(at, l) + " geändert.\n" +
			"Neue Adresse: " + clean(newAddress, 254) + "\n\n" +
			"Benachrichtigungen gehen ab jetzt an die neue Adresse. Wenn du das nicht warst, " +
			"melde dich an, prüf dein Konto und sag einem Admin Bescheid.\n"
	}

	return Message{To: to, Subject: subject, Body: withFooter(l, body, panel)},
		Envelope{Template: "email-changed", Category: Account}
}

// germanRoles name the roles of ADR-0035 like the German panel does.
var germanRoles = map[string]string{"admin": "Admin", "manager": "Manager", "user": "Benutzer"}

// Invitation sends the one-time link of a new account (ADR-0035).
func Invitation(l Lang, to, invitedBy, username, role, link, panel string, expires time.Time) (Message, Envelope) {
	host := clean(panel, 253)
	subject := "You are invited to Hostashi"
	body := wordsOf(l).helloAll + "\n\n" +
		clean(invitedBy, 64) + " invited you to the Hostashi panel at " + host + ".\n" +
		"Your user name: " + clean(username, 64) + " (role " + clean(role, 16) + ")\n\n" +
		"Choose your password here:\n\n" +
		link + "\n\n" +
		"The link works once, until " + FormatTime(expires, l) + ". Opening it also confirms this email address for your account.\n" +
		"If you did not expect this invitation, ignore this email.\n"

	if l == German {
		if r, ok := germanRoles[role]; ok {
			role = r
		}

		subject = "Du bist zu Hostashi eingeladen"
		body = wordsOf(l).helloAll + "\n\n" +
			clean(invitedBy, 64) + " hat dich zum Hostashi-Panel auf " + host + " eingeladen.\n" +
			"Dein Benutzername: " + clean(username, 64) + " (Rolle " + clean(role, 16) + ")\n\n" +
			"Wähle hier dein Passwort:\n\n" +
			link + "\n\n" +
			"Der Link funktioniert einmal, bis " + FormatTime(expires, l) + ". Wenn du ihn öffnest, ist diese E-Mail-Adresse auch für dein Konto bestätigt.\n" +
			"Wenn du diese Einladung nicht erwartet hast, ignoriere diese E-Mail.\n"
	}

	return Message{To: to, Subject: subject, Body: withFooter(l, body, panel)},
		Envelope{Template: "invitation", Category: Account}
}

// ResetLink sends the new link after an admin reset the account.
func ResetLink(l Lang, to, resetBy, username, link, panel string, expires time.Time) (Message, Envelope) {
	host := clean(panel, 253)
	subject := "Your Hostashi account was reset"
	body := greet(l, username) +
		clean(resetBy, 64) + " reset your Hostashi account at " + host + ".\n" +
		"Your password, second factors and sessions were removed. Choose a new password here:\n\n" +
		link + "\n\n" +
		"The link works once, until " + FormatTime(expires, l) + ".\n" +
		"If you did not ask for this, tell an administrator.\n"

	if l == German {
		subject = "Dein Hostashi-Konto wurde zurückgesetzt"
		body = greet(l, username) +
			clean(resetBy, 64) + " hat dein Hostashi-Konto auf " + host + " zurückgesetzt.\n" +
			"Dein Passwort, deine zweiten Faktoren und deine Sitzungen wurden entfernt. Wähle hier ein neues Passwort:\n\n" +
			link + "\n\n" +
			"Der Link funktioniert einmal, bis " + FormatTime(expires, l) + ".\n" +
			"Wenn du das nicht angefordert hast, sag einem Admin Bescheid.\n"
	}

	return Message{To: to, Subject: subject, Body: withFooter(l, body, panel)},
		Envelope{Template: "account-reset", Category: Account}
}

// securityText says what happened, by event kind of internal/auth.
var securityText = map[Lang]map[string]string{ //nolint:gosec // event texts, not credentials
	English: {
		"totp-enabled":     "An authenticator app (TOTP) was set up as a second factor.",
		"totp-disabled":    "The authenticator app (TOTP) was removed as a second factor.",
		"totp-replaced":    "The authenticator app (TOTP) was moved to a new device. Codes from the old app and the old recovery codes no longer work.",
		"passkey-added":    "A passkey was added",
		"passkey-removed":  "A passkey was removed",
		"token-created":    "A new API token was created",
		"password-changed": "The password was changed and every other session was logged out.",
	},
	German: {
		"totp-enabled":     "Eine Authenticator-App (TOTP) wurde als zweiter Faktor eingerichtet.",
		"totp-disabled":    "Die Authenticator-App (TOTP) wurde als zweiter Faktor entfernt.",
		"totp-replaced":    "Die Authenticator-App (TOTP) ist auf ein neues Gerät umgezogen. Codes aus der alten App und die alten Wiederherstellungscodes funktionieren nicht mehr.",
		"passkey-added":    "Ein Passkey wurde hinzugefügt",
		"passkey-removed":  "Ein Passkey wurde entfernt",
		"token-created":    "Ein neues API-Token wurde erstellt",
		"password-changed": "Das Passwort wurde geändert und alle anderen Sitzungen wurden abgemeldet.",
	},
}

// AccountSecurity tells the user about a change to their login or access.
// It reports false for an unknown kind.
func AccountSecurity(l Lang, to, username, kind, name, ip, panel string, at time.Time) (Message, Envelope, bool) {
	texts, ok := securityText[l]
	if !ok {
		l, texts = English, securityText[English]
	}

	what, ok := texts[kind]
	if !ok {
		return Message{}, Envelope{}, false
	}

	if name != "" {
		what += ": " + clean(name, 64) + "."
	} else if !strings.HasSuffix(what, ".") {
		what += "."
	}

	host := clean(panel, 253)
	subject := "Security change in your Hostashi account"
	body := greet(l, username) + "your Hostashi account at " + host + " changed on " + FormatTime(at, l) + ".\n" + what + "\n"

	if ip != "" {
		body += "From the address " + clean(ip, 45) + ".\n"
	}

	body += "\nIf this was you, nothing needs to be done. If not, log in, check your account and tell an administrator.\n"

	if l == German {
		subject = "Sicherheitsänderung in deinem Hostashi-Konto"
		body = greet(l, username) + "dein Hostashi-Konto auf " + host + " wurde am " + FormatTime(at, l) + " geändert.\n" + what + "\n"

		if ip != "" {
			body += "Von der Adresse " + clean(ip, 45) + ".\n"
		}

		body += "\nWenn du das warst, ist nichts zu tun. Wenn nicht, melde dich an, prüf dein Konto und sag einem Admin Bescheid.\n"
	}

	return Message{To: to, Subject: subject, Body: withFooter(l, body, panel)},
		Envelope{Template: kind, Category: Account}, true
}

// OpsText is the subject and the lines of an operations email in one
// language.
type OpsText struct {
	Subject string
	Lines   []string
}

// OpsEvent is an operations notification before it has a recipient: a
// fixed subject and a few lines built from panel values. Subject and Lines
// are English; German is used for recipients who read German and falls
// back to English while its subject is empty.
type OpsEvent struct {
	Template string
	Subject  string
	Lines    []string
	German   OpsText
}

// Lang returns the language the event is written in for a recipient who
// reads l: l itself, or English when the event has no text in l.
func (e OpsEvent) Lang(l Lang) Lang {
	if l == German && e.German.Subject != "" {
		return German
	}

	return English
}

// Text returns the subject and lines in e.Lang(l).
func (e OpsEvent) Text(l Lang) OpsText {
	if e.Lang(l) == German {
		return e.German
	}

	return OpsText{Subject: e.Subject, Lines: e.Lines}
}

// OperationsMail addresses an operations event to one recipient. The
// whole email is in ev.Lang(l), and category names the notification switch
// in that language. link, when set, points to the page in the panel that
// shows the problem.
func OperationsMail(l Lang, to, username, category string, ev OpsEvent, link, panel string) (Message, Envelope) {
	l = ev.Lang(l)
	text := ev.Text(l)

	var b strings.Builder

	b.WriteString(greet(l, username))

	for _, line := range text.Lines {
		b.WriteString(clean(line, 300) + "\n")
	}

	if link != "" {
		b.WriteString("\n" + wordsOf(l).details + link + "\n")
	}

	f := strings.Replace(wordsOf(l).opsFooter, "%s", clean(panel, 253), 1)
	f = strings.Replace(f, "%s", clean(category, 32), 1)
	b.WriteString(f)

	return Message{To: to, Subject: clean(text.Subject, 120), Body: b.String()},
		Envelope{Template: ev.Template, Category: Operations}
}
