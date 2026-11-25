// SPDX-License-Identifier: AGPL-3.0-or-later

package agentapi

import (
	"errors"
	"fmt"
	"net/mail"
	"regexp"
	"slices"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/maki-it/hostashi/internal/agentrpc"
)

// WordPress quick install (ADR-0069): the values of one installation. The
// panel creates the database first; the agent writes the values into the
// job of hostashi-wp@<site> and hands the passwords to the unit as
// credentials, which the runner passes to WP-CLI on standard input.
type WPInstall struct {
	// Locale of the download and the site: one of WPLocales.
	Locale string `json:"locale"`
	// URL is the site's address, http:// or https:// with the primary
	// domain and no path.
	URL        string `json:"url"`
	Title      string `json:"title"`
	AdminUser  string `json:"adminUser"`
	AdminEmail string `json:"adminEmail"`
	// Discourage asks search engines not to index the site.
	Discourage bool `json:"discourage,omitempty"`
	// DBName, DBUser and DBHost are the site's database (ADR-0024).
	DBName string `json:"dbName"`
	DBUser string `json:"dbUser"`
	DBHost string `json:"dbHost"`
}

// WPLocales are the languages offered for the quick install.
var WPLocales = []string{"en_US", "de_DE"}

// Bounds of the quick install's values.
const (
	MaxWPTitle    = 100
	MinWPPassword = 12
	MaxWPPassword = 128
)

var (
	wpUserRe = regexp.MustCompile(`^[A-Za-z0-9._@-]{1,60}$`)
	wpDBRe   = regexp.MustCompile(`^[A-Za-z0-9_]{1,64}$`)
	wpHostRe = regexp.MustCompile(`^(localhost|127\.0\.0\.1|\[::1\])(:[0-9]{1,5})?$`)
)

// Validate checks every value; each one becomes a single argument of
// WP-CLI, never a shell word.
func (w WPInstall) Validate(domains []string) error {
	if !slices.Contains(WPLocales, w.Locale) {
		return fmt.Errorf("wordpress: language %q is not offered", w.Locale)
	}

	host, ok := strings.CutPrefix(w.URL, "https://")
	if !ok {
		host, ok = strings.CutPrefix(w.URL, "http://")
	}

	if !ok || len(domains) == 0 || host != domains[0] {
		return errors.New("wordpress: the address must be http:// or https:// with the site's primary domain")
	}

	if err := checkText(w.Title, MaxWPTitle); err != nil {
		return fmt.Errorf("wordpress: site title: %w", err)
	}

	if !wpUserRe.MatchString(w.AdminUser) || strings.HasPrefix(w.AdminUser, "-") {
		return errors.New("wordpress: admin user name: use 1 to 60 letters, digits, '.', '_', '@' or '-', not starting with '-'")
	}

	if strings.EqualFold(w.AdminUser, "admin") {
		return errors.New("wordpress: admin user name: choose a name other than admin, which bots try first")
	}

	a, err := mail.ParseAddress(w.AdminEmail)
	if err != nil || a.Address != w.AdminEmail || a.Name != "" || strings.HasPrefix(w.AdminEmail, "-") {
		return errors.New("wordpress: admin email: not a plain email address")
	}

	if !wpDBRe.MatchString(w.DBName) || !wpDBRe.MatchString(w.DBUser) {
		return errors.New("wordpress: invalid database or database user name")
	}

	if !wpHostRe.MatchString(w.DBHost) {
		return errors.New("wordpress: the database must be on this server")
	}

	return nil
}

// checkText accepts printable text up to max characters, not starting
// with '-'.
func checkText(s string, maxLen int) error {
	if strings.TrimSpace(s) == "" {
		return errors.New("must not be empty")
	}

	if !utf8.ValidString(s) || utf8.RuneCountInString(s) > maxLen {
		return fmt.Errorf("at most %d characters", maxLen)
	}

	if strings.HasPrefix(s, "-") {
		return errors.New("must not start with '-'")
	}

	for _, r := range s {
		if unicode.IsControl(r) || unicode.Is(unicode.Cf, r) {
			return errors.New("must not hold control or format characters")
		}
	}

	return nil
}

// ValidateWPPassword checks a password that reaches WP-CLI on one line of
// standard input.
func ValidateWPPassword(p string) error {
	if n := utf8.RuneCountInString(p); n < MinWPPassword || n > MaxWPPassword || !utf8.ValidString(p) {
		return fmt.Errorf("wordpress: the password must have %d to %d characters", MinWPPassword, MaxWPPassword)
	}

	for _, r := range p {
		if unicode.IsControl(r) {
			return errors.New("wordpress: the password must not hold control characters")
		}
	}

	return nil
}

// WordPress quick install in the agent (ADR-0069): site.wordpress.install
// starts hostashi-wp@<site>, site.wordpress.status reports its run.
var (
	WPInstallStart  = agentrpc.Op[WPInstallRequest, WPInstallStatus]{Name: "site.wordpress.install"}
	WPInstallStatOp = agentrpc.Op[WPStatusRequest, WPInstallStatus]{Name: "site.wordpress.status"}
)

// WPInstallRequest starts the quick install of a site.
type WPInstallRequest struct {
	Site string `json:"site"`
	// PHP is the site's PHP version; its CLI runs WP-CLI.
	PHP string `json:"php"`
	// WebDir is the site's document root below htdocs (ValidateWebDir).
	// It must be empty or missing.
	WebDir string `json:"webDir,omitempty"`
	// Domain is the site's primary domain.
	Domain  string    `json:"domain"`
	Install WPInstall `json:"install"`
	// DBPassword and AdminPassword reach the unit as credentials.
	DBPassword    string `json:"dbPassword"`
	AdminPassword string `json:"adminPassword"`
}

// Validate checks every field.
func (r WPInstallRequest) Validate() error {
	if err := ValidateSiteUser(r.Site); err != nil {
		return err
	}

	if err := ValidatePHPVersion(r.PHP); err != nil {
		return err
	}

	if err := ValidateWebDir(r.WebDir); err != nil {
		return err
	}

	if err := ValidateDomain(r.Domain); err != nil {
		return err
	}

	if err := r.Install.Validate([]string{r.Domain}); err != nil {
		return err
	}

	for _, p := range []string{r.DBPassword, r.AdminPassword} {
		if err := ValidateWPPassword(p); err != nil {
			return err
		}
	}

	return nil
}

// WPStatusRequest asks for the last quick install of a site.
type WPStatusRequest struct {
	Site string `json:"site"`
}

// Validate checks the site name.
func (r WPStatusRequest) Validate() error { return ValidateSiteUser(r.Site) }

// States of a quick install: the deploy states (DeployRunning, ...) plus
// WPNone for a site that never ran one.
const WPNone = "none"

// MaxWPLog bounds the log tail of a status.
const MaxWPLog = 16 << 10

// WPInstallStatus is the state of a site's quick install.
type WPInstallStatus struct {
	Site  string `json:"site"`
	State string `json:"state"`
	Error string `json:"error,omitempty"`
	Log   string `json:"log,omitempty"`
}
