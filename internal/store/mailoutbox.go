// SPDX-License-Identifier: AGPL-3.0-or-later

package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"
)

// Outbox states (ADR-0044).
const (
	MailQueued = "queued"
	MailSent   = "sent"
	MailFailed = "failed"
)

// Mail categories (ADR-0044): account emails always go out, operations
// emails are rate limited.
const (
	MailAccount    = "account"
	MailOperations = "operations"
)

// OutboxMail is one row of the outbox. Body is sealed by the caller and
// empty once the message is sent or failed.
type OutboxMail struct {
	ID          int64
	CreatedAt   time.Time
	Recipient   string
	Template    string
	Category    string
	DedupKey    string
	Body        []byte
	State       string
	Attempts    int
	NextAttempt time.Time
	DoneAt      time.Time
	LastError   string
	MessageID   string
}

const outboxCols = `id, created_at, recipient, template, category, dedup_key, body, state, attempts, next_attempt, done_at, last_error, message_id`

func scanOutbox(row interface{ Scan(...any) error }) (OutboxMail, error) {
	var (
		m                       OutboxMail
		created, next, doneUnix int64
	)

	err := row.Scan(&m.ID, &created, &m.Recipient, &m.Template, &m.Category, &m.DedupKey, &m.Body, &m.State,
		&m.Attempts, &next, &doneUnix, &m.LastError, &m.MessageID)
	if err != nil {
		return OutboxMail{}, err
	}

	m.CreatedAt, m.NextAttempt = time.Unix(created, 0), time.Unix(next, 0)
	if doneUnix != 0 {
		m.DoneAt = time.Unix(doneUnix, 0)
	}

	return m, nil
}

// EnqueueMail adds a message that is due right away and returns its ID.
func (s *Store) EnqueueMail(ctx context.Context, m OutboxMail) (int64, error) {
	if len(m.Body) == 0 {
		return 0, errors.New("store: mail body is empty")
	}

	res, err := s.w.ExecContext(ctx,
		`INSERT INTO mail_outbox (created_at, recipient, template, category, dedup_key, body, next_attempt)
		 VALUES (?, ?, ?, ?, ?, ?, ?)`,
		m.CreatedAt.Unix(), m.Recipient, m.Template, m.Category, m.DedupKey, m.Body, m.CreatedAt.Unix())
	if err != nil {
		return 0, fmt.Errorf("store: %w", err)
	}

	id, err := res.LastInsertId()
	if err != nil {
		return 0, fmt.Errorf("store: %w", err)
	}

	return id, nil
}

// MailDedupSince reports whether a message with the key was queued at or
// after since, whatever became of it.
func (s *Store) MailDedupSince(ctx context.Context, key string, since time.Time) (bool, error) {
	var n int

	err := s.db.QueryRowContext(ctx,
		`SELECT count(*) FROM mail_outbox WHERE dedup_key = ? AND dedup_key != '' AND created_at >= ?`, key, since.Unix()).Scan(&n)
	if err != nil {
		return false, fmt.Errorf("store: %w", err)
	}

	return n > 0, nil
}

// DueMail returns queued messages whose next attempt is not after now,
// oldest first.
func (s *Store) DueMail(ctx context.Context, now time.Time, limit int) ([]OutboxMail, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT `+outboxCols+` FROM mail_outbox WHERE state = 'queued' AND next_attempt <= ? ORDER BY next_attempt, id LIMIT ?`,
		now.Unix(), limit)
	if err != nil {
		return nil, fmt.Errorf("store: %w", err)
	}

	defer func() { _ = rows.Close() }()

	var out []OutboxMail

	for rows.Next() {
		m, err := scanOutbox(rows)
		if err != nil {
			return nil, fmt.Errorf("store: %w", err)
		}

		out = append(out, m)
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("store: %w", err)
	}

	return out, nil
}

// SentMailSince counts the messages sent at or after since, to everyone when
// recipient is empty.
func (s *Store) SentMailSince(ctx context.Context, recipient string, since time.Time) (int, error) {
	var n int

	err := s.db.QueryRowContext(ctx,
		`SELECT count(*) FROM mail_outbox WHERE state = 'sent' AND done_at >= ? AND (? = '' OR recipient = ?)`,
		since.Unix(), recipient, recipient).Scan(&n)
	if err != nil {
		return 0, fmt.Errorf("store: %w", err)
	}

	return n, nil
}

func (s *Store) updateMail(ctx context.Context, query string, args ...any) error {
	res, err := s.w.ExecContext(ctx, query, args...)
	if err != nil {
		return fmt.Errorf("store: %w", err)
	}

	if n, err := res.RowsAffected(); err != nil {
		return fmt.Errorf("store: %w", err)
	} else if n == 0 {
		return ErrNotFound
	}

	return nil
}

// MailSent marks a queued message sent and drops its body.
func (s *Store) MailSent(ctx context.Context, id int64, at time.Time, messageID string) error {
	return s.updateMail(ctx,
		`UPDATE mail_outbox SET state = 'sent', body = NULL, attempts = attempts + 1, done_at = ?, message_id = ?, last_error = ''
		 WHERE id = ? AND state = 'queued'`, at.Unix(), messageID, id)
}

// MailRetry records a failed attempt and when to try again.
func (s *Store) MailRetry(ctx context.Context, id int64, next time.Time, lastError string) error {
	return s.updateMail(ctx,
		`UPDATE mail_outbox SET attempts = attempts + 1, next_attempt = ?, last_error = ? WHERE id = ? AND state = 'queued'`,
		next.Unix(), lastError, id)
}

// MailPostpone moves a message to later without counting an attempt, for a
// rate limit.
func (s *Store) MailPostpone(ctx context.Context, id int64, next time.Time) error {
	return s.updateMail(ctx,
		`UPDATE mail_outbox SET next_attempt = ? WHERE id = ? AND state = 'queued'`, next.Unix(), id)
}

// MailFailed gives up on a message and drops its body.
func (s *Store) MailFailed(ctx context.Context, id int64, at time.Time, lastError string, countAttempt bool) error {
	inc := 0
	if countAttempt {
		inc = 1
	}

	return s.updateMail(ctx,
		`UPDATE mail_outbox SET state = 'failed', body = NULL, attempts = attempts + ?, done_at = ?, last_error = ?
		 WHERE id = ? AND state = 'queued'`, inc, at.Unix(), lastError, id)
}

// FailedMailSince returns the messages that failed at or after since, newest
// first, without bodies.
func (s *Store) FailedMailSince(ctx context.Context, since time.Time) ([]OutboxMail, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT `+outboxCols+` FROM mail_outbox WHERE state = 'failed' AND done_at >= ? ORDER BY done_at DESC, id DESC`, since.Unix())
	if err != nil {
		return nil, fmt.Errorf("store: %w", err)
	}

	defer func() { _ = rows.Close() }()

	var out []OutboxMail

	for rows.Next() {
		m, err := scanOutbox(rows)
		if err != nil {
			return nil, fmt.Errorf("store: %w", err)
		}

		out = append(out, m)
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("store: %w", err)
	}

	return out, nil
}

// PruneMail deletes sent and failed messages finished before before.
func (s *Store) PruneMail(ctx context.Context, before time.Time) (int64, error) {
	res, err := s.w.ExecContext(ctx,
		`DELETE FROM mail_outbox WHERE state != 'queued' AND done_at < ?`, before.Unix())
	if err != nil {
		return 0, fmt.Errorf("store: %w", err)
	}

	n, err := res.RowsAffected()
	if err != nil {
		return 0, fmt.Errorf("store: %w", err)
	}

	return n, nil
}

// MailByID returns one message; a missing one is ErrNotFound.
func (s *Store) MailByID(ctx context.Context, id int64) (OutboxMail, error) {
	m, err := scanOutbox(s.db.QueryRowContext(ctx, `SELECT `+outboxCols+` FROM mail_outbox WHERE id = ?`, id))
	if errors.Is(err, sql.ErrNoRows) {
		return OutboxMail{}, ErrNotFound
	}

	if err != nil {
		return OutboxMail{}, fmt.Errorf("store: %w", err)
	}

	return m, nil
}
