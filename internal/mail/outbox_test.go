// SPDX-License-Identifier: AGPL-3.0-or-later

package mail

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/maki-it/hostashi/internal/mail/smtptest"
	"github.com/maki-it/hostashi/internal/secrets"
	"github.com/maki-it/hostashi/internal/store"
)

type fixedRelay struct {
	cfg Config
	off bool
}

func (r *fixedRelay) Load(context.Context) (Config, error) {
	if r.off {
		return Config{}, ErrNotConfigured
	}

	return r.cfg, nil
}

type syncBuf struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (s *syncBuf) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	return s.b.Write(p)
}

func (s *syncBuf) String() string {
	s.mu.Lock()
	defer s.mu.Unlock()

	return s.b.String()
}

type outboxEnv struct {
	o     *Outbox
	st    *store.Store
	relay *fixedRelay
	now   time.Time
	logs  *syncBuf
	sent  []Message
	fail  error
}

func newOutbox(t *testing.T) *outboxEnv {
	t.Helper()

	dir := filepath.Join(t.TempDir(), "lib")
	if err := os.Mkdir(dir, 0o750); err != nil {
		t.Fatal(err)
	}

	st, err := store.Open(t.Context(), filepath.Join(dir, "h.db"))
	if err != nil {
		t.Fatal(err)
	}

	t.Cleanup(func() { _ = st.Close() })

	box, err := secrets.New(bytes.Repeat([]byte{5}, 32))
	if err != nil {
		t.Fatal(err)
	}

	e := &outboxEnv{
		st:    st,
		relay: &fixedRelay{cfg: Config{Host: "smtp.example.com", Port: 587, TLS: TLSStartTLS, From: "panel@example.com"}},
		now:   time.Date(2026, 11, 21, 10, 0, 0, 0, time.UTC),
		logs:  &syncBuf{},
	}

	e.o = NewOutbox(st, e.relay, box, slog.New(slog.NewTextHandler(e.logs, nil)))
	e.o.Now = func() time.Time { return e.now }
	e.o.Send = func(_ context.Context, _ Config, m Message) (string, error) {
		if e.fail != nil {
			return "", e.fail
		}

		e.sent = append(e.sent, m)

		return fmt.Sprintf("<%d@example.com>", len(e.sent)), nil
	}

	return e
}

func (e *outboxEnv) mail(t *testing.T, id int64) store.OutboxMail {
	t.Helper()

	m, err := e.st.MailByID(t.Context(), id)
	if err != nil {
		t.Fatal(err)
	}

	return m
}

func (e *outboxEnv) process(t *testing.T) {
	t.Helper()

	if err := e.o.Process(t.Context()); err != nil {
		t.Fatal(err)
	}
}

const secretLink = "https://panel.example.com/#/invite/abcdefghijklmnopqrstuvwxyz0123456789"

var invite = Message{To: "kim@example.com", Subject: "You are invited to Hostashi", Body: "Open " + secretLink + " within 24 hours.\n"}

func TestOutboxSendsAndForgetsBody(t *testing.T) {
	e := newOutbox(t)

	ok, err := e.o.Enqueue(t.Context(), invite, Envelope{Template: "invitation", Category: Account})
	if err != nil || !ok {
		t.Fatal(ok, err)
	}

	var n int
	if err := e.st.RawCount(t.Context(), `SELECT count(*) FROM mail_outbox WHERE CAST(body AS TEXT) LIKE ?`, "%invite%", &n); err != nil || n != 0 {
		t.Fatalf("body stored in clear: %d %v", n, err)
	}

	e.process(t)

	if len(e.sent) != 1 || e.sent[0] != invite {
		t.Fatalf("sent: %+v", e.sent)
	}

	m := e.mail(t, 1)
	if m.State != store.MailSent || m.Body != nil || m.MessageID != "<1@example.com>" || m.Attempts != 1 {
		t.Fatalf("%+v", m)
	}

	if strings.Contains(e.logs.String(), "invite/") || strings.Contains(e.logs.String(), "You are invited") {
		t.Fatalf("link or subject in the log:\n%s", e.logs)
	}

	// Sent rows go after 30 days.
	e.now = e.now.Add(31 * 24 * time.Hour)
	e.process(t)

	if _, err := e.st.MailByID(t.Context(), 1); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("not pruned: %v", err)
	}
}

func TestOutboxOffRefusesToQueue(t *testing.T) {
	e := newOutbox(t)
	e.relay.off = true

	if ok, err := e.o.Enqueue(t.Context(), invite, Envelope{Template: "invitation", Category: Account}); ok || !errors.Is(err, ErrNotConfigured) {
		t.Fatal(ok, err)
	}

	if ok, err := e.o.Enqueue(t.Context(), invite, Envelope{Template: "", Category: Account}); ok || err == nil {
		t.Fatal("empty template accepted")
	}

	e.relay.off = false

	bad := invite
	bad.To = "kim@example.com\r\nBcc: x@example.com"

	if _, err := e.o.Enqueue(t.Context(), bad, Envelope{Template: "invitation", Category: Account}); !errors.Is(err, ErrInvalid) {
		t.Fatal(err)
	}
}

func TestOutboxRetriesThenGivesUp(t *testing.T) {
	e := newOutbox(t)
	e.fail = errors.New("dial failed: dial tcp 192.0.2.1:587: i/o timeout")

	if _, err := e.o.Enqueue(t.Context(), invite, Envelope{Template: "invitation", Category: Account}); err != nil {
		t.Fatal(err)
	}

	start := e.now
	e.process(t)

	m := e.mail(t, 1)
	if m.State != store.MailQueued || m.Attempts != 1 || !m.NextAttempt.Equal(start.Add(time.Minute)) || !strings.Contains(m.LastError, "i/o timeout") {
		t.Fatalf("after 1: %+v", m)
	}

	// Not due yet: nothing happens.
	e.now = start.Add(30 * time.Second)
	e.process(t)

	if e.mail(t, 1).Attempts != 1 {
		t.Fatal("retried too early")
	}

	// Walk through the backoff until 24 hours are over.
	for i := 0; i < 40 && e.mail(t, 1).State == store.MailQueued; i++ {
		e.now = e.mail(t, 1).NextAttempt
		e.process(t)
	}

	m = e.mail(t, 1)
	if m.State != store.MailFailed || m.Body != nil || !strings.HasPrefix(m.LastError, "gave up after 24 hours") {
		t.Fatalf("end: %+v", m)
	}

	if m.DoneAt.Sub(start) > 24*time.Hour {
		t.Fatalf("ran past 24 hours: %s", m.DoneAt.Sub(start))
	}

	failed, err := e.st.FailedMailSince(t.Context(), start)
	if err != nil || len(failed) != 1 {
		t.Fatal(failed, err)
	}
}

func TestOutboxPermanentFailure(t *testing.T) {
	e := newOutbox(t)
	s := smtptest.New(t, smtptest.StartTLS)
	s.RejectRcpt = true
	e.relay.cfg = Config{Host: s.Host, Port: s.Port, TLS: TLSStartTLS, From: "panel@example.com", CAPEM: s.CAPEM}
	e.o.Send = Deliver

	if _, err := e.o.Enqueue(t.Context(), invite, Envelope{Template: "invitation", Category: Account}); err != nil {
		t.Fatal(err)
	}

	e.process(t)

	m := e.mail(t, 1)
	if m.State != store.MailFailed || m.Attempts != 1 || !strings.Contains(m.LastError, "SMTP 550") {
		t.Fatalf("%+v", m)
	}
}

func TestOutboxThroughRelay(t *testing.T) {
	e := newOutbox(t)
	s := smtptest.New(t, smtptest.Implicit)
	e.relay.cfg = Config{Host: s.Host, Port: s.Port, TLS: TLSImplicit, From: "panel@example.com", CAPEM: s.CAPEM}
	e.o.Send = Deliver

	if _, err := e.o.Enqueue(t.Context(), invite, Envelope{Template: "invitation", Category: Account}); err != nil {
		t.Fatal(err)
	}

	e.process(t)

	got := s.Messages()
	// Quoted-printable folds long lines with soft breaks.
	if len(got) != 1 || !strings.Contains(strings.ReplaceAll(got[0].Data, "=\r\n", ""), secretLink) {
		t.Fatalf("%+v", got)
	}

	m := e.mail(t, 1)
	if m.State != store.MailSent || m.MessageID == "" || !strings.Contains(got[0].Data, "Message-ID: "+m.MessageID) {
		t.Fatalf("%+v", m)
	}
}

func TestOutboxDedup(t *testing.T) {
	e := newOutbox(t)
	ev := Envelope{Template: "backup-failed", Category: Operations, DedupKey: "backup-failed:shop"}
	msg := Message{To: "kim@example.com", Subject: "Backup of shop failed", Body: "See the panel.\n"}

	for i, want := range []bool{true, false} {
		if ok, err := e.o.Enqueue(t.Context(), msg, ev); err != nil || ok != want {
			t.Fatalf("%d: %v %v", i, ok, err)
		}
	}

	e.now = e.now.Add(25 * time.Hour)

	if ok, err := e.o.Enqueue(t.Context(), msg, ev); err != nil || !ok {
		t.Fatalf("after a day: %v %v", ok, err)
	}
}

func TestOutboxRateLimits(t *testing.T) {
	e := newOutbox(t)
	ops := Envelope{Template: "doctor", Category: Operations}

	for i := range perRecipientHour + 2 {
		msg := Message{To: "kim@example.com", Subject: fmt.Sprintf("Report %d", i), Body: "x\n"}
		if _, err := e.o.Enqueue(t.Context(), msg, ops); err != nil {
			t.Fatal(err)
		}
	}

	// Account emails are always sent, even over the limit.
	if _, err := e.o.Enqueue(t.Context(), invite, Envelope{Template: "invitation", Category: Account}); err != nil {
		t.Fatal(err)
	}

	e.process(t)

	if len(e.sent) != perRecipientHour+1 {
		t.Fatalf("sent %d", len(e.sent))
	}

	if e.sent[len(e.sent)-1] != invite {
		t.Fatalf("account email not sent: %+v", e.sent[len(e.sent)-1])
	}

	// Another recipient is not held back by kim's limit.
	if _, err := e.o.Enqueue(t.Context(), Message{To: "ops@example.com", Subject: "Report", Body: "x\n"}, ops); err != nil {
		t.Fatal(err)
	}

	e.process(t)

	if last := e.sent[len(e.sent)-1]; last.To != "ops@example.com" {
		t.Fatalf("other recipient held back: %+v", last)
	}

	// An hour later the waiting ones go out.
	e.now = e.now.Add(time.Hour + time.Minute)
	e.process(t)

	if len(e.sent) != perRecipientHour+4 {
		t.Fatalf("after an hour: %d", len(e.sent))
	}
}

func TestOutboxWaitsWhileOff(t *testing.T) {
	e := newOutbox(t)

	if _, err := e.o.Enqueue(t.Context(), invite, Envelope{Template: "invitation", Category: Account}); err != nil {
		t.Fatal(err)
	}

	e.relay.off = true
	e.process(t)

	if m := e.mail(t, 1); m.State != store.MailQueued || m.Attempts != 0 {
		t.Fatalf("%+v", m)
	}

	e.relay.off = false
	e.now = e.now.Add(limitWait)
	e.process(t)

	if len(e.sent) != 1 {
		t.Fatal("not sent after the relay came back")
	}
}

func TestOutboxRefusesMovedBody(t *testing.T) {
	e := newOutbox(t)

	if _, err := e.o.Enqueue(t.Context(), invite, Envelope{Template: "invitation", Category: Account}); err != nil {
		t.Fatal(err)
	}

	// Someone with database access points the row at another address.
	if err := e.st.RawExec(t.Context(), `UPDATE mail_outbox SET recipient = 'evil@example.com'`); err != nil {
		t.Fatal(err)
	}

	e.process(t)

	if m := e.mail(t, 1); m.State != store.MailFailed || len(e.sent) != 0 {
		t.Fatalf("%+v sent=%d", m, len(e.sent))
	}
}

func TestOutboxRun(t *testing.T) {
	e := newOutbox(t)
	done := make(chan struct{})
	sent := make(chan struct{}, 1)
	e.o.Send = func(context.Context, Config, Message) (string, error) {
		sent <- struct{}{}

		return "<x@example.com>", nil
	}

	ctx, cancel := context.WithCancel(t.Context())

	go func() {
		e.o.Run(ctx)
		close(done)
	}()

	if _, err := e.o.Enqueue(t.Context(), invite, Envelope{Template: "invitation", Category: Account}); err != nil {
		t.Fatal(err)
	}

	select {
	case <-sent:
	case <-time.After(5 * time.Second):
		t.Fatal("Enqueue did not wake the worker")
	}

	cancel()
	<-done
}
