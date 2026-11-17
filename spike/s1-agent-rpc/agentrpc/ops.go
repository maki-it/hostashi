package agentrpc

import (
	"context"
	"fmt"
	"path/filepath"
)

// --- Operation 1: Ping ---------------------------------------------------

type PingReq struct{}

func (PingReq) Validate() error { return nil }

type PingResp struct {
	UID uint32 `json:"uid"` // what the kernel says about the caller
}

func ping(_ context.Context, c Caller, _ PingReq) (PingResp, error) {
	return PingResp{UID: c.UID}, nil
}

// --- Operation 2: SiteCreate (plan only, no side effects in the spike) ----

type SiteCreateReq struct {
	Domain     string   `json:"domain"`
	User       string   `json:"user"`
	Type       SiteType `json:"type"`
	PHPVersion string   `json:"phpVersion,omitempty"`
}

func (r SiteCreateReq) Validate() error {
	if err := ValidateDomain(r.Domain); err != nil {
		return err
	}
	if err := ValidateUser(r.User); err != nil {
		return err
	}
	if !r.Type.valid() {
		return fmt.Errorf("type %q: must be one of static, php, nodejs, proxy", r.Type)
	}
	switch {
	case r.Type == SitePHP && !phpVersionRe.MatchString(r.PHPVersion):
		return fmt.Errorf("phpVersion %q: required for php sites, form 8.3", r.PHPVersion)
	case r.Type != SitePHP && r.PHPVersion != "":
		return fmt.Errorf("phpVersion: only allowed for php sites")
	}
	return nil
}

type SiteCreateResp struct {
	Home    string `json:"home"`
	WebRoot string `json:"webRoot"`
}

// sitesBase is the only place the agent may create site homes.
const sitesBase = "/home"

func siteCreate(_ context.Context, _ Caller, r SiteCreateReq) (SiteCreateResp, error) {
	home := filepath.Join(sitesBase, r.User)
	// Defense in depth: validation already excludes separators and dots.
	if filepath.Dir(home) != sitesBase {
		return SiteCreateResp{}, fmt.Errorf("home escapes base: %s", home)
	}
	return SiteCreateResp{Home: home, WebRoot: filepath.Join(home, "htdocs", r.Domain)}, nil
}

// --- Operation 3: SiteDelete (plan only) ----------------------------------

type SiteDeleteReq struct {
	User string `json:"user"`
}

func (r SiteDeleteReq) Validate() error { return ValidateUser(r.User) }

type SiteDeleteResp struct {
	Home string `json:"home"`
}

func siteDelete(_ context.Context, _ Caller, r SiteDeleteReq) (SiteDeleteResp, error) {
	return SiteDeleteResp{Home: filepath.Join(sitesBase, r.User)}, nil
}

// RegisterAll wires the spike operations.
func RegisterAll(s *Server) {
	Register(s, "Ping", ping)
	Register(s, "SiteCreate", siteCreate)
	Register(s, "SiteDelete", siteDelete)
}
