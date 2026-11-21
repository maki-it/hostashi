// SPDX-License-Identifier: AGPL-3.0-or-later

package serve

import (
	"context"
	"net/http"

	"github.com/maki-it/hostashi/internal/notify"
)

// NotifyPrefs keeps the operations email switches of an account (ADR-0044).
type NotifyPrefs interface {
	NotificationPrefs(ctx context.Context, userID int64) (map[string]bool, error)
	SetNotificationPrefs(ctx context.Context, userID int64, prefs map[string]bool) error
}

type notifyCategoryJSON struct {
	ID      notify.Category `json:"id"`
	Enabled bool            `json:"enabled"`
}

type notificationsJSON struct {
	// Email is the confirmed address; without one nothing is sent.
	Email       string               `json:"email"`
	MailEnabled bool                 `json:"mail_enabled"`
	Categories  []notifyCategoryJSON `json:"categories"`
}

// registerNotifications adds the switches of the page Account,
// Notifications: only the categories the account's role may get.
func registerNotifications(mux *routeMux, a authn, relay MailRelay, prefs NotifyPrefs) {
	state := func(w http.ResponseWriter, r *http.Request, uid int64, role string) {
		e, err := a.svc.EmailState(r.Context(), uid)
		if err != nil {
			a.fail(w, err, "read email")

			return
		}

		p, err := prefs.NotificationPrefs(r.Context(), uid)
		if err != nil {
			a.fail(w, err, "read notifications")

			return
		}

		_, rerr := relay.Load(r.Context())
		out := notificationsJSON{Email: e.Confirmed, MailEnabled: rerr == nil, Categories: []notifyCategoryJSON{}}

		for _, c := range notify.Categories {
			if notify.Visible(role, c) {
				out.Categories = append(out.Categories, notifyCategoryJSON{ID: c, Enabled: notify.Enabled(role, p, c)})
			}
		}

		writeJSON(w, http.StatusOK, out)
	}

	mux.HandleFunc("GET /api/v1/account/notifications", func(w http.ResponseWriter, r *http.Request) {
		u, ok := a.session(w, r)
		if !ok || !a.secondFactorOK(w, r, u) {
			return
		}

		state(w, r, u.ID, u.Role)
	})

	mux.HandleFunc("PUT /api/v1/account/notifications", func(w http.ResponseWriter, r *http.Request) {
		u, ok := a.session(w, r)
		if !ok || !a.secondFactorOK(w, r, u) {
			return
		}

		var in map[string]bool
		if !decodeJSON(w, r, &in) {
			return
		}

		for k := range in {
			if !notify.Visible(u.Role, notify.Category(k)) {
				writeError(w, http.StatusUnprocessableEntity, "invalid_argument", "unknown notification category: "+k)

				return
			}
		}

		if err := prefs.SetNotificationPrefs(r.Context(), u.ID, in); err != nil {
			a.fail(w, err, "save notifications")

			return
		}

		state(w, r, u.ID, u.Role)
	})
}
