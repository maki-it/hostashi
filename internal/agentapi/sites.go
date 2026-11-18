// SPDX-License-Identifier: AGPL-3.0-or-later

package agentapi

import (
	"fmt"
	"regexp"
	"strings"

	"github.com/maki-it/hostashi/internal/agentrpc"
)

// MaxSites bounds the answer of SiteList. A single server hosts tens of sites,
// the limit only keeps one response below agentrpc.MaxFrame.
const MaxSites = 500

var (
	siteUserRe = regexp.MustCompile(`^[a-z][a-z0-9_-]{1,31}$`)
	poolNameRe = regexp.MustCompile(`^[a-z][a-z0-9-]{0,31}$`)
)

// reservedSiteUsers can never become a site user although they match siteUserRe.
var reservedSiteUsers = map[string]bool{
	"root": true, "daemon": true, "bin": true, "sys": true, "sync": true,
	"nobody": true, "www-data": true, "nginx": true, "mysql": true,
	"mariadb": true, "hostashi": true, "sshd": true, "postgres": true,
	"redis": true, "messagebus": true,
}

// DefaultPool is the pool a site lands in when the request names none.
const DefaultPool = "default"

// ValidateSiteUser accepts only names that match a fixed positive rule. The
// name becomes a Linux user, a group and one directory name, so it must never
// contain a path separator, a dot or a leading dash.
func ValidateSiteUser(name string) error {
	if !siteUserRe.MatchString(name) {
		return fmt.Errorf("user: must match %s", siteUserRe)
	}

	if reservedSiteUsers[name] || strings.HasPrefix(name, "systemd-") {
		return fmt.Errorf("user %q is reserved", name)
	}

	return nil
}

// ValidatePoolName accepts the name of a registered storage pool (ADR-0016).
func ValidatePoolName(name string) error {
	if !poolNameRe.MatchString(name) {
		return fmt.Errorf("pool: must match %s", poolNameRe)
	}

	return nil
}

// SiteInfo is what the agent reports about one site. It never contains paths:
// the agent alone knows where a pool lives.
type SiteInfo struct {
	User string `json:"user"`
	UID  uint32 `json:"uid"`
	GID  uint32 `json:"gid"`
	Pool string `json:"pool"`
}

// SiteCreateRequest asks for a new site account. uid, gid and the directory
// are chosen by the agent, never by the caller.
type SiteCreateRequest struct {
	User string `json:"user"`
	Pool string `json:"pool,omitempty"` // empty means DefaultPool
}

// Validate checks the request.
func (r SiteCreateRequest) Validate() error {
	if err := ValidateSiteUser(r.User); err != nil {
		return err
	}

	if r.Pool != "" {
		return ValidatePoolName(r.Pool)
	}

	return nil
}

// SiteCreate creates the Linux user, its group and its empty home directory.
var SiteCreate = agentrpc.Op[SiteCreateRequest, SiteInfo]{Name: "site.create"}

// SiteDeleteRequest names the site to remove.
type SiteDeleteRequest struct {
	User string `json:"user"`
}

// Validate checks the request.
func (r SiteDeleteRequest) Validate() error { return ValidateSiteUser(r.User) }

// SiteDeleteResponse is empty on success.
type SiteDeleteResponse struct{}

// SiteDelete removes the Linux user, its group and the whole home directory.
var SiteDelete = agentrpc.Op[SiteDeleteRequest, SiteDeleteResponse]{Name: "site.delete"}

// SiteListRequest has no parameters.
type SiteListRequest struct{}

// Validate accepts the empty request.
func (SiteListRequest) Validate() error { return nil }

// SiteListResponse lists all sites in the agent registry, sorted by user.
type SiteListResponse struct {
	Sites []SiteInfo `json:"sites"`
}

// SiteList reports the agent registry.
var SiteList = agentrpc.Op[SiteListRequest, SiteListResponse]{Name: "site.list"}
