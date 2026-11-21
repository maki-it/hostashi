// SPDX-License-Identifier: AGPL-3.0-or-later

package mail

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/maki-it/hostashi/internal/mail/smtptest"
	"github.com/maki-it/hostashi/internal/secrets"
)

func relay(s *smtptest.Server, mode TLSMode) Config {
	return Config{
		Host: s.Host, Port: s.Port, TLS: mode, From: "panel@example.com", FromName: "Hostashi",
		CAPEM: s.CAPEM,
	}
}

var hello = Message{To: "kim@example.com", Subject: "Test from Hostashi", Body: "It works.\n.\nA line with a dot.\n"}

func TestSendStartTLS(t *testing.T) {
	s := smtptest.New(t, smtptest.StartTLS)
	s.Users = map[string]string{"relay": "s3cret pass"}

	c := relay(s, TLSStartTLS)
	c.Username, c.Password = "relay", "s3cret pass"
	c.ReplyTo = "admin@example.com"

	if err := Send(t.Context(), c, hello); err != nil {
		t.Fatal(err)
	}

	got := s.Messages()
	if len(got) != 1 {
		t.Fatalf("messages: %d", len(got))
	}

	m := got[0]
	if m.User != "relay" || m.From != "panel@example.com" || len(m.To) != 1 || m.To[0] != "kim@example.com" {
		t.Fatalf("envelope: %+v", m)
	}

	for _, want := range []string{
		`From: "Hostashi" <panel@example.com>`, "To: <kim@example.com>", "Reply-To: <admin@example.com>",
		"Subject: Test from Hostashi", "Message-ID: <", "Content-Type: text/plain",
	} {
		if !strings.Contains(m.Data, want) {
			t.Errorf("missing %q in\n%s", want, m.Data)
		}
	}

	if strings.Contains(m.Data, "s3cret") {
		t.Fatal("password in the message")
	}
}

func TestSendImplicitTLSWithoutAuth(t *testing.T) {
	s := smtptest.New(t, smtptest.Implicit)

	if err := Send(t.Context(), relay(s, TLSImplicit), hello); err != nil {
		t.Fatal(err)
	}

	if len(s.Messages()) != 1 {
		t.Fatal("not received")
	}
}

func TestSendRefusesWithoutTLS(t *testing.T) {
	s := smtptest.New(t, smtptest.NoTLS)
	s.Users = map[string]string{"relay": "pw"}

	c := relay(s, TLSStartTLS)
	c.Username, c.Password = "relay", "pw"

	err := Send(t.Context(), c, hello)
	if err == nil {
		t.Fatal("sent without STARTTLS")
	}

	if len(s.Messages()) != 0 {
		t.Fatal("message delivered in plain text")
	}

	if strings.Contains(Summary(err), "pw") {
		t.Fatalf("summary: %s", Summary(err))
	}
}

func TestSendRefusesUnknownCertificate(t *testing.T) {
	for _, mode := range []struct {
		srv smtptest.Mode
		tls TLSMode
	}{{smtptest.StartTLS, TLSStartTLS}, {smtptest.Implicit, TLSImplicit}} {
		s := smtptest.New(t, mode.srv)

		c := relay(s, mode.tls)
		c.CAPEM = ""

		if err := Send(t.Context(), c, hello); err == nil {
			t.Fatalf("%s: accepted an unknown certificate", mode.tls)
		}

		// The same server under a name its certificate does not hold.
		c = relay(s, mode.tls)
		c.Host = "localhost"

		if err := Send(t.Context(), c, hello); err == nil {
			t.Fatalf("%s: accepted a certificate for another name", mode.tls)
		}

		if len(s.Messages()) != 0 {
			t.Fatal("delivered")
		}
	}
}

func TestSendAuthFailure(t *testing.T) {
	s := smtptest.New(t, smtptest.StartTLS)
	s.Users = map[string]string{"relay": "right"}

	c := relay(s, TLSStartTLS)
	c.Username, c.Password = "relay", "wrong-password"

	err := Send(t.Context(), c, hello)
	if err == nil {
		t.Fatal("no error")
	}

	if sum := Summary(err); strings.Contains(sum, "wrong-password") || sum == "" {
		t.Fatalf("summary: %q", sum)
	}
}

func TestSendRecipientRefused(t *testing.T) {
	s := smtptest.New(t, smtptest.StartTLS)
	s.RejectRcpt = true

	err := Send(t.Context(), relay(s, TLSStartTLS), hello)
	if err == nil {
		t.Fatal("no error")
	}

	if sum := Summary(err); !strings.Contains(sum, "SMTP 550") || strings.Contains(sum, "temporary") {
		t.Fatalf("summary: %q", sum)
	}

	s.RejectRcpt, s.TempFail = false, true

	err = Send(t.Context(), relay(s, TLSStartTLS), hello)
	if sum := Summary(err); !strings.Contains(sum, "SMTP 451") || !strings.Contains(sum, "temporary") {
		t.Fatalf("summary: %q", sum)
	}
}

func TestSendTimeout(t *testing.T) {
	old := sendTimeout
	sendTimeout = 300 * time.Millisecond

	t.Cleanup(func() { sendTimeout = old })

	s := smtptest.New(t, smtptest.Silent)
	start := time.Now()

	if err := Send(t.Context(), relay(s, TLSStartTLS), hello); err == nil {
		t.Fatal("no error")
	}

	if d := time.Since(start); d > 5*time.Second {
		t.Fatalf("took %s", d)
	}
}

func TestSendRefusesBadMessages(t *testing.T) {
	s := smtptest.New(t, smtptest.StartTLS)
	c := relay(s, TLSStartTLS)

	for _, m := range []Message{
		{To: "kim@example.com\r\nBcc: x@example.com", Subject: "s", Body: "b"},
		{To: "Kim <kim@example.com>", Subject: "s", Body: "b"},
		{To: "a@example.com, b@example.com", Subject: "s", Body: "b"},
		{To: "kim@example.com", Subject: "s\r\nBcc: x@example.com", Body: "b"},
		{To: "kim@example.com", Subject: "", Body: "b"},
		{To: "kim@example.com", Subject: strings.Repeat("s", 201), Body: "b"},
		{To: "kim@example.com", Subject: "s", Body: strings.Repeat("b", 64<<10+1)},
		{To: "kim@example.com", Subject: "s", Body: "\xff"},
	} {
		if err := Send(t.Context(), c, m); !errors.Is(err, ErrInvalid) {
			t.Errorf("%q: %v", m.To+" / "+m.Subject, err)
		}
	}

	if len(s.Messages()) != 0 {
		t.Fatal("delivered")
	}
}

func TestValidate(t *testing.T) {
	good := Config{Host: "Smtp.Example.com", Port: 587, TLS: TLSStartTLS, From: "panel@example.com"}

	c, err := good.Validate()
	if err != nil || c.Host != "smtp.example.com" {
		t.Fatalf("%+v %v", c, err)
	}

	bad := []func(*Config){
		func(c *Config) { c.Host = "" },
		func(c *Config) { c.Host = "unix:///run/x.sock" },
		func(c *Config) { c.Host = "smtp.example.com:587" },
		func(c *Config) { c.Host = "smtp example.com" },
		func(c *Config) { c.Host = "0.0.0.0" },
		func(c *Config) { c.Port = 0 },
		func(c *Config) { c.Port = 70000 },
		func(c *Config) { c.TLS = "" },
		func(c *Config) { c.TLS = "none" },
		func(c *Config) { c.Password = "pw" },
		func(c *Config) { c.Username = "u\nv" },
		func(c *Config) { c.From = "" },
		func(c *Config) { c.From = "Panel <panel@example.com>" },
		func(c *Config) { c.From = "panel@localhost" },
		func(c *Config) { c.FromName = "Evil\r\nBcc: x@example.com" },
		func(c *Config) { c.FromName = `a "quoted" name` },
		func(c *Config) { c.FromName = strings.Repeat("n", 65) },
		func(c *Config) { c.ReplyTo = "a@example.com, b@example.com" },
		func(c *Config) { c.CAPEM = "not a certificate" },
	}

	for i, f := range bad {
		c := good
		f(&c)

		if _, err := c.Validate(); !errors.Is(err, ErrInvalid) {
			t.Errorf("case %d: %+v accepted", i, c)
		}
	}
}

type memKV map[string]string

func (m memKV) Setting(_ context.Context, k string) (string, bool, error) {
	v, ok := m[k]

	return v, ok, nil
}

func (m memKV) SetSetting(_ context.Context, k, v string) error {
	m[k] = v

	return nil
}

func TestStoreSealsPassword(t *testing.T) {
	box, err := secrets.New(bytes.Repeat([]byte{7}, 32))
	if err != nil {
		t.Fatal(err)
	}

	kv := memKV{}
	st := NewStore(kv, box)
	ctx := t.Context()

	if _, err := st.Load(ctx); !errors.Is(err, ErrNotConfigured) {
		t.Fatalf("empty: %v", err)
	}

	in := Config{Host: "smtp.example.com", Port: 465, TLS: TLSImplicit, Username: "relay", Password: "the password", From: "panel@example.com"}
	if _, err := st.Save(ctx, in); err != nil {
		t.Fatal(err)
	}

	if strings.Contains(kv[SettingKey], "the password") {
		t.Fatalf("password stored in clear: %s", kv[SettingKey])
	}

	got, err := st.Load(ctx)
	if err != nil || got != in {
		t.Fatalf("%+v %v", got, err)
	}

	// Another key cannot open it.
	other, _ := secrets.New(bytes.Repeat([]byte{8}, 32))
	if _, err := NewStore(kv, other).Load(ctx); err == nil {
		t.Fatal("opened with another key")
	}

	if err := st.Clear(ctx); err != nil {
		t.Fatal(err)
	}

	if _, err := st.Load(ctx); !errors.Is(err, ErrNotConfigured) {
		t.Fatalf("after clear: %v", err)
	}
}

func TestSummary(t *testing.T) {
	if Summary(nil) != "" {
		t.Fatal("nil")
	}

	s := Summary(errors.New("line one\r\nline two " + strings.Repeat("x", 300)))
	if s != "line one" {
		t.Fatalf("%q", s)
	}

	s = Summary(errors.New(strings.Repeat("x", 300)))
	if len([]rune(s)) != maxSummaryLen+1 {
		t.Fatalf("%d", len([]rune(s)))
	}
}
