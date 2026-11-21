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
