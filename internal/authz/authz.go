// SPDX-License-Identifier: AGPL-3.0-or-later

// Package authz decides which role may do what (ADR-0035). It holds one
// closed table from action to roles. Every handler of the panel asks Allowed
// instead of comparing role names itself, so a new role changes this table
// and nothing else.
//
// The action names are the API token scopes (ADR-0018), plus a few actions
// that no token can carry, such as changing panel settings. A request needs
// both: the role must allow the action here, and a token must also carry the
// scope.
package authz

import "sort"

// Role is the role of a panel account, as stored in users.role.
type Role string

// RoleAdmin may do everything. It is the only role so far.
const RoleAdmin Role = "admin"

// Action is something a request does, named "resource:action".
type Action string

// Actions without a token scope: they need a browser session.
const (
	// SettingsRead reads panel settings such as the API IP allowlist.
	SettingsRead Action = "settings:read"
	// SettingsWrite changes panel settings such as the API IP allowlist and
	// the metrics endpoint.
	SettingsWrite Action = "settings:write"
	// UsersRead lists the panel accounts and their site grants.
	UsersRead Action = "users:read"
	// UsersWrite creates, changes, resets and deletes accounts and grants.
	// No token can carry it: a leaked token must not create admins.
	UsersWrite Action = "users:write"
)

// SessionOnly reports whether an action has no token scope and needs a
// browser session.
func SessionOnly(a Action) bool {
	switch a {
	case SettingsRead, SettingsWrite, UsersRead, UsersWrite:
		return true
	}

	return false
}

// admins is the role list of an action only administrators may do.
var admins = []Role{RoleAdmin}

// table is closed: an action that is missing here is denied to everyone.
// A feature that adds a scope adds its action here in the same change; a
// test compares the table with the scope catalog.
var table = map[Action][]Role{
	"backups:read":            admins,
	"backups:restore":         admins,
	"backups:write":           admins,
	"cron:read":               admins,
	"cron:write":              admins,
	"databases:delete":        admins,
	"databases:read":          admins,
	"databases:write":         admins,
	"doctor:read":             admins,
	"firewall:read":           admins,
	"firewall:write":          admins,
	"limits:read":             admins,
	"limits:write":            admins,
	"metrics:read":            admins,
	"migrations:read":         admins,
	"migrations:write":        admins,
	"pages:read":              admins,
	"pages:write":             admins,
	"php:install":             admins,
	"sites:delete":            admins,
	"sites:read":              admins,
	"sites:write":             admins,
	"sites.maintenance:write": admins,
	"sites.snippets:write":    admins,
	"ssh:read":                admins,
	"ssh:write":               admins,
	SettingsRead:              admins,
	SettingsWrite:             admins,
	UsersRead:                 admins,
	UsersWrite:                admins,
}

// Allowed reports whether role may do action. An unknown role or action is
// never allowed.
func Allowed(role Role, action Action) bool {
	for _, r := range table[action] {
		if r == role {
			return true
		}
	}

	return false
}

// Actions returns every action of the table, sorted.
func Actions() []Action {
	out := make([]Action, 0, len(table))
	for a := range table {
		out = append(out, a)
	}

	sort.Slice(out, func(i, j int) bool { return out[i] < out[j] })

	return out
}
