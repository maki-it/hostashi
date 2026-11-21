// SPDX-License-Identifier: AGPL-3.0-or-later

package serve

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"strconv"
	"sync"
	"time"

	"github.com/maki-it/hostashi/internal/authz"
	"github.com/maki-it/hostashi/internal/mail"
)

// MailRelay is the panel's SMTP relay settings (ADR-0044).
type MailRelay interface {
	Load(ctx context.Context) (mail.Config, error)
	Save(ctx context.Context, c mail.Config) (mail.Config, error)
	Clear(ctx context.Context) error
}

// MailSender delivers a message; mail.Send in production, tests replace it.
type MailSender func(ctx context.Context, c mail.Config, m mail.Message) error

// mailTestLimit is how many test emails the panel sends per hour, so the
// button cannot be used to flood a mailbox through the relay.
const mailTestLimit = 10

type mailSettingsJSON struct {
	Configured  bool         `json:"configured"`
	Host        string       `json:"host"`
	Port        int          `json:"port"`
	TLS         mail.TLSMode `json:"tls"`
	Username    string       `json:"username"`
	PasswordSet bool         `json:"password_set"`
	From        string       `json:"from"`
	FromName    string       `json:"from_name"`
	ReplyTo     string       `json:"reply_to"`
	CAPEM       string       `json:"ca_pem"`
}

func mailSettingsOut(c mail.Config) mailSettingsJSON {
	return mailSettingsJSON{
		Configured: true, Host: c.Host, Port: c.Port, TLS: c.TLS, Username: c.Username,
		PasswordSet: c.Password != "", From: c.From, FromName: c.FromName, ReplyTo: c.ReplyTo, CAPEM: c.CAPEM,
	}
}

// window counts events in the last hour.
type window struct {
	mu    sync.Mutex
	times []time.Time
}

func (w *window) take(now time.Time, limit int) bool {
	w.mu.Lock()
	defer w.mu.Unlock()

	keep := w.times[:0]

	for _, t := range w.times {
		if now.Sub(t) < time.Hour {
			keep = append(keep, t)
		}
	}

	w.times = keep
	if len(w.times) >= limit {
		return false
	}

	w.times = append(w.times, now)

	return true
}

// registerMail adds the relay settings and the test button. Admins only,
// with a browser session: the relay account can send in the operator's
// name, so no token reaches it.
func registerMail(mux *routeMux, a authn, relay MailRelay, send MailSender, st Settings, log *slog.Logger) {
	tests := &window{}

	load := func(w http.ResponseWriter, r *http.Request) (mail.Config, bool, bool) {
		c, err := relay.Load(r.Context())
		if errors.Is(err, mail.ErrNotConfigured) {
			return mail.Config{}, false, true
		}

		if err != nil {
			log.ErrorContext(r.Context(), "read mail settings", "err", err)
			writeError(w, http.StatusInternalServerError, "internal", "internal error")

			return mail.Config{}, false, false
		}

		return c, true, true
	}

	mux.HandleFunc("GET /api/v1/settings/mail", func(w http.ResponseWriter, r *http.Request) {
		if _, ok := a.sessionAllow(w, r, authz.SettingsRead); !ok {
			return
		}

		c, configured, ok := load(w, r)
		if !ok {
			return
		}

		if !configured {
			writeJSON(w, http.StatusOK, mailSettingsJSON{})

			return
		}

		writeJSON(w, http.StatusOK, mailSettingsOut(c))
	})

	mux.HandleFunc("PUT /api/v1/settings/mail", func(w http.ResponseWriter, r *http.Request) {
		u, ok := a.sessionAllow(w, r, authz.SettingsWrite)
		if !ok {
			return
		}

		var in struct {
			Host     string       `json:"host"`
			Port     int          `json:"port"`
			TLS      mail.TLSMode `json:"tls"`
			Username string       `json:"username"`
			// Password nil keeps the stored one, "" removes it.
			Password *string `json:"password"`
			From     string  `json:"from"`
			FromName string  `json:"from_name"`
			ReplyTo  string  `json:"reply_to"`
			CAPEM    string  `json:"ca_pem"`
		}
		if !decodeJSON(w, r, &in) {
			return
		}

		old, configured, ok := load(w, r)
		if !ok {
			return
		}

		c := mail.Config{
			Host: in.Host, Port: in.Port, TLS: in.TLS, Username: in.Username,
			From: in.From, FromName: in.FromName, ReplyTo: in.ReplyTo, CAPEM: in.CAPEM,
		}

		pw := "kept"

		switch {
		case in.Password != nil:
			c.Password = *in.Password
			pw = "changed"

			if c.Password == "" {
				pw = "removed"
			}
		case configured && in.Username == old.Username:
			c.Password = old.Password
		default:
			// A new user name never inherits the old password.
			pw = "removed"
		}

		saved, err := relay.Save(r.Context(), c)
		if errors.Is(err, mail.ErrInvalid) {
			writeError(w, http.StatusUnprocessableEntity, "invalid_argument", err.Error())

			return
		}

		if err != nil {
			log.ErrorContext(r.Context(), "store mail settings", "err", err)
			writeError(w, http.StatusInternalServerError, "internal", "internal error")

			return
		}

		uid := u.ID
		_ = st.Audit(r.Context(), &uid, "settings.mail", saved.Host, map[string]string{
			"port": strconv.Itoa(saved.Port), "tls": string(saved.TLS), "username": saved.Username,
			"password": pw, "from": saved.From, "ip": clientIP(r),
		})

		writeJSON(w, http.StatusOK, mailSettingsOut(saved))
	})

	mux.HandleFunc("DELETE /api/v1/settings/mail", func(w http.ResponseWriter, r *http.Request) {
		u, ok := a.sessionAllow(w, r, authz.SettingsWrite)
		if !ok {
			return
		}

		if err := relay.Clear(r.Context()); err != nil {
			log.ErrorContext(r.Context(), "clear mail settings", "err", err)
			writeError(w, http.StatusInternalServerError, "internal", "internal error")

			return
		}

		uid := u.ID
		_ = st.Audit(r.Context(), &uid, "settings.mail.clear", "", map[string]string{"ip": clientIP(r)})

		w.WriteHeader(http.StatusNoContent)
	})

	mux.HandleFunc("POST /api/v1/settings/mail/test", func(w http.ResponseWriter, r *http.Request) {
		u, ok := a.sessionAllow(w, r, authz.SettingsWrite)
		if !ok {
			return
		}

		var in struct {
			To string `json:"to"`
		}
		if !decodeJSON(w, r, &in) {
			return
		}

		to, err := mail.ParseAddress(in.To)
		if err != nil {
			writeError(w, http.StatusUnprocessableEntity, "invalid_argument", "to must be a single address like name@example.com")

			return
		}

		c, configured, ok := load(w, r)
		if !ok {
			return
		}

		if !configured {
			writeError(w, http.StatusConflict, "mail_not_configured", "set up the SMTP relay first")

			return
		}

		if !tests.take(time.Now(), mailTestLimit) {
			writeError(w, http.StatusTooManyRequests, "rate_limited", "at most 10 test emails per hour")

			return
		}

		msg := mail.Message{
			To:      to,
			Subject: "Test email from Hostashi",
			Body: "This is a test email from the Hostashi panel.\n\n" +
				"It was sent by " + u.Username + " through " + c.Host + ":" + strconv.Itoa(c.Port) + ".\n" +
				"If you did not expect it, you can ignore it.\n",
		}

		uid := u.ID
		detail := map[string]string{"to": to, "ip": clientIP(r)}

		if err := send(r.Context(), c, msg); err != nil {
			sum := mail.Summary(err)
			detail["result"] = "failed"

			_ = st.Audit(r.Context(), &uid, "settings.mail.test", c.Host, detail)
			log.WarnContext(r.Context(), "test email failed", "relay", c.Host, "err", sum)
			writeError(w, http.StatusBadGateway, "mail_failed", sum)

			return
		}

		detail["result"] = "sent"
		_ = st.Audit(r.Context(), &uid, "settings.mail.test", c.Host, detail)

		writeJSON(w, http.StatusOK, map[string]bool{"sent": true})
	})
}
