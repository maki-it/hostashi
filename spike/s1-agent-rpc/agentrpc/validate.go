package agentrpc

import (
	"fmt"
	"regexp"
	"strings"
)

var (
	userRe       = regexp.MustCompile(`^[a-z][a-z0-9_-]{1,31}$`)
	phpVersionRe = regexp.MustCompile(`^[5-9]\.[0-9]{1,2}$`)
	labelRe      = regexp.MustCompile(`^[a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?$`)
)

// reservedUsers can never be used as a site user, even though they match userRe.
var reservedUsers = map[string]bool{
	"root": true, "daemon": true, "bin": true, "sys": true, "sync": true,
	"nobody": true, "www-data": true, "nginx": true, "mysql": true,
	"mariadb": true, "hostashi": true, "sshd": true, "postgres": true,
	"redis": true, "systemd-network": true, "messagebus": true,
}

// ValidateUser accepts only names matching a fixed positive rule.
func ValidateUser(u string) error {
	if !userRe.MatchString(u) {
		return fmt.Errorf("user %q: must match %s", u, userRe)
	}
	if reservedUsers[u] || strings.HasPrefix(u, "systemd-") {
		return fmt.Errorf("user %q is reserved", u)
	}
	return nil
}

// ValidateDomain accepts lower-case ASCII (punycode) host names with at least
// two labels. No wildcards, no ports, no trailing dot, no IP literals.
func ValidateDomain(d string) error {
	if len(d) == 0 || len(d) > 253 {
		return fmt.Errorf("domain: length must be 1..253")
	}
	labels := strings.Split(d, ".")
	if len(labels) < 2 {
		return fmt.Errorf("domain %q: needs at least two labels", d)
	}
	for _, l := range labels {
		if !labelRe.MatchString(l) {
			return fmt.Errorf("domain %q: invalid label %q", d, l)
		}
	}
	// A numeric last label would make "1.2.3.4" a valid domain.
	if last := labels[len(labels)-1]; strings.Trim(last, "0123456789") == "" {
		return fmt.Errorf("domain %q: top-level label must not be numeric", d)
	}
	return nil
}

// SiteType is a closed set.
type SiteType string

const (
	SiteStatic SiteType = "static"
	SitePHP    SiteType = "php"
	SiteNode   SiteType = "nodejs"
	SiteProxy  SiteType = "proxy"
)

func (t SiteType) valid() bool {
	switch t {
	case SiteStatic, SitePHP, SiteNode, SiteProxy:
		return true
	}
	return false
}
