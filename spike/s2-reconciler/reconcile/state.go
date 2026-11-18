// Package reconcile is the S2 spike: desired state in, validated nginx
// "generation" out, atomic switch, health check, rollback. Throwaway code.
package reconcile

import (
	"errors"
	"fmt"
	"path"
	"regexp"
	"strings"

	"github.com/maki-it/hostashi/spike/s1-agent-rpc/agentrpc"
)

type SiteKind string

const (
	KindStatic SiteKind = "static"
	KindProxy  SiteKind = "proxy"
)

// Site is the typed desired state of one site. Every field that ends up in a
// config file is validated positively; Snippet is the one free-text escape
// hatch and is only trusted after the web server accepted it.
type Site struct {
	User      string   `json:"user"`
	Domains   []string `json:"domains"`
	Kind      SiteKind `json:"kind"`
	Root      string   `json:"root,omitempty"`      // static: absolute directory
	ProxyPort int      `json:"proxyPort,omitempty"` // proxy: 127.0.0.1 port
	Snippet   string   `json:"snippet,omitempty"`   // custom directives (own include file)
}

type Desired struct {
	Sites []Site `json:"sites"`
}

const maxSnippet = 8 << 10

var rootRe = regexp.MustCompile(`^/[A-Za-z0-9_./-]+$`)

// Validate checks the desired state. Nothing is written for invalid input.
func (d Desired) Validate() error {
	users := map[string]bool{}
	domains := map[string]bool{}
	for i, s := range d.Sites {
		if err := agentrpc.ValidateUser(s.User); err != nil {
			return fmt.Errorf("site %d: %w", i, err)
		}
		if users[s.User] {
			return fmt.Errorf("site %d: duplicate user %q", i, s.User)
		}
		users[s.User] = true
		if len(s.Domains) == 0 {
			return fmt.Errorf("site %q: no domain", s.User)
		}
		for _, dom := range s.Domains {
			if err := agentrpc.ValidateDomain(dom); err != nil {
				return fmt.Errorf("site %q: %w", s.User, err)
			}
			if domains[dom] {
				return fmt.Errorf("site %q: domain %q used twice", s.User, dom)
			}
			domains[dom] = true
		}
		switch s.Kind {
		case KindStatic:
			if !rootRe.MatchString(s.Root) || path.Clean(s.Root) != s.Root || strings.Contains(s.Root, "..") {
				return fmt.Errorf("site %q: invalid root %q", s.User, s.Root)
			}
			if s.ProxyPort != 0 {
				return fmt.Errorf("site %q: static site with proxy port", s.User)
			}
		case KindProxy:
			if s.ProxyPort < 1024 || s.ProxyPort > 65535 {
				return fmt.Errorf("site %q: invalid proxy port %d", s.User, s.ProxyPort)
			}
			if s.Root != "" {
				return fmt.Errorf("site %q: proxy site with root", s.User)
			}
		default:
			return fmt.Errorf("site %q: unknown kind %q", s.User, s.Kind)
		}
		if len(s.Snippet) > maxSnippet || strings.ContainsRune(s.Snippet, 0) {
			return fmt.Errorf("site %q: invalid snippet", s.User)
		}
	}
	return nil
}

var (
	ErrInvalidDesired = errors.New("invalid desired state")
	ErrValidation     = errors.New("web server rejected the generated config") // nothing was changed
	ErrApplyFailed    = errors.New("apply failed, previous generation restored")
	ErrRollbackFailed = errors.New("apply failed and rollback failed")
	ErrBusy           = errors.New("another reconcile run holds the lock")
)
