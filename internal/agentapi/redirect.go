// SPDX-License-Identifier: AGPL-3.0-or-later

package agentapi

import (
	"errors"
	"fmt"
	"net"
	"net/url"
	"regexp"
	"slices"
	"strconv"
	"strings"
)

// WebRedirect makes a site answer every address with a redirect (site type
// redirect, ADR-0054). Such a site has no Linux account and no files.
type WebRedirect struct {
	// Target is an http or https URL with a host, an optional port and an
	// optional path; no user info, query or fragment.
	Target string `json:"target"`
	// Status is 301, 302, 307 or 308.
	Status int `json:"status"`
	// KeepPath appends the request's path and query to the target.
	KeepPath bool `json:"keepPath,omitempty"`
}

// MaxRedirectTarget bounds the length of a redirect target.
const MaxRedirectTarget = 2048

// RedirectStatuses are the allowed redirect status codes; the first is the
// default.
var RedirectStatuses = []int{301, 302, 307, 308}

// redirectRe bounds the characters of a whole target to those that need no
// quoting in nginx: no quotes, spaces, ';', '{', '}', '$', '\', '?' or '#'.
// The path takes the RFC 3986 path characters among them.
var redirectRe = regexp.MustCompile(`^https?://[A-Za-z0-9.:\[\]-]+(/[A-Za-z0-9._~%!*+,=:@&()-]*)*$`)

// ParseRedirectTarget checks a target and returns it parsed. own lists the
// domains of the site: a target on one of them would redirect to itself.
func ParseRedirectTarget(target string, own []string) (*url.URL, error) {
	return parseTarget("redirect target", target, own)
}

// parseTarget checks the URL of a redirect or reverse proxy site: http or
// https, a host in lower case or an IP address, an optional port and path,
// nothing else, and not one of the site's own domains.
func parseTarget(what, target string, own []string) (*url.URL, error) {
	if target == "" || len(target) > MaxRedirectTarget {
		return nil, fmt.Errorf("%s: between 1 and %d characters", what, MaxRedirectTarget)
	}

	if !redirectRe.MatchString(target) {
		return nil, errors.New(what + ": use http:// or https://, a host and a path of letters, digits and - . _ ~ % ! * + , = : @ & ( ) / only")
	}

	u, err := url.Parse(target)
	if err != nil {
		return nil, errors.New(what + ": not a URL")
	}

	if u.Scheme != "http" && u.Scheme != "https" {
		return nil, errors.New(what + ": must start with http:// or https://")
	}

	if u.User != nil || u.RawQuery != "" || u.ForceQuery || u.Fragment != "" || u.Opaque != "" || strings.ContainsAny(target, "?#") {
		return nil, errors.New(what + ": no user name, query or fragment")
	}

	host := u.Hostname()
	if host == "" {
		return nil, errors.New(what + ": a host is required")
	}

	if ip := net.ParseIP(host); ip == nil {
		if err := ValidateDomain(strings.ToLower(host)); err != nil {
			return nil, fmt.Errorf("%s: %w", what, err)
		}

		if host != strings.ToLower(host) {
			return nil, errors.New(what + ": write the host in lower case")
		}
	}

	if p := u.Port(); p != "" {
		n, err := strconv.Atoi(p)
		if err != nil || n < 1 || n > 65535 || strconv.Itoa(n) != p {
			return nil, errors.New(what + ": port must be between 1 and 65535")
		}
	} else if strings.HasSuffix(u.Host, ":") {
		return nil, errors.New(what + ": empty port")
	}

	if slices.Contains(own, strings.ToLower(host)) {
		return nil, errors.New(what + ": points to one of the site's own domains")
	}

	return u, nil
}

// Validate checks the redirect of a site with the given domains.
func (r *WebRedirect) Validate(own []string) error {
	if !slices.Contains(RedirectStatuses, r.Status) {
		return fmt.Errorf("redirect status must be one of %v", RedirectStatuses)
	}

	_, err := ParseRedirectTarget(r.Target, own)

	return err
}
