// SPDX-License-Identifier: AGPL-3.0-or-later

package wpcli

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"path/filepath"
	"strings"

	"github.com/maki-it/hostashi/internal/agentapi"
)

// CloneDoneMarker starts the last line of a successful clone
// configuration.
const CloneDoneMarker = "wordpress configured:"

// CloneJob configures a cloned WordPress site (ADR-0070): wp-config.php
// gets the clone's database, the source's URLs become the clone's, and
// the cache is flushed.
type CloneJob struct {
	PHP  string `json:"php"`
	Path string `json:"path"`
	Home string `json:"home"`
	// URL is the clone's address.
	URL string `json:"url"`
	// From are the source's domains.
	From      []string             `json:"from"`
	Databases []agentapi.WPCloneDB `json:"databases"`
	Staging   bool                 `json:"staging,omitempty"`
}

// Validate checks the job without the passwords.
func (j CloneJob) Validate() error {
	r := agentapi.WPCloneRequest{Site: "clone", PHP: j.PHP, URL: j.URL, From: j.From, Databases: j.Databases, DBPasswords: map[string]string{}}
	for _, d := range j.Databases {
		r.DBPasswords[d.User] = strings.Repeat("x", agentapi.MinWPPassword)
	}

	if err := r.Validate(); err != nil {
		return fmt.Errorf("wpcli: %w", err)
	}

	for _, p := range []string{j.Path, j.Home} {
		if !filepath.IsAbs(p) || filepath.Clean(p) != p || strings.ContainsAny(p, "\n\x00") {
			return fmt.Errorf("wpcli: invalid path %q", p)
		}
	}

	if !strings.HasPrefix(j.Path+"/", j.Home+"/") {
		return errors.New("wpcli: the document root is outside the home")
	}

	return nil
}

// Replacement is one search-replace of a clone.
type Replacement struct{ Old, New string }

// Replacements returns the rewrites from the source's addresses to the
// clone's: every source domain with and without www, written with "//"
// (http, https and scheme-relative) and JSON-escaped as "\/\/" (block
// attributes), then the other scheme of the clone's own domain to its
// scheme. Mail addresses keep the source's domain: they have no "//".
func Replacements(scheme, host string, from []string) []Replacement {
	var out []Replacement

	seen := map[string]bool{host: true}
	add := func(d string) {
		if seen[d] {
			return
		}

		seen[d] = true
		out = append(out, Replacement{"//" + d, "//" + host}, Replacement{`\/\/` + d, `\/\/` + host})
	}

	for _, d := range from {
		add(d)

		if !strings.HasPrefix(d, "www.") {
			add("www." + d)
		}
	}

	other := "http"
	if scheme == "http" {
		other = "https"
	}

	return append(out,
		Replacement{other + "://" + host, scheme + "://" + host},
		Replacement{other + `:\/\/` + host, scheme + `:\/\/` + host})
}

// setDBPassword sets DB_PASSWORD in wp-config.php to the first line of
// standard input, with the editor WP-CLI's config commands use. "wp config
// set DB_PASSWORD --prompt=value" cannot do it: WP-CLI refuses a prompt
// for a positional argument named in --prompt (usage error), and the
// password must not be in argv. --skip-wordpress loads no WordPress, so
// the old database password does not matter.
const setDBPassword = `$f = \WP_CLI\Utils\locate_wp_config();
if (!$f) { WP_CLI::error("no wp-config.php"); }
$v = fgets(STDIN);
if ($v === false || ($v = rtrim($v, "\n")) === "") { WP_CLI::error("no password on standard input"); }
(new WPConfigTransformer($f))->update("constant", "DB_PASSWORD", $v);
WP_CLI::success("Updated DB_PASSWORD.");`

// CloneSteps returns the WP-CLI calls after DB_NAME named db, the pair it
// belongs to, in order. pw is the password of db's user.
func CloneSteps(j CloneJob, db agentapi.WPCloneDB, pw string) []Step {
	path := "--path=" + j.Path
	steps := []Step{
		{Args: []string{"config", "set", "DB_NAME", db.Target, "--type=constant", path}},
		{Args: []string{"config", "set", "DB_USER", db.User, "--type=constant", path}},
		{Args: []string{"eval", setDBPassword, "--skip-wordpress", path}, Password: pw},
	}

	if j.Staging {
		steps = append(steps, Step{Args: []string{"config", "set", "WP_ENVIRONMENT_TYPE", "staging", "--type=constant", path}})
	}

	scheme, host, _ := agentapi.ParseWPCloneURL(j.URL)

	// Plugins and themes stay off: their code runs on load, could send
	// mail or call out, and is not needed to rewrite the tables.
	for _, r := range Replacements(scheme, host, j.From) {
		steps = append(steps, Step{Args: []string{
			"search-replace", r.Old, r.New, path, "--all-tables-with-prefix", "--skip-columns=guid",
			"--skip-plugins", "--skip-themes", "--report-changed-only",
		}})
	}

	return append(steps, Step{Args: []string{"cache", "flush", path, "--skip-plugins", "--skip-themes"}})
}

// RunClone configures a cloned site. pw holds the passwords of the
// clone's database users. It reads DB_NAME from wp-config.php and refuses
// a database that is not one of the copied ones.
func (r *Runner) RunClone(ctx context.Context, j CloneJob, pw map[string]string) error {
	if r.Euid == 0 {
		return ErrRoot
	}

	if err := j.Validate(); err != nil {
		return err
	}

	if err := agentapi.ValidateWPCloneDBs(j.Databases, pw); err != nil {
		return fmt.Errorf("wpcli: %w", err)
	}

	if filepath.Clean(r.Phar) != r.Phar || !strings.HasPrefix(r.Phar, PharDir+"/") || filepath.Base(r.Phar) != "wp-cli.phar" {
		return fmt.Errorf("wpcli: the phar %q is not below %s", r.Phar, PharDir)
	}

	out := r.Out
	if out == nil {
		out = io.Discard
	}

	secrets := make([]string, 0, len(pw))
	for _, p := range pw {
		secrets = append(secrets, p)
	}

	run := r.stepper(j.PHP, j.Home, out, secrets...)

	got, err := run(ctx, Step{Args: []string{"config", "get", "DB_NAME", "--path=" + j.Path}, Capture: true})
	if err != nil {
		return err
	}

	name := strings.TrimSpace(got)

	var db *agentapi.WPCloneDB

	for i := range j.Databases {
		if j.Databases[i].Source == name {
			db = &j.Databases[i]
		}
	}

	if db == nil {
		return fmt.Errorf("wpcli: wp-config.php uses the database %q, which is not one of the copied ones", name)
	}

	for _, s := range CloneSteps(j, *db, pw[db.User]) {
		if _, err := run(ctx, s); err != nil {
			return err
		}
	}

	fmt.Fprintf(out, "%s %s\n", CloneDoneMarker, j.URL)

	return nil
}

// cloneSecrets reads the database passwords of a clone run: a JSON object
// from user to password in the dbpass credential.
func cloneSecrets(raw []byte) (map[string]string, error) {
	var pw map[string]string
	if err := json.Unmarshal(raw, &pw); err != nil {
		return nil, fmt.Errorf("wpcli: credential %s: %w", CredDBPass, err)
	}

	return pw, nil
}
