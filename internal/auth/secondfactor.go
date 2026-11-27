// SPDX-License-Identifier: AGPL-3.0-or-later

package auth

import (
	"context"
	"errors"
	"fmt"
	"strconv"

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

// secondFactorPolicyKey is the setting that requires a second factor of
// every account, whatever its role (ADR-0035, addendum 1).
const secondFactorPolicyKey = "auth.require_second_factor"

// SecondFactorRequired reports whether the account must have a second
// factor: admins and managers always, a user if an admin demands it for
// that account or for every account.
func (s *Service) SecondFactorRequired(ctx context.Context, u store.User) (bool, error) {
	if authz.NeedsSecondFactor(authz.Role(u.Role)) || u.RequireSecondFactor {
		return true, nil
	}

	return s.SecondFactorForAll(ctx)
}

// SecondFactorForAll reports whether an admin requires a second factor of
// every account. It is off unless an admin turned it on.
func (s *Service) SecondFactorForAll(ctx context.Context) (bool, error) {
	v, ok, err := s.st.Setting(ctx, secondFactorPolicyKey)
	if err != nil || !ok {
		return false, err
	}

	on, err := strconv.ParseBool(v)
	if err != nil {
		return false, fmt.Errorf("auth: setting %s: %w", secondFactorPolicyKey, err)
	}

	return on, nil
}

// SetSecondFactorForAll turns the requirement for every account on or
// off. An actor without a second factor cannot turn it on, so that the
// switch never locks out the admin who flips it: admins have one anyway,
// this guards against a change of that rule. Accounts without a second
// factor are led to set one up at their next login, and their API tokens
// stop working until then.
func (s *Service) SetSecondFactorForAll(ctx context.Context, actor store.User, on bool, ip string) error {
	// A value that does not parse is overwritten, so an admin can always
	// repair it from the panel.
	was, err := s.SecondFactorForAll(ctx)
	if err != nil {
		was = !on
	}

	if on {
		has, err := s.HasSecondFactor(ctx, actor)
		if err != nil {
			return err
		}

		if !has {
			return ErrSecondFactorRequired
		}
	}

	if was == on {
		return nil
	}

	if err := s.st.SetSetting(ctx, secondFactorPolicyKey, strconv.FormatBool(on)); err != nil {
		return fmt.Errorf("auth: %w", err)
	}

	_ = s.st.AuditChange(ctx, &actor.ID, "settings.second_factor", "", map[string]string{
		"requireSecondFactor": strconv.FormatBool(on), "ip": ip,
	}, store.DiffOf(map[string]string{"requireSecondFactor": strconv.FormatBool(was)},
		map[string]string{"requireSecondFactor": strconv.FormatBool(on)}))

	return nil
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
	req, err := s.SecondFactorRequired(ctx, u)
	if err != nil || !req {
		return false, err
	}

	has, err := s.HasSecondFactor(ctx, u)

	return !has, err
}

// keepsSecondFactor fails with ErrSecondFactorRequired if the account must
// have a second factor and would have none after removing one TOTP setup
// (dropTOTP) or one passkey (dropPasskeys = 1).
func (s *Service) keepsSecondFactor(ctx context.Context, u store.User, dropTOTP bool, dropPasskeys int) error {
	req, err := s.SecondFactorRequired(ctx, u)
	if err != nil {
		return err
	}

	if !req {
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
