// SPDX-License-Identifier: AGPL-3.0-or-later

// Package mail hands messages to an external SMTP relay (ADR-0044). It is
// not a mail server: it opens no port and keeps no queue of its own beyond
// what the panel stores. Only encrypted connections are allowed, with a
// verified certificate.
package mail

import (
	"crypto/x509"
	"errors"
	"fmt"
	"net"
	"net/mail"
	"regexp"
	"strings"
	"unicode"
	"unicode/utf8"
)

// TLSMode is how the connection to the relay is encrypted.
type TLSMode string

const (
	// TLSStartTLS connects in plain text and requires STARTTLS before
	// anything else is sent (usually port 587).
	TLSStartTLS TLSMode = "starttls"
	// TLSImplicit speaks TLS from the first byte (usually port 465).
	TLSImplicit TLSMode = "tls"
)

// Limits of the settings, checked by Validate.
const (
	maxHostLen     = 253
	maxAddressLen  = 254
	maxNameLen     = 64
	maxUserLen     = 256
	maxPasswordLen = 1024
	maxCAPEMLen    = 64 << 10
)

// Config is how the panel reaches the relay. Password is the plain text in
// memory only; the store seals it.
type Config struct {
	Host     string
	Port     int
	TLS      TLSMode
	Username string // empty: the relay trusts the server without login
	Password string
	From     string // sender address, a bare address
	FromName string // optional display name
	ReplyTo  string // optional bare address
	// CAPEM is an optional extra root CA for a relay with a private
	// certificate. The system roots stay trusted too.
	CAPEM string
}

// ErrInvalid wraps every validation error, so callers can answer 422.
var ErrInvalid = errors.New("mail: invalid settings")

func invalid(format string, args ...any) error {
	return fmt.Errorf("%w: %s", ErrInvalid, fmt.Sprintf(format, args...))
}

var hostLabel = regexp.MustCompile(`^[a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?$`)

// validHost accepts an IP address or a DNS name in lower case. Schemes such
// as unix:// and anything with a port or a path are refused.
func validHost(h string) bool {
	if h == "" || len(h) > maxHostLen {
		return false
	}

	if ip := net.ParseIP(h); ip != nil {
		return !ip.IsUnspecified() && !ip.IsMulticast()
	}

	for _, l := range strings.Split(h, ".") {
		if !hostLabel.MatchString(l) {
			return false
		}
	}

	return true
}

// ParseAddress accepts exactly one bare address such as a@example.com: no
// display name, no angle brackets, no comments, no line breaks. Anything
// that net/mail would rewrite is refused, so what is shown is what is used.
func ParseAddress(s string) (string, error) {
	if s == "" || len(s) > maxAddressLen || hasControl(s) {
		return "", invalid("address must be a single address like name@example.com")
	}

	a, err := mail.ParseAddress(s)
	if err != nil || a.Name != "" || a.Address != s || strings.Count(s, "@") != 1 {
		return "", invalid("address must be a single address like name@example.com")
	}

	local, domain, _ := strings.Cut(s, "@")
	if local == "" || !strings.Contains(domain, ".") {
		return "", invalid("address must be a single address like name@example.com")
	}

	return s, nil
}

func hasControl(s string) bool {
	for _, r := range s {
		if unicode.IsControl(r) {
			return true
		}
	}

	return false
}

// Validate checks the settings and returns them normalised.
func (c Config) Validate() (Config, error) {
	c.Host = strings.ToLower(strings.TrimSpace(c.Host))
	if !validHost(c.Host) {
		return Config{}, invalid("host must be a host name or an IP address")
	}

	if c.Port < 1 || c.Port > 65535 {
		return Config{}, invalid("port must be between 1 and 65535")
	}

	if c.TLS != TLSStartTLS && c.TLS != TLSImplicit {
		return Config{}, invalid(`tls must be "starttls" or "tls"`)
	}

	if len(c.Username) > maxUserLen || hasControl(c.Username) {
		return Config{}, invalid("username is too long or has control characters")
	}

	if len(c.Password) > maxPasswordLen || hasControl(c.Password) {
		return Config{}, invalid("password is too long or has control characters")
	}

	if c.Username == "" && c.Password != "" {
		return Config{}, invalid("a password needs a username")
	}

	if _, err := ParseAddress(c.From); err != nil {
		return Config{}, invalid("from must be a single address like panel@example.com")
	}

	c.FromName = strings.TrimSpace(c.FromName)
	if utf8.RuneCountInString(c.FromName) > maxNameLen || hasControl(c.FromName) || strings.ContainsAny(c.FromName, `<>"`) {
		return Config{}, invalid("from_name must be at most %d characters without control characters, quotes or angle brackets", maxNameLen)
	}

	if c.ReplyTo != "" {
		if _, err := ParseAddress(c.ReplyTo); err != nil {
			return Config{}, invalid("reply_to must be a single address like admin@example.com")
		}
	}

	if c.CAPEM != "" {
		if len(c.CAPEM) > maxCAPEMLen {
			return Config{}, invalid("ca_pem is too long")
		}

		if !x509.NewCertPool().AppendCertsFromPEM([]byte(c.CAPEM)) {
			return Config{}, invalid("ca_pem holds no PEM certificate")
		}
	}

	return c, nil
}
