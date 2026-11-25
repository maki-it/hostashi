// SPDX-License-Identifier: AGPL-3.0-or-later

package hosting

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"

	"github.com/maki-it/hostashi/internal/profile"
	"github.com/maki-it/hostashi/internal/store"
)

// Errors of the own profiles (ADR-0069).
var (
	ErrProfileNotFound = errors.New("hosting: unknown profile")
	ErrProfileExists   = errors.New("hosting: a profile with this name exists")
	// ErrProfileBuiltin: built-in profiles cannot be changed or deleted,
	// only copied.
	ErrProfileBuiltin = errors.New("hosting: built-in profiles cannot be changed")
	// ErrProfileInUse: sites use the profile; the error names them.
	ErrProfileInUse = errors.New("hosting: sites use this profile")
)

// ListProfiles returns the built-in profiles in their order, then the own
// ones by name.
func (s *Service) ListProfiles(ctx context.Context) ([]profile.Profile, error) {
	ps, err := s.profileSet(ctx)
	if err != nil {
		return nil, err
	}

	return ps.sorted(), nil
}

// Profile returns a built-in or own profile by name.
func (s *Service) Profile(ctx context.Context, name string) (profile.Profile, error) {
	ps, err := s.profileSet(ctx)
	if err != nil {
		return profile.Profile{}, err
	}

	p, ok := ps.lookup(name)
	if !ok {
		return profile.Profile{}, ErrProfileNotFound
	}

	return p, nil
}

// checkOwn checks an own profile and returns it as it is stored.
func checkOwn(p profile.Profile) (profile.Profile, error) {
	p = profile.Normalize(p)
	if err := profile.Check(p); err != nil {
		return profile.Profile{}, fmt.Errorf("%w: %s", ErrInvalid, strings.TrimPrefix(err.Error(), profile.ErrInvalid.Error()+": "))
	}

	return p, nil
}

func profileAudit(p profile.Profile) map[string]string {
	m := store.FieldsOf(p)
	delete(m, "builtin")

	return m
}

// CreateProfile adds an own profile.
func (s *Service) CreateProfile(ctx context.Context, actor int64, p profile.Profile) (profile.Profile, error) {
	return s.createProfile(ctx, actor, p, "profile.create")
}

// ImportProfile adds an own profile from its YAML form.
func (s *Service) ImportProfile(ctx context.Context, actor int64, yaml []byte) (profile.Profile, error) {
	p, err := profile.ImportYAML(yaml)
	if err != nil {
		return profile.Profile{}, fmt.Errorf("%w: %s", ErrInvalid, strings.TrimPrefix(err.Error(), profile.ErrInvalid.Error()+": "))
	}

	return s.createProfile(ctx, actor, p, "profile.import")
}

func (s *Service) createProfile(ctx context.Context, actor int64, p profile.Profile, action string) (profile.Profile, error) {
	p, err := checkOwn(p)
	if err != nil {
		return profile.Profile{}, err
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	out, err := s.st.CreateOwnProfile(ctx, p, actor, s.now())
	if errors.Is(err, store.ErrProfileExists) {
		return profile.Profile{}, ErrProfileExists
	}

	if err != nil {
		return profile.Profile{}, err
	}

	s.auditChange(ctx, actor, action, p.Name, nil, nil, profileAudit(p))

	return profile.Normalize(out.Profile), nil
}

// UpdateProfile replaces the values of an own profile. The sites that use
// it get its new hardening rules right away; they store only their own
// changes (ADR-0069). The document root folder and the other values only
// apply to sites created afterwards. A profile cannot be renamed, and its
// type cannot change while sites use it.
func (s *Service) UpdateProfile(ctx context.Context, actor int64, name string, p profile.Profile) (profile.Profile, error) {
	if _, ok := profile.Builtin(name); ok {
		return profile.Profile{}, ErrProfileBuiltin
	}

	if p.Name == "" {
		p.Name = name
	}

	if p.Name != name {
		return profile.Profile{}, fmt.Errorf("%w: a profile cannot be renamed; copy it to the new name and delete the old one", ErrInvalid)
	}

	p, err := checkOwn(p)
	if err != nil {
		return profile.Profile{}, err
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	cur, err := s.st.OwnProfile(ctx, name)
	if errors.Is(err, store.ErrNotFound) {
		return profile.Profile{}, ErrProfileNotFound
	}

	if err != nil {
		return profile.Profile{}, err
	}

	sites, err := s.st.ProfileSites(ctx, name)
	if err != nil {
		return profile.Profile{}, err
	}

	if len(sites) > 0 && cur.Profile.Type != p.Type {
		return profile.Profile{}, fmt.Errorf("%w: the type cannot change while sites use the profile: %s", ErrProfileInUse, strings.Join(sites, ", "))
	}

	old, err := s.st.UpdateOwnProfile(ctx, p, actor, s.now())
	if err != nil {
		return profile.Profile{}, err
	}

	if len(sites) > 0 {
		if err := s.applyWeb(ctx); err != nil {
			if rerr := s.st.RestoreOwnProfile(context.WithoutCancel(ctx), old); rerr != nil {
				s.log.ErrorContext(ctx, "restore profile after failed change", "profile", name, "err", rerr)
			}

			return profile.Profile{}, err
		}
	}

	s.auditChange(ctx, actor, "profile.update", name, map[string]string{"sites": strconv.Itoa(len(sites))},
		profileAudit(profile.Normalize(old.Profile)), profileAudit(p))

	return p, nil
}

// DeleteProfile removes an own profile that no site uses.
func (s *Service) DeleteProfile(ctx context.Context, actor int64, name string) error {
	if _, ok := profile.Builtin(name); ok {
		return ErrProfileBuiltin
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	old, err := s.st.DeleteOwnProfile(ctx, name)

	switch {
	case errors.Is(err, store.ErrNotFound):
		return ErrProfileNotFound
	case errors.Is(err, store.ErrProfileInUse):
		sites, serr := s.st.ProfileSites(ctx, name)
		if serr != nil {
			return serr
		}

		return fmt.Errorf("%w: %s", ErrProfileInUse, strings.Join(sites, ", "))
	case err != nil:
		return err
	}

	s.auditChange(ctx, actor, "profile.delete", name, nil, profileAudit(profile.Normalize(old.Profile)), nil)

	return nil
}
