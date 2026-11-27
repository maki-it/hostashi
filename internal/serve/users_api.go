// SPDX-License-Identifier: AGPL-3.0-or-later

package serve

import (
	"errors"
	"net/http"
	"strconv"
	"time"

	"github.com/maki-it/hostashi/internal/auth"
	"github.com/maki-it/hostashi/internal/authz"
	"github.com/maki-it/hostashi/internal/openapi"
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
	// EmailedTo is the address the link was queued for; empty when it is
	// only shown (ADR-0044).
	EmailedTo string `json:"emailedTo,omitempty"`
}

func emailedTo(emailed bool, inv auth.NewInvitation) string {
	if emailed {
		return inv.Email
	}

	return ""
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
	case errors.Is(err, auth.ErrInvalidRole), errors.Is(err, auth.ErrEmailInvalid):
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

// userPolicyJSON is what applies to every account (ADR-0035, addendum 1).
type userPolicyJSON struct {
	// RequireSecondFactor requires TOTP or a passkey of every account,
	// whatever its role. Accounts without one are led to set one up at
	// their next login; until then they and their API tokens can do
	// nothing else.
	RequireSecondFactor bool `json:"requireSecondFactor"`
}

type userPolicyRequest struct {
	RequireSecondFactor *bool `json:"requireSecondFactor"`
}

type accountListJSON struct {
	Users []accountJSON `json:"users"`
}

// accountCreatedJSON is a new account with its invitation link token.
type accountCreatedJSON struct {
	User       accountJSON    `json:"user"`
	Invitation invitationJSON `json:"invitation"`
}

type invitationResultJSON struct {
	Invitation invitationJSON `json:"invitation"`
}

// grantsJSON lists the sites a user may reach.
type grantsJSON struct {
	Sites []string `json:"sites"`
}

type invitationCheckJSON struct {
	Username string `json:"username"`
}

type userCreateRequest struct {
	Username string `json:"username"`
	Role     string `json:"role"`
	// Email is optional: the invitation goes there (ADR-0044).
	Email string `json:"email"`
}

type userUpdateRequest struct {
	Role                *string `json:"role"`
	Disabled            *bool   `json:"disabled"`
	RequireSecondFactor *bool   `json:"requireSecondFactor"`
}

type grantsRequest struct {
	Sites []string `json:"sites"`
}

type invitationTokenRequest struct {
	Token string `json:"token"`
}

type invitationAcceptRequest struct {
	Token    string `json:"token"`
	Password string `json:"password"`
}

// registerUsers adds account management (admins, browser session only) and
// the public invitation routes (ADR-0035).
func registerUsers(mux *routeMux, a authn, m *mailer) {
	mux.Op("GET /api/v1/users", openapi.Operation{
		ID: "listUsers", Tag: "users", Summary: "List the panel accounts", Out: accountListJSON{},
	}, func(w http.ResponseWriter, r *http.Request) {
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

		writeJSON(w, http.StatusOK, accountListJSON{Users: out})
	})

	mux.Op("POST /api/v1/users", openapi.Operation{
		ID: "createUser", Tag: "users", Summary: "Invite a new account", In: userCreateRequest{}, Out: accountCreatedJSON{}, Status: http.StatusCreated,
	}, func(w http.ResponseWriter, r *http.Request) {
		u, ok := a.sessionAllow(w, r, authz.UsersWrite)
		if !ok {
			return
		}

		var in userCreateRequest
		if !decodeJSON(w, r, &in) {
			return
		}

		if in.Email != "" && !m.ready(r.Context()) {
			writeError(w, http.StatusConflict, "mail_not_configured", "email is not set up; leave the address empty and pass the link on yourself")

			return
		}

		acc, inv, err := a.svc.CreateAccount(r.Context(), u, in.Username, in.Role, in.Email, clientIP(r))
		if err != nil {
			usersFail(a, w, err, "create user")

			return
		}

		emailed := m.invitation(r.Context(), u.Username, acc.Username, acc.Role, false, inv)
		if inv.Email != "" && !emailed {
			// Not sent: accepting the shown link must not confirm the address.
			_ = a.svc.ForgetInvitationEmail(r.Context(), acc.ID)
		}

		writeJSON(w, http.StatusCreated, accountCreatedJSON{
			User:       toAccountJSON(acc),
			Invitation: invitationJSON{Token: inv.Secret, ExpiresAt: inv.Expires, EmailedTo: emailedTo(emailed, inv)},
		})
	})

	mux.Op("GET /api/v1/users/policy", openapi.Operation{
		ID: "getUserPolicy", Tag: "users", Summary: "What applies to every account, such as a required second factor", Out: userPolicyJSON{},
	}, func(w http.ResponseWriter, r *http.Request) {
		if _, ok := a.sessionAllow(w, r, authz.UsersRead); !ok {
			return
		}

		on, err := a.svc.SecondFactorForAll(r.Context())
		if err != nil {
			usersFail(a, w, err, "read user policy")

			return
		}

		writeJSON(w, http.StatusOK, userPolicyJSON{RequireSecondFactor: on})
	})

	mux.Op("PUT /api/v1/users/policy", openapi.Operation{
		ID: "setUserPolicy", Tag: "users", Summary: "Require a second factor of every account, or stop requiring it", In: userPolicyRequest{}, Out: userPolicyJSON{},
	}, func(w http.ResponseWriter, r *http.Request) {
		u, ok := a.sessionAllow(w, r, authz.UsersWrite)
		if !ok {
			return
		}

		var in userPolicyRequest
		if !decodeJSON(w, r, &in) {
			return
		}

		if in.RequireSecondFactor == nil {
			writeError(w, http.StatusUnprocessableEntity, "invalid_argument", "requireSecondFactor is required")

			return
		}

		if err := a.svc.SetSecondFactorForAll(r.Context(), u, *in.RequireSecondFactor, clientIP(r)); err != nil {
			if errors.Is(err, auth.ErrSecondFactorRequired) {
				writeError(w, http.StatusConflict, "second_factor_required", "set up a second factor for your own account first")

				return
			}

			usersFail(a, w, err, "store user policy")

			return
		}

		writeJSON(w, http.StatusOK, userPolicyJSON{RequireSecondFactor: *in.RequireSecondFactor})
	})

	mux.Op("GET /api/v1/users/{id}", openapi.Operation{
		ID: "getUser", Tag: "users", Summary: "Read one account", Out: accountJSON{},
	}, func(w http.ResponseWriter, r *http.Request) {
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

	mux.Op("PUT /api/v1/users/{id}", openapi.Operation{
		ID: "updateUser", Tag: "users", Summary: "Change role, disabled state or the second-factor rule of an account", In: userUpdateRequest{}, Out: accountJSON{},
	}, func(w http.ResponseWriter, r *http.Request) {
		u, ok := a.sessionAllow(w, r, authz.UsersWrite)
		if !ok {
			return
		}

		id, ok := pathID(w, r)
		if !ok {
			return
		}

		var in userUpdateRequest
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

	mux.Op("DELETE /api/v1/users/{id}", openapi.Operation{
		ID: "deleteUser", Tag: "users", Summary: "Delete an account",
	}, func(w http.ResponseWriter, r *http.Request) {
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

	mux.Op("POST /api/v1/users/{id}/reset", openapi.Operation{
		ID: "resetUser", Tag: "users", Summary: "Reset password and second factors and make a new invitation", Out: invitationResultJSON{},
	}, func(w http.ResponseWriter, r *http.Request) {
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

		acc, err := a.svc.Account(r.Context(), id)
		if err != nil {
			usersFail(a, w, err, "read user")

			return
		}

		emailed := m.invitation(r.Context(), u.Username, acc.Username, acc.Role, true, inv)

		writeJSON(w, http.StatusOK, invitationResultJSON{Invitation: invitationJSON{Token: inv.Secret, ExpiresAt: inv.Expires, EmailedTo: emailedTo(emailed, inv)}})
	})

	mux.Op("GET /api/v1/users/{id}/grants", openapi.Operation{
		ID: "getUserGrants", Tag: "users", Summary: "The sites a user account may reach", Out: grantsJSON{},
	}, func(w http.ResponseWriter, r *http.Request) {
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

		writeJSON(w, http.StatusOK, grantsJSON{Sites: g})
	})

	mux.Op("PUT /api/v1/users/{id}/grants", openapi.Operation{
		ID: "setUserGrants", Tag: "users", Summary: "Set the sites a user account may reach; an empty list removes all", In: grantsRequest{}, Out: grantsJSON{},
	}, func(w http.ResponseWriter, r *http.Request) {
		u, ok := a.sessionAllow(w, r, authz.UsersWrite)
		if !ok {
			return
		}

		id, ok := pathID(w, r)
		if !ok {
			return
		}

		var in grantsRequest
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

		writeJSON(w, http.StatusOK, grantsJSON{Sites: g})
	})

	// The invitation routes are public: the token in the body is the
	// credential. It never goes into a URL path or query.
	mux.Op("POST /api/v1/invitations/check", openapi.Operation{
		ID: "checkInvitation", Tag: "users", Summary: "The account name of an invitation token", In: invitationTokenRequest{}, Out: invitationCheckJSON{},
	}, func(w http.ResponseWriter, r *http.Request) {
		var in invitationTokenRequest
		if !decodeJSON(w, r, &in) {
			return
		}

		name, err := a.svc.InvitedUsername(r.Context(), in.Token, clientIP(r))
		if err != nil {
			usersFail(a, w, err, "check invitation")

			return
		}

		writeJSON(w, http.StatusOK, invitationCheckJSON{Username: name})
	})

	mux.Op("POST /api/v1/invitations/accept", openapi.Operation{
		ID: "acceptInvitation", Tag: "users", Summary: "Set the password of an invited account and log in", In: invitationAcceptRequest{}, Out: meJSON{},
	}, func(w http.ResponseWriter, r *http.Request) {
		var in invitationAcceptRequest
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
