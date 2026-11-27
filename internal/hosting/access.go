// SPDX-License-Identifier: AGPL-3.0-or-later

package hosting

import (
	"context"
	"errors"
	"fmt"
	"strconv"

	"golang.org/x/crypto/bcrypt"

	"github.com/maki-it/hostashi/internal/agentapi"
	"github.com/maki-it/hostashi/internal/store"
)

// Password protection and IP rules per site (ADR-0066).

// GeneratedAccessPasswordLen is the length of a generated basic auth
// password: 24 letters and digits, about 143 bits.
const GeneratedAccessPasswordLen = 24

// ErrAuthUserExists is a basic auth user name the site already has.
var ErrAuthUserExists = errors.New("hosting: user exists")

// ErrAuthUserNotFound is a basic auth user the site does not have.
var ErrAuthUserNotFound = errors.New("hosting: no such user")

// SiteAccessView is a site's access setting with its users, without
// their hashes.
type SiteAccessView struct {
	store.SiteAccess
	Users []store.AuthUser
}

// Access returns the access setting and users of a site.
func (s *Service) Access(ctx context.Context, name string) (SiteAccessView, error) {
	site, err := s.Get(ctx, name)
	if err != nil {
		return SiteAccessView{}, err
	}

	return s.accessView(ctx, site.ID)
}

func (s *Service) accessView(ctx context.Context, siteID int64) (SiteAccessView, error) {
	a, err := s.st.SiteAccess(ctx, siteID)
	if err != nil {
		return SiteAccessView{}, err
	}

	us, err := s.st.AuthUsers(ctx, siteID)
	if err != nil {
		return SiteAccessView{}, err
	}

	for i := range us {
		us[i].Hash = ""
	}

	return SiteAccessView{SiteAccess: a, Users: us}, nil
}

// webAccessOf combines a setting and its users into what the agent gets.
func webAccessOf(a store.AccessSettings, users []store.AuthUser) agentapi.WebAccess {
	out := agentapi.WebAccess{IPMode: a.IPMode, IPs: a.IPs}
	if a.Password {
		out.Auth = &agentapi.WebAuth{Paths: a.Paths, Bypass: a.Bypass, Users: make([]agentapi.WebAuthUser, 0, len(users))}
		for _, u := range users {
			out.Auth.Users = append(out.Auth.Users, agentapi.WebAuthUser{Name: u.Name, Hash: u.Hash})
		}
	}

	return out
}

// checkAccess validates a setting with its users, also while password
// protection is off, and refuses bypass addresses of this server and
// snippets with the same directives. An error matches ErrInvalid and
// agentapi.ErrAccessRejected or agentapi.ErrAccessConflict.
func (s *Service) checkAccess(a store.AccessSettings, users []store.AuthUser, sn store.SiteSnippets) error {
	w := webAccessOf(a, users)
	if err := w.Validate(); err != nil {
		return fmt.Errorf("%w: %w", ErrInvalid, err)
	}

	if !a.Password {
		if err := (agentapi.WebAuth{Paths: a.Paths, Bypass: a.Bypass}).ValidateRules(); err != nil {
			return fmt.Errorf("%w: %w", ErrInvalid, err)
		}
	}

	// The agent checks the bypass addresses against the interfaces; the
	// web process cannot read them (no netlink socket).
	if len(a.Bypass) > 0 && s.localAddrs != nil {
		own, err := s.localAddrs()
		if err != nil {
			return fmt.Errorf("hosting: addresses of this server: %w", err)
		}

		bw := agentapi.WebAccess{Auth: &agentapi.WebAuth{Bypass: a.Bypass}}
		if err := bw.CheckOwn(own); err != nil {
			return fmt.Errorf("%w: %w", ErrInvalid, err)
		}
	}

	if err := w.Conflict(sn.Server, sn.Root); err != nil {
		return fmt.Errorf("%w: %w", ErrInvalid, err)
	}

	return nil
}

// SetAccess replaces the access setting of an active site, applies the
// web state and restores the old setting if that fails.
func (s *Service) SetAccess(ctx context.Context, actor int64, name string, a store.AccessSettings) (SiteAccessView, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	site, err := s.activeSite(ctx, name)
	if err != nil {
		return SiteAccessView{}, err
	}

	// A redirect answers in nginx's rewrite phase, before the access phase
	// would check anything.
	if site.Type == store.SiteTypeRedirect && (a.IPMode != "" || a.Password) {
		return SiteAccessView{}, ErrSiteType
	}

	users, err := s.st.AuthUsers(ctx, site.ID)
	if err != nil {
		return SiteAccessView{}, err
	}

	sn, err := s.st.SiteSnippets(ctx, site.ID)
	if err != nil {
		return SiteAccessView{}, err
	}

	if err := s.checkAccess(a, users, sn); err != nil {
		return SiteAccessView{}, err
	}

	old, err := s.st.SetSiteAccess(ctx, store.SiteAccess{SiteID: site.ID, Settings: a, UpdatedBy: actor}, s.now())
	if err != nil {
		return SiteAccessView{}, err
	}

	if err := s.applyWeb(ctx); err != nil {
		if _, rerr := s.st.SetSiteAccess(context.WithoutCancel(ctx), old, old.UpdatedAt); rerr != nil {
			s.log.ErrorContext(ctx, "restore access setting after failed change", "site", name, "err", rerr)
		}

		return SiteAccessView{}, err
	}

	s.auditChange(ctx, actor, "site.access.set", name, nil, store.FieldsOf(old.Settings), store.FieldsOf(a))

	return s.accessView(ctx, site.ID)
}

// accessPassword returns the password to hash: the given one after its
// check, or a generated one (generated true).
func accessPassword(password string) (pw string, generated bool, err error) {
	if password == "" {
		return agentapi.GenerateAccessPassword(GeneratedAccessPasswordLen), true, nil
	}

	if err := agentapi.ValidateAccessPassword(password); err != nil {
		return "", false, fmt.Errorf("%w: %w", ErrInvalid, err)
	}

	return password, false, nil
}

func hashAccessPassword(pw string) (string, error) {
	h, err := bcrypt.GenerateFromPassword([]byte(pw), agentapi.AccessBcryptCost)
	if err != nil {
		return "", fmt.Errorf("hosting: %w", err)
	}

	return string(h), nil
}

// AddAccessUser adds a basic auth user with password, or a generated one
// when password is empty, and returns the generated password ("" for an
// own one).
func (s *Service) AddAccessUser(ctx context.Context, actor int64, name, user, password string) (string, error) {
	if err := agentapi.ValidateAccessUserName(user); err != nil {
		return "", fmt.Errorf("%w: %w: %w", ErrInvalid, agentapi.ErrAccessRejected, err)
	}

	pw, generated, err := accessPassword(password)
	if err != nil {
		return "", err
	}

	hash, err := hashAccessPassword(pw)
	if err != nil {
		return "", err
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	site, err := s.activeSite(ctx, name)
	if err != nil {
		return "", err
	}

	users, err := s.st.AuthUsers(ctx, site.ID)
	if err != nil {
		return "", err
	}

	if len(users) >= agentapi.MaxAccessUsers {
		return "", fmt.Errorf("%w: %w: at most %d users", ErrInvalid, agentapi.ErrAccessRejected, agentapi.MaxAccessUsers)
	}

	err = s.st.AddAuthUser(ctx, store.AuthUser{SiteID: site.ID, Name: user, Hash: hash, UpdatedBy: actor}, s.now())
	if errors.Is(err, store.ErrAuthUserExists) {
		return "", ErrAuthUserExists
	} else if err != nil {
		return "", err
	}

	if err := s.applyIfPassword(ctx, site.ID); err != nil {
		if _, rerr := s.st.DeleteAuthUser(context.WithoutCancel(ctx), site.ID, user); rerr != nil {
			s.log.ErrorContext(ctx, "remove basic auth user after failed change", "site", name, "err", rerr)
		}

		return "", err
	}

	s.audit(ctx, actor, "site.access.user.add", name, map[string]string{"user": user, "generated": strconv.FormatBool(generated)})

	if generated {
		return pw, nil
	}

	return "", nil
}

// SetAccessUserPassword sets a new password for a user, generated when
// password is empty, and returns the generated one.
func (s *Service) SetAccessUserPassword(ctx context.Context, actor int64, name, user, password string) (string, error) {
	pw, generated, err := accessPassword(password)
	if err != nil {
		return "", err
	}

	hash, err := hashAccessPassword(pw)
	if err != nil {
		return "", err
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	site, err := s.activeSite(ctx, name)
	if err != nil {
		return "", err
	}

	old, err := s.st.SetAuthUserHash(ctx, site.ID, user, hash, actor, s.now())
	if errors.Is(err, store.ErrNotFound) {
		return "", ErrAuthUserNotFound
	} else if err != nil {
		return "", err
	}

	if err := s.applyIfPassword(ctx, site.ID); err != nil {
		if rerr := s.st.RestoreAuthUser(context.WithoutCancel(ctx), old); rerr != nil {
			s.log.ErrorContext(ctx, "restore basic auth user after failed change", "site", name, "err", rerr)
		}

		return "", err
	}

	s.audit(ctx, actor, "site.access.user.password", name, map[string]string{"user": user, "generated": strconv.FormatBool(generated)})

	if generated {
		return pw, nil
	}

	return "", nil
}

// DeleteAccessUser removes a user. The last user cannot go while password
// protection is on.
func (s *Service) DeleteAccessUser(ctx context.Context, actor int64, name, user string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	site, err := s.activeSite(ctx, name)
	if err != nil {
		return err
	}

	a, err := s.st.SiteAccess(ctx, site.ID)
	if err != nil {
		return err
	}

	users, err := s.st.AuthUsers(ctx, site.ID)
	if err != nil {
		return err
	}

	if a.Settings.Password && len(users) == 1 && users[0].Name == user {
		return fmt.Errorf("%w: %w: the last user cannot be removed while password protection is on", ErrInvalid, agentapi.ErrAccessRejected)
	}

	old, err := s.st.DeleteAuthUser(ctx, site.ID, user)
	if errors.Is(err, store.ErrNotFound) {
		return ErrAuthUserNotFound
	} else if err != nil {
		return err
	}

	if err := s.applyIfPassword(ctx, site.ID); err != nil {
		if rerr := s.st.RestoreAuthUser(context.WithoutCancel(ctx), old); rerr != nil {
			s.log.ErrorContext(ctx, "restore basic auth user after failed change", "site", name, "err", rerr)
		}

		return err
	}

	s.audit(ctx, actor, "site.access.user.delete", name, map[string]string{"user": user})

	return nil
}

// applyIfPassword applies the web state when the site asks for a password,
// so a user change reaches the password file; otherwise nginx does not
// read the users.
func (s *Service) applyIfPassword(ctx context.Context, siteID int64) error {
	a, err := s.st.SiteAccess(ctx, siteID)
	if err != nil {
		return err
	}

	if !a.Settings.Password {
		return nil
	}

	return s.applyWeb(ctx)
}

// webAccess is the setting sent with web.apply; nil for none and for a
// redirect site, which keeps its setting after a type change but answers
// before nginx checks access.
func webAccess(all map[int64]store.SiteAccess, users map[int64][]store.AuthUser, site store.Site) *agentapi.WebAccess {
	a, ok := all[site.ID]
	if !ok || site.Type == store.SiteTypeRedirect {
		return nil
	}

	w := webAccessOf(a.Settings, users[site.ID])
	if w.IsZero() {
		return nil
	}

	return &w
}
