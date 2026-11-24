// SPDX-License-Identifier: AGPL-3.0-or-later

package serve

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"testing"

	"github.com/maki-it/hostashi/internal/auth"
	"github.com/maki-it/hostashi/internal/authz"
)

// Access kinds of a route that is not guarded by an authz action.
const (
	// accessPublic needs no login (setup, login, health).
	accessPublic authz.Action = "public"
	// accessSession is the caller's own account; only a browser session.
	accessSession authz.Action = "session"
	// accessAny is the caller's own account; a session or any token.
	accessAny authz.Action = "any"
)

// routeAccess is the route matrix of ADR-0035: every route of the panel and
// what guards it. A new route without an entry fails TestRouteMatrixComplete.
// Each entry is checked against the running handler in TestRouteMatrix.
var routeAccess = map[string]authz.Action{
	"GET /api/v1/health":                     accessPublic,
	"GET /api/v1/setup":                      accessPublic,
	"POST /api/v1/setup":                     accessPublic,
	"POST /api/v1/auth/login":                accessPublic,
	"POST /api/v1/auth/device":               accessPublic,
	"GET /api/v1/auth/device/{userCode}":     accessSession,
	"POST /api/v1/auth/device/approve":       accessSession,
	"POST /api/v1/auth/device/token":         accessPublic,
	"POST /api/v1/auth/logout":               accessPublic,
	"POST /api/v1/auth/passkey/login/begin":  accessPublic,
	"POST /api/v1/auth/passkey/login/finish": accessPublic,

	"GET /api/v1/auth/me":      accessAny,
	"GET /api/v1/openapi.json": accessAny,
	"GET /api/v1/scopes":       accessAny,

	"POST /api/v1/auth/totp/enroll":              accessSession,
	"POST /api/v1/auth/totp/confirm":             accessSession,
	"POST /api/v1/auth/totp/disable":             accessSession,
	"POST /api/v1/auth/password":                 accessSession,
	"POST /api/v1/tokens":                        accessSession,
	"GET /api/v1/tokens":                         accessSession,
	"DELETE /api/v1/tokens/{id}":                 accessSession,
	"POST /api/v1/auth/passkeys/register/begin":  accessSession,
	"POST /api/v1/auth/passkeys/register/finish": accessSession,
	"GET /api/v1/auth/passkeys":                  accessSession,
	"DELETE /api/v1/auth/passkeys/{id}":          accessSession,
	"PATCH /api/v1/me":                           accessSession,
	"GET /api/v1/account/email":                  accessSession,
	"PUT /api/v1/account/email":                  accessSession,
	"DELETE /api/v1/account/email":               accessSession,
	"GET /api/v1/account/notifications":          accessSession,
	"PUT /api/v1/account/notifications":          accessSession,

	"POST /api/v1/invitations/check":     accessPublic,
	"POST /api/v1/invitations/accept":    accessPublic,
	"POST /api/v1/account/email/confirm": accessPublic,

	"GET /api/v1/users":             authz.UsersRead,
	"POST /api/v1/users":            authz.UsersWrite,
	"GET /api/v1/users/{id}":        authz.UsersRead,
	"PUT /api/v1/users/{id}":        authz.UsersWrite,
	"DELETE /api/v1/users/{id}":     authz.UsersWrite,
	"POST /api/v1/users/{id}/reset": authz.UsersWrite,
	"GET /api/v1/users/{id}/grants": authz.UsersRead,
	"PUT /api/v1/users/{id}/grants": authz.UsersWrite,

	"GET /api/v1/settings/api-ip-allowlist": authz.SettingsRead,
	"PUT /api/v1/settings/api-ip-allowlist": authz.SettingsWrite,
	"GET /api/v1/settings/metrics":          authz.SettingsRead,
	"PUT /api/v1/settings/metrics":          authz.SettingsWrite,
	"GET /api/v1/settings/mail":             authz.SettingsRead,
	"PUT /api/v1/settings/mail":             authz.SettingsWrite,
	"DELETE /api/v1/settings/mail":          authz.SettingsWrite,
	"POST /api/v1/settings/mail/test":       authz.SettingsWrite,
	"GET /api/v1/settings/logs":             authz.SettingsRead,
	"PUT /api/v1/settings/logs":             authz.SettingsWrite,
	"GET /api/v1/settings/panel-real-ip":    authz.SettingsRead,
	"PUT /api/v1/settings/panel-real-ip":    authz.SettingsWrite,

	"GET /api/v1/sites":        "sites:list",
	"GET /api/v1/sites/{name}": "sites:read",

	"GET /api/v1/sites/{name}/files":                "files:read",
	"GET /api/v1/sites/{name}/files/stat":           "files:read",
	"GET /api/v1/sites/{name}/files/content":        "files:read",
	"PUT /api/v1/sites/{name}/files/content":        "files:write",
	"POST /api/v1/sites/{name}/files/mkdir":         "files:write",
	"POST /api/v1/sites/{name}/files/touch":         "files:write",
	"POST /api/v1/sites/{name}/files/rename":        "files:write",
	"POST /api/v1/sites/{name}/files/move":          "files:write",
	"POST /api/v1/sites/{name}/files/copy":          "files:write",
	"POST /api/v1/sites/{name}/files/delete":        "files:write",
	"POST /api/v1/sites/{name}/files/chmod":         "files:write",
	"POST /api/v1/sites/{name}/files/upload-start":  "files:write",
	"POST /api/v1/sites/{name}/files/upload-cancel": "files:write",
	"POST /api/v1/sites/{name}/files/extract":       "files:write",
	"GET /api/v1/sites/{name}/files/zip":            "files:read",
	"GET /api/v1/sites/{name}/logs/{kind}":          "sites.logs:read",
	"PUT /api/v1/sites/{name}/files/upload":         "files:write",

	"POST /api/v1/sites":                                     "sites:create",
	"PUT /api/v1/sites/{name}/domains":                       "sites.domains:write",
	"PUT /api/v1/sites/{name}/php":                           "sites.php:write",
	"GET /api/v1/sites/{name}/php/settings":                  "sites:read",
	"PUT /api/v1/sites/{name}/php/settings":                  "sites.php:write",
	"PUT /api/v1/sites/{name}/tls":                           "sites.tls:write",
	"GET /api/v1/sites/{name}/tls/certificate":               "sites:read",
	"PUT /api/v1/sites/{name}/tls/certificate":               "sites.tls:write",
	"DELETE /api/v1/sites/{name}/tls/certificate":            "sites.tls:write",
	"POST /api/v1/sites/{name}/tls/csr":                      "sites.tls:write",
	"PUT /api/v1/sites/{name}/tls/source":                    "sites.tls:write",
	"POST /api/v1/sites/{name}/tls/renew":                    "sites.tls:write",
	"PUT /api/v1/sites/{name}/hsts":                          "sites.hsts:write",
	"GET /api/v1/sites/{name}/headers":                       "sites:read",
	"PUT /api/v1/sites/{name}/headers":                       "sites.headers:write",
	"GET /api/v1/sites/{name}/real-ip":                       "sites:read",
	"PUT /api/v1/sites/{name}/real-ip":                       "sites.realip:write",
	"PUT /api/v1/sites/{name}/web":                           "sites.web:write",
	"PUT /api/v1/sites/{name}/redirect":                      "sites.redirect:write",
	"PUT /api/v1/sites/{name}/proxy":                         "sites.proxy:write",
	"POST /api/v1/sites/{name}/type":                         "sites.type:write",
	"DELETE /api/v1/sites/{name}/account":                    "sites.account:delete",
	"PUT /api/v1/sites/{name}/node":                          "sites.app:write",
	"PUT /api/v1/sites/{name}/app":                           "sites.app:write",
	"PUT /api/v1/sites/{name}/python":                        "sites.app:write",
	"POST /api/v1/sites/{name}/python/venv":                  "sites.app:write",
	"GET /api/v1/sites/{name}/app":                           "sites:read",
	"POST /api/v1/sites/{name}/app/start":                    "sites.app:write",
	"POST /api/v1/sites/{name}/app/stop":                     "sites.app:write",
	"POST /api/v1/sites/{name}/app/restart":                  "sites.app:write",
	"GET /api/v1/sites/{name}/app/env":                       "sites:read",
	"GET /api/v1/sites/{name}/app/env/values":                "sites.app.env:read",
	"PUT /api/v1/sites/{name}/app/env":                       "sites.app:write",
	"PUT /api/v1/sites/{name}/php-programs":                  "sites.programs:write",
	"PUT /api/v1/sites/{name}/disabled":                      "sites.disabled:write",
	"GET /api/v1/php/versions":                               "php.versions:read",
	"GET /api/v1/php/install":                                "php.install:read",
	"POST /api/v1/php/install":                               "php:install",
	"POST /api/v1/php/remove":                                "php:remove",
	"GET /api/v1/node":                                       "node:read",
	"POST /api/v1/node/install":                              "node:install",
	"POST /api/v1/node/remove":                               "node:remove",
	"GET /api/v1/python":                                     "python:read",
	"POST /api/v1/python/install":                            "python:install",
	"POST /api/v1/python/remove":                             "python:remove",
	"DELETE /api/v1/sites/{name}":                            "sites:delete",
	"POST /api/v1/sites/{name}/park":                         "sites:park",
	"GET /api/v1/panel/tls":                                  "panel.tls:read",
	"GET /api/v1/panel/tls/certificate":                      "panel.tls:read",
	"PUT /api/v1/panel/tls/certificate":                      "panel.tls:write",
	"POST /api/v1/panel/tls/csr":                             "panel.tls:write",
	"PUT /api/v1/panel/tls/source":                           "panel.tls:write",
	"POST /api/v1/panel/tls/renew":                           "panel.tls:write",
	"GET /api/v1/parked-domains/{domain}/tls/certificate":    "parked:read",
	"PUT /api/v1/parked-domains/{domain}/tls/certificate":    "parked:write",
	"DELETE /api/v1/parked-domains/{domain}/tls/certificate": "parked:write",
	"POST /api/v1/parked-domains/{domain}/tls/csr":           "parked:write",
	"PUT /api/v1/parked-domains/{domain}/tls/source":         "parked:write",
	"POST /api/v1/parked-domains/{domain}/tls/renew":         "parked:write",
	"POST /api/v1/web/sync":                                  "web:sync",

	"GET /api/v1/databases":                                 "databases:list",
	"GET /api/v1/sites/{name}/databases":                    "databases:read",
	"GET /api/v1/databases/{db}":                            "databases:read",
	"POST /api/v1/sites/{name}/databases":                   "databases:write",
	"DELETE /api/v1/databases/{db}":                         "databases:delete",
	"POST /api/v1/databases/{db}/users":                     "databases:write",
	"PUT /api/v1/databases/{db}/users/{user}/password":      "databases:write",
	"DELETE /api/v1/databases/{db}/users/{user}":            "databases:write",
	"POST /api/v1/databases/{db}/import":                    "databases:write",
	"POST /api/v1/databases/{db}/export":                    "databases:write",
	"GET /api/v1/databases/{db}/jobs/{id}":                  "databases:read",
	"GET /api/v1/dbtool":                                    "databases:list",
	"POST /api/v1/databases/{db}/tool":                      "databases:open",
	"GET /api/v1/databases/{db}/tool/sessions":              "databases:open",
	"POST /api/v1/databases/{db}/tool/sessions/{id}/extend": "databases:open",
	"DELETE /api/v1/databases/{db}/tool/sessions/{id}":      "databases:open",
	"GET /api/v1/db-servers":                                "db_servers:list",
	"POST /api/v1/db-servers":                               "db_servers:write",
	"POST /api/v1/db-servers/{server}/check":                "db_servers:write",
	"PUT /api/v1/db-servers/{server}/default":               "db_servers:write",
	"DELETE /api/v1/db-servers/{server}":                    "db_servers:write",
	"GET /api/v1/db-servers/postgresql-client":              "db_servers:write",
	"POST /api/v1/db-servers/postgresql-client":             "db_servers:write",

	"GET /api/v1/status-pages":                "pages:read",
	"GET /api/v1/status-pages/{id}":           "pages:read",
	"POST /api/v1/status-pages":               "pages:write",
	"PUT /api/v1/status-pages/{id}":           "pages:write",
	"DELETE /api/v1/status-pages/{id}":        "pages:write",
	"POST /api/v1/status-pages/{id}/preview":  "pages:read",
	"GET /api/page-preview/{token}/{path...}": accessPublic,
	"PUT /api/v1/sites/{name}/maintenance":    "sites.maintenance:write",
	"GET /api/v1/parked-domains":              "parked:read",
	"GET /api/v1/parked-domains/{domain}":     "parked:read",
	"PUT /api/v1/parked-domains/{domain}":     "parked:write",
	"DELETE /api/v1/parked-domains/{domain}":  "parked:write",

	"GET /api/v1/sites/{name}/object-cache":           "sites.objectcache:read",
	"PUT /api/v1/sites/{name}/object-cache":           "sites.objectcache:write",
	"PUT /api/v1/sites/{name}/object-cache/size":      "sites.objectcache.size:write",
	"POST /api/v1/sites/{name}/object-cache/flush":    "sites.objectcache:write",
	"GET /api/v1/sites/{name}/cron":                   "cron:read",
	"GET /api/v1/sites/{name}/cron/{id}":              "cron:read",
	"POST /api/v1/sites/{name}/cron":                  "cron:write",
	"PUT /api/v1/sites/{name}/cron/{id}":              "cron:write",
	"DELETE /api/v1/sites/{name}/cron/{id}":           "cron:write",
	"POST /api/v1/sites/{name}/cron/{id}/run":         "cron:write",
	"GET /api/v1/sites/{name}/git":                    "sites:read",
	"PUT /api/v1/sites/{name}/git":                    "sites.git:write",
	"DELETE /api/v1/sites/{name}/git":                 "sites.git:write",
	"POST /api/v1/sites/{name}/git/key":               "sites.git:write",
	"POST /api/v1/sites/{name}/git/hostkey/scan":      "sites.git:write",
	"PUT /api/v1/sites/{name}/git/hostkey":            "sites.git:write",
	"GET /api/v1/sites/{name}/deploys":                "sites:read",
	"POST /api/v1/sites/{name}/deploys":               "sites.deploy",
	"POST /api/v1/sites/{name}/deploys/{id}/rollback": "sites.deploy",
	"GET /api/v1/sites/{name}/deploys/{id}/log":       "sites.deploy",

	"GET /api/v1/ssh/host-keys":                 "ssh.hostkeys:read",
	"GET /api/v1/sites/{name}/ssh-keys":         "ssh:read",
	"GET /api/v1/sites/{name}/ssh-keys/{id}":    "ssh:read",
	"POST /api/v1/sites/{name}/ssh-keys":        "ssh:write",
	"PUT /api/v1/sites/{name}/ssh-keys/{id}":    "ssh:write",
	"DELETE /api/v1/sites/{name}/ssh-keys/{id}": "ssh:write",

	"GET /api/v1/sites/{name}/snippets": "sites:read",
	"PUT /api/v1/sites/{name}/snippets": "sites.snippets:write",

	"GET /api/v1/sites/{name}/page-cache":        "sites:read",
	"PUT /api/v1/sites/{name}/page-cache":        "sites.pagecache:write",
	"PUT /api/v1/sites/{name}/page-cache/size":   "sites.pagecache.size:write",
	"POST /api/v1/sites/{name}/page-cache/purge": "sites.pagecache:purge",

	"GET /api/v1/limit-presets":           "limits.presets:read",
	"GET /api/v1/limit-presets/{name}":    "limits.presets:read",
	"POST /api/v1/limit-presets":          "limits.presets:write",
	"PUT /api/v1/limit-presets/{name}":    "limits.presets:write",
	"DELETE /api/v1/limit-presets/{name}": "limits.presets:write",
	"GET /api/v1/sites/{name}/limits":     "limits:read",
	"PUT /api/v1/sites/{name}/limits":     "limits:write",

	"GET /api/v1/backups":                                  "backups.server:read",
	"PUT /api/v1/backups/settings":                         "backups.server:write",
	"POST /api/v1/backups/hostkey/scan":                    "backups.server:write",
	"PUT /api/v1/backups/hostkey":                          "backups.server:write",
	"POST /api/v1/backups/run":                             "backups.server:write",
	"GET /api/v1/sites/{name}/backups":                     "backups:read",
	"GET /api/v1/backups/panel":                            "backups.server:read",
	"POST /api/v1/sites/{name}/backups/{snapshot}/restore": "backups:restore",
	"POST /api/v1/backups/restores/{id}/import":            "backups:restore",
	"GET /api/v1/backups/imports/{id}":                     "backups:read",
	"GET /api/v1/backups/restores/{id}":                    "backups:read",

	"POST /api/v1/migrations/cloudpanel/reports":                                 "migrations:write",
	"GET /api/v1/migrations/cloudpanel/reports":                                  "migrations:read",
	"GET /api/v1/migrations/cloudpanel/reports/{id}":                             "migrations:read",
	"POST /api/v1/migrations/cloudpanel/reports/{id}/sites/{name}":               "migrations:write",
	"POST /api/v1/migrations/cloudpanel/reports/{id}/sites/{name}/import":        "migrations:write",
	"POST /api/v1/migrations/cloudpanel/sites/{name}/activate":                   "migrations:write",
	"GET /api/v1/migrations/cloudpanel/imports":                                  "migrations:read",
	"GET /api/v1/migrations/cloudpanel/imports/{id}":                             "migrations:read",
	"POST /api/v1/migrations/cloudpanel/imports/{id}/load":                       "migrations:write",
	"GET /api/v1/migrations/cloudpanel/imports/{id}/loads":                       "migrations:read",
	"POST /api/v1/migrations/cloudpanel/sites/{name}/cutover":                    "migrations:write",
	"GET /api/v1/migrations/cloudpanel/sites/{name}/cutover":                     "migrations:read",
	"DELETE /api/v1/migrations/cloudpanel/sites/{name}/cutover":                  "migrations:write",
	"DELETE /api/v1/migrations/cloudpanel/reports/{id}":                          "migrations:write",
	"GET /api/v1/migrations/cloudpanel/reports/{id}/sites/{name}/snippets-draft": "migrations:read",
	"GET /api/v1/migrations/cloudpanel/source":                                   "migrations:read",
	"PUT /api/v1/migrations/cloudpanel/source":                                   "migrations:write",
	"POST /api/v1/migrations/cloudpanel/source/hostkey/scan":                     "migrations:write",
	"PUT /api/v1/migrations/cloudpanel/source/hostkey":                           "migrations:write",
	"DELETE /api/v1/migrations/cloudpanel/source":                                "migrations:write",
	"POST /api/v1/migrations/cloudpanel/source/database":                         "migrations:write",

	"GET /api/v1/firewall":              "firewall:read",
	"PUT /api/v1/firewall":              "firewall:write",
	"POST /api/v1/firewall/confirm":     "firewall:write",
	"GET /api/v1/doctor":                "doctor:read",
	"GET /api/v1/pools":                 "pools:read",
	"POST /api/v1/pools/{name}/rename":  "pools:write",
	"POST /api/v1/pools/{name}/default": "pools:write",
	"GET /api/v1/audit":                 "audit:read",
	"GET /api/v1/audit/actions":         "audit:read",
	"GET /api/v1/audit/export":          "audit:read",

	"GET /api/v1/services":                             "services:read",
	"GET /api/v1/sites/{name}/services":                "services:read",
	"GET /api/v1/services/events":                      "services:read",
	"POST /api/v1/services/{id}/{action}":              "services:write",
	"POST /api/v1/sites/{name}/services/{id}/{action}": "services:write",
	"GET /api/v1/dashboard":                            "services:read",
	"GET /api/v1/system":                               "system:read",
	"GET /api/v1/system/timezones":                     "system:read",
	"PUT /api/v1/system/timezone":                      "system:write",
	"PUT /api/v1/system/hostname":                      "system:write",
	"POST /api/v1/system/reboot":                       "system:write",
	"GET /metrics":                                     "metrics:read",
}

// newFullAPI serves every route group at once.
func newFullAPI(t *testing.T) (*apiEnv, []string) {
	t.Helper()

	e, mux := newFullAPIMux(t)

	return e, mux.patterns
}

// newFullAPIMux is newFullAPI with the whole route mux.
func newFullAPIMux(t *testing.T) (*apiEnv, *routeMux) {
	t.Helper()

	e, lb, _ := newBackupAPI(t)
	ok := func(context.Context) error { return nil }

	var mux *routeMux

	e.h, mux = handler(Config{PublicURL: testPublicURL}, Deps{
		PingAgent: ok, PingDB: ok, Auth: e.svc, Sites: e.sites, Backups: lb,
		CloudPanel: e.cp, CloudPanelSource: newStubSource(t), Metrics: e.metrics, Settings: e.settings,
		Doctor: e.doctor.run, Pools: e.pools.list, PoolAdmin: e.pools, PoolSites: e.st, Firewall: e.firewall, PHP: e.php, PGClient: e.pgclient, Node: e.node, Python: e.python, Mail: e.mail, MailSend: e.mailSent.send, Outbox: e.outbox, Files: e.files.open, NotifyPrefs: e.st, AuditLog: e.st,
		Monitor: e.monitor, Logs: e.logs, System: e.system,
	})

	return e, mux
}

func TestRouteMatrixComplete(t *testing.T) {
	_, patterns := newFullAPI(t)

	seen := map[string]bool{}

	for _, p := range patterns {
		if seen[p] {
			t.Errorf("route %q registered twice", p)
		}

		seen[p] = true

		if _, ok := routeAccess[p]; !ok {
			t.Errorf("route %q has no entry in routeAccess", p)
		}
	}

	for p, a := range routeAccess {
		if !seen[p] {
			t.Errorf("routeAccess names %q, which is not registered", p)
		}

		if a != accessPublic && a != accessSession && a != accessAny && !isAction(a) {
			t.Errorf("route %q: unknown action %q", p, a)
		}
	}
}

func isAction(a authz.Action) bool {
	for _, x := range authz.Actions() {
		if x == a {
			return true
		}
	}

	return false
}

var pathParam = regexp.MustCompile(`\{[a-z]+\}`)

// matrixPath turns a pattern into a request: method and a concrete path.
// {name} becomes site, {db} a database of it, every other value "1".
func matrixPath(pattern, site string) (method, path string) {
	method, path, _ = strings.Cut(pattern, " ")
	path = strings.ReplaceAll(path, "{name}", site)
	path = strings.ReplaceAll(path, "{db}", site+"db")

	return method, pathParam.ReplaceAllString(path, "1")
}

func matrixBody(method string) string {
	if method == http.MethodGet || method == http.MethodDelete {
		return ""
	}

	return "{}"
}

// deniedByAuthz reports whether the answer is one that authn writes itself
// when a role or a grant does not allow the request.
func deniedByAuthz(t *testing.T, rec *httptest.ResponseRecorder) bool {
	t.Helper()

	switch rec.Code {
	case http.StatusForbidden:
		return errCode(t, rec) == "forbidden"
	case http.StatusNotFound:
		var e apiError

		_ = json.Unmarshal(rec.Body.Bytes(), &e)

		return e.Error.Code == "not_found" && (e.Error.Message == "no such site" || e.Error.Message == "not found")
	}

	return false
}

// matrixLogin creates an account with the role through an invitation and
// returns its session.
func matrixLogin(t *testing.T, e *apiEnv, admin *http.Cookie, name, role string) (*http.Cookie, int64) {
	t.Helper()

	u := e.createUser(t, admin, name, role)

	rec := e.do(t, reqOpts{
		method: "POST", path: "/api/v1/invitations/accept",
		body: `{"token":"` + u.Invitation.Token + `","password":"` + apiPassword + `"}`,
	})
	if rec.Code != http.StatusOK {
		t.Fatalf("accept %s: %d %s", name, rec.Code, rec.Body)
	}

	e.giveSecondFactor(t, name)

	return sessionCookieOf(t, rec), u.User.ID
}

// TestRouteMatrix checks every route against the running handler
// (ADR-0035, the IDOR matrix of 05-security.md):
//
//   - without a login it answers 401, unless it is public;
//   - a token without the route's scope gets 403, a token with every scope
//     passes; a session-only route refuses every token;
//   - a role the action does not allow gets 403;
//   - a user without a grant for the route's site gets the same 404 as for
//     a missing site, a user with a grant passes.
//
// Requests that would pass are only sent where they change nothing: GET,
// or a site that does not exist.
func TestRouteMatrix(t *testing.T) {
	e, patterns := newFullAPI(t)
	c := e.adminCookie(t)

	// The metrics endpoint answers 404 while it is off.
	e.setMetrics(t, c, `{"enabled":true}`)

	if rec := e.do(t, reqOpts{method: "POST", path: "/api/v1/sites", cookie: c, body: `{"name":"alpha","domains":["alpha.example"]}`}); rec.Code >= 300 {
		t.Fatalf("create site: %d %s", rec.Code, rec.Body)
	}

	if rec := e.do(t, reqOpts{method: "POST", path: "/api/v1/sites/alpha/databases", cookie: c, body: `{"name":"alphadb"}`}); rec.Code >= 300 {
		t.Fatalf("create database: %d %s", rec.Code, rec.Body)
	}

	manager, _ := matrixLogin(t, e, c, "mona", "manager")
	granted, gid := matrixLogin(t, e, c, "gina", "user")
	stranger, _ := matrixLogin(t, e, c, "otto", "user")

	if rec := e.do(t, reqOpts{method: "PUT", path: userPath(gid, "/grants"), cookie: c, body: `{"sites":["alpha"]}`}); rec.Code != http.StatusOK {
		t.Fatalf("grant: %d %s", rec.Code, rec.Body)
	}

	all := make([]string, 0, len(auth.Scopes()))
	for _, s := range auth.Scopes() {
		all = append(all, s.Name)
	}

	fullToken := e.createToken(t, c, tokenBody(all))

	sort.Strings(patterns)

	for _, p := range patterns {
		access := routeAccess[p]

		t.Run(p, func(t *testing.T) {
			// Checks that may reach the handler use a site that does not exist.
			method, path := matrixPath(p, "nosite")
			body := matrixBody(method)

			rec := e.do(t, reqOpts{method: method, path: path, body: body})

			unauth := rec.Code == http.StatusUnauthorized && errCode(t, rec) == "unauthenticated"
			if access == accessPublic {
				if unauth {
					t.Fatalf("public route wants a login: %d %s", rec.Code, rec.Body)
				}

				return
			}

			if !unauth {
				t.Fatalf("without login: %d %s, want 401", rec.Code, rec.Body)
			}

			withToken := func(tok string) *httptest.ResponseRecorder {
				return e.do(t, reqOpts{method: method, path: path, body: body, header: bearerHeader(tok)})
			}

			switch {
			case access == accessAny:
				if rec := withToken(fullToken.Token); rec.Code == http.StatusUnauthorized || rec.Code == http.StatusForbidden {
					t.Fatalf("token refused: %d %s", rec.Code, rec.Body)
				}

				return
			case access == accessSession || authz.SessionOnly(access):
				if rec := withToken(fullToken.Token); rec.Code != http.StatusForbidden || errCode(t, rec) != "session_required" {
					t.Fatalf("token with every scope: %d %s, want 403 session_required", rec.Code, rec.Body)
				}
			default:
				scope := authz.ScopeOf(access)

				var others []string

				for _, s := range all {
					if s != scope {
						others = append(others, s)
					}
				}

				tok := e.createToken(t, c, tokenBody(others))
				defer e.do(t, reqOpts{method: "DELETE", path: "/api/v1/tokens/" + strconv.FormatInt(tok.ID, 10), cookie: c})

				if rec := withToken(tok.Token); rec.Code != http.StatusForbidden || errCode(t, rec) != "insufficient_scope" {
					t.Fatalf("token without %s: %d %s, want 403 insufficient_scope", scope, rec.Code, rec.Body)
				}

				// An admin is never stopped by authz; the handler may still
				// answer 404 for the missing site.
				if rec := withToken(fullToken.Token); rec.Code == http.StatusUnauthorized ||
					rec.Code == http.StatusForbidden && (errCode(t, rec) == "insufficient_scope" || errCode(t, rec) == "forbidden") {
					t.Fatalf("admin token with %s: %d %s", scope, rec.Code, rec.Body)
				}
			}

			// The metrics endpoint takes no session; TestMetricsRoles covers it.
			if access == accessSession || p == "GET /metrics" {
				return
			}

			rule, _ := authz.Lookup(access)

			// The role dimension, on the existing site alpha.
			method, path = matrixPath(p, "alpha")
			body = matrixBody(method)

			for _, who := range []struct {
				name   string
				cookie *http.Cookie
				role   authz.Role
				grant  bool
			}{
				{"manager", manager, authz.RoleManager, true},
				{"user with grant", granted, authz.RoleUser, true},
				{"user without grant", stranger, authz.RoleUser, false},
			} {
				mayRole := authz.Allowed(who.role, access)
				may := mayRole && (!rule.Site || who.grant)

				if may && method != http.MethodGet {
					continue // would change state; covered by the feature tests
				}

				rec := e.do(t, reqOpts{method: method, path: path, body: body, cookie: who.cookie})

				switch {
				case !mayRole:
					if rec.Code != http.StatusForbidden || errCode(t, rec) != "forbidden" {
						t.Errorf("%s: %d %s, want 403 forbidden", who.name, rec.Code, rec.Body)
					}
				case !may:
					if rec.Code != http.StatusNotFound || !deniedByAuthz(t, rec) {
						t.Errorf("%s: %d %s, want the 404 of a missing site", who.name, rec.Code, rec.Body)
					}
				default:
					// A route that names its site through another resource
					// (a restore, an import job) cannot tell a missing
					// resource from a missing grant; "1" does not exist.
					resourceMissing := !strings.Contains(p, "{name}") && !strings.Contains(p, "{db}") && rec.Code == http.StatusNotFound
					if rec.Code == http.StatusUnauthorized || deniedByAuthz(t, rec) && !resourceMissing {
						t.Errorf("%s: %d %s, want it to pass", who.name, rec.Code, rec.Body)
					}
				}
			}
		})
	}
}

func tokenBody(scopes []string) string {
	b, _ := json.Marshal(map[string]any{"name": "matrix", "scopes": scopes, "expiresInDays": 1})

	return string(b)
}

// A token never grants more than its owner's role: a manager cannot create
// a token with the scope metrics:read, and an admin's token with it stops
// working when the admin becomes a manager.
func TestMetricsRoles(t *testing.T) {
	e := newAPI(t)
	c := e.adminCookie(t)
	e.setMetrics(t, c, `{"enabled":true}`)

	mona, _ := matrixLogin(t, e, c, "mona", "manager")

	rec := e.do(t, reqOpts{method: "POST", path: "/api/v1/tokens", cookie: mona, body: tokenBody([]string{"metrics:read"})})
	if rec.Code != http.StatusUnprocessableEntity || errCode(t, rec) != "invalid_argument" {
		t.Fatalf("manager creates a metrics token: %d %s", rec.Code, rec.Body)
	}

	ada, adaID := matrixLogin(t, e, c, "ada", "admin")
	tok := e.createToken(t, ada, tokenBody([]string{"metrics:read"}))

	if rec := e.do(t, reqOpts{method: "GET", path: "/metrics", header: bearerHeader(tok.Token)}); rec.Code != http.StatusOK {
		t.Fatalf("admin token: %d", rec.Code)
	}

	if rec := e.do(t, reqOpts{method: "PUT", path: userPath(adaID, ""), cookie: c, body: `{"role":"manager"}`}); rec.Code != http.StatusOK {
		t.Fatalf("demote: %d %s", rec.Code, rec.Body)
	}

	rec = e.do(t, reqOpts{method: "GET", path: "/metrics", header: bearerHeader(tok.Token)})
	if rec.Code != http.StatusForbidden || errCode(t, rec) != "forbidden" {
		t.Fatalf("token of a demoted admin: %d %s", rec.Code, rec.Body)
	}

	admin := e.createToken(t, c, tokenBody([]string{"metrics:read"}))
	if rec := e.do(t, reqOpts{method: "GET", path: "/metrics", header: bearerHeader(admin.Token)}); rec.Code != http.StatusOK {
		t.Fatalf("admin token: %d", rec.Code)
	}
}
