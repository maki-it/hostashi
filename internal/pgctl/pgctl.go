// SPDX-License-Identifier: AGPL-3.0-or-later

// Package pgctl runs the PostgreSQL client tools pg_dump, pg_restore and
// psql against external PostgreSQL servers (ADR-0051), for backups, import
// and export.
//
// It is the sixth package allowed to start processes (depguard rule in
// .golangci.yml), with the same narrow exception as internal/mariadbctl:
//
//   - Three binaries by absolute path, from the PostgreSQL major the caller
//     names (/usr/lib/postgresql/<major>/bin, installed from
//     apt.postgresql.org by hostashi-pg-client-install@<major>.service).
//     Not /usr/bin: there pg_wrapper picks a version by its own rules.
//   - Fixed argument lists. The only variables are a database name and role
//     names that passed strict validators; they go in as separate
//     arguments, never through a shell or a connection string.
//   - The connection is set through libpq's environment variables (host,
//     port, database, user, sslmode, CA file); the environment holds
//     nothing else besides PATH, LANG and a HOME that does not exist, so
//     no ~/.pgpass, ~/.psqlrc or service file is read.
//   - The password is only in a passfile (PGPASSFILE), mode 0600, owned by
//     the user the tool runs as, in a directory the caller names (the job's
//     private temp directory), and removed after the run. It is never in
//     argv or the environment.
//   - The process never runs as root.
//
// psql runs SQL files for imports. It cannot switch off its own
// meta-commands (\!, \o |cmd, \copy ... program); the guard is that it runs
// as the site's Linux user inside the job's sandbox, which that user could
// reach over SSH anyway, and with a temporary role that has only the site
// role's rights. It runs with -X (no psqlrc) and ON_ERROR_STOP.
package pgctl

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"syscall"

	"github.com/maki-it/hostashi/internal/agentapi"
)

// LibDir holds one directory per installed major.
const LibDir = "/usr/lib/postgresql"

// SystemCAFile is the distribution's CA bundle, for TLS "verify".
const SystemCAFile = "/etc/ssl/certs/ca-certificates.crt"

// The tools pgctl runs, relative to LibDir/<major>/bin.
const (
	toolDump    = "pg_dump"
	toolRestore = "pg_restore"
	toolPSQL    = "psql"
)

// ErrFailed carries a short, fixed description. The tool's output stays
// out of it: it may contain parts of the data.
var ErrFailed = errors.New("pgctl: failed")

// Run describes one run: which client, as which Linux user, against which
// server, with which role.
type Run struct {
	// Major is the client's PostgreSQL major, for example "19".
	Major    string
	Database string
	UID, GID uint32
	User     string // the role that logs in
	Password string

	Host string // as the admin entered it
	Port int
	// TLS is "verify" (system CAs, host name checked), "verify-ca"
	// (CAFile, chain checked) or "none".
	TLS    string
	CAFile string // for "verify-ca"

	// TempDir is where the passfile is written, for example the job's
	// private /tmp. It must exist; the file is removed after the run.
	TempDir string

	// Root is prefixed to LibDir (tests); empty means /.
	Root string
}

var (
	roleRe  = regexp.MustCompile(`^[a-z][a-z0-9_]{0,62}$`)
	majorRe = regexp.MustCompile(`^[1-9][0-9]{0,2}$`)
	hostRe  = regexp.MustCompile(`^[A-Za-z0-9]([A-Za-z0-9.:_-]{0,252}[A-Za-z0-9])?$`)
)

// Dump writes the database in pg_dump's custom format to out.
func Dump(ctx context.Context, r Run, out io.Writer) error {
	return run(ctx, r, toolDump, []string{"--format=custom", "--no-password"}, nil, out)
}

// Restore loads a custom-format dump from in. Objects are created as
// role (the site's role), existing ones are replaced, ownership in the
// dump is ignored, and the first error stops the restore.
func Restore(ctx context.Context, r Run, role string, in io.Reader) error {
	if !roleRe.MatchString(role) {
		return fmt.Errorf("%w: invalid role", ErrFailed)
	}

	return run(ctx, r, toolRestore, []string{
		"--no-password", "--clean", "--if-exists", "--no-owner", "--exit-on-error", "--single-transaction",
		"--role=" + role, "--dbname=" + r.Database,
	}, in, io.Discard)
}

// Exec runs the plain SQL file from in with psql, in one transaction, as
// role; the first error stops it.
func Exec(ctx context.Context, r Run, role string, in io.Reader) error {
	if !roleRe.MatchString(role) {
		return fmt.Errorf("%w: invalid role", ErrFailed)
	}

	// SET ROLE first, through a variable: psql quotes it as an identifier.
	pre := strings.NewReader("SET ROLE :\"hostashi_role\";\n")

	return run(ctx, r, toolPSQL, []string{
		"-X", "--no-password", "--quiet", "--single-transaction",
		"--set=ON_ERROR_STOP=1", "--set=hostashi_role=" + role, "--file=-",
	}, io.MultiReader(pre, in), io.Discard)
}

// Binary is the path of a tool of a major, after checking that it is a
// regular executable file there.
func Binary(root, major, tool string) (string, error) {
	if !majorRe.MatchString(major) {
		return "", fmt.Errorf("%w: invalid major", ErrFailed)
	}

	switch tool {
	case toolDump, toolRestore, toolPSQL:
	default:
		return "", fmt.Errorf("%w: unknown tool", ErrFailed)
	}

	p := filepath.Join(LibDir, major, "bin", tool)
	if root != "" {
		p = filepath.Join(root, p)
	}

	st, err := os.Lstat(p)
	if err != nil || !st.Mode().IsRegular() || st.Mode().Perm()&0o111 == 0 {
		return "", fmt.Errorf("%w: the PostgreSQL %s client is not installed", ErrFailed, major)
	}

	return p, nil
}

func run(ctx context.Context, r Run, tool string, args []string, in io.Reader, out io.Writer) error {
	if err := agentapi.ValidateDBName(r.Database); err != nil {
		return fmt.Errorf("%w: %w", ErrFailed, err)
	}

	if r.UID == 0 || r.GID == 0 {
		return fmt.Errorf("%w: refusing to run as root", ErrFailed)
	}

	env, err := environment(r)
	if err != nil {
		return err
	}

	bin, err := Binary(r.Root, r.Major, tool)
	if err != nil {
		return err
	}

	pass, err := writePassfile(r)
	if err != nil {
		return err
	}

	defer os.Remove(pass)

	var stderr bytes.Buffer

	env = append(env, "PGPASSFILE="+pass)

	// pg_dump and psql take the database from PGDATABASE; pg_restore
	// connects only with --dbname, which its arguments carry.
	cmd := exec.CommandContext(ctx, bin, args...)
	cmd.Env = env
	cmd.Dir = "/"
	cmd.Stdin = in
	cmd.Stdout = out
	cmd.Stderr = &limited{w: &stderr, n: 4096}
	cmd.SysProcAttr = &syscall.SysProcAttr{
		Credential: &syscall.Credential{Uid: r.UID, Gid: r.GID, Groups: []uint32{}},
		Pdeathsig:  syscall.SIGKILL,
	}

	if err := cmd.Run(); err != nil {
		return describe(err, stderr.Bytes())
	}

	return nil
}

// environment is the process environment without the password.
func environment(r Run) ([]string, error) {
	if !roleRe.MatchString(r.User) {
		return nil, fmt.Errorf("%w: invalid user", ErrFailed)
	}

	if !hostRe.MatchString(r.Host) || r.Port < 1 || r.Port > 65535 {
		return nil, fmt.Errorf("%w: host and port are required", ErrFailed)
	}

	env := []string{
		"PATH=/usr/bin:/bin", "HOME=/nonexistent", "LANG=C.UTF-8",
		// No service or system configuration files.
		"PGSERVICEFILE=/nonexistent", "PGSYSCONFDIR=/nonexistent",
		"PGHOST=" + r.Host, "PGPORT=" + strconv.Itoa(r.Port), "PGDATABASE=" + r.Database, "PGUSER=" + r.User,
		"PGCONNECT_TIMEOUT=20", "PGAPPNAME=hostashi",
	}

	switch r.TLS {
	case "verify":
		return append(env, "PGSSLMODE=verify-full", "PGSSLROOTCERT="+SystemCAFile), nil
	case "verify-ca":
		if r.CAFile == "" || !filepath.IsAbs(r.CAFile) || strings.ContainsAny(r.CAFile, "\x00\n") {
			return nil, fmt.Errorf("%w: CA file is required", ErrFailed)
		}

		return append(env, "PGSSLMODE=verify-ca", "PGSSLROOTCERT="+r.CAFile), nil
	case "none":
		return append(env, "PGSSLMODE=disable"), nil
	default:
		return nil, fmt.Errorf("%w: unknown TLS mode", ErrFailed)
	}
}

// passfileLine is the one line of the passfile: any host, port and
// database, this user. ':' and '\' are escaped as libpq expects.
func passfileLine(user, password string) (string, error) {
	if password == "" {
		return "", fmt.Errorf("%w: password is required", ErrFailed)
	}

	for i := range len(password) {
		if c := password[i]; c < 0x20 || c == 0x7f {
			return "", fmt.Errorf("%w: invalid character in password", ErrFailed)
		}
	}

	esc := strings.NewReplacer(`\`, `\\`, `:`, `\:`)

	return "*:*:*:" + esc.Replace(user) + ":" + esc.Replace(password) + "\n", nil
}

// writePassfile writes the passfile in TempDir, 0600 and owned by the
// run's user (libpq ignores a file that others can read).
func writePassfile(r Run) (string, error) {
	line, err := passfileLine(r.User, r.Password)
	if err != nil {
		return "", err
	}

	if r.TempDir == "" || !filepath.IsAbs(r.TempDir) {
		return "", fmt.Errorf("%w: a temporary directory is required", ErrFailed)
	}

	f, err := os.CreateTemp(r.TempDir, "pgpass-*")
	if err != nil {
		return "", fmt.Errorf("pgctl: passfile: %w", err)
	}

	name := f.Name()
	fail := func(err error) (string, error) {
		_ = f.Close()
		_ = os.Remove(name)

		return "", fmt.Errorf("pgctl: passfile: %w", err)
	}

	if err := f.Chmod(0o600); err != nil {
		return fail(err)
	}

	if os.Geteuid() == 0 {
		if err := f.Chown(int(r.UID), int(r.GID)); err != nil {
			return fail(err)
		}
	}

	if _, err := f.WriteString(line); err != nil {
		return fail(err)
	}

	if err := f.Close(); err != nil {
		_ = os.Remove(name)

		return "", fmt.Errorf("pgctl: passfile: %w", err)
	}

	return name, nil
}

// Patterns in the tools' messages that describe picks the fixed text by;
// psql also names the line of the file.
var (
	psqlLineRe = regexp.MustCompile(`psql:[^\n]*?:(\d{1,12}): ERROR:`)
	authRe     = regexp.MustCompile(`password authentication failed|no password supplied|pg_hba\.conf`)
	tlsRe      = regexp.MustCompile(`SSL|certificate`)
	connRe     = regexp.MustCompile(`connection to server .* failed|could not translate host name|timeout expired`)
	versionRe  = regexp.MustCompile(`server version mismatch|aborting because of server version mismatch`)
)

// describe returns a fixed text: the kind of error and for psql the line
// of the file, nothing of the data.
func describe(err error, stderr []byte) error {
	switch {
	case versionRe.Match(stderr):
		return fmt.Errorf("%w: the server is newer than the client", ErrFailed)
	case authRe.Match(stderr):
		return fmt.Errorf("%w: the server refused the login", ErrFailed)
	case connRe.Match(stderr) && tlsRe.Match(stderr):
		return fmt.Errorf("%w: TLS connection failed", ErrFailed)
	case connRe.Match(stderr):
		return fmt.Errorf("%w: cannot connect to the server", ErrFailed)
	}

	if m := psqlLineRe.FindSubmatch(stderr); m != nil {
		return fmt.Errorf("%w: SQL error at line %s of the file", ErrFailed, m[1])
	}

	if errors.Is(err, context.DeadlineExceeded) || errors.Is(err, context.Canceled) {
		return fmt.Errorf("%w: stopped", ErrFailed)
	}

	var ee *exec.ExitError
	if errors.As(err, &ee) {
		return fmt.Errorf("%w: exit code %d", ErrFailed, ee.ExitCode())
	}

	return fmt.Errorf("%w: %w", ErrFailed, err)
}

// limited keeps the first n bytes and drops the rest.
type limited struct {
	w io.Writer
	n int
}

func (l *limited) Write(p []byte) (int, error) {
	if l.n > 0 {
		k := min(len(p), l.n)
		_, _ = l.w.Write(p[:k])
		l.n -= k
	}

	return len(p), nil
}
