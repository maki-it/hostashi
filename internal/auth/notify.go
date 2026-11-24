// SPDX-License-Identifier: AGPL-3.0-or-later

package auth

import "context"

// Security events of an account that its owner hears about by email
// (ADR-0044, account emails).
const (
	EventTOTPEnabled     = "totp-enabled"
	EventTOTPDisabled    = "totp-disabled"
	EventTOTPReplaced    = "totp-replaced"
	EventPasskeyAdded    = "passkey-added"
	EventPasskeyRemoved  = "passkey-removed"
	EventTokenCreated    = "token-created"
	EventPasswordChanged = "password-changed"
)

// AccountEvent is a change to an account's login or access.
type AccountEvent struct {
	UserID int64
	Kind   string
	// Name is the passkey or token name, if any.
	Name string
	// IP is the client address, if known.
	IP string
}

// OnAccountEvent sets the function told about security events. Call it
// once, before serving requests. It runs inside the request and must not
// block.
func (s *Service) OnAccountEvent(f func(context.Context, AccountEvent)) {
	s.notifyFn = f
}

func (s *Service) notify(ctx context.Context, e AccountEvent) {
	if s.notifyFn != nil {
		s.notifyFn(ctx, e)
	}
}
