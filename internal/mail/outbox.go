// SPDX-License-Identifier: AGPL-3.0-or-later

package mail

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/maki-it/hostashi/internal/secrets"
	"github.com/maki-it/hostashi/internal/store"
)

// Outbox rules (ADR-0044). The limits are estimates for a single server.
const (
	// giveUpAfter is how long a message is retried before it fails.
	giveUpAfter = 24 * time.Hour
	// keepDone is how long sent and failed rows stay, without a body.
	keepDone = 30 * 24 * time.Hour
	// dedupWindow: the same event for the same object at most once.
	dedupWindow = 24 * time.Hour
	// perRecipientHour and perServerHour limit operations emails; account
	// emails are rare and security relevant and are always sent.
	perRecipientHour = 10
	perServerHour    = 100
	// limitWait is how long a message over a limit waits before the next try.
	limitWait = 15 * time.Minute
	// pollEvery is how often the worker looks for due messages.
	pollEvery = 30 * time.Second
	batch     = 50
)

// Category says whether a message is an account email (always on, not rate
// limited) or an operations email.
type Category string

const (
	// Account emails go to the user themself: invitation, password changed.
	Account Category = store.MailAccount
	// Operations emails report on sites and the server.
	Operations Category = store.MailOperations
)

// Envelope describes a message for the outbox and the log.
type Envelope struct {
	// Template names the message in logs and the audit log, for example
	// "invitation"; never the content.
	Template string
	Category Category
	// DedupKey, when set, sends the message at most once per 24 hours, for
	// example "backup-failed:shop".
	DedupKey string
}

// OutboxStore is the part of the panel store the outbox uses.
type OutboxStore interface {
	EnqueueMail(ctx context.Context, m store.OutboxMail) (int64, error)
	MailDedupSince(ctx context.Context, key string, since time.Time) (bool, error)
	DueMail(ctx context.Context, now time.Time, limit int) ([]store.OutboxMail, error)
	SentMailSince(ctx context.Context, recipient string, since time.Time) (int, error)
	MailSent(ctx context.Context, id int64, at time.Time, messageID string) error
	MailRetry(ctx context.Context, id int64, next time.Time, lastError string) error
	MailPostpone(ctx context.Context, id int64, next time.Time) error
	MailFailed(ctx context.Context, id int64, at time.Time, lastError string, countAttempt bool) error
	PruneMail(ctx context.Context, before time.Time) (int64, error)
}

// Relay loads the relay settings.
type Relay interface {
	Load(ctx context.Context) (Config, error)
}

// Outbox queues messages in the panel database and sends them with retries.
type Outbox struct {
	st    OutboxStore
	relay Relay
	box   *secrets.Box
	log   *slog.Logger
	wake  chan struct{}

	// Send and Now are replaced by tests.
	Send func(ctx context.Context, c Config, m Message) (messageID string, err error)
	Now  func() time.Time
}

// NewOutbox returns an outbox; call Run to send.
func NewOutbox(st OutboxStore, relay Relay, box *secrets.Box, log *slog.Logger) *Outbox {
	if log == nil {
		log = slog.New(slog.DiscardHandler)
	}

	return &Outbox{st: st, relay: relay, box: box, log: log, wake: make(chan struct{}, 1), Send: Deliver, Now: time.Now}
}

type sealedMessage struct {
	Subject string `json:"s"`
	Body    string `json:"b"`
}

// sealContext binds a stored message to its recipient, so a row's body
// cannot be moved to another address.
func outboxContext(recipient string) string { return "mail-outbox:" + recipient }

// Enqueue stores a message for sending. It returns ErrNotConfigured while
// email is off, so callers fall back to what the panel shows; false without
// an error means the same event was sent within 24 hours.
func (o *Outbox) Enqueue(ctx context.Context, m Message, e Envelope) (bool, error) {
	if e.Template == "" || (e.Category != Account && e.Category != Operations) {
		return false, errors.New("mail: envelope needs a template and a category")
	}

	if _, err := o.relay.Load(ctx); err != nil {
		return false, err
	}

	if err := m.check(); err != nil {
		return false, err
	}

	now := o.Now()

	if e.DedupKey != "" {
		seen, err := o.st.MailDedupSince(ctx, e.DedupKey, now.Add(-dedupWindow))
		if err != nil || seen {
			return false, err
		}
	}

	pt, err := json.Marshal(sealedMessage{Subject: m.Subject, Body: m.Body})
	if err != nil {
		return false, fmt.Errorf("mail: %w", err)
	}

	sealed, err := o.box.Seal(pt, outboxContext(m.To))
	if err != nil {
		return false, err
	}

	id, err := o.st.EnqueueMail(ctx, store.OutboxMail{
		CreatedAt: now, Recipient: m.To, Template: e.Template, Category: string(e.Category), DedupKey: e.DedupKey, Body: sealed,
	})
	if err != nil {
		return false, err
	}

	o.log.InfoContext(ctx, "mail queued", "id", id, "template", e.Template, "to", m.To)
	o.Wake()

	return true, nil
}

// Wake makes Run look at the outbox now instead of at its next tick.
func (o *Outbox) Wake() {
	select {
	case o.wake <- struct{}{}:
	default:
	}
}

// Run sends due messages until ctx is canceled.
func (o *Outbox) Run(ctx context.Context) {
	t := time.NewTicker(pollEvery)
	defer t.Stop()

	for {
		if err := o.Process(ctx); err != nil && ctx.Err() == nil {
			o.log.ErrorContext(ctx, "mail outbox", "err", err)
		}

		select {
		case <-ctx.Done():
			return
		case <-t.C:
		case <-o.wake:
		}
	}
}

// backoff is the wait after the nth failed attempt.
func backoff(n int) time.Duration {
	steps := []time.Duration{time.Minute, 5 * time.Minute, 15 * time.Minute, 30 * time.Minute, time.Hour}
	if n >= 1 && n <= len(steps) {
		return steps[n-1]
	}

	return 2 * time.Hour
}

// Process sends what is due once.
func (o *Outbox) Process(ctx context.Context) error {
	now := o.Now()

	if _, err := o.st.PruneMail(ctx, now.Add(-keepDone)); err != nil {
		return err
	}

	due, err := o.st.DueMail(ctx, now, batch)
	if err != nil || len(due) == 0 {
		return err
	}

	cfg, err := o.relay.Load(ctx)
	if err != nil && !errors.Is(err, ErrNotConfigured) {
		return err
	}

	configured := err == nil

	for _, m := range due {
		if ctx.Err() != nil {
			return ctx.Err()
		}

		if err := o.one(ctx, cfg, configured, m); err != nil {
			return err
		}
	}

	return nil
}

func (o *Outbox) fail(ctx context.Context, m store.OutboxMail, reason string, attempted bool) error {
	now := o.Now()
	if err := o.st.MailFailed(ctx, m.ID, now, cut(reason), attempted); err != nil {
		return err
	}

	o.log.WarnContext(ctx, "mail failed", "id", m.ID, "template", m.Template, "to", m.Recipient, "state", store.MailFailed, "err", cut(reason))

	return nil
}

func (o *Outbox) one(ctx context.Context, cfg Config, configured bool, m store.OutboxMail) error {
	now := o.Now()
	deadline := m.CreatedAt.Add(giveUpAfter)

	if now.After(deadline) {
		reason := "gave up after 24 hours"
		if m.LastError != "" {
			reason += ": " + m.LastError
		}

		return o.fail(ctx, m, reason, false)
	}

	if !configured {
		// Email was turned off after the message was queued; it waits
		// until the relay is back or the message gives up.
		return o.st.MailPostpone(ctx, m.ID, now.Add(limitWait))
	}

	if Category(m.Category) == Operations {
		over, err := o.overLimit(ctx, m.Recipient, now)
		if err != nil {
			return err
		}

		if over {
			o.log.InfoContext(ctx, "mail waits for the rate limit", "id", m.ID, "template", m.Template, "to", m.Recipient)

			return o.st.MailPostpone(ctx, m.ID, now.Add(limitWait))
		}
	}

	pt, err := o.box.Open(m.Body, outboxContext(m.Recipient))

	var sm sealedMessage
	if err == nil {
		err = json.Unmarshal(pt, &sm)
	}

	if err != nil {
		return o.fail(ctx, m, "the stored message cannot be opened", false)
	}

	msgID, err := o.Send(ctx, cfg, Message{To: m.Recipient, Subject: sm.Subject, Body: sm.Body})
	if err == nil {
		if err := o.st.MailSent(ctx, m.ID, o.Now(), cut(msgID)); err != nil {
			return err
		}

		o.log.InfoContext(ctx, "mail sent", "id", m.ID, "template", m.Template, "to", m.Recipient, "state", store.MailSent, "messageId", msgID)

		return nil
	}

	if ctx.Err() != nil {
		return ctx.Err()
	}

	sum := Summary(err)
	if Permanent(err) {
		return o.fail(ctx, m, sum, true)
	}

	next := o.Now().Add(backoff(m.Attempts + 1))
	if next.After(deadline) {
		return o.fail(ctx, m, "gave up after 24 hours: "+sum, true)
	}

	if err := o.st.MailRetry(ctx, m.ID, next, sum); err != nil {
		return err
	}

	o.log.WarnContext(ctx, "mail attempt failed", "id", m.ID, "template", m.Template, "to", m.Recipient,
		"attempt", m.Attempts+1, "next", next.UTC().Format(time.RFC3339), "err", sum)

	return nil
}

func (o *Outbox) overLimit(ctx context.Context, recipient string, now time.Time) (bool, error) {
	hour := now.Add(-time.Hour)

	n, err := o.st.SentMailSince(ctx, recipient, hour)
	if err != nil || n >= perRecipientHour {
		return n >= perRecipientHour, err
	}

	all, err := o.st.SentMailSince(ctx, "", hour)

	return all >= perServerHour, err
}
