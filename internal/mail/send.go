// SPDX-License-Identifier: AGPL-3.0-or-later

package mail

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"

	gomail "github.com/wneessen/go-mail"
)

// sendTimeout bounds one connection to the relay (ADR-0044); tests make it
// shorter.
var sendTimeout = 30 * time.Second

// Limits of a message.
const (
	maxSubjectLen = 200
	maxBodyLen    = 64 << 10
)

// Message is one plain-text email. Templates build it; no caller sets a
// header of its own.
type Message struct {
	To      string
	Subject string
	Body    string
}

func (m Message) check() error {
	if _, err := ParseAddress(m.To); err != nil {
		return err
	}

	if m.Subject == "" || utf8.RuneCountInString(m.Subject) > maxSubjectLen || hasControl(m.Subject) {
		return invalid("subject must be 1 to %d characters without control characters", maxSubjectLen)
	}

	if len(m.Body) > maxBodyLen || !utf8.ValidString(m.Body) {
		return invalid("body is too long or not UTF-8")
	}

	return nil
}

// tlsConfig verifies the relay's certificate against the system roots and
// the optional extra CA. There is no way to turn verification off.
func tlsConfig(c Config) (*tls.Config, error) {
	cfg := &tls.Config{ServerName: c.Host, MinVersion: tls.VersionTLS12}

	if c.CAPEM != "" {
		pool, err := x509.SystemCertPool()
		if err != nil || pool == nil {
			pool = x509.NewCertPool()
		}

		if !pool.AppendCertsFromPEM([]byte(c.CAPEM)) {
			return nil, invalid("ca_pem holds no PEM certificate")
		}

		cfg.RootCAs = pool
	}

	return cfg, nil
}

func client(c Config) (*gomail.Client, error) {
	tc, err := tlsConfig(c)
	if err != nil {
		return nil, err
	}

	opts := []gomail.Option{
		gomail.WithPort(c.Port),
		gomail.WithTimeout(sendTimeout),
		gomail.WithTLSConfig(tc),
	}

	if c.TLS == TLSImplicit {
		opts = append(opts, gomail.WithSSL())
	} else {
		// STARTTLS is required: a relay that does not offer it fails
		// before any login or message is sent.
		opts = append(opts, gomail.WithTLSPolicy(gomail.TLSMandatory))
	}

	if c.Username != "" {
		// The strongest mechanism the relay offers; always inside TLS.
		opts = append(opts,
			gomail.WithSMTPAuth(gomail.SMTPAuthAutoDiscover),
			gomail.WithUsername(c.Username),
			gomail.WithPassword(c.Password),
		)
	}

	cl, err := gomail.NewClient(c.Host, opts...)
	if err != nil {
		return nil, fmt.Errorf("mail: %w", err)
	}

	return cl, nil
}

func build(c Config, m Message) (*gomail.Msg, error) {
	msg := gomail.NewMsg()

	var err error
	if c.FromName != "" {
		err = msg.FromFormat(c.FromName, c.From)
	} else {
		err = msg.From(c.From)
	}

	if err == nil {
		err = msg.To(m.To)
	}

	if err == nil && c.ReplyTo != "" {
		err = msg.ReplyTo(c.ReplyTo)
	}

	if err != nil {
		return nil, invalid("address refused")
	}

	msg.Subject(m.Subject)
	msg.SetDate()
	msg.SetMessageID()
	msg.SetBodyString(gomail.TypeTextPlain, m.Body)

	return msg, nil
}

// Send delivers the messages over one connection. The error is safe to log
// only through Summary: it may carry the server's answer.
func Send(ctx context.Context, c Config, msgs ...Message) error {
	c, err := c.Validate()
	if err != nil {
		return err
	}

	built := make([]*gomail.Msg, 0, len(msgs))

	for _, m := range msgs {
		if err := m.check(); err != nil {
			return err
		}

		msg, err := build(c, m)
		if err != nil {
			return err
		}

		built = append(built, msg)
	}

	cl, err := client(c)
	if err != nil {
		return err
	}

	ctx, cancel := context.WithTimeout(ctx, sendTimeout)
	defer cancel()

	if err := cl.DialAndSendWithContext(ctx, built...); err != nil {
		return fmt.Errorf("mail: %w", err)
	}

	return nil
}

const maxSummaryLen = 200

// Summary turns a send error into one short line for a log or an answer:
// the SMTP status code where there is one, otherwise the first line of the
// error. It never holds a message body, and the password is never part of
// an error.
func Summary(err error) string {
	if err == nil {
		return ""
	}

	if errors.Is(err, ErrInvalid) {
		return cut(err.Error())
	}

	var se *gomail.SendError
	if errors.As(err, &se) {
		s := "send failed: " + se.Reason.String()
		if code := se.ErrorCode(); code != 0 {
			s += fmt.Sprintf(" (SMTP %d", code)
			if esc := se.EnhancedStatusCode(); esc != "" && esc != "0.0.0" {
				s += " " + esc
			}

			s += ")"
		}

		if se.IsTemp() {
			s += ", temporary"
		}

		return cut(s)
	}

	return cut(err.Error())
}

func cut(s string) string {
	s, _, _ = strings.Cut(s, "\n")
	s = strings.TrimSpace(strings.ReplaceAll(s, "\r", ""))

	if utf8.RuneCountInString(s) > maxSummaryLen {
		r := []rune(s)
		s = string(r[:maxSummaryLen]) + "…"
	}

	return s
}
