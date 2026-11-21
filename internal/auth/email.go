// SPDX-License-Identifier: AGPL-3.0-or-later

package auth

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/maki-it/hostashi/internal/mail"
	"github.com/maki-it/hostashi/internal/store"
)

// EmailConfirmTTL is how long a confirmation link works (ADR-0044).
const EmailConfirmTTL = 24 * time.Hour

const emailRequestsPerHour = 5

var (
	// ErrEmailLinkInvalid is returned for an unknown, used or expired link.
	ErrEmailLinkInvalid = errors.New("auth: the confirmation link is invalid or expired")
	// ErrEmailUnchanged is returned when the address is already the account's.
	ErrEmailUnchanged = errors.New("auth: this is already your address")
	// ErrEmailInvalid is returned for anything but one bare address.
	ErrEmailInvalid = errors.New("auth: enter a single address like name@example.com")
)

// EmailState returns the account's confirmed and pending address.
func (s *Service) EmailState(ctx context.Context, userID int64) (store.EmailState, error) {
	e, err := s.st.UserEmail(ctx, userID, s.now())
	if err != nil {
		return store.EmailState{}, accountErr(err)
	}

	return e, nil
}

// RequestEmail stores a new address for the account and returns the secret
// of its confirmation link. The address is used only after the link is
// opened. Addresses are compared in lower case.
func (s *Service) RequestEmail(ctx context.Context, u store.User, address, ip string) (string, NewInvitation, error) {
	address, err := mail.ParseAddress(strings.TrimSpace(address))
	if err != nil {
		return "", NewInvitation{}, ErrEmailInvalid
	}

	address = strings.ToLower(address)

	cur, err := s.st.UserEmail(ctx, u.ID, s.now())
	if err != nil {
		return "", NewInvitation{}, accountErr(err)
	}

	if cur.Confirmed == address {
		return "", NewInvitation{}, ErrEmailUnchanged
	}

	key := "user:" + idString(u.ID)
	if s.emailLimit.Blocked(key) {
		return "", NewInvitation{}, ErrRateLimited
	}

	inv, link, err := s.newInvitation()
	if err != nil {
		return "", NewInvitation{}, err
	}

	link.Expires = inv.Created.Add(EmailConfirmTTL)

	if err := s.st.SetPendingEmail(ctx, u.ID, address, inv.TokenHash, inv.Created, link.Expires); err != nil {
		return "", NewInvitation{}, accountErr(err)
	}

	s.emailLimit.Fail(key)

	_ = s.st.Audit(ctx, &u.ID, "account.email.request", u.Username, map[string]string{"email": address, "ip": ip})

	return address, link, nil
}

// ConfirmEmail opens a confirmation link: the pending address becomes the
// account's. It returns the account and its address before, so the caller
// can tell the old address. Wrong links count against the client address.
func (s *Service) ConfirmEmail(ctx context.Context, secret, ip string) (store.User, string, string, error) {
	key := "ip:" + ip
	if s.ipLimit.Blocked(key) {
		return store.User{}, "", "", ErrRateLimited
	}

	if secret == "" || len(secret) > 128 {
		s.ipLimit.Fail(key)

		return store.User{}, "", "", ErrEmailLinkInvalid
	}

	u, old, address, err := s.st.ConfirmEmail(ctx, hashToken(secret), s.now())
	if errors.Is(err, store.ErrNotFound) {
		s.ipLimit.Fail(key)

		return store.User{}, "", "", ErrEmailLinkInvalid
	}

	if err != nil {
		return store.User{}, "", "", fmt.Errorf("auth: %w", err)
	}

	_ = s.st.Audit(ctx, &u.ID, "account.email.confirm", u.Username, map[string]string{"email": address, "ip": ip})

	u.PasswordHash, u.TOTPSecret = "", nil

	return u, old, address, nil
}

// RemoveEmail clears the account's address and a pending one.
func (s *Service) RemoveEmail(ctx context.Context, u store.User, ip string) (string, error) {
	old, err := s.st.RemoveEmail(ctx, u.ID)
	if err != nil {
		return "", accountErr(err)
	}

	_ = s.st.Audit(ctx, &u.ID, "account.email.remove", u.Username, map[string]string{"ip": ip})

	return old, nil
}
