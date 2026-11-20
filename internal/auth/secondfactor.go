// SPDX-License-Identifier: AGPL-3.0-or-later

package auth

import (
	"context"
	"errors"
	"fmt"

	"github.com/maki-it/hostashi/internal/authz"
	"github.com/maki-it/hostashi/internal/store"
)

var (
	// ErrSecondFactorRequired is returned when the account must have a
	// second factor (ADR-0035) and the action would remove the last one.
	ErrSecondFactorRequired = errors.New("auth: this account must have a second factor (TOTP or a passkey)")
	// ErrPasskeyRequired is returned by a password login of an account that
	// must have a second factor and has passkeys but no TOTP: the password
	// alone is one factor, so such an account logs in with a passkey.
	ErrPasskeyRequired = errors.New("auth: log in with a passkey")
)

// SecondFactorRequired reports whether the account must have a second
// factor: admins and managers always, a user if an admin demands it.
func SecondFactorRequired(u store.User) bool {
	return authz.NeedsSecondFactor(authz.Role(u.Role)) || u.RequireSecondFactor
}

// HasSecondFactor reports whether the account has TOTP or a passkey.
func (s *Service) HasSecondFactor(ctx context.Context, u store.User) (bool, error) {
	if u.TOTPEnabled {
		return true, nil
	}

	n, err := s.st.PasskeyCount(ctx, u.ID)
	if err != nil {
		return false, fmt.Errorf("auth: %w", err)
	}

	return n > 0, nil
}

// SecondFactorMissing reports whether the account must have a second
// factor and has none. Such an account may only log in, log out, look at
// itself and set up TOTP or a passkey.
func (s *Service) SecondFactorMissing(ctx context.Context, u store.User) (bool, error) {
	if !SecondFactorRequired(u) {
		return false, nil
	}

	has, err := s.HasSecondFactor(ctx, u)

	return !has, err
}

// keepsSecondFactor fails with ErrSecondFactorRequired if the account must
// have a second factor and would have none after removing one TOTP setup
// (dropTOTP) or one passkey (dropPasskeys = 1).
func (s *Service) keepsSecondFactor(ctx context.Context, u store.User, dropTOTP bool, dropPasskeys int) error {
	if !SecondFactorRequired(u) {
		return nil
	}

	n, err := s.st.PasskeyCount(ctx, u.ID)
	if err != nil {
		return fmt.Errorf("auth: %w", err)
	}

	if (u.TOTPEnabled && !dropTOTP) || n-dropPasskeys > 0 {
		return nil
	}

	return ErrSecondFactorRequired
}
