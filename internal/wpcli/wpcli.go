// SPDX-License-Identifier: AGPL-3.0-or-later

// Package wpcli runs WP-CLI for the WordPress quick install (ADR-0069).
//
// It is the seventh package that may start processes (os/exec), with this
// narrow scope: Run only works inside hostashi-wp@<site>.service, as the
// site user (it refuses uid 0), and starts nothing but the site's own PHP
// CLI binary (/usr/bin/php<version>, version checked against
// agentapi.PHPVersions) with the pinned, root-owned WP-CLI phar
// (/opt/hostashi/wp-cli/<version>/wp-cli.phar) and a fixed list of
// sub-commands. Every value from the job is one argument after a checked
// "--name=" prefix, never a shell word; there is no shell. Passwords come
// from systemd's credentials and reach WP-CLI on standard input
// (--prompt), never in argv, the environment or the log.
package wpcli

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/maki-it/hostashi/internal/agentapi"
)

// Job is what hostashi-wp@<site> runs, from the job credential.
type Job struct {
	agentapi.WPInstall
	// PHP is the site's PHP version.
	PHP string `json:"php"`
	// Path is the site's document root, absolute.
	Path string `json:"path"`
	// Home is the site's home, for WP-CLI's cache.
	Home string `json:"home"`
	// Domain is the site's primary domain, which URL must name.
	Domain string `json:"domain"`
	// Clear empties Path before the install (a reinstall).
	Clear bool `json:"clear,omitempty"`
}

// Fixed places.
const (
	// PHPBin is the prefix of the distribution's PHP CLI binaries.
	PHPBin = "/usr/bin/php"
	// PharDir is wpinstall.Dir; the phar must be below it.
	PharDir = "/opt/hostashi/wp-cli"
	// DoneMarker starts the last line of a successful run.
	DoneMarker = "wordpress installed:"
	// Credential names of the unit (LoadCredential=).
	CredJob       = "job"
	CredDBPass    = "dbpass"
	CredAdminPass = "adminpass"
)

// DownloadURL is the zip of the latest WordPress per offered locale
// (agentapi.WPLocales). WP-CLI unpacks a tar.gz with PharData, which cuts
// paths longer than 100 characters (WordPress 7 has them below
// wp-includes/php-ai-client), so verify-checksums fails; a zip goes
// through ZipArchive (php-zip) intact. WP-CLI checks the zip against the
// .md5 next to it where wordpress.org has one (not for de_DE), and
// verify-checksums checks every file against wordpress.org afterwards.
var DownloadURL = map[string]string{
	"en_US": "https://wordpress.org/latest.zip",
	"de_DE": "https://de.wordpress.org/latest-de_DE.zip",
}

// ErrRoot: the runner never runs as root.
var ErrRoot = errors.New("wpcli: must not run as root")

// Validate checks the job.
func (j Job) Validate() error {
	if err := agentapi.ValidatePHPVersion(j.PHP); err != nil {
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

	return j.WPInstall.Validate([]string{j.Domain})
}

// Step is one WP-CLI call: its arguments after the phar and, for a
// password, which credential goes to standard input.
type Step struct {
	Args  []string
	Stdin string // "", CredDBPass or CredAdminPass
}

// Steps returns the WP-CLI calls of a job, in order.
func Steps(j Job) []Step {
	path := "--path=" + j.Path
	steps := []Step{
		{Args: []string{"core", "download", DownloadURL[j.Locale], path}},
		{Args: []string{"core", "verify-checksums", path, "--locale=" + j.Locale}},
		{Args: []string{
			"config", "create", path, "--dbname=" + j.DBName, "--dbuser=" + j.DBUser, "--dbhost=" + j.DBHost,
			"--locale=" + j.Locale, "--prompt=dbpass",
		}, Stdin: CredDBPass},
		{Args: []string{"config", "set", "DISALLOW_FILE_EDIT", "true", "--raw", "--type=constant", path}},
		{Args: []string{"config", "set", "WP_ENVIRONMENT_TYPE", "production", "--type=constant", path}},
		{Args: []string{
			"core", "install", path, "--url=" + j.URL, "--title=" + j.Title, "--admin_user=" + j.AdminUser,
			"--admin_email=" + j.AdminEmail, "--skip-email", "--prompt=admin_password",
		}, Stdin: CredAdminPass},
		{Args: []string{"rewrite", "structure", "/%postname%/", path}},
	}

	if j.Discourage {
		steps = append(steps, Step{Args: []string{"option", "update", "blog_public", "0", path}})
	}

	return steps
}

// Runner starts the steps. Euid is the effective user id; Phar the pinned
// phar; Secrets holds the credentials by name.
type Runner struct {
	Euid    int
	Phar    string
	Secrets map[string]string
	Out     io.Writer
	// start runs one command; tests replace it.
	start func(ctx context.Context, bin string, args []string, env []string, stdin io.Reader, out io.Writer) error
}

func startCmd(ctx context.Context, bin string, args []string, env []string, stdin io.Reader, out io.Writer) error {
	cmd := exec.CommandContext(ctx, bin, args...) // #nosec G204 -- fixed binary and checked arguments, no shell
	cmd.Env = env
	cmd.Stdin = stdin
	cmd.Stdout = out
	cmd.Stderr = out

	return cmd.Run()
}

// Run runs every step of j. It stops at the first failing one.
func (r *Runner) Run(ctx context.Context, j Job) error {
	if r.Euid == 0 {
		return ErrRoot
	}

	if err := j.Validate(); err != nil {
		return err
	}

	if filepath.Clean(r.Phar) != r.Phar || !strings.HasPrefix(r.Phar, PharDir+"/") || filepath.Base(r.Phar) != "wp-cli.phar" {
		return fmt.Errorf("wpcli: the phar %q is not below %s", r.Phar, PharDir)
	}

	for _, name := range []string{CredDBPass, CredAdminPass} {
		if err := agentapi.ValidateWPPassword(r.Secrets[name]); err != nil {
			return fmt.Errorf("wpcli: %s: %w", name, err)
		}
	}

	start := r.start
	if start == nil {
		start = startCmd
	}

	out := r.Out
	if out == nil {
		out = io.Discard
	}

	bin := PHPBin + j.PHP
	env := []string{
		"PATH=/usr/bin:/bin", "HOME=" + j.Home, "LANG=C.UTF-8",
		"WP_CLI_CACHE_DIR=" + filepath.Join(j.Home, ".wp-cli", "cache"),
		// No packages, no config of the site user: only the pinned phar.
		"WP_CLI_PACKAGES_DIR=/nonexistent", "WP_CLI_CONFIG_PATH=/dev/null", "WP_CLI_DISABLE_AUTO_CHECK_UPDATE=1",
	}

	if j.Clear {
		fmt.Fprintf(out, "$ empty %s\n", j.Path)

		if err := clearDir(j.Path); err != nil {
			return err
		}
	}

	for _, s := range Steps(j) {
		fmt.Fprintf(out, "$ wp %s\n", strings.Join(redact(s.Args), " "))

		var stdin io.Reader = strings.NewReader("")
		if s.Stdin != "" {
			stdin = strings.NewReader(r.Secrets[s.Stdin] + "\n")
		}

		// WP-CLI may repeat what it read on standard input, for example
		// after a prompt; the log never gets a password.
		w := &maskWriter{out: out, secrets: []string{r.Secrets[CredDBPass], r.Secrets[CredAdminPass]}}

		args := append([]string{"-d", "memory_limit=512M", r.Phar}, s.Args...)
		err := start(ctx, bin, args, env, stdin, w)
		w.Flush()

		if err != nil {
			return fmt.Errorf("wpcli: wp %s %s: %w", s.Args[0], s.Args[1], err)
		}
	}

	fmt.Fprintf(out, "%s %s\n", DoneMarker, j.URL)

	return nil
}

// clearDir removes everything inside dir, as the user that runs it, and
// keeps dir itself. Links are removed, never followed. A missing dir is
// fine: WP-CLI creates it.
func clearDir(dir string) error {
	root, err := os.OpenRoot(dir)
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}

	if err != nil {
		return fmt.Errorf("wpcli: empty the document root: %w", err)
	}

	defer root.Close()

	f, err := root.Open(".")
	if err != nil {
		return fmt.Errorf("wpcli: empty the document root: %w", err)
	}

	names, err := f.Readdirnames(-1)
	f.Close()

	if err != nil {
		return fmt.Errorf("wpcli: empty the document root: %w", err)
	}

	for _, n := range names {
		if err := root.RemoveAll(n); err != nil {
			return fmt.Errorf("wpcli: empty the document root: %w", err)
		}
	}

	return nil
}

// maskWriter passes output on line by line with every secret replaced,
// so a secret split over two writes is still found.
type maskWriter struct {
	out     io.Writer
	secrets []string
	buf     []byte
}

// maxLine bounds a line kept back while waiting for its end.
const maxLine = 64 << 10

func (m *maskWriter) Write(p []byte) (int, error) {
	m.buf = append(m.buf, p...)

	for {
		i := bytes.IndexByte(m.buf, '\n')
		if i < 0 {
			break
		}

		m.emit(m.buf[:i+1])
		m.buf = m.buf[i+1:]
	}

	if len(m.buf) > maxLine {
		m.Flush()
	}

	return len(p), nil
}

// Flush writes what is left of an unfinished line.
func (m *maskWriter) Flush() {
	if len(m.buf) > 0 {
		m.emit(m.buf)
		m.buf = nil
	}
}

func (m *maskWriter) emit(line []byte) {
	s := string(line)

	for _, sec := range m.secrets {
		if sec != "" {
			s = strings.ReplaceAll(s, sec, "…")
		}
	}

	_, _ = io.WriteString(m.out, s)
}

// redact keeps the log free of the admin email.
func redact(args []string) []string {
	out := make([]string, len(args))
	for i, a := range args {
		if strings.HasPrefix(a, "--admin_email=") {
			a = "--admin_email=…"
		}

		out[i] = a
	}

	return out
}

// Main is `hostashi wp-run PHAR`: it reads the job and the
// passwords from systemd's credentials ($CREDENTIALS_DIRECTORY).
func Main(ctx context.Context, args []string, euid int, out io.Writer) error {
	if euid == 0 {
		return ErrRoot
	}

	if len(args) != 1 {
		return errors.New("wpcli: usage: hostashi wp-run PHAR")
	}

	dir := os.Getenv("CREDENTIALS_DIRECTORY")
	if dir == "" {
		return errors.New("wpcli: no credentials: run inside hostashi-wp@<site>.service")
	}

	read := func(name string) ([]byte, error) {
		b, err := os.ReadFile(filepath.Join(dir, name)) //nolint:gosec // systemd sets the directory, the names are fixed
		if err != nil {
			return nil, fmt.Errorf("wpcli: credential %s: %w", name, err)
		}

		if len(b) > 64<<10 {
			return nil, fmt.Errorf("wpcli: credential %s is too large", name)
		}

		return b, nil
	}

	raw, err := read(CredJob)
	if err != nil {
		return err
	}

	var j Job

	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()

	if err := dec.Decode(&j); err != nil {
		return fmt.Errorf("wpcli: job: %w", err)
	}

	secrets := map[string]string{}

	for _, name := range []string{CredDBPass, CredAdminPass} {
		b, err := read(name)
		if err != nil {
			return err
		}

		secrets[name] = strings.TrimSuffix(string(b), "\n")
	}

	return (&Runner{Euid: euid, Phar: args[0], Secrets: secrets, Out: out}).Run(ctx, j)
}
