// SPDX-License-Identifier: AGPL-3.0-or-later

package serve

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/maki-it/hostashi/internal/auth"
	"github.com/maki-it/hostashi/internal/mail"
)

// MailOutbox queues email (ADR-0044).
type MailOutbox interface {
	Enqueue(ctx context.Context, m mail.Message, e mail.Envelope) (bool, error)
}

// panelLink builds a link into the panel from the configured public URL,
// never from the request's Host header (ADR-0044). Empty when the panel has
// no public URL.
func panelLink(publicURL, fragment string) string {
	u, err := url.Parse(publicURL)
	if publicURL == "" || err != nil || (u.Scheme != "https" && u.Scheme != "http") || u.Host == "" {
		return ""
	}

	return strings.TrimRight(publicURL, "/") + "/#/" + fragment
}

func panelHost(publicURL string) string {
	if u, err := url.Parse(publicURL); err == nil {
		return u.Host
	}

	return ""
}

type accountEmailJSON struct {
	Email          string     `json:"email"`
	Pending        string     `json:"pending"`
	PendingExpires *time.Time `json:"pending_expires,omitempty"`
	// MailEnabled tells the page whether an address can be added now.
	MailEnabled bool `json:"mail_enabled"`
}

// registerAccountEmail adds the account's own email address (ADR-0044): it
// is used only after the user opened the link sent to it.
func registerAccountEmail(mux *routeMux, a authn, relay MailRelay, outbox MailOutbox, publicURL string, log *slog.Logger) {
	enabled := func(ctx context.Context) (bool, error) {
		_, err := relay.Load(ctx)
		if errors.Is(err, mail.ErrNotConfigured) {
			return false, nil
		}

		return err == nil, err
	}

	state := func(w http.ResponseWriter, r *http.Request, uid int64) {
		e, err := a.svc.EmailState(r.Context(), uid)
		if err != nil {
			a.fail(w, err, "read email")

			return
		}

		on, err := enabled(r.Context())
		if err != nil {
			a.fail(w, err, "read mail settings")

			return
		}

		out := accountEmailJSON{Email: e.Confirmed, Pending: e.Pending, MailEnabled: on && panelLink(publicURL, "") != ""}
		if e.Pending != "" {
			t := e.PendingExpires.UTC()
			out.PendingExpires = &t
		}

		writeJSON(w, http.StatusOK, out)
	}

	mux.HandleFunc("GET /api/v1/account/email", func(w http.ResponseWriter, r *http.Request) {
		u, ok := a.session(w, r)
		if !ok || !a.secondFactorOK(w, r, u) {
			return
		}

		state(w, r, u.ID)
	})

	mux.HandleFunc("PUT /api/v1/account/email", func(w http.ResponseWriter, r *http.Request) {
		u, ok := a.session(w, r)
		if !ok || !a.secondFactorOK(w, r, u) {
			return
		}

		var in struct {
			Email string `json:"email"`
		}
		if !decodeJSON(w, r, &in) {
			return
		}

		on, err := enabled(r.Context())
		if err != nil {
			a.fail(w, err, "read mail settings")

			return
		}

		if !on {
			writeError(w, http.StatusConflict, "mail_not_configured", "an administrator has to set up email first")

			return
		}

		if panelLink(publicURL, "") == "" {
			writeError(w, http.StatusConflict, "no_public_url", "the panel has no public address for links")

			return
		}

		address, link, err := a.svc.RequestEmail(r.Context(), u, in.Email, clientIP(r))

		switch {
		case errors.Is(err, auth.ErrEmailInvalid), errors.Is(err, auth.ErrEmailUnchanged):
			writeError(w, http.StatusUnprocessableEntity, "invalid_argument", strings.TrimPrefix(err.Error(), "auth: "))

			return
		case errors.Is(err, auth.ErrRateLimited):
			writeError(w, http.StatusTooManyRequests, "rate_limited", "too many addresses in the last hour, try again later")

			return
		case err != nil:
			a.fail(w, err, "request email")

			return
		}

		msg, env := mail.ConfirmEmail(address, u.Username, panelLink(publicURL, "confirm-email/"+link.Secret), panelHost(publicURL), link.Expires)
		if _, err := outbox.Enqueue(r.Context(), msg, env); err != nil {
			log.ErrorContext(r.Context(), "queue confirmation email", "err", mail.Summary(err))
			writeError(w, http.StatusBadGateway, "mail_failed", "the confirmation email could not be queued")

			return
		}

		state(w, r, u.ID)
	})

	mux.HandleFunc("DELETE /api/v1/account/email", func(w http.ResponseWriter, r *http.Request) {
		u, ok := a.session(w, r)
		if !ok || !a.secondFactorOK(w, r, u) {
			return
		}

		if _, err := a.svc.RemoveEmail(r.Context(), u, clientIP(r)); err != nil {
			a.fail(w, err, "remove email")

			return
		}

		w.WriteHeader(http.StatusNoContent)
	})

	// Public: the link may be opened in a browser without a session. The
	// secret in the link is the proof.
	mux.HandleFunc("POST /api/v1/account/email/confirm", func(w http.ResponseWriter, r *http.Request) {
		var in struct {
			Token string `json:"token"`
		}
		if !decodeJSON(w, r, &in) {
			return
		}

		u, old, address, err := a.svc.ConfirmEmail(r.Context(), in.Token, clientIP(r))

		switch {
		case errors.Is(err, auth.ErrEmailLinkInvalid):
			writeError(w, http.StatusNotFound, "link_invalid", "the confirmation link is invalid or expired")

			return
		case errors.Is(err, auth.ErrRateLimited):
			writeError(w, http.StatusTooManyRequests, "rate_limited", "too many attempts, try again later")

			return
		case err != nil:
			a.fail(w, err, "confirm email")

			return
		}

		if old != "" && old != address {
			msg, env := mail.EmailChanged(old, u.Username, address, panelHost(publicURL), time.Now())
			if _, err := outbox.Enqueue(r.Context(), msg, env); err != nil && !errors.Is(err, mail.ErrNotConfigured) {
				log.WarnContext(r.Context(), "queue address change note", "err", mail.Summary(err))
			}
		}

		writeJSON(w, http.StatusOK, map[string]string{"email": address})
	})
}
