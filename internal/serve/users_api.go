// SPDX-License-Identifier: AGPL-3.0-or-later

package serve

import (
	"errors"
	"net/http"
	"strconv"
	"time"

	"github.com/maki-it/hostashi/internal/auth"
	"github.com/maki-it/hostashi/internal/authz"
	"github.com/maki-it/hostashi/internal/store"
)

type accountJSON struct {
	ID          int64  `json:"id"`
	Username    string `json:"username"`
	Role        string `json:"role"`
	Disabled    bool   `json:"disabled"`
	TOTPEnabled bool   `json:"totpEnabled"`
	Passkeys    int    `json:"passkeys"`
	// RequireSecondFactor is the admin's demand for the role user;
	// admins and managers need a second factor regardless.
	RequireSecondFactor bool       `json:"requireSecondFactor"`
	Invited             bool       `json:"invited"`
	InviteEnd           *time.Time `json:"invitationExpiresAt"`
}

func toAccountJSON(a store.Account) accountJSON {
	return accountJSON{
		ID: a.ID, Username: a.Username, Role: a.Role, Disabled: a.Disabled, TOTPEnabled: a.TOTPEnabled,
		Passkeys: a.Passkeys, RequireSecondFactor: a.RequireSecondFactor, Invited: a.Invited, InviteEnd: a.InviteEnd,
	}
}

// invitationJSON carries the secret of a new invitation link, once. The
// panel builds the link as <panel>/#/invite/<token>: the token stays in the
// URL fragment, which browsers never send and nginx never logs.
type invitationJSON struct {
	Token     string    `json:"token"`
	ExpiresAt time.Time `json:"expiresAt"`
}

// usersFail writes the answer for an error of the account functions.
func usersFail(a authn, w http.ResponseWriter, err error, what string) {
	switch {
	case errors.Is(err, auth.ErrUserNotFound):
		writeError(w, http.StatusNotFound, "not_found", "no such user")
	case errors.Is(err, auth.ErrSelf):
		writeError(w, http.StatusConflict, "own_account", "you cannot do this to your own account")
	case errors.Is(err, store.ErrLastAdmin):
		writeError(w, http.StatusConflict, "last_admin", "the last enabled admin cannot be demoted, disabled or deleted")
	case errors.Is(err, store.ErrUserExists):
		writeError(w, http.StatusConflict, "user_exists", "this username is taken")
	case errors.Is(err, store.ErrNotGrantee):
		writeError(w, http.StatusConflict, "not_grantee", "only users with the role user get site grants; admins and managers reach every site")
	case errors.Is(err, store.ErrNotFound):
		writeError(w, http.StatusUnprocessableEntity, "invalid_argument", "unknown site")
	case errors.Is(err, auth.ErrInvalidRole):
		writeError(w, http.StatusUnprocessableEntity, "invalid_argument", err.Error())
	case errors.Is(err, auth.ErrInvitationInvalid):
		writeError(w, http.StatusNotFound, "invitation_invalid", "the invitation link is invalid or expired")
	default:
		a.fail(w, err, what)
	}
}

func pathID(w http.ResponseWriter, r *http.Request) (int64, bool) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil || id < 1 {
		writeError(w, http.StatusNotFound, "not_found", "no such user")

		return 0, false
	}

	return id, true
}

// registerUsers adds account management (admins, browser session only) and
// the public invitation routes (ADR-0035).
func registerUsers(mux *routeMux, a authn) {
	mux.HandleFunc("GET /api/v1/users", func(w http.ResponseWriter, r *http.Request) {
		if _, ok := a.sessionAllow(w, r, authz.UsersRead); !ok {
			return
		}

		all, err := a.svc.Accounts(r.Context())
		if err != nil {
			usersFail(a, w, err, "list users")

			return
		}

		out := make([]accountJSON, 0, len(all))
		for _, x := range all {
			out = append(out, toAccountJSON(x))
		}

		writeJSON(w, http.StatusOK, map[string][]accountJSON{"users": out})
	})

	mux.HandleFunc("POST /api/v1/users", func(w http.ResponseWriter, r *http.Request) {
		u, ok := a.sessionAllow(w, r, authz.UsersWrite)
		if !ok {
			return
		}

		var in struct {
			Username string `json:"username"`
			Role     string `json:"role"`
		}
		if !decodeJSON(w, r, &in) {
			return
		}

		acc, inv, err := a.svc.CreateAccount(r.Context(), u, in.Username, in.Role, clientIP(r))
		if err != nil {
			usersFail(a, w, err, "create user")

			return
		}

		writeJSON(w, http.StatusCreated, map[string]any{
			"user":       toAccountJSON(acc),
			"invitation": invitationJSON{Token: inv.Secret, ExpiresAt: inv.Expires},
		})
	})

	mux.HandleFunc("GET /api/v1/users/{id}", func(w http.ResponseWriter, r *http.Request) {
		if _, ok := a.sessionAllow(w, r, authz.UsersRead); !ok {
			return
		}

		id, ok := pathID(w, r)
		if !ok {
			return
		}

		acc, err := a.svc.Account(r.Context(), id)
		if err != nil {
			usersFail(a, w, err, "read user")

			return
		}

		writeJSON(w, http.StatusOK, toAccountJSON(acc))
	})

	mux.HandleFunc("PUT /api/v1/users/{id}", func(w http.ResponseWriter, r *http.Request) {
		u, ok := a.sessionAllow(w, r, authz.UsersWrite)
		if !ok {
			return
		}

		id, ok := pathID(w, r)
		if !ok {
			return
		}

		var in struct {
			Role                *string `json:"role"`
			Disabled            *bool   `json:"disabled"`
			RequireSecondFactor *bool   `json:"requireSecondFactor"`
		}
		if !decodeJSON(w, r, &in) {
			return
		}

		if in.Role == nil && in.Disabled == nil && in.RequireSecondFactor == nil {
			writeError(w, http.StatusUnprocessableEntity, "invalid_argument", "role, disabled or requireSecondFactor is required")

			return
		}

		acc, err := a.svc.UpdateAccount(r.Context(), u, id, store.AccountChange{
			Role: in.Role, Disabled: in.Disabled, RequireSecondFactor: in.RequireSecondFactor,
		}, clientIP(r))
		if err != nil {
			usersFail(a, w, err, "update user")

			return
		}

		writeJSON(w, http.StatusOK, toAccountJSON(acc))
	})

	mux.HandleFunc("DELETE /api/v1/users/{id}", func(w http.ResponseWriter, r *http.Request) {
		u, ok := a.sessionAllow(w, r, authz.UsersWrite)
		if !ok {
			return
		}

		id, ok := pathID(w, r)
		if !ok {
			return
		}

		if err := a.svc.DeleteAccount(r.Context(), u, id, clientIP(r)); err != nil {
			usersFail(a, w, err, "delete user")

			return
		}

		w.WriteHeader(http.StatusNoContent)
	})

	mux.HandleFunc("POST /api/v1/users/{id}/reset", func(w http.ResponseWriter, r *http.Request) {
		u, ok := a.sessionAllow(w, r, authz.UsersWrite)
		if !ok {
			return
		}

		id, ok := pathID(w, r)
		if !ok {
			return
		}

		inv, err := a.svc.ResetAccount(r.Context(), u, id, clientIP(r))
		if err != nil {
			usersFail(a, w, err, "reset user")

			return
		}

		writeJSON(w, http.StatusOK, map[string]invitationJSON{"invitation": {Token: inv.Secret, ExpiresAt: inv.Expires}})
	})

	mux.HandleFunc("GET /api/v1/users/{id}/grants", func(w http.ResponseWriter, r *http.Request) {
		if _, ok := a.sessionAllow(w, r, authz.UsersRead); !ok {
			return
		}

		id, ok := pathID(w, r)
		if !ok {
			return
		}

		g, err := a.svc.SiteGrants(r.Context(), id)
		if err != nil {
			usersFail(a, w, err, "read grants")

			return
		}

		writeJSON(w, http.StatusOK, map[string][]string{"sites": g})
	})

	mux.HandleFunc("PUT /api/v1/users/{id}/grants", func(w http.ResponseWriter, r *http.Request) {
		u, ok := a.sessionAllow(w, r, authz.UsersWrite)
		if !ok {
			return
		}

		id, ok := pathID(w, r)
		if !ok {
			return
		}

		var in struct {
			Sites []string `json:"sites"`
		}
		if !decodeJSON(w, r, &in) {
			return
		}

		if in.Sites == nil {
			writeError(w, http.StatusUnprocessableEntity, "invalid_argument", "sites is required, an empty list removes every grant")

			return
		}

		if len(in.Sites) > store.MaxGrantsPerUser {
			writeError(w, http.StatusUnprocessableEntity, "invalid_argument", "too many sites")

			return
		}

		g, err := a.svc.SetSiteGrants(r.Context(), u, id, in.Sites, clientIP(r))
		if err != nil {
			usersFail(a, w, err, "set grants")

			return
		}

		writeJSON(w, http.StatusOK, map[string][]string{"sites": g})
	})

	// The invitation routes are public: the token in the body is the
	// credential. It never goes into a URL path or query.
	mux.HandleFunc("POST /api/v1/invitations/check", func(w http.ResponseWriter, r *http.Request) {
		var in struct {
			Token string `json:"token"`
		}
		if !decodeJSON(w, r, &in) {
			return
		}

		name, err := a.svc.InvitedUsername(r.Context(), in.Token, clientIP(r))
		if err != nil {
			usersFail(a, w, err, "check invitation")

			return
		}

		writeJSON(w, http.StatusOK, map[string]string{"username": name})
	})

	mux.HandleFunc("POST /api/v1/invitations/accept", func(w http.ResponseWriter, r *http.Request) {
		var in struct {
			Token    string `json:"token"`
			Password string `json:"password"`
		}
		if !decodeJSON(w, r, &in) {
			return
		}

		token, u, err := a.svc.AcceptInvitation(r.Context(), in.Token, in.Password, clientIP(r))
		if err != nil {
			usersFail(a, w, err, "accept invitation")

			return
		}

		setSessionCookie(w, token, int(auth.SessionTTL.Seconds()))
		a.writeLoggedIn(w, r, u)
	})
}
