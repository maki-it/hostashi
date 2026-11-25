// SPDX-License-Identifier: AGPL-3.0-or-later

// Package resticctl runs the distribution's restic for the backups of
// ADR-0008.
//
// It is one of the few packages allowed to start processes (depguard rule
// in .golangci.yml), and the exception is as narrow as the others: one binary
// by absolute path, a fixed set of sub-commands with fixed flags, no shell,
// a cleared environment, --json output parsed into types, and stderr only in
// the log and in error values. Every variable part of an argument list
// (repository, paths, tags, host, database name, check share) is validated
// here, whoever the caller is.
//
// Database dumps use restic's own --stdin-from-command: restic starts
// mariadb-dump itself. The dump command is built here from a validated
// database name and is never passed in.
package resticctl

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/maki-it/hostashi/internal/agentapi"
	"github.com/maki-it/hostashi/internal/dbserver"
	"github.com/maki-it/hostashi/internal/pgclient"
	"github.com/maki-it/hostashi/internal/pgctl"
)

// Fixed paths (Debian 13 and Ubuntu 26.04 have merged /usr).
const (
	Binary      = "/usr/bin/restic"
	MariaDBDump = "/usr/bin/mariadb-dump"
)

// DefaultTimeout bounds one restic call. A backup of a large site can take
// hours; the hard limit exists so that a hanging call (restic 0.18 can hang
// in --stdin-from-command after a failed upload, fixed in 0.19.0) ends.
const DefaultTimeout = 6 * time.Hour

// Errors callers can tell apart.
var (
	ErrInvalid       = errors.New("resticctl: invalid argument")
	ErrNoRepository  = errors.New("resticctl: repository does not exist")
	ErrExists        = errors.New("resticctl: repository already exists")
	ErrWrongPassword = errors.New("resticctl: wrong repository password")
	ErrLocked        = errors.New("resticctl: repository is locked")
	// ErrPartial means the snapshot was saved, but some files could not be
	// read (restic exit code 3). The summary is still returned.
	ErrPartial = errors.New("resticctl: snapshot is incomplete, some files could not be read")
)

var (
	// The repository is either SFTP (user, host or bracketed IPv6 address,
	// port, and a path relative to the login directory or, after a second
	// slash, absolute) or an absolute local path.
	sftpRepoRe  = regexp.MustCompile(`^sftp://[A-Za-z0-9_][A-Za-z0-9._-]{0,63}@(?:[a-z0-9](?:[a-z0-9.-]{0,251}[a-z0-9])?|\[[0-9a-f:.]{2,45}\]):([0-9]{1,5})//?[A-Za-z0-9._-][A-Za-z0-9._/-]{0,199}$`)
	localPathRe = regexp.MustCompile(`^/[A-Za-z0-9._/-]{1,1000}$`)
	tagRe       = regexp.MustCompile(`^[a-z]{1,16}:[a-z0-9_-]{1,64}$`)
	hostRe      = regexp.MustCompile(`^[a-z0-9]([a-z0-9.-]{0,251}[a-z0-9])?$`)
	snapshotRe  = regexp.MustCompile(`^[0-9a-f]{64}$`)
)

// Repo is one restic repository and the files that open it. All fields are
// set by Hostashi's own code, never from a request; they are validated
// anyway before every call.
type Repo struct {
	Repository   string // sftp://user@host:port/path or /absolute/path
	PasswordFile string // absolute
	CacheDir     string // absolute
	// SSHKey and KnownHosts are required for an SFTP repository. restic
	// starts ssh with exactly this key and host key file, strict checking
	// and no user or system ssh config.
	SSHKey     string
	KnownHosts string
	Host       string // stored in every snapshot (--host)

	// Binary defaults to Binary. Tests point it at another restic.
	Binary  string
	Timeout time.Duration // per call; 0 means DefaultTimeout
	Log     *slog.Logger
	// TempDir holds the option files of external dumps; "" is
	// os.TempDir(), which is private to the backup unit (PrivateTmp).
	TempDir string
	// PGRoot is prefixed to /usr/lib/postgresql (tests); "" is /.
	PGRoot string
}

// Summary is the result of one backup.
type Summary struct {
	SnapshotID     string  `json:"snapshot_id"`
	FilesNew       int64   `json:"files_new"`
	FilesChanged   int64   `json:"files_changed"`
	DataAdded      int64   `json:"data_added"`
	TotalFiles     int64   `json:"total_files_processed"`
	TotalBytes     int64   `json:"total_bytes_processed"`
	DurationSecond float64 `json:"total_duration"`
}

// Snapshot is one entry of restic snapshots.
type Snapshot struct {
	ID       string    `json:"id"`
	Time     time.Time `json:"time"`
	Paths    []string  `json:"paths"`
	Hostname string    `json:"hostname"`
	Tags     []string  `json:"tags"`
	// Summary is what the backup that made the snapshot read (restic
	// 0.17 and later); nil for older snapshots.
	Summary *SnapshotSummary `json:"summary,omitempty"`
}

// SnapshotSummary is the size of a snapshot's content.
type SnapshotSummary struct {
	TotalFilesProcessed int64 `json:"total_files_processed"`
	TotalBytesProcessed int64 `json:"total_bytes_processed"`
}

// Keep is a retention policy for forget. Every group (host and tag set) is
// kept by these counts.
type Keep struct {
	Daily, Weekly, Monthly int
}

// Validate requires at least one daily snapshot and bounded counts.
func (k Keep) Validate() error {
	if k.Daily < 1 || k.Daily > 366 || k.Weekly < 0 || k.Weekly > 520 || k.Monthly < 0 || k.Monthly > 1200 {
		return fmt.Errorf("%w: retention counts out of range", ErrInvalid)
	}

	return nil
}

func (r *Repo) validate() error {
	sftp := strings.HasPrefix(r.Repository, "sftp://")

	switch {
	case sftp && (!sftpRepoRe.MatchString(r.Repository) || strings.Contains(r.Repository, "..")):
		return fmt.Errorf("%w: repository", ErrInvalid)
	case sftp:
		if err := checkPort(r.Repository); err != nil {
			return err
		}

		if !isPath(r.SSHKey) || !isPath(r.KnownHosts) {
			return fmt.Errorf("%w: an SFTP repository needs an SSH key and a known_hosts file", ErrInvalid)
		}
	case !isPath(r.Repository):
		return fmt.Errorf("%w: repository", ErrInvalid)
	}

	if !isPath(r.PasswordFile) || !isPath(r.CacheDir) {
		return fmt.Errorf("%w: password file and cache directory must be absolute paths", ErrInvalid)
	}

	if !hostRe.MatchString(r.Host) {
		return fmt.Errorf("%w: host", ErrInvalid)
	}

	if r.Binary != "" && !isPath(r.Binary) {
		return fmt.Errorf("%w: binary", ErrInvalid)
	}

	return nil
}

func checkPort(repo string) error {
	m := sftpRepoRe.FindStringSubmatch(repo)
	if m == nil {
		return fmt.Errorf("%w: repository", ErrInvalid)
	}

	if port, err := strconv.Atoi(m[1]); err != nil || port < 1 || port > 65535 {
		return fmt.Errorf("%w: port", ErrInvalid)
	}

	return nil
}

// isPath accepts an absolute clean path of plain characters.
func isPath(p string) bool {
	return localPathRe.MatchString(p) && filepath.Clean(p) == p && !strings.Contains(p, "..")
}

func checkTags(tags []string) error {
	if len(tags) == 0 || len(tags) > 8 {
		return fmt.Errorf("%w: 1 to 8 tags", ErrInvalid)
	}

	for _, t := range tags {
		if !tagRe.MatchString(t) {
			return fmt.Errorf("%w: tag %q", ErrInvalid, t)
		}
	}

	return nil
}

// Init creates the repository. An existing one is ErrExists.
func (r *Repo) Init(ctx context.Context) error {
	_, err := r.run(ctx, "init")
	return err
}

// Unlock removes stale locks left by a crashed run. Locks of running
// processes stay.
func (r *Repo) Unlock(ctx context.Context) error {
	_, err := r.run(ctx, "unlock")
	return err
}

// Backup saves the given directories as one snapshot. Every path must be an
// absolute clean path of plain characters. restic does not follow symlinks
// and does not cross into other file systems.
func (r *Repo) Backup(ctx context.Context, tags []string, paths ...string) (Summary, error) {
	if err := checkTags(tags); err != nil {
		return Summary{}, err
	}

	if len(paths) == 0 || len(paths) > 32 {
		return Summary{}, fmt.Errorf("%w: 1 to 32 paths", ErrInvalid)
	}

	for _, p := range paths {
		if !isPath(p) {
			return Summary{}, fmt.Errorf("%w: path %q", ErrInvalid, p)
		}
	}

	args := []string{"backup", "--quiet", "--one-file-system", "--exclude-caches", "--tag", strings.Join(tags, ",")}
	args = append(args, "--")
	args = append(args, paths...)

	return r.backup(ctx, args)
}

// BackupDatabase stores a consistent dump of one MariaDB database as
// <database>.sql. restic runs mariadb-dump itself and fails the snapshot if
// the dump fails. The agent runs as root, which logs in over the socket.
func (r *Repo) BackupDatabase(ctx context.Context, tags []string, database string) (Summary, error) {
	if err := checkTags(tags); err != nil {
		return Summary{}, err
	}

	if err := agentapi.ValidateDBName(database); err != nil {
		return Summary{}, fmt.Errorf("%w: %w", ErrInvalid, err)
	}

	// No directory in the file name: restic 0.18.0 fails on one (fixed in
	// 0.18.1). No --databases, so the dump has no CREATE DATABASE and can be
	// restored into a database of another name.
	args := []string{
		"backup", "--quiet", "--tag", strings.Join(tags, ","),
		"--stdin-filename", database + ".sql", "--stdin-from-command", "--",
		MariaDBDump, "--single-transaction", "--routines", "--events", "--triggers",
		"--hex-blob", "--skip-dump-date", "--default-character-set=utf8mb4", "--", database,
	}

	return r.backup(ctx, args)
}

// BackupRemoteDatabase stores a dump of database on an external server
// (ADR-0041) like BackupDatabase. The connection and the password go to
// mariadb-dump in an option file of mode 0600 in the private temporary
// directory, never on a command line; the file is removed afterwards.
//
// mariadb-dump runs as root here. It answers a server's request for a
// local file (LOAD DATA LOCAL) with nothing, so a hostile server cannot
// read this host's files [V: mariadb-dump 11.8.6, Debian 13, against a
// fake server, 2026-11-21; TestRemoteDumpRefusesLocalInfile keeps it so].
func (r *Repo) BackupRemoteDatabase(ctx context.Context, tags []string, database string, cfg dbserver.Config) (Summary, error) {
	if err := checkTags(tags); err != nil {
		return Summary{}, err
	}

	if err := agentapi.ValidateDBName(database); err != nil {
		return Summary{}, fmt.Errorf("%w: %w", ErrInvalid, err)
	}

	dir, err := os.MkdirTemp(r.TempDir, "hostashi-dump-")
	if err != nil {
		return Summary{}, fmt.Errorf("resticctl: %w", err)
	}

	defer os.RemoveAll(dir)

	if dbserver.IsPostgres(cfg.Engine) {
		return r.backupPostgres(ctx, tags, database, cfg, dir)
	}

	optFile, err := WriteDumpOptions(dir, cfg)
	if err != nil {
		return Summary{}, err
	}

	args := []string{
		"backup", "--quiet", "--tag", strings.Join(tags, ","),
		"--stdin-filename", database + ".sql", "--stdin-from-command", "--",
	}
	args = append(args, RemoteDumpArgs(optFile, database)...)

	return r.backup(ctx, args)
}

// backupPostgres stores a dump of a database on an external PostgreSQL
// server (ADR-0051) as <database>.dump in pg_dump's custom format. restic
// starts pg_dump of the newest installed client (pgctl.DumpCommand) with
// the admin login switched to the database's owner role; the connection
// and the passfile's path reach pg_dump through restic's environment.
func (r *Repo) backupPostgres(ctx context.Context, tags []string, database string, cfg dbserver.Config, dir string) (Summary, error) {
	majors := (&pgclient.Manager{Root: r.PGRoot}).Installed()
	if len(majors) == 0 {
		return Summary{}, ErrNoPGClient
	}

	run := pgctl.Run{
		Major: majors[len(majors)-1], Database: database, User: cfg.AdminUser, Password: cfg.AdminPassword,
		Host: cfg.Host, Port: cfg.Port, TLS: string(cfg.TLS), TempDir: dir, Root: r.PGRoot,
	}

	if cfg.TLS == dbserver.TLSVerifyCA {
		run.CAFile = filepath.Join(dir, "ca.pem")

		if err := os.WriteFile(run.CAFile, []byte(cfg.CAPEM), 0o600); err != nil {
			return Summary{}, fmt.Errorf("resticctl: %w", err)
		}
	}

	argv, env, cleanup, err := pgctl.DumpCommand(run, dbserver.OwnerRole(database))
	if err != nil {
		return Summary{}, err
	}
	defer cleanup()

	args := []string{
		"backup", "--quiet", "--tag", strings.Join(tags, ","),
		"--stdin-filename", database + PGDumpSuffix, "--stdin-from-command", "--",
	}
	args = append(args, argv...)

	out, err := r.execEnv(ctx, env, args...)
	if err != nil && !errors.Is(err, ErrPartial) {
		return Summary{}, err
	}

	sum, perr := parseSummary(out)
	if perr != nil {
		return Summary{}, perr
	}

	return sum, err
}

// PGDumpSuffix ends the file name of a PostgreSQL database in a snapshot.
const PGDumpSuffix = ".dump"

// ErrNoPGClient: no PostgreSQL client is installed for a dump.
var ErrNoPGClient = errors.New("resticctl: no PostgreSQL client is installed")

// WriteDumpOptions writes the option file, and the CA for verify-ca, into
// dir and returns the option file's path.
func WriteDumpOptions(dir string, cfg dbserver.Config) (string, error) {
	caFile := ""

	if cfg.TLS == dbserver.TLSVerifyCA {
		caFile = filepath.Join(dir, "ca.pem")

		if err := os.WriteFile(caFile, []byte(cfg.CAPEM), 0o600); err != nil {
			return "", fmt.Errorf("resticctl: %w", err)
		}
	}

	opts, err := cfg.ClientOptions(caFile)
	if err != nil {
		return "", fmt.Errorf("%w: %w", ErrInvalid, err)
	}

	optFile := filepath.Join(dir, "client.cnf")

	if err := os.WriteFile(optFile, []byte(opts), 0o600); err != nil {
		return "", fmt.Errorf("resticctl: %w", err)
	}

	return optFile, nil
}

// RemoteDumpArgs is the mariadb-dump command for an external database.
// --defaults-extra-file must come first.
func RemoteDumpArgs(optFile, database string) []string {
	return []string{
		MariaDBDump, "--defaults-extra-file=" + optFile,
		"--single-transaction", "--routines", "--events", "--triggers",
		"--hex-blob", "--skip-dump-date", "--default-character-set=utf8mb4", "--", database,
	}
}

func (r *Repo) backup(ctx context.Context, args []string) (Summary, error) {
	out, err := r.run(ctx, args...)
	if err != nil && !errors.Is(err, ErrPartial) {
		return Summary{}, err
	}

	sum, perr := parseSummary(out)
	if perr != nil {
		return Summary{}, perr
	}

	return sum, err
}

func parseSummary(out []byte) (Summary, error) {
	sc := bufio.NewScanner(bytes.NewReader(out))
	sc.Buffer(make([]byte, 64<<10), 1<<20)

	for sc.Scan() {
		var m struct {
			Type string `json:"message_type"`
		}

		if json.Unmarshal(sc.Bytes(), &m) != nil || m.Type != "summary" {
			continue
		}

		var s Summary
		if err := json.Unmarshal(sc.Bytes(), &s); err != nil {
			return Summary{}, fmt.Errorf("resticctl: summary: %w", err)
		}

		if !snapshotRe.MatchString(s.SnapshotID) {
			return Summary{}, errors.New("resticctl: summary without snapshot id")
		}

		return s, nil
	}

	return Summary{}, errors.New("resticctl: backup printed no summary")
}

// Snapshots lists this host's snapshots that carry all given tags; no
// tags lists all of them.
func (r *Repo) Snapshots(ctx context.Context, tags []string) ([]Snapshot, error) {
	args := []string{"snapshots"}

	if len(tags) > 0 {
		if err := checkTags(tags); err != nil {
			return nil, err
		}

		args = append(args, "--tag", strings.Join(tags, ","))
	}

	out, err := r.run(ctx, args...)
	if err != nil {
		return nil, err
	}

	var s []Snapshot
	if err := json.Unmarshal(out, &s); err != nil {
		return nil, fmt.Errorf("resticctl: snapshots: %w", err)
	}

	for _, x := range s {
		if !snapshotRe.MatchString(x.ID) {
			return nil, errors.New("resticctl: snapshot with an invalid id")
		}
	}

	return s, nil
}

// Forget applies the retention policy to every group of host and tags. It
// does not delete data; Prune does.
func (r *Repo) Forget(ctx context.Context, k Keep) error {
	if err := k.Validate(); err != nil {
		return err
	}

	_, err := r.run(ctx, "forget", "--group-by", "host,tags",
		"--keep-daily", strconv.Itoa(k.Daily),
		"--keep-weekly", strconv.Itoa(k.Weekly),
		"--keep-monthly", strconv.Itoa(k.Monthly))

	return err
}

// Prune removes data no snapshot refers to.
func (r *Repo) Prune(ctx context.Context) error {
	_, err := r.run(ctx, "prune")
	return err
}

// Check verifies the repository structure. With total > 0 it also reads
// and verifies part (1 to total) of total equal parts of the pack files, so
// a caller that moves part on with every run reads everything once in total
// runs. total 1 reads all data.
func (r *Repo) Check(ctx context.Context, part, total int) error {
	args := []string{"check"}

	switch {
	case total < 0 || total > 1000 || (total > 0 && (part < 1 || part > total)):
		return fmt.Errorf("%w: check part", ErrInvalid)
	case total == 1:
		args = append(args, "--read-data")
	case total > 1:
		args = append(args, "--read-data-subset="+strconv.Itoa(part)+"/"+strconv.Itoa(total))
	}

	_, err := r.run(ctx, args...)

	return err
}

// maxOutput bounds what restic may print on stdout (snapshot lists of a
// large server stay far below).
const maxOutput = 64 << 20

// Dump writes the snapshot's whole tree to w as a tar stream: restic dump
// --archive tar ID /. Entry names are relative ("home/blog/htdocs/a"). The
// snapshot must belong to this host; callers find it with Snapshots first.
func (r *Repo) Dump(ctx context.Context, id string, w io.Writer) error {
	if !snapshotRe.MatchString(id) {
		return fmt.Errorf("%w: snapshot id", ErrInvalid)
	}

	_, err := r.exec(ctx, w, nil, "dump", "--archive", "tar", id, "/")

	return err
}

func (r *Repo) run(ctx context.Context, args ...string) ([]byte, error) {
	var stdout bytes.Buffer

	return r.exec(ctx, &limitedWriter{buf: &stdout, max: maxOutput}, nil, args...)
}

// execEnv is run with more environment variables for restic, which hands
// its environment to a --stdin-from-command program.
func (r *Repo) execEnv(ctx context.Context, env []string, args ...string) ([]byte, error) {
	var stdout bytes.Buffer

	return r.exec(ctx, &limitedWriter{buf: &stdout, max: maxOutput}, env, args...)
}

// exec runs restic with stdout going to out. The returned bytes are
// stdout when out is the buffer of run, for the exit message and callers.
func (r *Repo) exec(ctx context.Context, out io.Writer, extraEnv []string, args ...string) ([]byte, error) {
	if err := r.validate(); err != nil {
		return nil, err
	}

	bin := r.Binary
	if bin == "" {
		bin = Binary
	}

	timeout := r.Timeout
	if timeout <= 0 {
		timeout = DefaultTimeout
	}

	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	full := []string{args[0], "--json", "--retry-lock", "2m"}

	// --host is a flag of backup, snapshots and forget: it names this
	// server in new snapshots and limits the others to its snapshots.
	switch args[0] {
	case "backup", "snapshots", "forget":
		full = append(full, "--host", r.Host)
	}

	if strings.HasPrefix(r.Repository, "sftp://") {
		// restic splits this value at spaces; the validated paths have none.
		full = append(full, "-o", "sftp.args=-F /dev/null -i "+r.SSHKey+
			" -o IdentitiesOnly=yes -o BatchMode=yes -o StrictHostKeyChecking=yes -o UserKnownHostsFile="+r.KnownHosts+
			" -o ServerAliveInterval=30 -o ServerAliveCountMax=6")
	}

	full = append(full, args[1:]...)

	var stderr bytes.Buffer

	cmd := exec.CommandContext(ctx, bin, full...)
	cmd.Env = []string{
		"PATH=/usr/sbin:/usr/bin:/sbin:/bin", "LC_ALL=C", "HOME=" + r.CacheDir,
		"RESTIC_REPOSITORY=" + r.Repository,
		"RESTIC_PASSWORD_FILE=" + r.PasswordFile,
		"RESTIC_CACHE_DIR=" + r.CacheDir,
		"RESTIC_PROGRESS_FPS=0.016",
	}
	cmd.Env = append(cmd.Env, extraEnv...)
	cmd.Dir = "/"
	cmd.Stdout = out
	cmd.Stderr = &limitedWriter{buf: &stderr, max: 16 << 10}
	cmd.WaitDelay = 30 * time.Second

	err := cmd.Run()

	var stdout []byte
	if lw, ok := out.(*limitedWriter); ok {
		stdout = lw.buf.Bytes()
	}

	if r.Log != nil && stderr.Len() > 0 {
		r.Log.InfoContext(ctx, "restic", "command", args[0], "stderr", strings.TrimSpace(stderr.String()))
	}

	if err == nil {
		return stdout, nil
	}

	var ee *exec.ExitError
	if !errors.As(err, &ee) {
		return nil, fmt.Errorf("resticctl: %s: %w", args[0], err)
	}

	if ctx.Err() != nil {
		return nil, fmt.Errorf("resticctl: %s: %w", args[0], ctx.Err())
	}

	msg := exitMessage(stdout, stderr.Bytes())

	switch ee.ExitCode() {
	case 3:
		return stdout, ErrPartial
	case 10:
		return nil, ErrNoRepository
	case 11:
		return nil, ErrLocked
	case 12:
		return nil, ErrWrongPassword
	}

	if args[0] == "init" && strings.Contains(msg, "already exists") {
		return nil, ErrExists
	}

	return nil, fmt.Errorf("resticctl: %s exited %d: %s", args[0], ee.ExitCode(), msg)
}

// exitMessage picks restic's own error text: the exit_error message of the
// JSON output, or else the last line of stderr.
func exitMessage(stdout, stderr []byte) string {
	for _, out := range [][]byte{stdout, stderr} {
		sc := bufio.NewScanner(bytes.NewReader(out))
		sc.Buffer(make([]byte, 64<<10), 1<<20)

		for sc.Scan() {
			var m struct {
				Type    string `json:"message_type"`
				Message string `json:"message"`
			}

			if json.Unmarshal(sc.Bytes(), &m) == nil && m.Type == "exit_error" {
				return truncate(m.Message)
			}
		}
	}

	lines := strings.Split(strings.TrimSpace(string(stderr)), "\n")

	return truncate(lines[len(lines)-1])
}

func truncate(s string) string {
	if len(s) > 500 {
		return s[:500]
	}

	return s
}

// limitedWriter keeps the first max bytes and drops the rest.
type limitedWriter struct {
	buf *bytes.Buffer
	max int
}

func (w *limitedWriter) Write(p []byte) (int, error) {
	if room := w.max - w.buf.Len(); room > 0 {
		_, _ = w.buf.Write(p[:min(room, len(p))])
	}

	return len(p), nil
}

// ValidSnapshotID reports whether id is a full restic snapshot id.
func ValidSnapshotID(id string) bool { return snapshotRe.MatchString(id) }
