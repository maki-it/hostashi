// SPDX-License-Identifier: AGPL-3.0-or-later

package agentapi

import (
	"errors"
	"fmt"
	"net/netip"
	"net/url"
	"path"
	"strconv"
	"strings"
	"time"
	"unicode"

	"github.com/maki-it/hostashi/internal/cron"
)

// Git deploy of a site (ADR-0062). The panel checks the settings with these
// rules before it stores them; the agent checks them again before a run.

// Kinds of access to a repository.
const (
	// GitAuthNone: a public repository over HTTPS.
	GitAuthNone = "none"
	// GitAuthToken: HTTPS with a user name and a token.
	GitAuthToken = "token"
	// GitAuthSSH: SSH with the site's deploy key.
	GitAuthSSH = "ssh"
)

// Limits of the Git settings of a site.
const (
	MaxGitURL           = 512
	MaxGitBranch        = 255
	MaxGitSharedPaths   = 32
	MaxGitPath          = 255
	MaxGitCommands      = 20
	MinGitKeep          = 2
	MaxGitKeep          = 20
	DefaultGitKeep      = 5
	DefaultGitBranch    = "main"
	MinGitBuildTimeout  = time.Minute
	MaxGitBuildTimeout  = time.Hour
	DefaultBuildTimeout = 15 * time.Minute
	MaxGitTokenUser     = 64
	MaxGitToken         = 1024
)

// GitDeployDirRoot is the directory below the home where Hostashi keeps the
// repository cache, the releases and the shared files. A deploy directory
// or a shared path must not point into it.
const GitDeployDirRoot = ".deploy"

// GitRemote is a parsed repository address.
type GitRemote struct {
	// Scheme is "https" or "ssh".
	Scheme string
	// User is the SSH user ("git" for the scp form without one); empty for
	// HTTPS.
	User string
	Host string
	// Port is the explicit port, 0 for the scheme's default.
	Port int
	// Path is the repository path without a leading slash.
	Path string
}

// ErrGitURL: the repository address is not one Hostashi deploys from.
var ErrGitURL = errors.New("invalid repository address")

// ParseGitURL checks a repository address: https://host[:port]/path,
// ssh://[user@]host[:port]/path or the scp form [user@]host:path. Local
// paths, file://, git:// and the ext:: transport are refused, as are user
// info with a password, queries and fragments. An IP address must be one a
// reverse proxy may reach (ProxyAddrAllowed); names below .localhost and
// "localhost" are refused. Names are not resolved here.
func ParseGitURL(raw string) (GitRemote, error) {
	if raw == "" || len(raw) > MaxGitURL {
		return GitRemote{}, fmt.Errorf("%w: empty or longer than %d bytes", ErrGitURL, MaxGitURL)
	}

	for _, r := range raw {
		if r <= ' ' || r == 0x7f || r > unicode.MaxASCII {
			return GitRemote{}, fmt.Errorf("%w: only printable ASCII without spaces", ErrGitURL)
		}
	}

	var (
		rem GitRemote
		err error
	)

	switch {
	case strings.HasPrefix(raw, "https://"), strings.HasPrefix(raw, "ssh://"):
		rem, err = parseGitURLForm(raw)
	case strings.Contains(raw, "://"), strings.HasPrefix(raw, "ext::"):
		return GitRemote{}, fmt.Errorf("%w: only https://, ssh:// and user@host:path are allowed", ErrGitURL)
	default:
		rem, err = parseGitSCP(raw)
	}

	if err != nil {
		return GitRemote{}, err
	}

	if err := checkGitHost(rem.Host); err != nil {
		return GitRemote{}, err
	}

	if err := checkGitPath(rem.Path); err != nil {
		return GitRemote{}, err
	}

	return rem, nil
}

func parseGitURLForm(raw string) (GitRemote, error) {
	u, err := url.Parse(raw)
	if err != nil {
		return GitRemote{}, fmt.Errorf("%w: %w", ErrGitURL, err)
	}

	if u.RawQuery != "" || u.ForceQuery || u.Fragment != "" || u.Opaque != "" {
		return GitRemote{}, fmt.Errorf("%w: no query or fragment", ErrGitURL)
	}

	rem := GitRemote{Scheme: u.Scheme, Host: u.Hostname(), Path: strings.TrimPrefix(u.Path, "/")}

	if u.User != nil {
		if _, has := u.User.Password(); has {
			return GitRemote{}, fmt.Errorf("%w: no password in the address, use a token", ErrGitURL)
		}

		if u.Scheme == "https" {
			return GitRemote{}, fmt.Errorf("%w: no user in an https address, set the token's user instead", ErrGitURL)
		}

		rem.User = u.User.Username()
	}

	if u.Scheme == "ssh" && rem.User == "" {
		rem.User = "git"
	}

	if p := u.Port(); p != "" {
		n, err := strconv.Atoi(p)
		if err != nil || n < 1 || n > 65535 {
			return GitRemote{}, fmt.Errorf("%w: port %q", ErrGitURL, p)
		}

		rem.Port = n
	} else if strings.HasSuffix(u.Host, ":") {
		return GitRemote{}, fmt.Errorf("%w: empty port", ErrGitURL)
	}

	if err := checkGitUser(rem.User); err != nil {
		return GitRemote{}, err
	}

	return rem, nil
}

// parseGitSCP parses [user@]host:path. Git reads a colon after a slash as a
// local path; such an address is refused.
func parseGitSCP(raw string) (GitRemote, error) {
	hostPart, p, ok := strings.Cut(raw, ":")
	if !ok || strings.Contains(hostPart, "/") || strings.HasPrefix(raw, "[") {
		return GitRemote{}, fmt.Errorf("%w: only https://, ssh:// and user@host:path are allowed", ErrGitURL)
	}

	user := "git"
	if u, h, has := strings.Cut(hostPart, "@"); has {
		user, hostPart = u, h
	}

	if err := checkGitUser(user); err != nil {
		return GitRemote{}, err
	}

	if strings.HasPrefix(p, "//") {
		return GitRemote{}, fmt.Errorf("%w: path %q", ErrGitURL, p)
	}

	return GitRemote{Scheme: "ssh", User: user, Host: hostPart, Path: strings.TrimPrefix(p, "/")}, nil
}

func checkGitUser(u string) error {
	if u == "" {
		return nil
	}

	if len(u) > 64 || strings.HasPrefix(u, "-") {
		return fmt.Errorf("%w: user %q", ErrGitURL, u)
	}

	for _, r := range u {
		if !isGitNameRune(r) {
			return fmt.Errorf("%w: user %q", ErrGitURL, u)
		}
	}

	return nil
}

func isGitNameRune(r rune) bool {
	return r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '-' || r == '_' || r == '.'
}

func isGitHostRune(r rune) bool {
	return r >= 'a' && r <= 'z' || r >= '0' && r <= '9' || r == '-' || r == '.'
}

func checkGitHost(h string) error {
	if h == "" {
		return fmt.Errorf("%w: no host", ErrGitURL)
	}

	if a, err := netip.ParseAddr(strings.Trim(h, "[]")); err == nil {
		if err := ProxyAddrAllowed(a); err != nil {
			return fmt.Errorf("%w: %w", ErrGitURL, err)
		}

		return nil
	}

	h = strings.ToLower(h)
	if h == "localhost" || strings.HasSuffix(h, ".localhost") {
		return fmt.Errorf("%w: localhost points to this server", ErrGitURL)
	}

	if len(h) > 253 || strings.HasPrefix(h, "-") || strings.HasPrefix(h, ".") || strings.HasSuffix(h, ".") || strings.Contains(h, "..") {
		return fmt.Errorf("%w: host %q", ErrGitURL, h)
	}

	for _, r := range h {
		if !isGitHostRune(r) {
			return fmt.Errorf("%w: host %q", ErrGitURL, h)
		}
	}

	return nil
}

func checkGitPath(p string) error {
	if p == "" || strings.HasPrefix(p, "-") {
		return fmt.Errorf("%w: no repository path", ErrGitURL)
	}

	for _, seg := range strings.Split(strings.TrimSuffix(p, "/"), "/") {
		if seg == "" || seg == "." || seg == ".." {
			return fmt.Errorf("%w: path %q", ErrGitURL, p)
		}
	}

	return nil
}

// ValidateGitBranch checks a branch name with the rules of
// git check-ref-format for one branch, narrowed to ASCII letters, digits
// and "._-/".
func ValidateGitBranch(b string) error {
	bad := func() error { return fmt.Errorf("invalid branch %q", b) }

	if b == "" || len(b) > MaxGitBranch || b == "@" || b == "HEAD" {
		return bad()
	}

	if strings.HasPrefix(b, "-") || strings.HasPrefix(b, "/") || strings.HasSuffix(b, "/") || strings.HasSuffix(b, ".") ||
		strings.Contains(b, "..") || strings.Contains(b, "//") || strings.Contains(b, "@{") {
		return bad()
	}

	for _, seg := range strings.Split(b, "/") {
		if strings.HasPrefix(seg, ".") || strings.HasSuffix(seg, ".lock") {
			return bad()
		}
	}

	for _, r := range b {
		if !isGitNameRune(r) && r != '/' {
			return bad()
		}
	}

	return nil
}

// checkRelPath checks a relative path: clean, no "..", no absolute path,
// no component starting with a dot unless allowDot, printable ASCII.
func checkRelPath(what, p string, allowDot bool) error {
	if p == "" || len(p) > MaxGitPath {
		return fmt.Errorf("%s: empty or longer than %d bytes", what, MaxGitPath)
	}

	if path.Clean(p) != p || path.IsAbs(p) || p == "." {
		return fmt.Errorf("%s %q: must be a clean relative path", what, p)
	}

	for _, seg := range strings.Split(p, "/") {
		if seg == ".." || !allowDot && strings.HasPrefix(seg, ".") {
			return fmt.Errorf("%s %q: no .. and no hidden folders", what, p)
		}

		for _, r := range seg {
			if r < ' ' || r == 0x7f || r > unicode.MaxASCII || r == '\\' {
				return fmt.Errorf("%s %q: only printable ASCII", what, p)
			}
		}
	}

	return nil
}

// ValidateGitDeployDir checks the directory, relative to the home, that
// becomes the link to the live release: not hidden, not the home itself and
// not inside .deploy.
func ValidateGitDeployDir(d string) error {
	return checkRelPath("deploy directory", d, false)
}

// ValidateGitShared checks the shared paths, relative to a release: at most
// MaxGitSharedPaths, unique, none inside another and none inside .git.
// Hidden files such as .env are allowed.
func ValidateGitShared(paths []string) error {
	if len(paths) > MaxGitSharedPaths {
		return fmt.Errorf("at most %d shared paths", MaxGitSharedPaths)
	}

	for i, p := range paths {
		if err := checkRelPath("shared path", p, true); err != nil {
			return err
		}

		if p == ".git" || strings.HasPrefix(p, ".git/") {
			return fmt.Errorf("shared path %q: not inside .git", p)
		}

		for _, q := range paths[:i] {
			if p == q || strings.HasPrefix(p, q+"/") || strings.HasPrefix(q, p+"/") {
				return fmt.Errorf("shared paths %q and %q overlap", q, p)
			}
		}
	}

	return nil
}

// ValidateGitCommands checks the build commands with the rules of cron
// commands (ADR-0028): at most MaxGitCommands, each one line of at most
// 4 KiB.
func ValidateGitCommands(cmds []string) error {
	if len(cmds) > MaxGitCommands {
		return fmt.Errorf("at most %d build commands", MaxGitCommands)
	}

	for i, c := range cmds {
		if err := cron.ValidateCommand(c); err != nil {
			return fmt.Errorf("build command %d: %w", i+1, err)
		}
	}

	return nil
}

// ValidateGitKeep checks how many releases are kept.
func ValidateGitKeep(n int) error {
	if n < MinGitKeep || n > MaxGitKeep {
		return fmt.Errorf("releases to keep must be %d-%d", MinGitKeep, MaxGitKeep)
	}

	return nil
}

// ValidateGitBuildTimeout checks the time all build commands may take.
func ValidateGitBuildTimeout(d time.Duration) error {
	if d < MinGitBuildTimeout || d > MaxGitBuildTimeout || d%time.Minute != 0 {
		return fmt.Errorf("build timeout must be whole minutes from %s to %s", MinGitBuildTimeout, MaxGitBuildTimeout)
	}

	return nil
}

// ValidateGitTokenUser checks the user name that goes with a token; empty
// is allowed (Hostashi then sends a fixed one).
func ValidateGitTokenUser(u string) error {
	if len(u) > MaxGitTokenUser {
		return fmt.Errorf("token user longer than %d bytes", MaxGitTokenUser)
	}

	for _, r := range u {
		if !isGitNameRune(r) && r != '@' {
			return fmt.Errorf("invalid token user %q", u)
		}
	}

	return nil
}

// ValidateGitToken checks an access token: printable ASCII without spaces.
func ValidateGitToken(t string) error {
	if t == "" || len(t) > MaxGitToken {
		return fmt.Errorf("token must be 1-%d bytes", MaxGitToken)
	}

	for _, r := range t {
		if r <= ' ' || r == 0x7f || r > unicode.MaxASCII {
			return errors.New("token: only printable ASCII without spaces")
		}
	}

	return nil
}
