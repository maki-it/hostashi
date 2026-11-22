// SPDX-License-Identifier: AGPL-3.0-or-later

package serve

import (
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"mime"
	"net"
	"net/http"
	"net/url"
	"strings"

	"github.com/maki-it/hostashi/internal/auth"
	"github.com/maki-it/hostashi/internal/cloudpanel/plan"
	"github.com/maki-it/hostashi/internal/openapi"
	"github.com/maki-it/hostashi/internal/store"
)

const (
	sessionCookie = "hostashi_session"
	maxBodyBytes  = 4096
)

type apiError struct {
	Error apiErrorBody `json:"error"`
}

type apiErrorBody struct {
	Code    string `json:"code"`
	Message string `json:"message"`
	// RequestID lets a user quote the failed request (ADR-0022).
	RequestID string `json:"requestId,omitempty"`
	// Reasons explain a refused CloudPanel import step (the plan's reasons).
	Reasons []plan.Reason `json:"reasons,omitempty"`
}

type userJSON struct {
	Username string   `json:"username"`
	Role     string   `json:"role"`
	Auth     string   `json:"auth,omitempty"`   // "session" or "token", only on /auth/me
	Scopes   []string `json:"scopes,omitempty"` // token scopes, only on /auth/me
	// ClientIP is the address the panel sees for this request, only on
	// /auth/me ("add my current IP" for maintenance, ADR-0027).
	ClientIP string `json:"clientIp,omitempty"`
}

// meJSON is the answer of /auth/me. An account with secondFactorRequired
// and without secondFactor gets 403 second_factor_required on every route
// except the ones that set up TOTP or a passkey (ADR-0035).
type meJSON struct {
	userJSON

	SecondFactor         bool `json:"secondFactor"`
	SecondFactorRequired bool `json:"secondFactorRequired"`
	preferencesJSON
}

// setupStatusJSON is the answer of GET /api/v1/setup.
type setupStatusJSON struct {
	Required bool `json:"required"`
}

// setupRequest creates the first admin.
type setupRequest struct {
	Token    string `json:"token"` // from hostashi setup-token
	Username string `json:"username"`
	Password string `json:"password"`
}

// loginRequest starts a session; TOTP is the code of an account with TOTP.
type loginRequest struct {
	Username string `json:"username"`
	Password string `json:"password"`
	TOTP     string `json:"totp"`
}

// preferencesJSON is how the panel looks for the account (ADR-0037).
type preferencesJSON struct {
	ColorScheme string `json:"colorScheme"`
	Theme       string `json:"theme"`
}

// me returns the second factor state of the account with its name and
// role. It writes the error answer itself.
func (a authn) me(w http.ResponseWriter, r *http.Request, u store.User) (meJSON, bool) {
	has, err := a.svc.HasSecondFactor(r.Context(), u)
	if err != nil {
		a.fail(w, err, "check second factor")

		return meJSON{}, false
	}

	p, err := a.svc.Preferences(r.Context(), u.ID)
	if err != nil {
		a.fail(w, err, "read preferences")

		return meJSON{}, false
	}

	return meJSON{
		userJSON:     userJSON{Username: u.Username, Role: u.Role},
		SecondFactor: has, SecondFactorRequired: auth.SecondFactorRequired(u),
		preferencesJSON: preferencesJSON{ColorScheme: p.ColorScheme, Theme: p.Theme},
	}, true
}

// writeLoggedIn answers a request that started a session, so the panel
// knows at once whether it must ask for a second factor first.
func (a authn) writeLoggedIn(w http.ResponseWriter, r *http.Request, u store.User) {
	if out, ok := a.me(w, r, u); ok {
		writeJSON(w, http.StatusOK, out)
	}
}

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	enc := json.NewEncoder(w)
	enc.SetEscapeHTML(false) // keep & in otpauth URIs readable; the type is application/json
	_ = enc.Encode(v)
}

func writeError(w http.ResponseWriter, code int, errCode, msg string) {
	writeJSON(w, code, apiError{apiErrorBody{Code: errCode, Message: msg, RequestID: w.Header().Get(RequestIDHeader)}})
}

// decodeJSON reads one JSON object with a size limit. It rejects other
// content types, unknown fields and trailing data.
func decodeJSON(w http.ResponseWriter, r *http.Request, v any) bool {
	return decodeJSONLimit(w, r, v, maxBodyBytes)
}

// decodeJSONLimit is decodeJSON with another size limit.
func decodeJSONLimit(w http.ResponseWriter, r *http.Request, v any, limit int64) bool {
	mt, _, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if err != nil || mt != "application/json" {
		writeError(w, http.StatusUnsupportedMediaType, "unsupported_media_type", "Content-Type must be application/json")

		return false
	}

	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, limit))
	dec.DisallowUnknownFields()

	if err := dec.Decode(v); err != nil {
		writeError(w, http.StatusBadRequest, "bad_request", "request body is not valid for this endpoint")

		return false
	}

	if _, err := dec.Token(); !errors.Is(err, io.EOF) {
		writeError(w, http.StatusBadRequest, "bad_request", "request body is not valid for this endpoint")

		return false
	}

	return true
}

// clientIP is the TCP peer address. Forwarded headers are not trusted,
// because anyone who can reach the port could forge them.
func clientIP(r *http.Request) string {
	if ip, ok := r.Context().Value(clientIPKey{}).(string); ok {
		return ip
	}

	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}

	return host
}

// secure wraps the API: security headers, and CSRF defense for unsafe
// methods. A request with an Origin must come from the same host, and a
// browser that says it is cross-site is refused. Cookies are SameSite=Strict
// on top of that.
func secure(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h := w.Header()
		h.Set("X-Content-Type-Options", "nosniff")
		h.Set("X-Frame-Options", "DENY")
		h.Set("Referrer-Policy", "no-referrer")
		h.Set("Content-Security-Policy", "default-src 'none'; frame-ancestors 'none'")
		h.Set("Cache-Control", "no-store")

		switch r.Method {
		case http.MethodGet, http.MethodHead, http.MethodOptions:
		default:
			if !sameOrigin(r) {
				writeError(w, http.StatusForbidden, "forbidden", "cross-site request refused")

				return
			}
		}

		next.ServeHTTP(w, r)
	})
}

func sameOrigin(r *http.Request) bool {
	if site := r.Header.Get("Sec-Fetch-Site"); site != "" && site != "same-origin" && site != "none" {
		return false
	}

	origin := r.Header.Get("Origin")
	if origin == "" {
		return true
	}

	u, err := url.Parse(origin)

	return err == nil && u.Host != "" && strings.EqualFold(u.Host, r.Host)
}

func setSessionCookie(w http.ResponseWriter, token string, maxAge int) {
	http.SetCookie(w, &http.Cookie{
		Name:     sessionCookie,
		Value:    token,
		Path:     "/",
		MaxAge:   maxAge,
		HttpOnly: true,
		Secure:   true,
		SameSite: http.SameSiteStrictMode,
	})
}

func sessionToken(r *http.Request) string {
	c, err := r.Cookie(sessionCookie)
	if err != nil {
		return ""
	}

	return c.Value
}

// failFunc writes the HTTP answer for an error of the auth service.
type failFunc func(w http.ResponseWriter, err error, what string)

func newFail(log *slog.Logger) failFunc {
	return func(w http.ResponseWriter, err error, what string) {
		switch {
		case errors.Is(err, auth.ErrPasskeyUnavailable):
			writeError(w, http.StatusNotImplemented, "passkeys_unavailable", "passkeys are not configured on this server (public URL missing)")
		case errors.Is(err, auth.ErrPasskeyFailed):
			writeError(w, http.StatusUnauthorized, "passkey_failed", "passkey verification failed")
		case errors.Is(err, auth.ErrPasskeyNotFound):
			writeError(w, http.StatusNotFound, "not_found", "no such passkey")
		case errors.Is(err, auth.ErrTooManyPasskeys):
			writeError(w, http.StatusConflict, "too_many_passkeys", "too many passkeys, remove one first")
		case errors.Is(err, auth.ErrBusy):
			w.Header().Set("Retry-After", "60")
			writeError(w, http.StatusServiceUnavailable, "busy", "too many open passkey requests, try again shortly")
		case errors.Is(err, auth.ErrTokenNotAllowedFromIP):
			writeError(w, http.StatusForbidden, "ip_not_allowed", "API tokens are not accepted from this address")
		case errors.Is(err, auth.ErrInvalidToken), errors.Is(err, auth.ErrInvalidAllowlist):
			writeError(w, http.StatusUnprocessableEntity, "invalid_argument", err.Error())
		case errors.Is(err, auth.ErrTooManyTokens):
			writeError(w, http.StatusConflict, "too_many_tokens", "too many API tokens, revoke one first")
		case errors.Is(err, auth.ErrTokenNotFound):
			writeError(w, http.StatusNotFound, "not_found", "no such token")
		case errors.Is(err, auth.ErrUnauthenticated):
			w.Header().Set("WWW-Authenticate", "Bearer")
			writeError(w, http.StatusUnauthorized, "unauthenticated", "not logged in")
		case errors.Is(err, auth.ErrRateLimited):
			w.Header().Set("Retry-After", "900")
			writeError(w, http.StatusTooManyRequests, "rate_limited", "too many attempts, try again later")
		case errors.Is(err, auth.ErrTOTPRequired):
			writeError(w, http.StatusUnauthorized, "totp_required", "a one-time code is required")
		case errors.Is(err, auth.ErrTOTPUnavailable):
			writeError(w, http.StatusNotImplemented, "totp_unavailable", "two-factor authentication is not configured on this server")
		case errors.Is(err, auth.ErrPasskeyRequired):
			writeError(w, http.StatusUnauthorized, "passkey_required", "this account logs in with a passkey")
		case errors.Is(err, auth.ErrSecondFactorRequired):
			writeError(w, http.StatusConflict, "second_factor_required", "this account must keep a second factor (TOTP or a passkey)")
		case errors.Is(err, auth.ErrTOTPState):
			writeError(w, http.StatusConflict, "totp_state", "two-factor authentication is not in the right state for this action")
		case errors.Is(err, auth.ErrInvalidCredentials):
			writeError(w, http.StatusUnauthorized, "invalid_credentials", "invalid username or password")
		case errors.Is(err, auth.ErrSetupNotAllowed):
			writeError(w, http.StatusForbidden, "setup_not_allowed", "setup is not possible with this token")
		case errors.Is(err, auth.ErrWeakPassword), errors.Is(err, auth.ErrInvalidUsername):
			writeError(w, http.StatusUnprocessableEntity, "invalid_argument", err.Error())
		default:
			log.Error(what+" failed", "err", err)
			writeError(w, http.StatusInternalServerError, "internal", "internal error")
		}
	}
}

// registerAuth adds the setup, session and token routes.
func registerAuth(mux *routeMux, svc *auth.Service, m *mailer, log *slog.Logger) {
	fail := newFail(log)
	a := authn{svc: svc, fail: fail}

	mux.Op("GET /api/v1/setup", openapi.Operation{
		ID: "getSetup", Tag: "auth", Summary: "Whether the first admin still has to be set up", Out: setupStatusJSON{},
	}, func(w http.ResponseWriter, r *http.Request) {
		req, err := svc.SetupRequired(r.Context())
		if err != nil {
			fail(w, err, "setup status")

			return
		}

		writeJSON(w, http.StatusOK, setupStatusJSON{Required: req})
	})

	mux.Op("POST /api/v1/setup", openapi.Operation{
		ID: "setup", Tag: "auth", Summary: "Create the first admin with the one-time setup token",
		In: setupRequest{}, Out: userJSON{}, Status: http.StatusCreated,
	}, func(w http.ResponseWriter, r *http.Request) {
		var in setupRequest
		if !decodeJSON(w, r, &in) {
			return
		}

		u, err := svc.Setup(r.Context(), in.Token, in.Username, in.Password, clientIP(r))
		if err != nil {
			fail(w, err, "setup")

			return
		}

		writeJSON(w, http.StatusCreated, userJSON{Username: u.Username, Role: u.Role})
	})

	mux.Op("POST /api/v1/auth/login", openapi.Operation{
		ID: "login", Tag: "auth", Summary: "Log in and start a browser session (cookie)", In: loginRequest{}, Out: meJSON{},
	}, func(w http.ResponseWriter, r *http.Request) {
		var in loginRequest
		if !decodeJSON(w, r, &in) {
			return
		}

		token, u, err := svc.Login(r.Context(), in.Username, in.Password, in.TOTP, clientIP(r))
		if err != nil {
			fail(w, err, "login")

			return
		}

		setSessionCookie(w, token, int(auth.SessionTTL.Seconds()))
		a.writeLoggedIn(w, r, u)
	})

	mux.Op("POST /api/v1/auth/logout", openapi.Operation{
		ID: "logout", Tag: "auth", Summary: "End the browser session",
	}, func(w http.ResponseWriter, r *http.Request) {
		if err := svc.Logout(r.Context(), sessionToken(r)); err != nil {
			fail(w, err, "logout")

			return
		}

		setSessionCookie(w, "", -1)
		w.WriteHeader(http.StatusNoContent)
	})

	// current returns the user of the browser session or writes the 401 itself.
	// Account security changes never accept an API token.
	current := a.session

	mux.HandleFunc("POST /api/v1/auth/totp/enroll", func(w http.ResponseWriter, r *http.Request) {
		u, ok := current(w, r)
		if !ok {
			return
		}

		secret, uri, err := svc.TOTPEnroll(r.Context(), u.ID)
		if err != nil {
			fail(w, err, "totp enroll")

			return
		}

		writeJSON(w, http.StatusOK, map[string]string{"secret": secret, "uri": uri})
	})

	mux.HandleFunc("POST /api/v1/auth/totp/confirm", func(w http.ResponseWriter, r *http.Request) {
		u, ok := current(w, r)
		if !ok {
			return
		}

		var in struct {
			Code string `json:"code"`
		}
		if !decodeJSON(w, r, &in) {
			return
		}

		codes, err := svc.TOTPConfirm(r.Context(), u.ID, in.Code)
		if err != nil {
			fail(w, err, "totp confirm")

			return
		}

		writeJSON(w, http.StatusOK, map[string][]string{"recoveryCodes": codes})
	})

	mux.HandleFunc("POST /api/v1/auth/totp/disable", func(w http.ResponseWriter, r *http.Request) {
		u, ok := current(w, r)
		if !ok {
			return
		}

		var in struct {
			Password string `json:"password"`
			Code     string `json:"code"`
		}
		if !decodeJSON(w, r, &in) {
			return
		}

		if err := svc.TOTPDisable(r.Context(), u.ID, in.Password, in.Code); err != nil {
			fail(w, err, "totp disable")

			return
		}

		w.WriteHeader(http.StatusNoContent)
	})

	mux.Op("GET /api/v1/auth/me", openapi.Operation{
		ID: "getMe", Tag: "auth", Summary: "The caller's account, role and how it is logged in", Out: meJSON{},
	}, func(w http.ResponseWriter, r *http.Request) {
		p, ok := a.principal(w, r)
		if !ok {
			return
		}

		out, ok := a.me(w, r, p.User)
		if !ok {
			return
		}

		out.Auth, out.ClientIP = "session", clientIP(r)
		if p.IsToken() {
			out.Auth, out.Scopes = "token", p.Scopes
		}

		writeJSON(w, http.StatusOK, out)
	})

	// PATCH /api/v1/me changes how the panel looks for the logged-in
	// account (ADR-0037). Only a browser session can change it.
	mux.HandleFunc("PATCH /api/v1/me", func(w http.ResponseWriter, r *http.Request) {
		u, ok := a.session(w, r)
		if !ok || !a.secondFactorOK(w, r, u) {
			return
		}

		var in struct {
			ColorScheme string `json:"colorScheme"`
			Theme       string `json:"theme"`
		}
		if !decodeJSON(w, r, &in) {
			return
		}

		p, err := svc.SetPreferences(r.Context(), u.ID, in.ColorScheme, in.Theme)
		if errors.Is(err, auth.ErrInvalidPreference) {
			writeError(w, http.StatusUnprocessableEntity, "invalid_argument", err.Error())

			return
		}

		if err != nil {
			fail(w, err, "set preferences")

			return
		}

		writeJSON(w, http.StatusOK, preferencesJSON{ColorScheme: p.ColorScheme, Theme: p.Theme})
	})

	registerTokens(mux, a)
	registerUsers(mux, a, m)
	registerPasskeys(mux, a)
}
