// SPDX-License-Identifier: AGPL-3.0-or-later

package serve

import (
	"context"
	"encoding/json"
	"net/http"
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
	"POST /api/v1/auth/logout":               accessPublic,
	"POST /api/v1/auth/passkey/login/begin":  accessPublic,
	"POST /api/v1/auth/passkey/login/finish": accessPublic,

	"GET /api/v1/auth/me": accessAny,
	"GET /api/v1/scopes":  accessAny,

	"POST /api/v1/auth/totp/enroll":              accessSession,
	"POST /api/v1/auth/totp/confirm":             accessSession,
	"POST /api/v1/auth/totp/disable":             accessSession,
	"POST /api/v1/tokens":                        accessSession,
	"GET /api/v1/tokens":                         accessSession,
	"DELETE /api/v1/tokens/{id}":                 accessSession,
	"POST /api/v1/auth/passkeys/register/begin":  accessSession,
	"POST /api/v1/auth/passkeys/register/finish": accessSession,
	"GET /api/v1/auth/passkeys":                  accessSession,
	"DELETE /api/v1/auth/passkeys/{id}":          accessSession,

	"GET /api/v1/settings/api-ip-allowlist": authz.SettingsRead,
	"PUT /api/v1/settings/api-ip-allowlist": authz.SettingsWrite,
	"GET /api/v1/settings/metrics":          authz.SettingsRead,
	"PUT /api/v1/settings/metrics":          authz.SettingsWrite,

	"GET /api/v1/sites":                     "sites:read",
	"GET /api/v1/sites/{name}":              "sites:read",
	"POST /api/v1/sites":                    "sites:write",
	"PUT /api/v1/sites/{name}/domains":      "sites:write",
	"PUT /api/v1/sites/{name}/php":          "sites:write",
	"PUT /api/v1/sites/{name}/tls":          "sites:write",
	"PUT /api/v1/sites/{name}/hsts":         "sites:write",
	"PUT /api/v1/sites/{name}/php-programs": "sites:write",
	"PUT /api/v1/sites/{name}/disabled":     "sites:write",
	"GET /api/v1/php/versions":              "sites:read",
	"GET /api/v1/php/install":               "sites:read",
	"POST /api/v1/php/install":              "php:install",
	"DELETE /api/v1/sites/{name}":           "sites:delete",
	"GET /api/v1/panel/tls":                 "sites:read",
	"POST /api/v1/web/sync":                 "sites:write",

	"GET /api/v1/databases":                            "databases:read",
	"GET /api/v1/sites/{name}/databases":               "databases:read",
	"GET /api/v1/databases/{db}":                       "databases:read",
	"POST /api/v1/sites/{name}/databases":              "databases:write",
	"DELETE /api/v1/databases/{db}":                    "databases:delete",
	"POST /api/v1/databases/{db}/users":                "databases:write",
	"PUT /api/v1/databases/{db}/users/{user}/password": "databases:write",
	"DELETE /api/v1/databases/{db}/users/{user}":       "databases:write",
	"POST /api/v1/databases/{db}/import":               "databases:write",
	"POST /api/v1/databases/{db}/export":               "databases:write",
	"GET /api/v1/databases/{db}/jobs/{id}":             "databases:read",

	"GET /api/v1/status-pages":               "pages:read",
	"GET /api/v1/status-pages/{id}":          "pages:read",
	"POST /api/v1/status-pages":              "pages:write",
	"PUT /api/v1/status-pages/{id}":          "pages:write",
	"DELETE /api/v1/status-pages/{id}":       "pages:write",
	"PUT /api/v1/sites/{name}/maintenance":   "sites.maintenance:write",
	"GET /api/v1/parked-domains":             "sites:read",
	"GET /api/v1/parked-domains/{domain}":    "sites:read",
	"PUT /api/v1/parked-domains/{domain}":    "sites.maintenance:write",
	"DELETE /api/v1/parked-domains/{domain}": "sites.maintenance:write",

	"GET /api/v1/sites/{name}/cron":           "cron:read",
	"GET /api/v1/sites/{name}/cron/{id}":      "cron:read",
	"POST /api/v1/sites/{name}/cron":          "cron:write",
	"PUT /api/v1/sites/{name}/cron/{id}":      "cron:write",
	"DELETE /api/v1/sites/{name}/cron/{id}":   "cron:write",
	"POST /api/v1/sites/{name}/cron/{id}/run": "cron:write",

	"GET /api/v1/ssh/host-keys":                 "ssh:read",
	"GET /api/v1/sites/{name}/ssh-keys":         "ssh:read",
	"GET /api/v1/sites/{name}/ssh-keys/{id}":    "ssh:read",
	"POST /api/v1/sites/{name}/ssh-keys":        "ssh:write",
	"PUT /api/v1/sites/{name}/ssh-keys/{id}":    "ssh:write",
	"DELETE /api/v1/sites/{name}/ssh-keys/{id}": "ssh:write",

	"GET /api/v1/sites/{name}/snippets": "sites:read",
	"PUT /api/v1/sites/{name}/snippets": "sites.snippets:write",

	"GET /api/v1/limit-presets":           "limits:read",
	"GET /api/v1/limit-presets/{name}":    "limits:read",
	"POST /api/v1/limit-presets":          "limits:write",
	"PUT /api/v1/limit-presets/{name}":    "limits:write",
	"DELETE /api/v1/limit-presets/{name}": "limits:write",
	"GET /api/v1/sites/{name}/limits":     "limits:read",
	"PUT /api/v1/sites/{name}/limits":     "limits:write",

	"GET /api/v1/backups":                                  "backups:read",
	"PUT /api/v1/backups/settings":                         "backups:write",
	"POST /api/v1/backups/hostkey/scan":                    "backups:write",
	"PUT /api/v1/backups/hostkey":                          "backups:write",
	"POST /api/v1/backups/run":                             "backups:write",
	"GET /api/v1/sites/{name}/backups":                     "backups:read",
	"GET /api/v1/backups/panel":                            "backups:read",
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

	"GET /api/v1/firewall":          "firewall:read",
	"PUT /api/v1/firewall":          "firewall:write",
	"POST /api/v1/firewall/confirm": "firewall:write",
	"GET /api/v1/doctor":            "doctor:read",
	"GET /metrics":                  "metrics:read",
}

// newFullAPI serves every route group at once.
func newFullAPI(t *testing.T) (*apiEnv, []string) {
	t.Helper()

	e, lb, _ := newBackupAPI(t)
	ok := func(context.Context) error { return nil }

	var patterns []string

	e.h, patterns = handler(Config{}, Deps{
		PingAgent: ok, PingDB: ok, Auth: e.svc, Sites: e.sites, Backups: lb,
		CloudPanel: e.cp, CloudPanelSource: newStubSource(t), Metrics: e.metrics, Settings: e.settings,
		Doctor: e.doctor.run, Firewall: e.firewall, PHP: e.php,
	})

	return e, patterns
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
func matrixPath(pattern string) (method, path string) {
	method, path, _ = strings.Cut(pattern, " ")

	return method, pathParam.ReplaceAllString(path, "1")
}

func matrixBody(method string) string {
	if method == http.MethodGet || method == http.MethodDelete {
		return ""
	}

	return "{}"
}

// TestRouteMatrix checks every route against the running handler: without
// a login it answers 401 unless it is public; a token without the route's
// scope gets 403; an own-account or session-only route refuses a token that
// carries every scope.
func TestRouteMatrix(t *testing.T) {
	e, patterns := newFullAPI(t)
	c := e.adminCookie(t)

	// The metrics endpoint answers 404 while it is off.
	e.setMetrics(t, c, `{"enabled":true}`)

	all := make([]string, 0, len(auth.Scopes()))
	for _, s := range auth.Scopes() {
		all = append(all, s.Name)
	}

	fullToken := e.createToken(t, c, tokenBody(all))

	sort.Strings(patterns)

	for _, p := range patterns {
		access := routeAccess[p]
		method, path := matrixPath(p)

		t.Run(p, func(t *testing.T) {
			rec := e.do(t, reqOpts{method: method, path: path, body: matrixBody(method)})

			unauth := rec.Code == http.StatusUnauthorized && rec.Header().Get("WWW-Authenticate") != "" ||
				rec.Code == http.StatusUnauthorized && errCode(t, rec) == "unauthenticated"
			if access == accessPublic {
				if unauth {
					t.Fatalf("public route wants a login: %d %s", rec.Code, rec.Body)
				}

				return
			}

			if !unauth {
				t.Fatalf("without login: %d %s, want 401", rec.Code, rec.Body)
			}

			switch access {
			case accessAny:
				rec := e.do(t, reqOpts{method: method, path: path, body: matrixBody(method), header: bearerHeader(fullToken.Token)})
				if rec.Code == http.StatusUnauthorized || rec.Code == http.StatusForbidden {
					t.Fatalf("token refused: %d %s", rec.Code, rec.Body)
				}
			case accessSession, authz.SettingsRead, authz.SettingsWrite:
				rec := e.do(t, reqOpts{method: method, path: path, body: matrixBody(method), header: bearerHeader(fullToken.Token)})
				if rec.Code != http.StatusForbidden || errCode(t, rec) != "session_required" {
					t.Fatalf("token with every scope: %d %s, want 403 session_required", rec.Code, rec.Body)
				}
			default:
				var others []string

				for _, s := range all {
					if s != string(access) {
						others = append(others, s)
					}
				}

				tok := e.createToken(t, c, tokenBody(others))
				defer e.do(t, reqOpts{method: "DELETE", path: "/api/v1/tokens/" + strconv.FormatInt(tok.ID, 10), cookie: c})

				rec := e.do(t, reqOpts{method: method, path: path, body: matrixBody(method), header: bearerHeader(tok.Token)})
				if rec.Code != http.StatusForbidden || errCode(t, rec) != "insufficient_scope" {
					t.Fatalf("token without %s: %d %s, want 403 insufficient_scope", access, rec.Code, rec.Body)
				}

				rec = e.do(t, reqOpts{method: method, path: path, body: matrixBody(method), header: bearerHeader(fullToken.Token)})
				if rec.Code == http.StatusUnauthorized || rec.Code == http.StatusForbidden && errCode(t, rec) != "invalid_argument" {
					t.Fatalf("token with %s: %d %s", access, rec.Code, rec.Body)
				}
			}
		})
	}
}

func tokenBody(scopes []string) string {
	b, _ := json.Marshal(map[string]any{"name": "matrix", "scopes": scopes, "expiresInDays": 1})

	return string(b)
}
