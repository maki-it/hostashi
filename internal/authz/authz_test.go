// SPDX-License-Identifier: AGPL-3.0-or-later

package authz_test

import (
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
		{"user", "sites:read"},
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

// Every token scope must be an action, or a token with the scope could
// never be used. Every action that is not a scope needs a session.
func TestTableCoversScopes(t *testing.T) {
	actions := map[authz.Action]bool{}
	for _, a := range authz.Actions() {
		actions[a] = true
	}

	for _, s := range auth.Scopes() {
		if !actions[authz.Action(s.Name)] {
			t.Errorf("scope %s has no entry in the authz table", s.Name)
		}

		delete(actions, authz.Action(s.Name))
	}

	for a := range actions {
		if !authz.SessionOnly(a) {
			t.Errorf("action %s is neither a scope nor a session-only action", a)
		}
	}
}

func TestSessionOnlyIsNoScope(t *testing.T) {
	for _, s := range auth.Scopes() {
		if authz.SessionOnly(authz.Action(s.Name)) {
			t.Errorf("scope %s is marked session-only", s.Name)
		}
	}
}
