// SPDX-License-Identifier: AGPL-3.0-or-later

package auth

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/maki-it/hostashi/internal/mail"
	"github.com/maki-it/hostashi/internal/store"
)

// InvitationTTL is how long an invitation link works (ADR-0035).
const InvitationTTL = 24 * time.Hour

var (
	// ErrInvalidRole is returned for a role outside admin, manager, user.
	ErrInvalidRole = errors.New("auth: role must be admin, manager or user")
	// ErrSelf is returned when an admin tries to change their own role,
	// disable, delete or reset their own account.
	ErrSelf = errors.New("auth: you cannot do this to your own account")
	// ErrInvitationInvalid is returned for an unknown, used or expired link.
	ErrInvitationInvalid = errors.New("auth: the invitation link is invalid or expired")
	// ErrUserNotFound is returned for an unknown account id.
	ErrUserNotFound = errors.New("auth: no such user")
)

// ValidRole reports whether r is a role of ADR-0035.
func ValidRole(r string) bool {
	return r == "admin" || r == "manager" || r == "user"
}

// NewInvitation is returned once, when an invitation is made. The secret
// is never stored.
type NewInvitation struct {
	Secret  string
	Expires time.Time
	// Email is the address to send the link to; empty means show only.
	Email string
}

// ForgetInvitationEmail is called when the invitation email could not be
// queued: the link is then only shown, and accepting it confirms nothing.
func (s *Service) ForgetInvitationEmail(ctx context.Context, userID int64) error {
	if err := s.st.ClearInvitationEmail(ctx, userID); err != nil {
		return fmt.Errorf("auth: %w", err)
	}

	return nil
}

func (s *Service) newInvitation() (store.Invitation, NewInvitation, error) {
	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		return store.Invitation{}, NewInvitation{}, fmt.Errorf("auth: %w", err)
	}

	secret := base64.RawURLEncoding.EncodeToString(raw)
	now := s.now()
	end := now.Add(InvitationTTL)

	return store.Invitation{TokenHash: hashToken(secret), Created: now, Expires: end},
		NewInvitation{Secret: secret, Expires: end}, nil
}

func accountErr(err error) error {
	switch {
	case errors.Is(err, store.ErrNotFound):
		return ErrUserNotFound
	case errors.Is(err, store.ErrLastAdmin), errors.Is(err, store.ErrUserExists):
		return err
	default:
		return fmt.Errorf("auth: %w", err)
	}
}

func idString(id int64) string { return strconv.FormatInt(id, 10) }

// Accounts lists every account without secrets.
func (s *Service) Accounts(ctx context.Context) ([]store.Account, error) {
	a, err := s.st.Accounts(ctx)
	if err != nil {
		return nil, fmt.Errorf("auth: %w", err)
	}

	return a, nil
}

// Account returns one account or ErrUserNotFound.
func (s *Service) Account(ctx context.Context, id int64) (store.Account, error) {
	a, err := s.st.AccountByID(ctx, id)
	if err != nil {
		return store.Account{}, accountErr(err)
	}

	return a, nil
}

// CreateAccount adds an account without a password and returns its
// invitation. The caller has checked that actor may manage users.
//
// email is optional: the address the invitation is emailed to, which
// becomes the account's address once the link is accepted (ADR-0044).
func (s *Service) CreateAccount(ctx context.Context, actor store.User, username, role, email, ip string) (store.Account, NewInvitation, error) {
	if err := ValidateUsername(username); err != nil {
		return store.Account{}, NewInvitation{}, err
	}

	if !ValidRole(role) {
		return store.Account{}, NewInvitation{}, ErrInvalidRole
	}

	if email != "" {
		addr, err := mail.ParseAddress(strings.TrimSpace(email))
		if err != nil {
			return store.Account{}, NewInvitation{}, ErrEmailInvalid
		}

		email = strings.ToLower(addr)
	}

	inv, out, err := s.newInvitation()
	if err != nil {
		return store.Account{}, NewInvitation{}, err
	}

	inv.Email = email
	out.Email = email

	a, err := s.st.CreateAccount(ctx, username, role, inv)
	if err != nil {
		return store.Account{}, NewInvitation{}, accountErr(err)
	}

	detail := map[string]string{"role": role, "id": idString(a.ID), "ip": ip}
	if email != "" {
		detail["email"] = email
	}

	_ = s.st.Audit(ctx, &actor.ID, "user.create", username, detail)

	return a, out, nil
}

// UpdateAccount changes another account's role, disabled flag or demand
// for a second factor. Nobody changes their own; the last enabled admin
// stays.
func (s *Service) UpdateAccount(ctx context.Context, actor store.User, id int64, c store.AccountChange, ip string) (store.Account, error) {
	if id == actor.ID {
		return store.Account{}, ErrSelf
	}

	if c.Role != nil && !ValidRole(*c.Role) {
		return store.Account{}, ErrInvalidRole
	}

	before, after, err := s.st.UpdateAccount(ctx, id, c)
	if err != nil {
		return store.Account{}, accountErr(err)
	}

	detail := map[string]string{"id": idString(id), "ip": ip}
	if before.Role != after.Role {
		detail["role"] = before.Role + " -> " + after.Role
	}

	if before.Disabled != after.Disabled {
		detail["disabled"] = strconv.FormatBool(after.Disabled)
	}

	if before.RequireSecondFactor != after.RequireSecondFactor {
		detail["requireSecondFactor"] = strconv.FormatBool(after.RequireSecondFactor)
	}

	if len(detail) > 2 {
		_ = s.st.Audit(ctx, &actor.ID, "user.update", after.Username, detail)
	}

	return after, nil
}

// DeleteAccount removes another account. The last enabled admin stays.
func (s *Service) DeleteAccount(ctx context.Context, actor store.User, id int64, ip string) error {
	if id == actor.ID {
		return ErrSelf
	}

	a, err := s.st.DeleteAccount(ctx, id)
	if err != nil {
		return accountErr(err)
	}

	_ = s.st.Audit(ctx, &actor.ID, "user.delete", a.Username, map[string]string{"id": idString(id), "role": a.Role, "ip": ip})

	return nil
}

// ResetAccount removes another account's password and second factors,
// ends its sessions and returns a new invitation.
func (s *Service) ResetAccount(ctx context.Context, actor store.User, id int64, ip string) (NewInvitation, error) {
	if id == actor.ID {
		return NewInvitation{}, ErrSelf
	}

	a, err := s.st.AccountByID(ctx, id)
	if err != nil {
		return NewInvitation{}, accountErr(err)
	}

	e, err := s.st.UserEmail(ctx, id, s.now())
	if err != nil {
		return NewInvitation{}, accountErr(err)
	}

	inv, out, err := s.newInvitation()
	if err != nil {
		return NewInvitation{}, err
	}

	// The reset link goes to the confirmed address, if there is one.
	inv.Email, out.Email = e.Confirmed, e.Confirmed

	if err := s.st.ResetAccount(ctx, id, inv); err != nil {
		return NewInvitation{}, accountErr(err)
	}

	_ = s.st.Audit(ctx, &actor.ID, "user.reset", a.Username, map[string]string{"id": idString(id), "ip": ip})

	return out, nil
}

// RecoverAccount is the way back in for someone with root on the server
// (ADR-0035): it resets the account like ResetAccount, enables it if it
// was disabled, and returns a new invitation. No panel account acts, so
// the audit log names root.
func (s *Service) RecoverAccount(ctx context.Context, username string) (NewInvitation, store.Account, error) {
	u, err := s.st.UserByUsername(ctx, username)
	if err != nil {
		return NewInvitation{}, store.Account{}, accountErr(err)
	}

	inv, out, err := s.newInvitation()
	if err != nil {
		return NewInvitation{}, store.Account{}, err
	}

	if err := s.st.ResetAccount(ctx, u.ID, inv); err != nil {
		return NewInvitation{}, store.Account{}, accountErr(err)
	}

	detail := map[string]string{"id": idString(u.ID), "by": "root"}

	if u.Disabled {
		off := false
		if _, _, err := s.st.UpdateAccount(ctx, u.ID, store.AccountChange{Disabled: &off}); err != nil {
			return NewInvitation{}, store.Account{}, accountErr(err)
		}

		detail["disabled"] = "false"
	}

	_ = s.st.Audit(ctx, nil, "user.recover", u.Username, detail)

	a, err := s.st.AccountByID(ctx, u.ID)
	if err != nil {
		return NewInvitation{}, store.Account{}, accountErr(err)
	}

	return out, a, nil
}

// SetSiteGrants replaces the sites granted to a user with the role user.
func (s *Service) SetSiteGrants(ctx context.Context, actor store.User, id int64, sites []string, ip string) ([]string, error) {
	a, err := s.st.AccountByID(ctx, id)
	if err != nil {
		return nil, accountErr(err)
	}

	added, removed, err := s.st.SetSiteGrants(ctx, id, sites, s.now())
	if err != nil {
		return nil, err
	}

	if len(added)+len(removed) > 0 {
		_ = s.st.Audit(ctx, &actor.ID, "user.grants", a.Username, map[string]string{
			"id": idString(id), "added": fmt.Sprint(added), "removed": fmt.Sprint(removed), "ip": ip,
		})
	}

	return s.st.SiteGrants(ctx, id)
}

// SiteGrants returns the sites granted to a user.
func (s *Service) SiteGrants(ctx context.Context, id int64) ([]string, error) {
	if _, err := s.st.AccountByID(ctx, id); err != nil {
		return nil, accountErr(err)
	}

	g, err := s.st.SiteGrants(ctx, id)
	if err != nil {
		return nil, fmt.Errorf("auth: %w", err)
	}

	return g, nil
}

// InvitedUsername returns the username of an open invitation, so the page
// can greet the new user. Wrong links count against the address.
func (s *Service) InvitedUsername(ctx context.Context, secret, ip string) (string, error) {
	key := "ip:" + ip
	if s.ipLimit.Blocked(key) {
		return "", ErrRateLimited
	}

	if secret == "" || len(secret) > 128 {
		s.ipLimit.Fail(key)

		return "", ErrInvitationInvalid
	}

	u, err := s.st.InvitedUser(ctx, hashToken(secret), s.now())
	if errors.Is(err, store.ErrNotFound) || err == nil && u.Disabled {
		s.ipLimit.Fail(key)

		return "", ErrInvitationInvalid
	}

	if err != nil {
		return "", fmt.Errorf("auth: %w", err)
	}

	return u.Username, nil
}

// AcceptInvitation sets the password through an invitation link and
// starts a session, so the new user can enroll a second factor right away.
func (s *Service) AcceptInvitation(ctx context.Context, secret, password, ip string) (string, store.User, error) {
	key := "ip:" + ip
	if s.ipLimit.Blocked(key) {
		return "", store.User{}, ErrRateLimited
	}

	if err := ValidatePassword(password); err != nil {
		return "", store.User{}, err
	}

	if secret == "" || len(secret) > 128 {
		s.ipLimit.Fail(key)

		return "", store.User{}, ErrInvitationInvalid
	}

	hash, err := HashPassword(password)
	if err != nil {
		return "", store.User{}, err
	}

	u, err := s.st.AcceptInvitation(ctx, hashToken(secret), hash, s.now())
	if errors.Is(err, store.ErrNotFound) {
		s.ipLimit.Fail(key)

		return "", store.User{}, ErrInvitationInvalid
	}

	if err != nil {
		return "", store.User{}, fmt.Errorf("auth: %w", err)
	}

	token, err := s.newSession(ctx, u.ID)
	if err != nil {
		return "", store.User{}, err
	}

	_ = s.st.Audit(ctx, &u.ID, "invitation.accept", u.Username, map[string]string{"ip": ip})

	u.PasswordHash, u.TOTPSecret = "", nil

	return token, u, nil
}

// HasSiteGrant reports whether the site is granted to the user.
func (s *Service) HasSiteGrant(ctx context.Context, userID int64, site string) (bool, error) {
	ok, err := s.st.HasSiteGrant(ctx, userID, site)
	if err != nil {
		return false, fmt.Errorf("auth: %w", err)
	}

	return ok, nil
}
