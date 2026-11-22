// SPDX-License-Identifier: AGPL-3.0-or-later

package authz_test

import (
	"slices"
	"testing"

	"github.com/maki-it/hostashi/internal/auth"
	"github.com/maki-it/hostashi/internal/authz"
)

func TestAdminMayDoEverything(t *testing.T) {
	for _, a := range authz.Actions() {
		if !authz.Allowed(authz.RoleAdmin, a) {
			t.Errorf("admin may not %s", a)
		}
	}
}

func TestUnknownIsDenied(t *testing.T) {
	cases := []struct {
		role   authz.Role
		action authz.Action
	}{
		{"", "sites:read"},
		{"Admin", "sites:read"},
		{"root", "sites:read"},
		{authz.RoleAdmin, ""},
		{authz.RoleAdmin, "sites:*"},
		{authz.RoleAdmin, "users:delete"},
	}

	for _, c := range cases {
		if authz.Allowed(c.role, c.action) {
			t.Errorf("Allowed(%q, %q) = true", c.role, c.action)
		}
	}
}

// The decisions of ADR-0035, spelled out so a change of the table shows up
// here as well.
func TestDecidedRules(t *testing.T) {
	never := map[authz.Role][]authz.Action{
		authz.RoleManager: {
			authz.UsersRead, authz.UsersWrite, authz.SettingsWrite, "firewall:write", "backups.server:write",
			"migrations:write", "doctor:read", "sites.snippets:write", "limits.presets:write", "web:sync",
		},
		authz.RoleUser: {
			authz.UsersRead, authz.SettingsRead, "sites:create", "sites:delete", "sites.domains:write",
			"sites.tls:write", "sites.disabled:write", "sites.programs:write", "limits:write",
			"sites.snippets:write", "parked:write", "pages:write", "backups.server:read", "firewall:read",
			"sites.pagecache.size:write", "sites.type:write",
		},
	}

	for role, actions := range never {
		for _, a := range actions {
			if authz.Allowed(role, a) {
				t.Errorf("%s may %s", role, a)
			}
		}
	}

	userMay := []authz.Action{
		"sites:list", "sites:read", "sites.php:write", "sites.maintenance:write", "limits:read",
		"cron:write", "ssh:write", "databases:write", "databases:delete", "backups:read", "backups:restore",
		"sites.pagecache:write", "sites.pagecache:purge", "sites.web:write", "sites.redirect:write", "sites.proxy:write", "sites.app:write", "sites.headers:write",
	}
	for _, a := range userMay {
		if !authz.Allowed(authz.RoleUser, a) {
			t.Errorf("user may not %s", a)
		}
	}

	for _, a := range []authz.Action{"sites:create", "sites.domains:write", "limits:write", "sites:delete", "sites.pagecache.size:write", "sites.type:write"} {
		if !authz.Allowed(authz.RoleManager, a) {
			t.Errorf("manager may not %s", a)
		}
	}
}

// A user's actions on one site must be site actions, or a grant would not
// limit them.
func TestUserActionsAreSiteBound(t *testing.T) {
	unbound := []authz.Action{"sites:list", "databases:list", "db_servers:list", "php.versions:read", "node:read", "ssh.hostkeys:read", "pages:read"}

	for _, a := range authz.Actions() {
		r, _ := authz.Lookup(a)
		if authz.Allowed(authz.RoleUser, a) && !r.Site && !slices.Contains(unbound, a) {
			t.Errorf("user action %s is not bound to a site", a)
		}
	}
}

// Every token scope is needed by an action, and every action names a scope
// of the catalog or none.
func TestTableMatchesScopes(t *testing.T) {
	scopes := map[string]bool{}
	for _, s := range auth.Scopes() {
		scopes[s.Name] = false
	}

	for _, a := range authz.Actions() {
		s := authz.ScopeOf(a)
		if s == "" {
			if !authz.SessionOnly(a) {
				t.Errorf("action %s without scope is not session-only", a)
			}

			continue
		}

		if _, ok := scopes[s]; !ok {
			t.Errorf("action %s names unknown scope %s", a, s)
		}

		scopes[s] = true
	}

	for s, used := range scopes {
		if !used {
			t.Errorf("scope %s has no action", s)
		}
	}
}

func TestRoleScopes(t *testing.T) {
	user := authz.RoleScopes(authz.RoleUser)
	for _, s := range []string{"firewall:write", "migrations:write", "sites.snippets:write", "doctor:read", "metrics:read"} {
		if slices.Contains(user, s) {
			t.Errorf("user offered %s", s)
		}
	}

	if !slices.Contains(user, "cron:write") {
		t.Error("user not offered cron:write")
	}

	if got := authz.RoleScopes(authz.RoleAdmin); len(got) != len(auth.Scopes()) {
		t.Errorf("admin offered %d of %d scopes", len(got), len(auth.Scopes()))
	}
}

func TestForSiteToken(t *testing.T) {
	for a, want := range map[authz.Action]bool{
		"sites:read":      true,  // a site action
		"cron:write":      true,  // a site action
		"sites:list":      true,  // filtered list
		"databases:list":  true,  // filtered list
		"sites:create":    false, // claims a name for all sites
		"firewall:read":   false, // server
		"parked:write":    false, // spans all sites
		authz.UsersWrite:  false, // session only, no scope
		"unknown:action":  false,
		"sites.tls:write": true, // a site action of managers
	} {
		if got := authz.ForSiteToken(a); got != want {
			t.Errorf("%s: %v, want %v", a, got, want)
		}
	}
}
