// SPDX-License-Identifier: AGPL-3.0-or-later

package agentapi

import (
	"errors"
	"fmt"
	"net/netip"
	"strconv"
	"strings"

	"github.com/maki-it/hostashi/internal/nginxconf"
)

// Password protection and IP rules of a site (ADR-0066). The panel and the
// agent both check a setting with Validate and CheckOwn; package web renders
// it with nginx's access and auth_basic modules.

// IP rule modes of a site.
const (
	// AccessIPAllow lets only the listed addresses in; others get 403.
	AccessIPAllow = "allow"
	// AccessIPDeny blocks the listed addresses with 403.
	AccessIPDeny = "deny"
)

// Bounds of one site's access setting.
const (
	MaxAccessIPs       = 256
	MaxAccessPaths     = 20
	MaxAccessPathBytes = 200
	MaxAccessBypass    = 64
	MaxAccessUsers     = 50
	MaxAccessUserName  = 64
	// MinAccessPassword and MaxAccessPassword bound a password in bytes;
	// bcrypt reads at most 72 bytes.
	MinAccessPassword = 12
	MaxAccessPassword = 72
	// AccessBcryptCost is the cost the panel hashes with: nginx checks the
	// hash inside its worker on every protected request (ADR-0066).
	AccessBcryptCost = 5
	minAccessCost    = 4
	maxAccessCost    = 12
)

// ErrAccessRejected wraps every reason an access setting is refused.
var ErrAccessRejected = errors.New("access rejected")

// ErrAccessConflict is a custom snippet that sets access directives while
// the matching setting is on.
var ErrAccessConflict = errors.New("access conflict")

// WebAccess is a site's IP rule and password protection. The zero value
// lets everybody in without a password.
type WebAccess struct {
	// IPMode is empty (off), AccessIPAllow or AccessIPDeny.
	IPMode string `json:"ipMode,omitempty"`
	// IPs are addresses or CIDR prefixes for IPMode.
	IPs []string `json:"ips,omitempty"`
	// Auth asks for a password; nil asks for none.
	Auth *WebAuth `json:"auth,omitempty"`
}

// WebAuth is the basic auth part of WebAccess.
type WebAuth struct {
	// Paths are the protected path prefixes; empty protects the whole site.
	Paths []string `json:"paths,omitempty"`
	// Bypass are addresses or CIDR prefixes that need no password.
	Bypass []string `json:"bypass,omitempty"`
	// Users are the accounts with their bcrypt hashes, at least one.
	Users []WebAuthUser `json:"users"`
}

// WebAuthUser is one basic auth account.
type WebAuthUser struct {
	Name string `json:"name"`
	Hash string `json:"hash"`
}

// IsZero reports whether the setting changes nothing.
func (a WebAccess) IsZero() bool { return a.IPMode == "" && a.Auth == nil }

// Validate checks every field. The panel calls it before it stores a
// setting and the agent again before it renders one.
func (a WebAccess) Validate() error {
	if err := a.validateIPs(); err != nil {
		return fmt.Errorf("%w: %w", ErrAccessRejected, err)
	}

	if a.Auth != nil {
		if err := a.Auth.validate(); err != nil {
			return fmt.Errorf("%w: %w", ErrAccessRejected, err)
		}
	}

	return nil
}

func (a WebAccess) validateIPs() error {
	switch a.IPMode {
	case "":
		if len(a.IPs) > 0 {
			return errors.New("addresses need an IP rule")
		}

		return nil
	case AccessIPAllow, AccessIPDeny:
	default:
		return fmt.Errorf("IP rule %q is not %q or %q", a.IPMode, AccessIPAllow, AccessIPDeny)
	}

	if len(a.IPs) == 0 {
		return errors.New("an IP rule needs at least one address")
	}

	_, err := accessPrefixes("address", a.IPs, MaxAccessIPs)

	return err
}

func accessPrefixes(what string, list []string, limit int) ([]netip.Prefix, error) {
	if len(list) > limit {
		return nil, fmt.Errorf("more than %d %ses", limit, what)
	}

	out := make([]netip.Prefix, 0, len(list))

	for _, s := range list {
		p, err := ParsePrefix(s)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", what, err)
		}

		for _, q := range out {
			if q == p {
				return nil, fmt.Errorf("%s %s is listed twice", what, p)
			}
		}

		out = append(out, p)
	}

	return out, nil
}

func (u WebAuth) validate() error {
	if err := u.validateRules(); err != nil {
		return err
	}

	return u.validateUsers()
}

// ValidateRules checks the paths and bypass addresses without the users,
// for a setting that is stored while password protection is off.
func (u WebAuth) ValidateRules() error {
	if err := u.validateRules(); err != nil {
		return fmt.Errorf("%w: %w", ErrAccessRejected, err)
	}

	return nil
}

func (u WebAuth) validateRules() error {
	if len(u.Paths) > MaxAccessPaths {
		return fmt.Errorf("more than %d paths", MaxAccessPaths)
	}

	seen := map[string]bool{}

	for _, p := range u.Paths {
		if err := ValidateAccessPath(p); err != nil {
			return err
		}

		k := strings.ToLower(NormalAccessPath(p))
		if seen[k] {
			return fmt.Errorf("path %s is listed twice", p)
		}

		seen[k] = true
	}

	bypass, err := accessPrefixes("bypass address", u.Bypass, MaxAccessBypass)
	if err != nil {
		return err
	}

	for _, p := range bypass {
		if a := p.Addr(); a.IsLoopback() || p.Contains(netip.IPv6Loopback()) || p.Contains(netip.AddrFrom4([4]byte{127, 0, 0, 1})) || a.IsUnspecified() {
			return fmt.Errorf("bypass address %s contains loopback addresses", p)
		}
	}

	return nil
}

func (u WebAuth) validateUsers() error {
	if len(u.Users) == 0 {
		return errors.New("password protection needs at least one user")
	}

	if len(u.Users) > MaxAccessUsers {
		return fmt.Errorf("more than %d users", MaxAccessUsers)
	}

	names := map[string]bool{}

	for _, x := range u.Users {
		if err := ValidateAccessUserName(x.Name); err != nil {
			return err
		}

		if names[x.Name] {
			return fmt.Errorf("user %s is listed twice", x.Name)
		}

		names[x.Name] = true

		if err := validBcrypt(x.Hash); err != nil {
			return fmt.Errorf("user %s: %w", x.Name, err)
		}
	}

	return nil
}

// ValidateAccessPath checks one protected path prefix: it starts with "/",
// uses only unreserved characters and has no "." or ".." segment, because
// nginx matches against the normalized $uri.
func ValidateAccessPath(p string) error {
	if p == "" || p[0] != '/' {
		return fmt.Errorf("path %q must start with /", p)
	}

	if len(p) > MaxAccessPathBytes {
		return fmt.Errorf("path %q is longer than %d bytes", p, MaxAccessPathBytes)
	}

	for _, c := range []byte(p) {
		if !isAlnum(c) && strings.IndexByte("._~/-", c) < 0 {
			return fmt.Errorf("path %q may only contain letters, digits and . _ ~ / -", p)
		}
	}

	if strings.Contains(p, "//") {
		return fmt.Errorf("path %q contains //", p)
	}

	for _, seg := range strings.Split(p, "/") {
		if seg == "." || seg == ".." {
			return fmt.Errorf("path %q contains a . or .. segment", p)
		}
	}

	if NormalAccessPath(p) == "/" {
		return fmt.Errorf("path %q protects the whole site; leave the paths empty instead", p)
	}

	return nil
}

// NormalAccessPath drops a trailing slash, so "/wp-admin/" and "/wp-admin"
// protect the same prefix. Call it only on a checked path.
func NormalAccessPath(p string) string {
	if len(p) > 1 {
		return strings.TrimSuffix(p, "/")
	}

	return p
}

// ValidateAccessUserName checks a basic auth user name: 1 to 64 of
// A-Z a-z 0-9 . _ @ -. A colon would end the name in the password file.
func ValidateAccessUserName(n string) error {
	if n == "" || len(n) > MaxAccessUserName {
		return fmt.Errorf("user name %q must have 1 to %d characters", n, MaxAccessUserName)
	}

	for _, c := range []byte(n) {
		if !isAlnum(c) && strings.IndexByte("._@-", c) < 0 {
			return fmt.Errorf("user name %q may only contain letters, digits and . _ @ -", n)
		}
	}

	return nil
}

// ValidateAccessPassword checks a password before the panel hashes it:
// 12 to 72 bytes of printable ASCII or other non-control characters.
func ValidateAccessPassword(pw string) error {
	if len(pw) < MinAccessPassword || len(pw) > MaxAccessPassword {
		return fmt.Errorf("%w: a password must have %d to %d bytes", ErrAccessRejected, MinAccessPassword, MaxAccessPassword)
	}

	for _, r := range pw {
		if r < 0x20 || r == 0x7f || (r >= 0x80 && r < 0xa0) || r == 0xfffd {
			return fmt.Errorf("%w: a password must not contain control characters", ErrAccessRejected)
		}
	}

	return nil
}

func isAlnum(c byte) bool {
	return c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9'
}

// validBcrypt checks the form "$2b$05$" + 53 characters of bcrypt's base64,
// so a hash can never carry a newline or a colon into the password file.
func validBcrypt(h string) error {
	if len(h) != 60 || h[0] != '$' || h[1] != '2' || strings.IndexByte("aby", h[2]) < 0 || h[3] != '$' || h[6] != '$' {
		return errors.New("hash is not a bcrypt hash")
	}

	cost, err := strconv.Atoi(h[4:6])
	if err != nil || cost < minAccessCost || cost > maxAccessCost {
		return fmt.Errorf("bcrypt cost must be %d to %d", minAccessCost, maxAccessCost)
	}

	for _, c := range []byte(h[7:]) {
		if !isAlnum(c) && c != '.' && c != '/' {
			return errors.New("hash is not a bcrypt hash")
		}
	}

	return nil
}

// CheckOwn refuses a bypass list that contains an address of the server:
// every site user can connect from those.
func (a WebAccess) CheckOwn(own []netip.Addr) error {
	if a.Auth == nil {
		return nil
	}

	for _, s := range a.Auth.Bypass {
		p, err := ParsePrefix(s)
		if err != nil {
			continue
		}

		for _, o := range own {
			if p.Contains(o.Unmap()) {
				return fmt.Errorf("%w: bypass address %s contains %s, an address of this server", ErrAccessRejected, p, o.Unmap())
			}
		}
	}

	return nil
}

// accessDirectives are the snippet directives the setting takes over.
var (
	ipDirectives   = []string{"allow", "deny"}
	authDirectives = []string{"auth_basic", "auth_basic_user_file", "satisfy"}
)

// usesDirective reports whether name appears at any depth: a nested
// location with its own allow or auth_basic would override the site's.
func usesDirective(ds []nginxconf.Directive, name string) bool {
	for _, d := range ds {
		if d.Name == name || usesDirective(d.Block, name) {
			return true
		}
	}

	return false
}

// Conflict refuses custom snippets (ADR-0029) that use access directives,
// at any depth, while the matching setting is on; satisfy counts for both.
// A snippet that does not parse is left to the snippet check.
func (a WebAccess) Conflict(server, root string) error {
	var names []string

	if a.IPMode != "" {
		names = append(names, ipDirectives...)
		names = append(names, "satisfy")
	}

	if a.Auth != nil {
		names = append(names, authDirectives...)
	}

	if len(names) == 0 {
		return nil
	}

	for _, sn := range []struct{ slot, src string }{{"server", server}, {"location /", root}} {
		if sn.src == "" {
			continue
		}

		ds, err := nginxconf.Parse(sn.src)
		if err != nil {
			continue
		}

		for _, n := range names {
			if usesDirective(ds, n) {
				return fmt.Errorf("%w: the %s snippet uses %s, which the site's access protection sets", ErrAccessConflict, sn.slot, n)
			}
		}
	}

	return nil
}
