// SPDX-License-Identifier: AGPL-3.0-or-later

package serve

import (
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/maki-it/hostashi/internal/auth"
	"github.com/maki-it/hostashi/internal/authz"
	"github.com/maki-it/hostashi/internal/store"
)

// authn turns a request into a principal. It is what every API handler uses:
// scoped for endpoints an API token may call, session for the ones it may not.
type authn struct {
	svc  *auth.Service
	fail failFunc
}

// bearer returns the token of an "Authorization: Bearer" header. present is
// true if the header exists at all, so a malformed header never falls back to
// the cookie.
func bearer(r *http.Request) (token string, present bool) {
	h := r.Header.Get("Authorization")
	if h == "" {
		return "", false
	}

	scheme, rest, found := strings.Cut(h, " ")
	if !found || !strings.EqualFold(scheme, "Bearer") {
		return "", true
	}

	return strings.TrimSpace(rest), true
}

// principal accepts an API token or a browser session, token first. It
// writes the error answer itself.
func (a authn) principal(w http.ResponseWriter, r *http.Request) (auth.Principal, bool) {
	var (
		p   auth.Principal
		err error
	)

	if tok, present := bearer(r); present {
		p, err = a.svc.AuthenticateToken(r.Context(), tok, clientIP(r))
	} else {
		p, err = a.svc.SessionPrincipal(r.Context(), sessionToken(r))
	}

	if err != nil {
		a.fail(w, err, "authenticate")

		return auth.Principal{}, false
	}

	return p, true
}

// session accepts only a browser session. A token, even one with every
// scope, cannot manage tokens, change the allowlist or touch account security.
func (a authn) session(w http.ResponseWriter, r *http.Request) (store.User, bool) {
	if _, present := bearer(r); present {
		writeError(w, http.StatusForbidden, "session_required", "this action needs a browser session, not an API token")

		return store.User{}, false
	}

	p, err := a.svc.SessionPrincipal(r.Context(), sessionToken(r))
	if err != nil {
		a.fail(w, err, "authenticate")

		return store.User{}, false
	}

	return p.User, true
}

// scoped wraps a handler that an API token with the given scope may call. A
// session passes as well (the handler still checks the owner's role).
func (a authn) scoped(scope string, h func(http.ResponseWriter, *http.Request, auth.Principal)) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		p, ok := a.principal(w, r)
		if !ok {
			return
		}

		if !p.Allows(scope) {
			writeError(w, http.StatusForbidden, "insufficient_scope", "the token lacks the scope "+scope)

			return
		}

		h(w, r, p)
	}
}

// allow wraps a handler for action (ADR-0035). An API token must carry the
// scope of the same name, and the caller's role must allow the action. This
// is the only place that checks a role for a token-capable route.
func (a authn) allow(action authz.Action, h func(http.ResponseWriter, *http.Request, auth.Principal)) http.HandlerFunc {
	return a.scoped(string(action), func(w http.ResponseWriter, r *http.Request, p auth.Principal) {
		if !authz.Allowed(authz.Role(p.User.Role), action) {
			writeError(w, http.StatusForbidden, "forbidden", "your role does not allow this")

			return
		}

		h(w, r, p)
	})
}

// sessionAllow accepts only a browser session whose role allows action, for
// the actions no token can carry. It writes the error answer itself.
func (a authn) sessionAllow(w http.ResponseWriter, r *http.Request, action authz.Action) (store.User, bool) {
	u, ok := a.session(w, r)
	if !ok {
		return store.User{}, false
	}

	if !authz.Allowed(authz.Role(u.Role), action) {
		writeError(w, http.StatusForbidden, "forbidden", "your role does not allow this")

		return store.User{}, false
	}

	return u, true
}

type tokenJSON struct {
	ID          int64      `json:"id"`
	Name        string     `json:"name"`
	Prefix      string     `json:"prefix"`
	Scopes      []string   `json:"scopes"`
	CreatedAt   time.Time  `json:"createdAt"`
	ExpiresAt   *time.Time `json:"expiresAt"` // null: never expires
	NeverExpire bool       `json:"neverExpires"`
	LastUsedAt  *time.Time `json:"lastUsedAt"`
	LastUsedIP  string     `json:"lastUsedIp,omitempty"`
	RevokedAt   *time.Time `json:"revokedAt,omitempty"`
	// Warnings are codes such as "expires_soon" for a monitoring token
	// that ends within 30 days (ADR-0022).
	Warnings []string `json:"warnings,omitempty"`
}

func toTokenJSON(t store.APIToken) tokenJSON {
	return tokenJSON{
		ID: t.ID, Name: t.Name, Prefix: auth.TokenPrefix + t.Prefix, Scopes: t.Scopes, CreatedAt: t.CreatedAt,
		ExpiresAt: t.ExpiresAt, NeverExpire: t.ExpiresAt == nil, LastUsedAt: t.LastUsedAt, LastUsedIP: t.LastUsedIP,
		RevokedAt: t.RevokedAt,
	}
}

func registerTokens(mux *routeMux, a authn) {
	mux.HandleFunc("GET /api/v1/scopes", func(w http.ResponseWriter, r *http.Request) {
		if _, ok := a.principal(w, r); !ok {
			return
		}

		writeJSON(w, http.StatusOK, map[string][]auth.ScopeInfo{"scopes": auth.Scopes()})
	})

	mux.HandleFunc("POST /api/v1/tokens", func(w http.ResponseWriter, r *http.Request) {
		u, ok := a.session(w, r)
		if !ok {
			return
		}

		var in struct {
			Name          string   `json:"name"`
			Scopes        []string `json:"scopes"`
			ExpiresInDays int      `json:"expiresInDays"`
			Unlimited     bool     `json:"unlimited"`
		}
		if !decodeJSON(w, r, &in) {
			return
		}

		nt, err := a.svc.CreateToken(r.Context(), u.ID, auth.TokenRequest{
			Name: in.Name, Scopes: in.Scopes, Days: in.ExpiresInDays, Unlimited: in.Unlimited,
		}, clientIP(r))
		if err != nil {
			a.fail(w, err, "create token")

			return
		}

		out := struct {
			tokenJSON
			Token    string   `json:"token"` // the secret, shown only now
			Warnings []string `json:"warnings"`
		}{tokenJSON: toTokenJSON(nt.Token), Token: nt.Secret, Warnings: []string{}}

		if nt.Token.ExpiresAt == nil {
			out.Warnings = append(out.Warnings, "never_expires: this token stays valid until it is revoked")
		}

		if len(a.svc.TokenWarnings(nt.Token)) > 0 {
			out.Warnings = append(out.Warnings, auth.WarnExpiresSoon+": a scrape with this token stops working within 30 days")
		}

		for _, s := range nt.Token.Scopes {
			for _, info := range auth.Scopes() {
				if info.Name == s && info.HighRisk {
					out.Warnings = append(out.Warnings, "high_risk_scope: "+s)
				}
			}
		}

		writeJSON(w, http.StatusCreated, out)
	})

	mux.HandleFunc("GET /api/v1/tokens", func(w http.ResponseWriter, r *http.Request) {
		u, ok := a.session(w, r)
		if !ok {
			return
		}

		ts, err := a.svc.ListTokens(r.Context(), u.ID)
		if err != nil {
			a.fail(w, err, "list tokens")

			return
		}

		out := make([]tokenJSON, 0, len(ts))
		for _, t := range ts {
			j := toTokenJSON(t)
			j.Warnings = a.svc.TokenWarnings(t)
			out = append(out, j)
		}

		writeJSON(w, http.StatusOK, map[string][]tokenJSON{"tokens": out})
	})

	mux.HandleFunc("DELETE /api/v1/tokens/{id}", func(w http.ResponseWriter, r *http.Request) {
		u, ok := a.session(w, r)
		if !ok {
			return
		}

		id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
		if err != nil || id <= 0 {
			a.fail(w, auth.ErrTokenNotFound, "revoke token")

			return
		}

		if err := a.svc.RevokeToken(r.Context(), u.ID, id, clientIP(r)); err != nil {
			a.fail(w, err, "revoke token")

			return
		}

		w.WriteHeader(http.StatusNoContent)
	})

	mux.HandleFunc("GET /api/v1/settings/api-ip-allowlist", func(w http.ResponseWriter, r *http.Request) {
		if _, ok := a.sessionAllow(w, r, authz.SettingsRead); !ok {
			return
		}

		list, err := a.svc.IPAllowlist(r.Context())
		if err != nil {
			a.fail(w, err, "read allowlist")

			return
		}

		writeJSON(w, http.StatusOK, map[string][]string{"entries": list})
	})

	mux.HandleFunc("PUT /api/v1/settings/api-ip-allowlist", func(w http.ResponseWriter, r *http.Request) {
		u, ok := a.sessionAllow(w, r, authz.SettingsWrite)
		if !ok {
			return
		}

		var in struct {
			Entries []string `json:"entries"`
		}
		if !decodeJSON(w, r, &in) {
			return
		}

		list, err := a.svc.SetIPAllowlist(r.Context(), u.ID, in.Entries, clientIP(r))
		if err != nil {
			a.fail(w, err, "set allowlist")

			return
		}

		writeJSON(w, http.StatusOK, map[string][]string{"entries": list})
	})
}
