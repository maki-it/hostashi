// SPDX-License-Identifier: AGPL-3.0-or-later

// Package authz decides which role may do what (ADR-0035). It holds one
// closed table from action to rule. Every handler of the panel asks this
// package instead of comparing role names itself, so a change of who may
// do what changes this table and nothing else.
//
// An action names one thing a route does. Each action has the API token
// scope a token must carry for it (ADR-0018), or none for the actions that
// need a browser session. Several actions can share a scope: "sites:write"
// covers PHP settings, which a user may change on their own site, and the
// domains, which only managers and admins change.
//
// A site action is checked against one site: admins and managers reach
// every site, a user only the sites granted to them.
package authz

import "sort"

// Role is the role of a panel account, as stored in users.role.
type Role string

// The roles of ADR-0035.
const (
	// RoleAdmin may do everything, including users and server settings.
	RoleAdmin Role = "admin"
	// RoleManager looks after every site, but not the server or the users.
	RoleManager Role = "manager"
	// RoleUser works on the sites granted to them.
	RoleUser Role = "user"
)

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

// Rule says who may do an action.
type Rule struct {
	// Scope is the token scope the action needs; empty means a browser
	// session is required.
	Scope string
	// Roles may do the action.
	Roles []Role
	// Site is true if the action concerns one site; a user then needs a
	// grant for it.
	Site bool
}

var (
	admins   = []Role{RoleAdmin}
	managers = []Role{RoleAdmin, RoleManager}
	everyone = []Role{RoleAdmin, RoleManager, RoleUser}
)

func server(scope string) Rule  { return Rule{Scope: scope, Roles: admins} }
func manage(scope string) Rule  { return Rule{Scope: scope, Roles: managers} }
func all(scope string) Rule     { return Rule{Scope: scope, Roles: everyone} }
func ownSite(scope string) Rule { return Rule{Scope: scope, Roles: everyone, Site: true} }
func mgrSite(scope string) Rule { return Rule{Scope: scope, Roles: managers, Site: true} }
func admSite(scope string) Rule { return Rule{Scope: scope, Roles: admins, Site: true} }

// table is closed: an action that is missing here is denied to everyone.
var table = map[Action]Rule{
	// Accounts and panel settings: admins, browser session only.
	SettingsRead:  {Roles: admins},
	SettingsWrite: {Roles: admins},
	UsersRead:     {Roles: admins},
	UsersWrite:    {Roles: admins},

	// Server-wide: admins only.
	"backups.server:read":  server("backups:read"),
	"backups.server:write": server("backups:write"),
	"db_servers:write":     server("db_servers:write"),
	"doctor:read":          server("doctor:read"),
	"audit:read":           server("audit:read"),
	"firewall:read":        server("firewall:read"),
	"firewall:write":       server("firewall:write"),
	"limits.presets:write": server("limits:write"),
	"metrics:read":         server("metrics:read"),
	"migrations:read":      server("migrations:read"),
	"migrations:write":     server("migrations:write"),
	"panel.tls:read":       server("sites:read"),
	"panel.tls:write":      server("sites:write"),
	"php:install":          server("php:install"),
	"php:remove":           server("php:install"),
	"php.install:read":     server("sites:read"),
	"node:install":         server("node:install"),
	"node:remove":          server("node:install"),
	"services:read":        server("services:read"),
	"services:write":       server("services:write"),
	"system:read":          server("system:read"),
	"system:write":         server("system:write"),
	"web:sync":             server("sites:write"),

	// Read-only things everyone needs. The lists of sites and databases
	// are filtered to the sites a user may see.
	"sites:list":        all("sites:read"),
	"databases:list":    all("databases:read"),
	"db_servers:list":   all("databases:read"),
	"php.versions:read": all("sites:read"),
	// The Node.js lines, for choosing a site's version (ADR-0054).
	"node:read":         all("sites:read"),
	"ssh.hostkeys:read": all("ssh:read"),
	"pages:read":        all("pages:read"),

	// What spans all sites or claims names: managers (ADR-0035, question 3).
	"sites:create":        manage("sites:write"),
	"limits.presets:read": manage("limits:read"),
	"pages:write":         manage("pages:write"),
	"parked:read":         manage("sites:read"),
	// The pool choice of "New site" (ADR-0016).
	"pools:read":   manage("sites:read"),
	"parked:write": manage("sites.maintenance:write"),
	"sites:delete": mgrSite("sites:delete"),
	// Delete a site and park domains of it (ADR-0048); the handler also
	// asks for parked:write.
	"sites:park":           mgrSite("sites:delete"),
	"sites.domains:write":  mgrSite("sites:write"),
	"sites.tls:write":      mgrSite("sites:write"),
	"sites.disabled:write": mgrSite("sites:write"),
	// A type change can create a Linux account, as a new site does
	// (ADR-0054 addendum 3).
	"sites.type:write": mgrSite("sites:write"),
	// Removing the account of a redirect or proxy site removes its files
	// and databases, like a delete.
	"sites.account:delete": mgrSite("sites:delete"),
	"limits:write":         mgrSite("limits:write"),
	// The page cache size counts against the site's disk limit (ADR-0046).
	"sites.pagecache.size:write": mgrSite("sites:write"),
	// The cache's memory counts against the site's limit (ADR-0045).
	"sites.objectcache.size:write": mgrSite("objectcache:write"),
	// Letting PHP start programs undoes a hardening default (ADR-0033).
	"sites.programs:write": mgrSite("sites:write"),

	// nginx runs for every site, so snippets stay with admins (ADR-0029).
	"sites.snippets:write": admSite("sites.snippets:write"),
	// Own proxy ranges also go into fail2ban's server-wide never-ban list
	// (ADR-0059); the real IP handler asks for this when a change adds one.
	"sites.realip.ranges:write": admSite("sites:write"),

	// The day-to-day work on one's own site: everyone with a grant. All of
	// it runs as the site's own Linux user inside its isolation.
	"sites:read":           ownSite("sites:read"),
	"sites.php:write":      ownSite("sites:write"),
	"sites.hsts:write":     ownSite("sites:write"),
	"sites.headers:write":  ownSite("sites:write"),
	"sites.realip:write":   ownSite("sites:write"),
	"sites.web:write":      ownSite("sites:write"),
	"sites.redirect:write": ownSite("sites:write"),
	"sites.proxy:write":    ownSite("sites:write"),
	// The app runs as the site user inside its isolation (ADR-0054).
	"sites.app:write": ownSite("sites:write"),
	// The values of the app's environment variables: whoever may change
	// the app can read them from it anyway.
	"sites.app.env:read":      ownSite("sites:write"),
	"sites.maintenance:write": ownSite("sites.maintenance:write"),
	"sites.pagecache:write":   ownSite("sites:write"),
	"sites.pagecache:purge":   ownSite("pagecache:purge"),
	"limits:read":             ownSite("limits:read"),
	"sites.objectcache:read":  ownSite("objectcache:read"),
	"sites.objectcache:write": ownSite("objectcache:write"),
	"cron:read":               ownSite("cron:read"),
	"cron:write":              ownSite("cron:write"),
	"ssh:read":                ownSite("ssh:read"),
	"ssh:write":               ownSite("ssh:write"),
	"databases:read":          ownSite("databases:read"),
	"databases:write":         ownSite("databases:write"),
	"databases:delete":        ownSite("databases:delete"),
	// The web database tool (ADR-0038) gives what a new database user
	// with a known password gives, so it shares databases:write.
	"databases:open":  ownSite("databases:write"),
	"backups:read":    ownSite("backups:read"),
	"backups:restore": ownSite("backups:restore"),
	// The file manager writes as the site user (ADR-0040).
	"files:read":  ownSite("files:read"),
	"files:write": ownSite("files:write"),
	// The site's own logs (ADR-0053); they hold its visitors' addresses.
	"sites.logs:read": ownSite("logs:read"),
}

// Lookup returns the rule of an action.
func Lookup(a Action) (Rule, bool) {
	r, ok := table[a]

	return r, ok
}

// Allowed reports whether role may do action at all. For a site action the
// caller must also check the site. An unknown role or action is never
// allowed.
func Allowed(role Role, action Action) bool {
	r, ok := table[action]
	if !ok {
		return false
	}

	for _, x := range r.Roles {
		if x == role {
			return true
		}
	}

	return false
}

// SeesAllSites reports whether the role reaches every site without grants.
func SeesAllSites(role Role) bool {
	return role == RoleAdmin || role == RoleManager
}

// NeedsSecondFactor reports whether every account of the role must have a
// second factor (ADR-0035, question 2). An admin can require one of a user
// as well; that is a flag of the account, not of the role.
func NeedsSecondFactor(role Role) bool {
	return role == RoleAdmin || role == RoleManager
}

// ForSiteToken reports whether a token limited to some sites may do the
// action: site actions (for its sites) and the read-only actions everyone
// has, whose lists are filtered to its sites. Server and manager actions
// that span all sites stay with tokens without a site list.
func ForSiteToken(a Action) bool {
	r, ok := table[a]
	if !ok {
		return false
	}

	return r.Site || len(r.Roles) == len(everyone)
}

// ScopeOf returns the token scope of an action; empty for session-only and
// unknown actions.
func ScopeOf(a Action) string {
	return table[a].Scope
}

// SessionOnly reports whether an action has no token scope and needs a
// browser session.
func SessionOnly(a Action) bool {
	r, ok := table[a]

	return ok && r.Scope == ""
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

// RoleScopes returns the token scopes useful to a role: those of at least
// one action the role may do.
func RoleScopes(role Role) []string {
	seen := map[string]bool{}

	for a, r := range table {
		if r.Scope != "" && Allowed(role, a) {
			seen[r.Scope] = true
		}
	}

	out := make([]string, 0, len(seen))
	for s := range seen {
		out = append(out, s)
	}

	sort.Strings(out)

	return out
}
