// SPDX-License-Identifier: AGPL-3.0-or-later

// Package sysuser creates and removes Linux users with useradd and userdel.
//
// It is one of the reviewed places in product code allowed to start a process
// (see the depguard rule in .golangci.yml). The exception is narrow: three
// fixed binaries (useradd, userdel, and usermod for the login shell only,
// ADR-0032) by absolute path, argument lists built here from validated names, no shell,
// an empty environment apart from PATH and LC_ALL, and no output returned to
// callers.
package sysuser

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os/exec"
	"os/user"
	"strconv"
	"strings"
	"time"

	"github.com/maki-it/hostashi/internal/agentapi"
	"github.com/maki-it/hostashi/internal/sites"
)

// Fixed binary paths (Debian 13 and Ubuntu 26.04 have merged /usr).
const (
	Useradd = "/usr/sbin/useradd"
	Userdel = "/usr/sbin/userdel"
	Usermod = "/usr/sbin/usermod"
	// NoLogin is the shell of a site user without a shell key: no
	// interactive login, SFTP only (ADR-0032).
	NoLogin = "/usr/sbin/nologin"
	// Bash is the shell of a site user with at least one shell key.
	Bash = "/bin/bash"
	// SSHGroup is the supplementary group of every site user. sshd's Match
	// block for it allows keys from the agent's key file only (ADR-0032).
	// The installer creates it.
	SSHGroup = "hostashi-sites"
)

const commandTimeout = 30 * time.Second

// Exit codes of shadow-utils that are mapped to typed errors.
const (
	exitUserNotFound = 6 // userdel: user does not exist
	exitUserInUse    = 8 // userdel: user is currently used by a process
)

// runner runs one command and returns its exit code and combined output.
// It is replaced in unit tests.
type runner func(ctx context.Context, path string, args []string) (code int, output string, err error)

// Accounts implements sites.Accounts with shadow-utils.
type Accounts struct {
	run runner
	log *slog.Logger
}

// New returns the production implementation.
func New(log *slog.Logger) *Accounts {
	if log == nil {
		log = slog.New(slog.DiscardHandler)
	}

	return &Accounts{run: execRun, log: log}
}

var _ sites.Accounts = (*Accounts)(nil)

// createArgs builds the useradd arguments. name has been validated, home is
// the pool path plus name. The directory is created by the caller on an open
// directory fd, not by useradd.
func createArgs(name, home string) []string {
	return []string{
		"--no-create-home",
		"--home-dir", home,
		"--shell", NoLogin,
		"--user-group",
		"--groups", SSHGroup,
		"--no-log-init",
		"--comment", "hostashi site",
		"--",
		name,
	}
}

func deleteArgs(name string) []string {
	// No --remove: the home directory is removed by the agent with fd-based
	// calls, and the mail spool does not exist.
	return []string{"--", name}
}

// Exists reports whether a user or group of that name is known to the system.
func (a *Accounts) Exists(name string) (bool, error) {
	if err := agentapi.ValidateSiteUser(name); err != nil {
		return false, err
	}

	if _, err := user.Lookup(name); err == nil {
		return true, nil
	} else if !isUnknown(err) {
		return false, err
	}

	if _, err := user.LookupGroup(name); err == nil {
		return true, nil
	} else if !isUnknown(err) {
		return false, err
	}

	return false, nil
}

func isUnknown(err error) bool {
	var u user.UnknownUserError

	var g user.UnknownGroupError

	return errors.As(err, &u) || errors.As(err, &g)
}

// Lookup returns uid and gid of the user, or false if it does not exist.
func (a *Accounts) Lookup(name string) (sites.Account, bool, error) {
	if err := agentapi.ValidateSiteUser(name); err != nil {
		return sites.Account{}, false, err
	}

	u, err := user.Lookup(name)
	if err != nil {
		if isUnknown(err) {
			return sites.Account{}, false, nil
		}

		return sites.Account{}, false, err
	}

	acct, err := parseIDs(u)
	if err != nil {
		return sites.Account{}, false, err
	}

	return acct, true, nil
}

func parseIDs(u *user.User) (sites.Account, error) {
	uid, uerr := strconv.ParseUint(u.Uid, 10, 32)
	gid, gerr := strconv.ParseUint(u.Gid, 10, 32)

	if uerr != nil || gerr != nil || uid == 0 || gid == 0 {
		return sites.Account{}, fmt.Errorf("sysuser: user %q has unusable ids %q:%q", u.Username, u.Uid, u.Gid)
	}

	return sites.Account{UID: uint32(uid), GID: uint32(gid)}, nil
}

// Create runs useradd and reads back the uid and gid.
func (a *Accounts) Create(ctx context.Context, name, home string) (sites.Account, error) {
	if err := agentapi.ValidateSiteUser(name); err != nil {
		return sites.Account{}, err
	}

	if !strings.HasPrefix(home, "/") || strings.ContainsAny(home, "\x00\n") {
		return sites.Account{}, fmt.Errorf("sysuser: home %q must be an absolute path", home)
	}

	code, out, err := a.run(ctx, Useradd, createArgs(name, home))
	if err != nil {
		return sites.Account{}, fmt.Errorf("sysuser: useradd: %w", err)
	}

	if code != 0 {
		return sites.Account{}, fmt.Errorf("sysuser: useradd exited with %d: %s", code, out)
	}

	acct, present, err := a.Lookup(name)
	if err != nil {
		return sites.Account{}, fmt.Errorf("sysuser: look up new user: %w", err)
	}

	if !present {
		return sites.Account{}, fmt.Errorf("sysuser: user %q missing after useradd", name)
	}

	return acct, nil
}

// SetShell sets the login shell of a site user to NoLogin or Bash; any
// other value is refused. usermod changes nothing else.
func (a *Accounts) SetShell(ctx context.Context, name, shell string) error {
	if err := agentapi.ValidateSiteUser(name); err != nil {
		return err
	}

	if shell != NoLogin && shell != Bash {
		return fmt.Errorf("sysuser: shell %q is not allowed", shell)
	}

	code, out, err := a.run(ctx, Usermod, shellArgs(name, shell))
	if err != nil {
		return fmt.Errorf("sysuser: usermod: %w", err)
	}

	if code == exitUserNotFound {
		return fmt.Errorf("sysuser: %w: %s", sites.ErrNotFound, name)
	}

	if code != 0 {
		return fmt.Errorf("sysuser: usermod exited with %d: %s", code, out)
	}

	return nil
}

func shellArgs(name, shell string) []string {
	return []string{"--shell", shell, "--", name}
}

// Delete runs userdel. A user that does not exist is not an error.
func (a *Accounts) Delete(ctx context.Context, name string) error {
	if err := agentapi.ValidateSiteUser(name); err != nil {
		return err
	}

	code, out, err := a.run(ctx, Userdel, deleteArgs(name))
	if err != nil {
		return fmt.Errorf("sysuser: userdel: %w", err)
	}

	switch code {
	case 0, exitUserNotFound:
		return nil
	case exitUserInUse:
		return sites.ErrInUse
	default:
		return fmt.Errorf("sysuser: userdel exited with %d: %s", code, out)
	}
}

func execRun(ctx context.Context, path string, args []string) (int, string, error) {
	ctx, cancel := context.WithTimeout(ctx, commandTimeout)
	defer cancel()

	var out bytes.Buffer

	cmd := exec.CommandContext(ctx, path, args...)
	cmd.Env = []string{"PATH=/usr/sbin:/usr/bin:/sbin:/bin", "LC_ALL=C"}
	cmd.Dir = "/"
	cmd.Stdout = &limitedWriter{buf: &out, max: 2048}
	cmd.Stderr = cmd.Stdout

	err := cmd.Run()

	var ee *exec.ExitError
	if errors.As(err, &ee) {
		return ee.ExitCode(), strings.TrimSpace(out.String()), nil
	}

	if err != nil {
		return -1, "", err
	}

	return 0, strings.TrimSpace(out.String()), nil
}

// limitedWriter keeps the first max bytes and drops the rest.
type limitedWriter struct {
	buf *bytes.Buffer
	max int
}

func (w *limitedWriter) Write(p []byte) (int, error) {
	if room := w.max - w.buf.Len(); room > 0 {
		if len(p) < room {
			room = len(p)
		}

		_, _ = w.buf.Write(p[:room]) // a bytes.Buffer never fails
	}

	return len(p), nil
}
