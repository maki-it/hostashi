// SPDX-License-Identifier: AGPL-3.0-or-later

package auth

// Kinds of credentials for Observer. The set is closed, so a metrics label
// built from it stays small (ADR-0022).
const (
	KindPassword = "password"
	KindTOTP     = "totp"
	KindPasskey  = "passkey"
	KindToken    = "token"
)

// Observer learns about failed and throttled logins. It gets only the kind
// of credential, never a user name, an address or a token.
type Observer interface {
	Failure(kind string)
	Throttled(kind string)
}

type nopObserver struct{}

func (nopObserver) Failure(string)   {}
func (nopObserver) Throttled(string) {}
