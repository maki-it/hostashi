// SPDX-License-Identifier: AGPL-3.0-or-later

package auth

import (
	"context"
	"log/slog"
	"net/netip"
)

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

// FailedEvent is the message of the log line written for every failed login,
// failed second factor, failed passkey login and unknown API token. fail2ban's
// hostashi-panel filter matches this line (ADR-0021), so its shape is fixed:
// the message, then "kind" from the closed set above, then "ip" (and the
// request ID where the logger adds one). The user
// name and the token never appear in it; they stay in the audit log.
const FailedEvent = "auth.failed"

// failed records one failure of kind from ip: for the Observer, and as the
// fixed log line for fail2ban. An address that does not parse is not logged,
// so the line only ever carries a canonical IP that fail2ban can ban.
func (s *Service) failed(ctx context.Context, kind, ip string) {
	s.obs.Failure(kind)

	addr, err := netip.ParseAddr(ip)
	if err != nil {
		return
	}

	addr = addr.Unmap().WithZone("")

	s.flog.Log(ctx, slog.LevelWarn, FailedEvent, "kind", kind, "ip", addr.String())
}
