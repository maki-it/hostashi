// SPDX-License-Identifier: AGPL-3.0-or-later

package serve

import (
	"context"
	"errors"
	"log/slog"
	"net/http"

	"github.com/maki-it/hostashi/internal/agentapi"
	"github.com/maki-it/hostashi/internal/auth"
	"github.com/maki-it/hostashi/internal/authz"
	"github.com/maki-it/hostashi/internal/hosting"
	"github.com/maki-it/hostashi/internal/openapi"
)

// wordPressIn starts a WordPress quick install (ADR-0069). Without
// adminPassword the panel generates one and returns it once.
type wordPressIn struct {
	Locale        string `json:"locale"`
	Title         string `json:"title"`
	AdminUser     string `json:"adminUser"`
	AdminEmail    string `json:"adminEmail"`
	AdminPassword string `json:"adminPassword,omitempty"`
	// Discourage asks search engines not to index the site.
	Discourage bool `json:"discourage,omitempty"`
}

// wordPressJSON is the state of the site's install run. Log is the run's
// output, which WP-CLI writes and the site user can change.
type wordPressJSON struct {
	State string `json:"state"`
	Error string `json:"error,omitempty"`
	Log   string `json:"log,omitempty"`
}

// wordPressStartJSON is a started install: the state, the database it
// created, and the admin password when the panel generated it. The panel
// keeps no copy of the password.
type wordPressStartJSON struct {
	wordPressJSON

	Database      string `json:"database"`
	AdminPassword string `json:"adminPassword,omitempty"`
}

func toWordPressJSON(st agentapi.WPInstallStatus) wordPressJSON {
	return wordPressJSON{State: st.State, Error: st.Error, Log: st.Log}
}

// wordPressFail answers the errors of the WordPress routes.
func wordPressFail(log *slog.Logger, w http.ResponseWriter, err error, what string) {
	if errors.Is(err, hosting.ErrNoWordPress) {
		writeError(w, http.StatusConflict, "wordpress_unavailable", "the WordPress install is not available on this panel")

		return
	}

	dbFail(log, w, err, what)
}

func registerWordPress(mux *routeMux, a authn, svc *hosting.Service, log *slog.Logger) {
	mux.Op("GET /api/v1/sites/{name}/wordpress", openapi.Operation{
		ID: "getSiteWordPress", Tag: "sites", Summary: "The state of the site's WordPress quick install", Out: wordPressJSON{},
	}, a.allow("sites:read", func(w http.ResponseWriter, r *http.Request, _ auth.Principal) {
		ctx, cancel := context.WithTimeout(r.Context(), agentTimeout)
		defer cancel()

		st, err := svc.WordPressStatus(ctx, r.PathValue("name"))
		if err != nil {
			wordPressFail(log, w, err, "read WordPress install")

			return
		}

		writeJSON(w, http.StatusOK, toWordPressJSON(st))
	}))

	// Creates a database and user for the site, then starts the install as
	// the site user. The document root must be empty. It creates a
	// database, so it also needs databases:write.
	mux.Op("POST /api/v1/sites/{name}/wordpress", openapi.Operation{
		ID: "installSiteWordPress", Tag: "sites", Summary: "Start the WordPress quick install", In: wordPressIn{}, Out: wordPressStartJSON{},
		Status: http.StatusAccepted,
	}, a.allow("sites.wordpress:install", func(w http.ResponseWriter, r *http.Request, p auth.Principal) {
		if !p.Allows(authz.ScopeOf("databases:write")) {
			writeError(w, http.StatusForbidden, "insufficient_scope", "the token lacks the scope "+authz.ScopeOf("databases:write"))

			return
		}

		if !authz.Allowed(authz.Role(p.User.Role), "databases:write") {
			writeError(w, http.StatusForbidden, "forbidden", "your role does not allow this")

			return
		}

		var in wordPressIn
		if !decodeJSON(w, r, &in) {
			return
		}

		ctx, cancel := context.WithTimeout(r.Context(), agentTimeout)
		defer cancel()

		got, err := svc.InstallWordPress(ctx, p.User.ID, r.PathValue("name"), hosting.WordPressInput{
			Locale: in.Locale, Title: in.Title, AdminUser: in.AdminUser, AdminEmail: in.AdminEmail,
			AdminPassword: in.AdminPassword, Discourage: in.Discourage,
		})
		if err != nil {
			wordPressFail(log, w, err, "start WordPress install")

			return
		}

		// The answer may hold the generated admin password.
		w.Header().Set("Cache-Control", "no-store")
		writeJSON(w, http.StatusAccepted, wordPressStartJSON{
			wordPressJSON: toWordPressJSON(got.Status), Database: got.Database, AdminPassword: got.AdminPassword,
		})
	}))
}
