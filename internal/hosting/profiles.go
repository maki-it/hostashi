// SPDX-License-Identifier: AGPL-3.0-or-later

package hosting

import (
	"context"
	"fmt"
	"time"

	"github.com/maki-it/hostashi/internal/agentapi"
	"github.com/maki-it/hostashi/internal/profile"
	"github.com/maki-it/hostashi/internal/store"
	"github.com/maki-it/hostashi/internal/web"
)

// siteProfile returns the profile a site follows: its stored one, or the
// default of its type (ADR-0069). A stored name that no longer exists
// falls back to the default as well, so a site never loses its rules.
func siteProfile(all map[int64]store.SiteProfile, site store.Site) (profile.Profile, profile.Overrides, bool) {
	def, ok := profile.Default(site.Type)
	if !ok {
		return profile.Profile{}, profile.Overrides{}, false
	}

	if sp, ok := all[site.ID]; ok {
		if p, ok := profile.Builtin(sp.Profile); ok && p.Type == site.Type {
			return p, sp.Overrides, true
		}
	}

	p, _ := profile.Builtin(def)

	return p, profile.Overrides{}, true
}

// webHardening is the hardening the agent gets for a static or PHP site
// (ADR-0017 Part 4); php is the PHP version it sends, empty while the
// site is disabled.
func webHardening(all map[int64]store.SiteProfile, site store.Site, php string) *agentapi.WebHardening {
	p, o, ok := siteProfile(all, site)
	if !ok {
		return nil
	}

	h := profile.Effective(p, o, php != "")
	if h.IsZero() {
		return nil
	}

	return &h
}

// SiteProfileState is a site's profile with its effective hardening and
// the profile's own rules, for the panel.
type SiteProfileState struct {
	Profile   profile.Profile
	Hardening agentapi.WebHardening
	// Changed: the site's hardening differs from its profile.
	Changed   bool
	UpdatedAt time.Time
}

// Profiles returns the profiles a site of that type can choose, Static for
// a static site.
func Profiles(siteType string) []profile.Profile {
	out := []profile.Profile{}

	for _, p := range profile.Builtins() {
		if p.Type == siteType {
			out = append(out, p)
		}
	}

	return out
}

// SiteProfile returns the profile of a static or PHP site.
func (s *Service) SiteProfile(ctx context.Context, name string) (SiteProfileState, error) {
	site, err := s.Get(ctx, name)
	if err != nil {
		return SiteProfileState{}, err
	}

	sp, err := s.st.SiteProfile(ctx, site.ID)
	if err != nil {
		return SiteProfileState{}, err
	}

	p, o, ok := siteProfile(map[int64]store.SiteProfile{site.ID: sp}, site)
	if !ok {
		return SiteProfileState{}, ErrSiteType
	}

	return SiteProfileState{
		Profile: p, Hardening: profile.Effective(p, o, site.Type == store.SiteTypePHP), Changed: !o.IsZero(), UpdatedAt: sp.UpdatedAt,
	}, nil
}

// SetSiteProfile sets the profile of a static or PHP site and the
// hardening it wants; only the values that differ from the profile are
// stored (ADR-0069). nil hardening takes the profile's rules. Changing
// the profile re-renders the site's server block and never touches its
// files or its document root folder.
func (s *Service) SetSiteProfile(ctx context.Context, actor int64, name, profileName string, want *agentapi.WebHardening) (SiteProfileState, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	site, err := s.Get(ctx, name)
	if err != nil {
		return SiteProfileState{}, err
	}

	if site.State != store.SiteActive {
		return SiteProfileState{}, ErrState
	}

	if _, ok := profile.Default(site.Type); !ok {
		return SiteProfileState{}, ErrSiteType
	}

	p, ok := profile.Builtin(profileName)
	if !ok || p.Type != site.Type {
		return SiteProfileState{}, fmt.Errorf("%w: unknown profile %q for a %s site", ErrInvalid, profileName, site.Type)
	}

	php := site.Type == store.SiteTypePHP

	var o profile.Overrides

	if want != nil {
		if err := want.Validate(php); err != nil {
			return SiteProfileState{}, fmt.Errorf("%w: %w", ErrInvalid, err)
		}

		o = profile.Diff(p.Hardening, *want)
	}

	// The default profile without changes needs no row.
	stored := store.SiteProfile{SiteID: site.ID, Profile: p.Name, Overrides: o, UpdatedBy: actor}
	if def, _ := profile.Default(site.Type); def == p.Name && o.IsZero() {
		stored = store.SiteProfile{SiteID: site.ID}
	}

	old, err := s.st.SetSiteProfile(ctx, stored, s.now())
	if err != nil {
		return SiteProfileState{}, err
	}

	if err := s.applyWeb(ctx); err != nil {
		if _, rerr := s.st.SetSiteProfile(context.WithoutCancel(ctx), old, old.UpdatedAt); rerr != nil {
			s.log.ErrorContext(ctx, "restore profile after failed change", "site", name, "err", rerr)
		}

		return SiteProfileState{}, err
	}

	oldP, oldO, _ := siteProfile(map[int64]store.SiteProfile{site.ID: old}, site)
	view := func(name string, h agentapi.WebHardening) map[string]string {
		m := store.FieldsOf(h)
		if m == nil {
			m = map[string]string{}
		}

		m["profile"] = name

		return m
	}

	s.auditChange(ctx, actor, "site.profile.set", name, nil,
		view(oldP.Name, profile.Effective(oldP, oldO, php)), view(p.Name, profile.Effective(p, o, php)))

	return SiteProfileState{Profile: p, Hardening: profile.Effective(p, o, php), Changed: !o.IsZero(), UpdatedAt: s.now()}, nil
}

// createType is the type Create gives a site of this request, for the
// profile check: static or PHP, empty for the other types.
func createType(req CreateRequest) string {
	switch {
	case req.Redirect != nil || req.Proxy != nil || req.App != nil:
		return ""
	case req.PHP != "":
		return store.SiteTypePHP
	}

	return store.SiteTypeStatic
}

// checkCreateProfile checks the profile of a new site: it must exist, fit
// the site's type and its PHP version.
func checkCreateProfile(req CreateRequest) error {
	if req.Profile == "" {
		return nil
	}

	p, ok := profile.Builtin(req.Profile)

	typ := createType(req)
	if !ok || p.Type != typ {
		return fmt.Errorf("%w: unknown profile %q for this type of site", ErrInvalid, req.Profile)
	}

	if p.MinPHP != "" && !profile.PHPAtLeast(req.PHP, p.MinPHP) {
		return fmt.Errorf("%w: %s needs PHP %s or newer", ErrInvalid, p.Title, p.MinPHP)
	}

	return nil
}

// storeCreateProfile stores the profile of a new site and its document
// root folder, before the site's first web state.
func (s *Service) storeCreateProfile(ctx context.Context, req CreateRequest, siteID int64) error {
	p, ok := profile.Builtin(req.Profile)
	if !ok {
		return nil
	}

	if def, _ := profile.Default(p.Type); def != p.Name {
		if _, err := s.st.SetSiteProfile(ctx, store.SiteProfile{SiteID: siteID, Profile: p.Name}, s.now()); err != nil {
			return err
		}
	}

	if p.WebDir != "" {
		if _, err := s.st.SetSiteWebOptions(ctx, req.Name, store.WebOptions{Dir: p.WebDir}, s.now()); err != nil {
			return err
		}
	}

	return nil
}

// SiteProfilePreview renders the server block a static or PHP site gets
// with a profile and hardening (ADR-0069), with the agent's templates and
// placeholder paths (web.Preview). It stores nothing and does not ask the
// agent. An empty profileName with nil hardening shows what the site has;
// otherwise the arguments mean what they mean for SetSiteProfile.
func (s *Service) SiteProfilePreview(ctx context.Context, name, profileName string, want *agentapi.WebHardening) (string, error) {
	site, err := s.Get(ctx, name)
	if err != nil {
		return "", err
	}

	if site.State != store.SiteActive {
		return "", ErrState
	}

	in, err := s.loadWebInputs(ctx)
	if err != nil {
		return "", err
	}

	p, o, ok := siteProfile(in.profiles, site)
	if !ok {
		return "", ErrSiteType
	}

	if profileName != "" || want != nil {
		if profileName != "" {
			if p, ok = profile.Builtin(profileName); !ok || p.Type != site.Type {
				return "", fmt.Errorf("%w: unknown profile %q for a %s site", ErrInvalid, profileName, site.Type)
			}
		}

		o = profile.Overrides{}

		if want != nil {
			if err := want.Validate(site.Type == store.SiteTypePHP); err != nil {
				return "", fmt.Errorf("%w: %w", ErrInvalid, err)
			}

			o = profile.Diff(p.Hardening, *want)
		}
	}

	if len(site.Domains) == 0 {
		return "", fmt.Errorf("%w: the site has no domain", ErrInvalid)
	}

	// As the site is served: the preview shows its rules also while it is
	// disabled or in maintenance.
	ws := in.webSite(site, site.PHP, site.TLS && s.certCovers(site, 0))
	in.addFiles(&ws, site, site.PHP)

	ws.Hardening = nil
	if h := profile.Effective(p, o, site.PHP != ""); !h.IsZero() {
		ws.Hardening = &h
	}

	out, err := web.Preview(ws)
	if err != nil {
		return "", fmt.Errorf("%w: %w", ErrInvalid, err)
	}

	return out, nil
}
