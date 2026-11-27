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

	"github.com/maki-it/hostashi/internal/openapi"
	"github.com/maki-it/hostashi/internal/store"

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

type emailConfirmedJSON struct {
	Email string `json:"email"`
}

type accountEmailRequest struct {
	Email string `json:"email"`
}

type emailConfirmRequest struct {
	Token string `json:"token"`
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

	mux.Op("GET /api/v1/account/email", openapi.Operation{
		ID: "getAccountEmail", Tag: "account", Summary: "The caller's email address and a pending change", Out: accountEmailJSON{},
	}, func(w http.ResponseWriter, r *http.Request) {
		u, ok := a.session(w, r)
		if !ok || !a.secondFactorOK(w, r, u) {
			return
		}

		state(w, r, u.ID)
	})

	mux.Op("PUT /api/v1/account/email", openapi.Operation{
		ID: "setAccountEmail", Tag: "account", Summary: "Set a new email address; a confirmation link is sent there", In: accountEmailRequest{}, Out: accountEmailJSON{},
	}, func(w http.ResponseWriter, r *http.Request) {
		u, ok := a.session(w, r)
		if !ok || !a.secondFactorOK(w, r, u) {
			return
		}

		var in accountEmailRequest
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

		msg, env := mail.ConfirmEmail(emailLang(r.Context(), a.svc.Preferences, u.ID), address, u.Username, panelLink(publicURL, "confirm-email/"+link.Secret), panelHost(publicURL), link.Expires)
		if _, err := outbox.Enqueue(r.Context(), msg, env); err != nil {
			log.ErrorContext(r.Context(), "queue confirmation email", "err", mail.Summary(err))
			writeError(w, http.StatusBadGateway, "mail_failed", "the confirmation email could not be queued")

			return
		}

		state(w, r, u.ID)
	})

	mux.Op("DELETE /api/v1/account/email", openapi.Operation{
		ID: "deleteAccountEmail", Tag: "account", Summary: "Remove the caller's email address",
	}, func(w http.ResponseWriter, r *http.Request) {
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
	mux.Op("POST /api/v1/account/email/confirm", openapi.Operation{
		ID: "confirmAccountEmail", Tag: "account", Summary: "Confirm an email address with the token from the link", In: emailConfirmRequest{}, Out: emailConfirmedJSON{},
	}, func(w http.ResponseWriter, r *http.Request) {
		var in emailConfirmRequest
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
			msg, env := mail.EmailChanged(emailLang(r.Context(), a.svc.Preferences, u.ID), old, u.Username, address, panelHost(publicURL), time.Now())
			if _, err := outbox.Enqueue(r.Context(), msg, env); err != nil && !errors.Is(err, mail.ErrNotConfigured) {
				log.WarnContext(r.Context(), "queue address change note", "err", mail.Summary(err))
			}
		}

		writeJSON(w, http.StatusOK, emailConfirmedJSON{Email: address})
	})
}

// mailer sends the account emails of ADR-0044 through the outbox. A nil
// mailer, or one without a relay, sends nothing: links are then only shown.
type mailer struct {
	relay     MailRelay
	outbox    MailOutbox
	publicURL string
	log       *slog.Logger
	// who returns the user name and confirmed address of an account.
	who func(ctx context.Context, userID int64) (username, address string, err error)
	// prefs returns the settings of an account, for the language of its
	// emails; nil sends English.
	prefs func(ctx context.Context, userID int64) (store.Preferences, error)
}

// emailLang returns the language of the emails to an account (ADR-0071):
// its setting, English for auto or when it cannot be read.
func emailLang(ctx context.Context, prefs func(context.Context, int64) (store.Preferences, error), userID int64) mail.Lang {
	if prefs == nil {
		return mail.English
	}

	p, err := prefs(ctx, userID)
	if err != nil {
		return mail.English
	}

	return mail.Language(p.Language)
}

// ready reports whether email can be sent now: a relay and a public URL
// for links.
func (m *mailer) ready(ctx context.Context) bool {
	if m == nil || panelLink(m.publicURL, "") == "" {
		return false
	}

	_, err := m.relay.Load(ctx)

	return err == nil
}

// send queues a message and reports whether it was queued. Failures are
// logged, never returned: the panel still shows what the email would hold.
func (m *mailer) send(ctx context.Context, msg mail.Message, env mail.Envelope) bool {
	if m == nil {
		return false
	}

	ok, err := m.outbox.Enqueue(ctx, msg, env)
	if err != nil && !errors.Is(err, mail.ErrNotConfigured) {
		m.log.WarnContext(ctx, "queue email", "template", env.Template, "err", mail.Summary(err))
	}

	return ok && err == nil
}

// invitation emails an invitation or reset link to inv.Email. A reset is
// in the language of the account (to); a new account has no language yet,
// so its invitation is in the language of the admin who sent it (from).
func (m *mailer) invitation(ctx context.Context, from, to int64, actor, username, role string, reset bool, inv auth.NewInvitation) bool {
	if inv.Email == "" || !m.ready(ctx) {
		return false
	}

	link := panelLink(m.publicURL, "invite/"+inv.Secret)
	host := panelHost(m.publicURL)

	if reset {
		msg, env := mail.ResetLink(emailLang(ctx, m.prefs, to), inv.Email, actor, username, link, host, inv.Expires)

		return m.send(ctx, msg, env)
	}

	msg, env := mail.Invitation(emailLang(ctx, m.prefs, from), inv.Email, actor, username, role, link, host, inv.Expires)

	return m.send(ctx, msg, env)
}

// accountEvent tells the account's confirmed address about a security
// change. It is the auth service's account event hook.
func (m *mailer) accountEvent(ctx context.Context, e auth.AccountEvent) {
	if m == nil || m.who == nil {
		return
	}

	username, addr, err := m.who(ctx, e.UserID)
	if err != nil || addr == "" {
		return
	}

	msg, env, ok := mail.AccountSecurity(emailLang(ctx, m.prefs, e.UserID), addr, username, e.Kind, e.Name, e.IP, panelHost(m.publicURL), time.Now())
	if ok {
		m.send(ctx, msg, env)
	}
}
