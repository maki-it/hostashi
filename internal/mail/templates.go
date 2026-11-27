// SPDX-License-Identifier: AGPL-3.0-or-later

package mail

import (
	"strings"
	"time"
	"unicode"
)

// Templates of ADR-0044: plain text in English, fixed wording, only short
// escaped values from the panel. Each returns a Message and its envelope.

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

func when(t time.Time) string { return t.UTC().Format("2 January 2006, 15:04 UTC") }

const footer = "\n-- \nSent by the Hostashi panel at %s. You get this email because of your account there.\n"

func withFooter(body, panel string) string {
	return body + strings.Replace(footer, "%s", clean(panel, 253), 1)
}

// ConfirmEmail asks the user to confirm a new address.
func ConfirmEmail(to, username, link, panel string, expires time.Time) (Message, Envelope) {
	body := "Hello " + clean(username, 64) + ",\n\n" +
		"please confirm that this is your email address for the Hostashi panel at " + clean(panel, 253) + ":\n\n" +
		link + "\n\n" +
		"The link works once, until " + when(expires) + ".\n" +
		"If you did not ask for this, ignore this email. The address will not be used.\n"

	return Message{To: to, Subject: "Confirm your email address for Hostashi", Body: withFooter(body, panel)},
		Envelope{Template: "email-confirm", Category: Account}
}

// EmailChanged tells the old address that the account uses another one now.
func EmailChanged(to, username, newAddress, panel string, at time.Time) (Message, Envelope) {
	body := "Hello " + clean(username, 64) + ",\n\n" +
		"the email address of your Hostashi account was changed on " + when(at) + ".\n" +
		"New address: " + clean(newAddress, 254) + "\n\n" +
		"Notifications go to the new address from now on. If you did not do this, " +
		"log in, check your account and tell an administrator.\n"

	return Message{To: to, Subject: "Your Hostashi email address was changed", Body: withFooter(body, panel)},
		Envelope{Template: "email-changed", Category: Account}
}

// Invitation sends the one-time link of a new account (ADR-0035).
func Invitation(to, invitedBy, username, role, link, panel string, expires time.Time) (Message, Envelope) {
	body := "Hello,\n\n" +
		clean(invitedBy, 64) + " invited you to the Hostashi panel at " + clean(panel, 253) + ".\n" +
		"Your user name: " + clean(username, 64) + " (role " + clean(role, 16) + ")\n\n" +
		"Choose your password here:\n\n" +
		link + "\n\n" +
		"The link works once, until " + when(expires) + ". Opening it also confirms this email address for your account.\n" +
		"If you did not expect this invitation, ignore this email.\n"

	return Message{To: to, Subject: "You are invited to Hostashi", Body: withFooter(body, panel)},
		Envelope{Template: "invitation", Category: Account}
}

// ResetLink sends the new link after an admin reset the account.
func ResetLink(to, resetBy, username, link, panel string, expires time.Time) (Message, Envelope) {
	body := "Hello " + clean(username, 64) + ",\n\n" +
		clean(resetBy, 64) + " reset your Hostashi account at " + clean(panel, 253) + ".\n" +
		"Your password, second factors and sessions were removed. Choose a new password here:\n\n" +
		link + "\n\n" +
		"The link works once, until " + when(expires) + ".\n" +
		"If you did not ask for this, tell an administrator.\n"

	return Message{To: to, Subject: "Your Hostashi account was reset", Body: withFooter(body, panel)},
		Envelope{Template: "account-reset", Category: Account}
}

// securityText says what happened, by event kind of internal/auth.
var securityText = map[string]string{ //nolint:gosec // event texts, not credentials
	"totp-enabled":     "An authenticator app (TOTP) was set up as a second factor.",
	"totp-disabled":    "The authenticator app (TOTP) was removed as a second factor.",
	"totp-replaced":    "The authenticator app (TOTP) was moved to a new device. Codes from the old app and the old recovery codes no longer work.",
	"passkey-added":    "A passkey was added",
	"passkey-removed":  "A passkey was removed",
	"token-created":    "A new API token was created",
	"password-changed": "The password was changed and every other session was logged out.",
}

// AccountSecurity tells the user about a change to their login or access.
// It reports false for an unknown kind.
func AccountSecurity(to, username, kind, name, ip, panel string, at time.Time) (Message, Envelope, bool) {
	what, ok := securityText[kind]
	if !ok {
		return Message{}, Envelope{}, false
	}

	if name != "" {
		what += ": " + clean(name, 64) + "."
	} else if !strings.HasSuffix(what, ".") {
		what += "."
	}

	body := "Hello " + clean(username, 64) + ",\n\n" +
		"your Hostashi account at " + clean(panel, 253) + " changed on " + when(at) + ".\n" +
		what + "\n"
	if ip != "" {
		body += "From the address " + clean(ip, 45) + ".\n"
	}

	body += "\nIf this was you, nothing needs to be done. If not, log in, check your account and tell an administrator.\n"

	return Message{To: to, Subject: "Security change in your Hostashi account", Body: withFooter(body, panel)},
		Envelope{Template: kind, Category: Account}, true
}

// OpsEvent is an operations notification before it has a recipient: a
// fixed subject and a few lines built from panel values.
type OpsEvent struct {
	Template string
	Subject  string
	Lines    []string
}

const opsFooter = "\n-- \nSent by the Hostashi panel at %s. You get this email because notifications about %s are on in your account. You can turn them off under Account, Notifications.\n"

// OperationsMail addresses an operations event to one recipient. link, when
// set, points to the page in the panel that shows the problem.
func OperationsMail(to, username, category string, ev OpsEvent, link, panel string) (Message, Envelope) {
	var b strings.Builder

	b.WriteString("Hello " + clean(username, 64) + ",\n\n")

	for _, l := range ev.Lines {
		b.WriteString(clean(l, 300) + "\n")
	}

	if link != "" {
		b.WriteString("\nDetails in the panel: " + link + "\n")
	}

	f := strings.Replace(opsFooter, "%s", clean(panel, 253), 1)
	f = strings.Replace(f, "%s", clean(category, 32), 1)
	b.WriteString(f)

	return Message{To: to, Subject: clean(ev.Subject, 120), Body: b.String()},
		Envelope{Template: ev.Template, Category: Operations}
}
