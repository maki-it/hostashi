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
}

type userJSON struct {
	Username string `json:"username"`
	Role     string `json:"role"`
}

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(v)
}

func writeError(w http.ResponseWriter, code int, errCode, msg string) {
	writeJSON(w, code, apiError{apiErrorBody{Code: errCode, Message: msg}})
}

// decodeJSON reads one JSON object with a size limit. It rejects other
// content types, unknown fields and trailing data.
func decodeJSON(w http.ResponseWriter, r *http.Request, v any) bool {
	mt, _, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if err != nil || mt != "application/json" {
		writeError(w, http.StatusUnsupportedMediaType, "unsupported_media_type", "Content-Type must be application/json")

		return false
	}

	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxBodyBytes))
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

// registerAuth adds the setup and session routes.
func registerAuth(mux *http.ServeMux, svc *auth.Service, log *slog.Logger) {
	fail := func(w http.ResponseWriter, err error, what string) {
		switch {
		case errors.Is(err, auth.ErrRateLimited):
			w.Header().Set("Retry-After", "900")
			writeError(w, http.StatusTooManyRequests, "rate_limited", "too many attempts, try again later")
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

	mux.HandleFunc("GET /api/v1/setup", func(w http.ResponseWriter, r *http.Request) {
		req, err := svc.SetupRequired(r.Context())
		if err != nil {
			fail(w, err, "setup status")

			return
		}

		writeJSON(w, http.StatusOK, map[string]bool{"required": req})
	})

	mux.HandleFunc("POST /api/v1/setup", func(w http.ResponseWriter, r *http.Request) {
		var in struct {
			Token    string `json:"token"`
			Username string `json:"username"`
			Password string `json:"password"`
		}
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

	mux.HandleFunc("POST /api/v1/auth/login", func(w http.ResponseWriter, r *http.Request) {
		var in struct {
			Username string `json:"username"`
			Password string `json:"password"`
		}
		if !decodeJSON(w, r, &in) {
			return
		}

		token, u, err := svc.Login(r.Context(), in.Username, in.Password, clientIP(r))
		if err != nil {
			fail(w, err, "login")

			return
		}

		setSessionCookie(w, token, int(auth.SessionTTL.Seconds()))
		writeJSON(w, http.StatusOK, userJSON{Username: u.Username, Role: u.Role})
	})

	mux.HandleFunc("POST /api/v1/auth/logout", func(w http.ResponseWriter, r *http.Request) {
		if err := svc.Logout(r.Context(), sessionToken(r)); err != nil {
			fail(w, err, "logout")

			return
		}

		setSessionCookie(w, "", -1)
		w.WriteHeader(http.StatusNoContent)
	})

	mux.HandleFunc("GET /api/v1/auth/me", func(w http.ResponseWriter, r *http.Request) {
		u, err := svc.Authenticate(r.Context(), sessionToken(r))
		if errors.Is(err, auth.ErrUnauthenticated) {
			writeError(w, http.StatusUnauthorized, "unauthenticated", "not logged in")

			return
		}

		if err != nil {
			fail(w, err, "me")

			return
		}

		writeJSON(w, http.StatusOK, userJSON{Username: u.Username, Role: u.Role})
	})
}
