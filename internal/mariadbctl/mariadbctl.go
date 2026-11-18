// SPDX-License-Identifier: AGPL-3.0-or-later

// Package mariadbctl runs the distribution's mariadb and mariadb-dump for
// database import and export (ADR-0025).
//
// Like internal/phpctl it is allowed to start processes (depguard rule in
// .golangci.yml), with the same narrow exception: two binaries by absolute
// path, fixed argument lists whose only variable is a database name that
// passed agentapi.ValidateDBName, no shell, a minimal environment. On top:
//
//   - The process runs as the site's Linux user, never as root.
//   - Credentials come from a pipe (--defaults-file=/dev/fd/3), so they are
//     neither in argv nor in a file.
//   - mariadb runs with --sandbox: system, \!, source and tee in a dump are
//     refused.
//
// The caller passes a temporary database user that has privileges on the
// target database only.
package mariadbctl

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"regexp"
	"syscall"

	"github.com/maki-it/hostashi/internal/agentapi"
)

// The distribution's binaries (Debian 13 and Ubuntu 26.04 have merged /usr).
const (
	Client = "/usr/bin/mariadb"
	Dump   = "/usr/bin/mariadb-dump"
)

// Run describes one run: as which Linux user, with which database account.
type Run struct {
	Database string
	UID, GID uint32
	User     string // temporary database user
	Password string
	Socket   string // server socket, empty for the default
}

// ErrFailed carries a short, fixed description. The client's output stays
// in the log, it may contain parts of the dump.
var ErrFailed = errors.New("mariadbctl: failed")

// Import feeds in to mariadb.
func Import(ctx context.Context, r Run, in io.Reader) error {
	return run(ctx, r, Client, []string{"--sandbox", "--batch", "--max-allowed-packet=1073741824", "--database=" + r.Database}, in, io.Discard)
}

// Export writes the dump of the database to out.
func Export(ctx context.Context, r Run, out io.Writer) error {
	return run(ctx, r, Dump, []string{
		"--single-transaction", "--routines", "--triggers", "--events", "--no-tablespaces",
		"--hex-blob", "--default-character-set=utf8mb4", "--max-allowed-packet=1073741824", "--", r.Database,
	}, nil, out)
}

func run(ctx context.Context, r Run, bin string, args []string, in io.Reader, out io.Writer) error {
	if err := agentapi.ValidateDBName(r.Database); err != nil {
		return fmt.Errorf("%w: %w", ErrFailed, err)
	}

	if r.UID == 0 || r.GID == 0 {
		return fmt.Errorf("%w: refusing to run as root", ErrFailed)
	}

	cnf, err := defaults(r)
	if err != nil {
		return err
	}

	pr, pw, err := os.Pipe()
	if err != nil {
		return fmt.Errorf("mariadbctl: pipe: %w", err)
	}

	defer pr.Close()

	// The client opens /dev/fd/3 again by path, which checks the pipe's
	// owner and mode, so the read end must belong to the client's user.
	if err := pr.Chown(int(r.UID), int(r.GID)); err != nil {
		_ = pw.Close()

		return fmt.Errorf("mariadbctl: chown options pipe: %w", err)
	}

	// The file is a few hundred bytes, far below the pipe buffer.
	if _, err := pw.Write(cnf); err != nil {
		_ = pw.Close()

		return fmt.Errorf("mariadbctl: write options: %w", err)
	}

	_ = pw.Close()

	var stderr bytes.Buffer

	//nolint:gosec // fixed binary, fixed flags, the database name is validated
	cmd := exec.CommandContext(ctx, bin, append([]string{"--defaults-file=/dev/fd/3"}, args...)...)
	cmd.Env = []string{"PATH=/usr/bin:/bin", "HOME=/nonexistent", "LANG=C.UTF-8"}
	cmd.Dir = "/"
	cmd.Stdin = in
	cmd.Stdout = out
	cmd.Stderr = &limited{w: &stderr, n: 4096}
	cmd.ExtraFiles = []*os.File{pr}
	cmd.SysProcAttr = &syscall.SysProcAttr{
		Credential: &syscall.Credential{Uid: r.UID, Gid: r.GID, Groups: []uint32{}},
		Pdeathsig:  syscall.SIGKILL,
	}

	if err := cmd.Run(); err != nil {
		return describe(err, stderr.Bytes())
	}

	return nil
}

// defaults is the option file. Names and password passed the validators, so
// none of them can break the file format.
func defaults(r Run) ([]byte, error) {
	for _, v := range []string{r.User, r.Password, r.Socket} {
		for i := range len(v) {
			if c := v[i]; c < 0x21 || c > 0x7e || c == '"' || c == '\'' || c == '\\' || c == '#' || c == ';' {
				return nil, fmt.Errorf("%w: invalid character in options", ErrFailed)
			}
		}
	}

	if r.User == "" || r.Password == "" {
		return nil, fmt.Errorf("%w: user and password are required", ErrFailed)
	}

	// mariadb-dump reads [mysqldump] and [mariadb-dump], the client
	// [mysql] and [mariadb]; [client] covers both.
	out := "[client]\nuser=" + r.User + "\npassword=" + r.Password + "\n"
	if r.Socket != "" {
		out += "socket=" + r.Socket + "\n"
	}

	return []byte(out), nil
}

var errLineRe = regexp.MustCompile(`ERROR (\d{4,5}) \([0-9A-Z]{5}\) at line (\d{1,12})`)

// describe returns a fixed text: the server error number and the line of
// the dump, nothing of the dump itself.
func describe(err error, stderr []byte) error {
	if m := errLineRe.FindSubmatch(stderr); m != nil {
		return fmt.Errorf("%w: server error %s at line %s of the dump", ErrFailed, m[1], m[2])
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
