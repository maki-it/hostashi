// SPDX-License-Identifier: AGPL-3.0-or-later

package agentapi

import (
	"fmt"
	"net/url"

	"github.com/maki-it/hostashi/internal/agentrpc"
)

// The configuration step of a cloned WordPress site (ADR-0070): after its
// files and databases are copied, hostashi-wp@<clone> points wp-config.php
// at the clone's database, rewrites the source's URLs to the clone's and
// flushes the cache. Its run is reported by site.wordpress.status like a
// quick install.
var WPCloneStart = agentrpc.Op[WPCloneRequest, WPInstallStatus]{Name: "site.wordpress.clone"}

// Bounds of a clone configuration.
const (
	MaxWPCloneDomains   = 64
	MaxWPCloneDatabases = 16
)

// WPCloneDB is one copied database: the source's, the clone's and the
// clone's user of it.
type WPCloneDB struct {
	Source string `json:"source"`
	Target string `json:"target"`
	User   string `json:"user"`
}

// WPCloneRequest starts the configuration of a cloned WordPress site.
type WPCloneRequest struct {
	Site string `json:"site"`
	// PHP is the clone's PHP version; its CLI runs WP-CLI.
	PHP string `json:"php"`
	// WebDir is the clone's document root below htdocs.
	WebDir string `json:"webDir,omitempty"`
	// URL is the clone's address, http or https and its primary domain.
	URL string `json:"url"`
	// From are the source's domains; every one is rewritten to URL's host,
	// with and without www.
	From []string `json:"from"`
	// Databases are the copied databases; wp-config.php's DB_NAME picks
	// one of them.
	Databases []WPCloneDB `json:"databases"`
	// Staging sets WP_ENVIRONMENT_TYPE to staging.
	Staging bool `json:"staging,omitempty"`
	// DBPasswords are the new passwords of the clone's database users, by
	// user; they reach the unit as a credential.
	DBPasswords map[string]string `json:"dbPasswords"`
}

// ParseWPCloneURL returns the scheme and the host of a clone's URL:
// http or https, a domain, nothing else.
func ParseWPCloneURL(raw string) (scheme, host string, err error) {
	u, err := url.Parse(raw)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.User != nil || u.Port() != "" ||
		(u.Path != "" && u.Path != "/") || u.RawQuery != "" || u.Fragment != "" || u.Opaque != "" {
		return "", "", fmt.Errorf("wordpress: the address %q must be http(s)://domain", raw)
	}

	if err := ValidateDomain(u.Host); err != nil {
		return "", "", err
	}

	return u.Scheme, u.Host, nil
}

// Validate checks every field.
func (r WPCloneRequest) Validate() error {
	if err := ValidateSiteUser(r.Site); err != nil {
		return err
	}

	if err := ValidatePHPVersion(r.PHP); err != nil {
		return err
	}

	if err := ValidateWebDir(r.WebDir); err != nil {
		return err
	}

	_, host, err := ParseWPCloneURL(r.URL)
	if err != nil {
		return err
	}

	if len(r.From) == 0 || len(r.From) > MaxWPCloneDomains {
		return fmt.Errorf("wordpress: 1 to %d source domains", MaxWPCloneDomains)
	}

	for _, d := range r.From {
		if err := ValidateDomain(d); err != nil {
			return err
		}

		if d == host {
			return fmt.Errorf("wordpress: the source domain %s is the clone's", d)
		}
	}

	return ValidateWPCloneDBs(r.Databases, r.DBPasswords)
}

// ValidateWPCloneDBs checks the database pairs of a clone job and the
// passwords of their users.
func ValidateWPCloneDBs(dbs []WPCloneDB, pw map[string]string) error {
	if len(dbs) == 0 || len(dbs) > MaxWPCloneDatabases {
		return fmt.Errorf("wordpress: 1 to %d databases", MaxWPCloneDatabases)
	}

	seen, users := map[string]bool{}, map[string]bool{}

	for _, d := range dbs {
		if err := ValidateDBName(d.Source); err != nil {
			return err
		}

		if err := ValidateDBName(d.Target); err != nil {
			return err
		}

		if err := ValidateDBUser(d.User); err != nil {
			return err
		}

		if seen[d.Source] {
			return fmt.Errorf("wordpress: database %s is listed twice", d.Source)
		}

		seen[d.Source], users[d.User] = true, true

		if err := ValidateWPPassword(pw[d.User]); err != nil {
			return fmt.Errorf("wordpress: password of %s: %w", d.User, err)
		}
	}

	for u := range pw {
		if !users[u] {
			return fmt.Errorf("wordpress: a password for %s, the user of no database", u)
		}
	}

	return nil
}

// The .env step of a cloned Laravel or Symfony site (ADR-0070): typed key
// edits in the clone's .env, run as the site user in the same unit as the
// WordPress step and reported by site.wordpress.status.
var DotEnvCloneStart = agentrpc.Op[DotEnvCloneRequest, WPInstallStatus]{Name: "site.dotenv.clone"}

// Frameworks of the .env step.
const (
	DotEnvLaravel = "laravel"
	DotEnvSymfony = "symfony"
)

// DotEnvCloneRequest starts the .env step of a clone.
type DotEnvCloneRequest struct {
	Site string `json:"site"`
	// Kind is DotEnvLaravel or DotEnvSymfony.
	Kind string `json:"kind"`
	// WebDir is the clone's document root below htdocs; the project is
	// its parent when it is called public, else the document root.
	WebDir string `json:"webDir,omitempty"`
	// URL is the clone's address (Laravel's APP_URL).
	URL         string            `json:"url"`
	Databases   []WPCloneDB       `json:"databases"`
	DBPasswords map[string]string `json:"dbPasswords"`
}

// Validate checks every field.
func (r DotEnvCloneRequest) Validate() error {
	if err := ValidateSiteUser(r.Site); err != nil {
		return err
	}

	if r.Kind != DotEnvLaravel && r.Kind != DotEnvSymfony {
		return fmt.Errorf("dotenv: unknown kind %q", r.Kind)
	}

	if err := ValidateWebDir(r.WebDir); err != nil {
		return err
	}

	if _, _, err := ParseWPCloneURL(r.URL); err != nil {
		return err
	}

	return ValidateWPCloneDBs(r.Databases, r.DBPasswords)
}
