// SPDX-License-Identifier: AGPL-3.0-or-later

package hosting

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"

	"github.com/maki-it/hostashi/internal/agentapi"
	"github.com/maki-it/hostashi/internal/profile"
	"github.com/maki-it/hostashi/internal/store"
)

// WordPress quick install (ADR-0069): the panel creates a database and
// user on the local MariaDB, then asks the agent to start
// hostashi-wp@<site>, which installs WordPress as the site user.

// ErrNoWordPress: this panel runs without an agent for WordPress installs.
var ErrNoWordPress = errors.New("hosting: the WordPress install is not available")

// WordPressAgent is the part of the agent the WordPress install uses.
type WordPressAgent interface {
	WPInstallStart(ctx context.Context, req agentapi.WPInstallRequest) (agentapi.WPInstallStatus, error)
	WPInstallStatus(ctx context.Context, req agentapi.WPStatusRequest) (agentapi.WPInstallStatus, error)
}

// WPInstallStart calls site.wordpress.install.
func (a RPCAgent) WPInstallStart(ctx context.Context, req agentapi.WPInstallRequest) (agentapi.WPInstallStatus, error) {
	return agentapi.WPInstallStart.Call(ctx, a.C, req)
}

// WPInstallStatus calls site.wordpress.status.
func (a RPCAgent) WPInstallStatus(ctx context.Context, req agentapi.WPStatusRequest) (agentapi.WPInstallStatus, error) {
	return agentapi.WPInstallStatOp.Call(ctx, a.C, req)
}

// EnableWordPress sets the agent for WordPress installs. Without it an
// install answers ErrNoWordPress.
func (s *Service) EnableWordPress(a WordPressAgent) { s.wp = a }

// WordPressInput is what the person chooses for a quick install. An empty
// AdminPassword generates one.
type WordPressInput struct {
	Locale        string
	Title         string
	AdminUser     string
	AdminEmail    string
	AdminPassword string
	Discourage    bool
}

// WordPressStart is a started install: the database it created and the
// admin password, set only when it was generated, to be shown once.
type WordPressStart struct {
	Status        agentapi.WPInstallStatus
	Database      string
	AdminPassword string
}

// InstallWordPress starts the quick install on an active PHP site whose
// document root is empty (the agent checks that). The database it creates
// is dropped again when the agent refuses to start.
func (s *Service) InstallWordPress(ctx context.Context, actor int64, name string, in WordPressInput) (WordPressStart, error) {
	if s.wp == nil {
		return WordPressStart{}, ErrNoWordPress
	}

	adminPW, generated := in.AdminPassword, false
	if adminPW == "" {
		adminPW, generated = agentapi.GenerateDBPassword(), true
	}

	if err := agentapi.ValidateWPPassword(adminPW); err != nil {
		return WordPressStart{}, fmt.Errorf("%w: %w", ErrInvalid, err)
	}

	site, err := s.Get(ctx, name)
	if err != nil {
		return WordPressStart{}, err
	}

	if site.Type != store.SiteTypePHP || site.PHP == "" {
		return WordPressStart{}, ErrSiteType
	}

	if site.State != store.SiteActive {
		return WordPressStart{}, ErrState
	}

	scheme := "http://"
	if site.TLS {
		scheme = "https://"
	}

	install := agentapi.WPInstall{
		Locale: in.Locale, URL: scheme + site.Domains[0], Title: in.Title,
		AdminUser: in.AdminUser, AdminEmail: in.AdminEmail, Discourage: in.Discourage,
		DBHost: "localhost",
	}

	// Check the person's values before anything is created; the database
	// names are filled in below.
	probe := install
	probe.DBName, probe.DBUser = "probe", "probe"

	if err := probe.Validate(site.Domains); err != nil {
		return WordPressStart{}, fmt.Errorf("%w: %w", ErrInvalid, err)
	}

	db, dbPW, err := s.createWPDatabase(ctx, actor, site.Name)
	if err != nil {
		return WordPressStart{}, err
	}

	install.DBName, install.DBUser = db, db

	st, err := s.wp.WPInstallStart(ctx, agentapi.WPInstallRequest{
		Site: site.Name, PHP: site.PHP, WebDir: site.WebDir, Domain: site.Domains[0],
		Install: install, DBPassword: dbPW, AdminPassword: adminPW,
	})
	if err != nil {
		if derr := s.DeleteDatabase(context.WithoutCancel(ctx), actor, db); derr != nil {
			s.log.ErrorContext(ctx, "drop the database of a WordPress install that did not start", "site", name, "database", db, "err", derr)
		}

		return WordPressStart{}, err
	}

	s.audit(ctx, actor, "site.wordpress.install", site.Name, map[string]string{
		"database": db, "locale": in.Locale, "adminUser": in.AdminUser, "url": install.URL,
	})

	s.wordPressProfile(ctx, actor, site)

	out := WordPressStart{Status: st, Database: db}
	if generated {
		out.AdminPassword = adminPW
	}

	return out, nil
}

// createWPDatabase creates <site>_wp, or the first free <site>_wp2 to
// <site>_wp9, with a user of the same name and a generated password.
func (s *Service) createWPDatabase(ctx context.Context, actor int64, site string) (string, string, error) {
	base := strings.ReplaceAll(site, "-", "_")
	if len(base) > 28 { // the user name allows 32 characters
		base = base[:28]
	}

	for i := 1; i <= 9; i++ {
		name := base + "_wp"
		if i > 1 {
			name += strconv.Itoa(i)
		}

		_, pw, err := s.CreateDatabase(ctx, actor, NewDatabase{Site: site, Name: name, Server: store.LocalDBServer, User: name})
		if errors.Is(err, ErrDatabaseExists) {
			continue
		}

		if err != nil {
			return "", "", err
		}

		return name, pw, nil
	}

	return "", "", fmt.Errorf("%w: the site has too many WordPress databases already", ErrDatabaseExists)
}

// wordPressProfile gives a site that follows the generic PHP profile the
// WordPress profile, so its hardening fits what was installed. A site with
// another profile keeps it. A failure is logged: the install runs anyway.
func (s *Service) wordPressProfile(ctx context.Context, actor int64, site store.Site) {
	sp, err := s.st.SiteProfile(ctx, site.ID)
	if err != nil {
		s.log.ErrorContext(ctx, "read the profile of a WordPress site", "site", site.Name, "err", err)

		return
	}

	if sp.Profile != "" && sp.Profile != profile.GenericPHP {
		return
	}

	if _, err := s.SetSiteProfile(ctx, actor, site.Name, profile.WordPress, nil); err != nil {
		s.log.ErrorContext(ctx, "set the WordPress profile", "site", site.Name, "err", err)
	}
}

// WordPressStatus returns the state of the site's install run.
func (s *Service) WordPressStatus(ctx context.Context, name string) (agentapi.WPInstallStatus, error) {
	if s.wp == nil {
		return agentapi.WPInstallStatus{}, ErrNoWordPress
	}

	site, err := s.Get(ctx, name)
	if err != nil {
		return agentapi.WPInstallStatus{}, err
	}

	if site.Type != store.SiteTypePHP {
		return agentapi.WPInstallStatus{}, ErrSiteType
	}

	return s.wp.WPInstallStatus(ctx, agentapi.WPStatusRequest{Site: site.Name})
}
