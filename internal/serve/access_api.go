// SPDX-License-Identifier: AGPL-3.0-or-later

package serve

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/maki-it/hostashi/internal/agentapi"
	"github.com/maki-it/hostashi/internal/auth"
	"github.com/maki-it/hostashi/internal/hosting"
	"github.com/maki-it/hostashi/internal/openapi"
	"github.com/maki-it/hostashi/internal/store"
)

// Password protection and IP rules per site (ADR-0066).

// accessSettingsJSON is the part of a site's access setting that PUT
// replaces.
type accessSettingsJSON struct {
	// IPMode is "" (off), "allow" (only these addresses) or "deny".
	IPMode string   `json:"ipMode"`
	IPs    []string `json:"ips"`
	// Password asks for a password, on Paths only or on the whole site
	// when Paths is empty; Bypass addresses need none.
	Password bool     `json:"password"`
	Paths    []string `json:"paths"`
	Bypass   []string `json:"bypass"`
}

type accessUserJSON struct {
	Name      string    `json:"name"`
	CreatedAt time.Time `json:"createdAt"`
	UpdatedAt time.Time `json:"updatedAt"`
}

type accessLimitsJSON struct {
	IPs         int `json:"ips"`
	Paths       int `json:"paths"`
	Bypass      int `json:"bypass"`
	Users       int `json:"users"`
	MinPassword int `json:"minPassword"`
	MaxPassword int `json:"maxPassword"`
}

// accessJSON is a site's access setting with its users (never hashes).
type accessJSON struct {
	accessSettingsJSON

	Users  []accessUserJSON `json:"users"`
	Limits accessLimitsJSON `json:"limits"`
	// ClientAddress is the address the panel sees for this request, so the
	// form can warn before an allowlist locks the caller out of the site.
	ClientAddress string     `json:"clientAddress"`
	UpdatedAt     *time.Time `json:"updatedAt,omitempty"`
}

func toAccessJSON(v hosting.SiteAccessView, client string) accessJSON {
	a := v.Settings
	out := accessJSON{
		accessSettingsJSON: accessSettingsJSON{IPMode: a.IPMode, IPs: nonNil(a.IPs), Password: a.Password, Paths: nonNil(a.Paths), Bypass: nonNil(a.Bypass)},
		Users:              make([]accessUserJSON, 0, len(v.Users)),
		Limits: accessLimitsJSON{
			IPs: agentapi.MaxAccessIPs, Paths: agentapi.MaxAccessPaths, Bypass: agentapi.MaxAccessBypass, Users: agentapi.MaxAccessUsers,
			MinPassword: agentapi.MinAccessPassword, MaxPassword: agentapi.MaxAccessPassword,
		},
		ClientAddress: client,
	}

	for _, u := range v.Users {
		out.Users = append(out.Users, accessUserJSON{Name: u.Name, CreatedAt: u.CreatedAt, UpdatedAt: u.UpdatedAt})
	}

	if !v.UpdatedAt.IsZero() {
		t := v.UpdatedAt
		out.UpdatedAt = &t
	}

	return out
}

func nonNil(s []string) []string {
	if s == nil {
		return []string{}
	}

	return s
}

// accessUserIn adds a user or sets a password; an empty password lets the
// panel generate one.
type accessUserIn struct {
	Name     string `json:"name,omitempty"`
	Password string `json:"password,omitempty"`
}

// accessPasswordJSON carries a generated password, shown once.
type accessPasswordJSON struct {
	Name     string `json:"name"`
	Password string `json:"password,omitempty"`
}

// accessFail answers a rejected setting, a snippet conflict and unknown or
// duplicate users with their own codes; everything else as for any site
// call.
func accessFail(log *slog.Logger, w http.ResponseWriter, err error, what string) {
	switch {
	case errors.Is(err, agentapi.ErrAccessConflict):
		writeError(w, http.StatusUnprocessableEntity, "access_conflict", reason(err, agentapi.ErrAccessConflict))
	case errors.Is(err, agentapi.ErrAccessRejected):
		writeError(w, http.StatusUnprocessableEntity, "access_rejected", reason(err, agentapi.ErrAccessRejected))
	case errors.Is(err, hosting.ErrAuthUserExists):
		writeError(w, http.StatusConflict, "user_exists", "the site already has this user")
	case errors.Is(err, hosting.ErrAuthUserNotFound):
		writeError(w, http.StatusNotFound, "user_not_found", "no such user")
	default:
		siteFail(log, w, err, what)
	}
}

// reason is the part of err after kind, the text a person can act on.
func reason(err, kind error) string {
	msg, k := err.Error(), kind.Error()+": "
	if i := strings.Index(msg, k); i >= 0 {
		return msg[i+len(k):]
	}

	return msg
}

func registerAccess(mux *routeMux, a authn, svc *hosting.Service, log *slog.Logger) {
	mux.Op("GET /api/v1/sites/{name}/access", openapi.Operation{
		ID: "getSiteAccess", Tag: "sites", Summary: "Get the site's IP rule, password protection and users", Out: accessJSON{},
	}, a.allow("sites:read", func(w http.ResponseWriter, r *http.Request, _ auth.Principal) {
		v, err := svc.Access(r.Context(), r.PathValue("name"))
		if err != nil {
			siteFail(log, w, err, "read access protection")

			return
		}

		writeJSON(w, http.StatusOK, toAccessJSON(v, clientIP(r)))
	}))

	// The body replaces the whole setting; users stay as they are.
	mux.Op("PUT /api/v1/sites/{name}/access", openapi.Operation{
		ID: "setSiteAccess", Tag: "sites", Summary: "Set the site's IP rule and password protection", In: accessSettingsJSON{}, Out: accessJSON{},
	}, a.allow("sites.access:write", func(w http.ResponseWriter, r *http.Request, p auth.Principal) {
		var in accessSettingsJSON
		if !decodeJSON(w, r, &in) {
			return
		}

		ctx, cancel := context.WithTimeout(r.Context(), agentTimeout)
		defer cancel()

		v, err := svc.SetAccess(ctx, p.User.ID, r.PathValue("name"), store.AccessSettings{
			IPMode: in.IPMode, IPs: in.IPs, Password: in.Password, Paths: in.Paths, Bypass: in.Bypass,
		})
		if err != nil {
			accessFail(log, w, err, "set access protection")

			return
		}

		writeJSON(w, http.StatusOK, toAccessJSON(v, clientIP(r)))
	}))

	mux.Op("POST /api/v1/sites/{name}/access/users", openapi.Operation{
		ID: "addSiteAccessUser", Tag: "sites", Summary: "Add a password protection user; without a password the panel generates one and returns it once",
		In: accessUserIn{}, Out: accessPasswordJSON{}, Status: http.StatusCreated,
	}, a.allow("sites.access:write", func(w http.ResponseWriter, r *http.Request, p auth.Principal) {
		var in accessUserIn
		if !decodeJSON(w, r, &in) {
			return
		}

		ctx, cancel := context.WithTimeout(r.Context(), agentTimeout)
		defer cancel()

		pw, err := svc.AddAccessUser(ctx, p.User.ID, r.PathValue("name"), in.Name, in.Password)
		if err != nil {
			accessFail(log, w, err, "add access user")

			return
		}

		writeJSON(w, http.StatusCreated, accessPasswordJSON{Name: in.Name, Password: pw})
	}))

	mux.Op("PUT /api/v1/sites/{name}/access/users/{user}", openapi.Operation{
		ID: "setSiteAccessUserPassword", Tag: "sites", Summary: "Set a new password for a password protection user; without one the panel generates it",
		In: accessUserIn{}, Out: accessPasswordJSON{},
	}, a.allow("sites.access:write", func(w http.ResponseWriter, r *http.Request, p auth.Principal) {
		var in accessUserIn
		if !decodeJSON(w, r, &in) {
			return
		}

		if in.Name != "" && in.Name != r.PathValue("user") {
			writeError(w, http.StatusBadRequest, "bad_request", "the name cannot be changed")

			return
		}

		ctx, cancel := context.WithTimeout(r.Context(), agentTimeout)
		defer cancel()

		pw, err := svc.SetAccessUserPassword(ctx, p.User.ID, r.PathValue("name"), r.PathValue("user"), in.Password)
		if err != nil {
			accessFail(log, w, err, "set access user password")

			return
		}

		writeJSON(w, http.StatusOK, accessPasswordJSON{Name: r.PathValue("user"), Password: pw})
	}))

	mux.Op("DELETE /api/v1/sites/{name}/access/users/{user}", openapi.Operation{
		ID: "deleteSiteAccessUser", Tag: "sites", Summary: "Remove a password protection user", Status: http.StatusNoContent,
	}, a.allow("sites.access:write", func(w http.ResponseWriter, r *http.Request, p auth.Principal) {
		ctx, cancel := context.WithTimeout(r.Context(), agentTimeout)
		defer cancel()

		if err := svc.DeleteAccessUser(ctx, p.User.ID, r.PathValue("name"), r.PathValue("user")); err != nil {
			accessFail(log, w, err, "remove access user")

			return
		}

		w.WriteHeader(http.StatusNoContent)
	}))
}
