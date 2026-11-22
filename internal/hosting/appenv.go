// SPDX-License-Identifier: AGPL-3.0-or-later

package hosting

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strconv"
	"strings"

	"github.com/maki-it/hostashi/internal/agentapi"
	"github.com/maki-it/hostashi/internal/secrets"
	"github.com/maki-it/hostashi/internal/store"
)

// ErrNoAppEnv: the panel has no secret key for environment variables.
var ErrNoAppEnv = errors.New("hosting: app environment variables are not available")

// EnableAppEnv gives the service the panel's secret key, which seals the
// values of the apps' environment variables in the database (ADR-0054).
func (s *Service) EnableAppEnv(box *secrets.Box) { s.envBox = box }

// envContext binds a sealed value to its site and name.
func envContext(siteID int64, name string) string {
	return "app-env:site:" + strconv.FormatInt(siteID, 10) + ":" + name
}

// appEnv returns the variables of a site's app with their values, sorted
// by name.
func (s *Service) appEnv(ctx context.Context, site store.Site) ([]agentapi.AppEnvVar, error) {
	sealed, err := s.st.AppEnv(ctx, site.ID)
	if err != nil {
		return nil, err
	}

	if len(sealed) == 0 {
		return nil, nil
	}

	if s.envBox == nil {
		return nil, ErrNoAppEnv
	}

	out := make([]agentapi.AppEnvVar, 0, len(sealed))

	for _, v := range sealed {
		plain, err := s.envBox.Open(v.Value, envContext(site.ID, v.Name))
		if err != nil {
			return nil, fmt.Errorf("hosting: environment variable %s of %s: %w", v.Name, site.Name, err)
		}

		out = append(out, agentapi.AppEnvVar{Name: v.Name, Value: string(plain)})
	}

	return out, nil
}

// envSite returns an active site that runs an app.
func (s *Service) envSite(ctx context.Context, name string) (store.Site, error) {
	site, err := s.Get(ctx, name)
	if err != nil {
		return store.Site{}, err
	}

	if !hasApp(site) {
		return store.Site{}, ErrSiteType
	}

	return site, nil
}

// AppEnvNames lists the names of the app's variables, without values.
func (s *Service) AppEnvNames(ctx context.Context, name string) ([]string, error) {
	site, err := s.envSite(ctx, name)
	if err != nil {
		return nil, err
	}

	sealed, err := s.st.AppEnv(ctx, site.ID)
	if err != nil {
		return nil, err
	}

	out := make([]string, 0, len(sealed))
	for _, v := range sealed {
		out = append(out, v.Name)
	}

	return out, nil
}

// AppEnv returns the app's variables with their values.
func (s *Service) AppEnv(ctx context.Context, name string) ([]agentapi.AppEnvVar, error) {
	site, err := s.envSite(ctx, name)
	if err != nil {
		return nil, err
	}

	env, err := s.appEnv(ctx, site)
	if env == nil && err == nil {
		env = []agentapi.AppEnvVar{}
	}

	return env, err
}

// SetAppEnv replaces the variables of the app of an active Node.js or app
// site and applies the app with them; a running app restarts. If the agent
// refuses, the previous variables are stored again. The audit log names
// the variables, never their values.
func (s *Service) SetAppEnv(ctx context.Context, actor int64, name string, env []agentapi.AppEnvVar) (agentapi.AppStatus, error) {
	if err := agentapi.ValidateAppEnv(env); err != nil {
		return agentapi.AppStatus{}, fmt.Errorf("%w: %w", ErrInvalid, err)
	}

	if s.envBox == nil {
		return agentapi.AppStatus{}, ErrNoAppEnv
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	site, err := s.envSite(ctx, name)
	if err != nil {
		return agentapi.AppStatus{}, err
	}

	if site.State != store.SiteActive {
		return agentapi.AppStatus{}, ErrState
	}

	old, err := s.st.AppEnv(ctx, site.ID)
	if err != nil {
		return agentapi.AppStatus{}, err
	}

	before, err := s.appEnv(ctx, site)
	if err != nil {
		return agentapi.AppStatus{}, err
	}

	env = slices.Clone(env)
	slices.SortFunc(env, func(a, b agentapi.AppEnvVar) int { return strings.Compare(a.Name, b.Name) })

	sealed := make([]store.SealedEnvVar, 0, len(env))

	for _, v := range env {
		b, err := s.envBox.Seal([]byte(v.Value), envContext(site.ID, v.Name))
		if err != nil {
			return agentapi.AppStatus{}, err
		}

		sealed = append(sealed, store.SealedEnvVar{Name: v.Name, Value: b})
	}

	if err := s.st.SetAppEnv(ctx, site.ID, sealed); err != nil {
		return agentapi.AppStatus{}, err
	}

	st, err := s.applyApp(ctx, site)
	if err != nil {
		undo := context.WithoutCancel(ctx)
		if rerr := s.st.SetAppEnv(undo, site.ID, old); rerr != nil {
			s.log.ErrorContext(ctx, "restore app environment after failed change", "site", site.Name, "err", rerr)
		} else if _, rerr := s.applyApp(undo, site); rerr != nil {
			s.log.ErrorContext(ctx, "apply previous app environment after failed change", "site", site.Name, "err", rerr)
		}

		return agentapi.AppStatus{}, err
	}

	b, a := envAudit(before, env)
	s.auditChange(ctx, actor, "site.app.env", site.Name, nil, b, a)

	return st, nil
}

// envAudit describes a change of variables by name only: a changed value
// shows as "changed", an unchanged one as "set".
func envAudit(before, after []agentapi.AppEnvVar) (map[string]string, map[string]string) {
	b, a := map[string]string{}, map[string]string{}
	old := map[string]string{}

	for _, v := range before {
		b[v.Name], old[v.Name] = "set", v.Value
	}

	for _, v := range after {
		a[v.Name] = "set"
		if prev, ok := old[v.Name]; ok && prev != v.Value {
			a[v.Name] = "changed"
		}
	}

	return b, a
}
