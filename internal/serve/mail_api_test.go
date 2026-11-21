// SPDX-License-Identifier: AGPL-3.0-or-later

package serve

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"testing"

	"github.com/maki-it/hostashi/internal/mail"
	"github.com/maki-it/hostashi/internal/mail/smtptest"
)

// stubMailSend records messages instead of sending them; with real set it
// sends through mail.Send.
type stubMailSend struct {
	mu   sync.Mutex
	sent []mail.Message
	err  error
	real bool
}

func (s *stubMailSend) send(ctx context.Context, c mail.Config, m mail.Message) error {
	if s.real {
		return mail.Send(ctx, c, m)
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	if s.err != nil {
		return s.err
	}

	s.sent = append(s.sent, m)

	return nil
}

const mailBody = `{"host":"smtp.example.com","port":587,"tls":"starttls","username":"relay","password":"relay password","from":"panel@example.com","from_name":"Hostashi"}`

func (e *apiEnv) putMail(t *testing.T, c *http.Cookie, body string) mailSettingsJSON {
	t.Helper()

	rec := e.do(t, reqOpts{method: "PUT", path: "/api/v1/settings/mail", body: body, cookie: c})
	if rec.Code != http.StatusOK {
		t.Fatalf("put mail: %d %s", rec.Code, rec.Body)
	}

	if strings.Contains(rec.Body.String(), "password\"") && strings.Contains(rec.Body.String(), "relay password") {
		t.Fatalf("password returned: %s", rec.Body)
	}

	var out mailSettingsJSON
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}

	return out
}

func TestMailSettings(t *testing.T) {
	e := newAPI(t)
	c := e.adminCookie(t)

	rec := e.do(t, reqOpts{method: "GET", path: "/api/v1/settings/mail", cookie: c})
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"configured":false`) {
		t.Fatalf("empty: %d %s", rec.Code, rec.Body)
	}

	out := e.putMail(t, c, mailBody)
	if !out.Configured || !out.PasswordSet || out.Host != "smtp.example.com" || out.FromName != "Hostashi" {
		t.Fatalf("%+v", out)
	}

	rec = e.do(t, reqOpts{method: "GET", path: "/api/v1/settings/mail", cookie: c})
	if strings.Contains(rec.Body.String(), "relay password") || !strings.Contains(rec.Body.String(), `"password_set":true`) {
		t.Fatalf("get: %s", rec.Body)
	}

	// Without a password field the stored one stays.
	out = e.putMail(t, c, `{"host":"smtp.example.com","port":465,"tls":"tls","username":"relay","from":"panel@example.com"}`)
	if !out.PasswordSet || out.Port != 465 {
		t.Fatalf("keep: %+v", out)
	}

	if cfg, err := e.mail.Load(t.Context()); err != nil || cfg.Password != "relay password" {
		t.Fatalf("stored: %+v %v", cfg, err)
	}

	// Another user name does not inherit the password.
	out = e.putMail(t, c, `{"host":"smtp.example.com","port":465,"tls":"tls","username":"other","from":"panel@example.com"}`)
	if out.PasswordSet {
		t.Fatalf("inherited: %+v", out)
	}

	// The audit log never holds the password.
	var n int
	if err := e.st.RawCount(t.Context(), `SELECT count(*) FROM audit_log WHERE action = 'settings.mail' AND detail LIKE ?`, "%relay password%", &n); err != nil || n != 0 {
		t.Fatalf("password in audit log: %d %v", n, err)
	}

	if err := e.st.RawCount(t.Context(), `SELECT count(*) FROM audit_log WHERE action = ?`, "settings.mail", &n); err != nil || n != 3 {
		t.Fatalf("audit: %d %v", n, err)
	}

	rec = e.do(t, reqOpts{method: "DELETE", path: "/api/v1/settings/mail", cookie: c})
	if rec.Code != http.StatusNoContent {
		t.Fatalf("delete: %d %s", rec.Code, rec.Body)
	}

	rec = e.do(t, reqOpts{method: "GET", path: "/api/v1/settings/mail", cookie: c})
	if !strings.Contains(rec.Body.String(), `"configured":false`) {
		t.Fatalf("after delete: %s", rec.Body)
	}
}

func TestMailSettingsInvalid(t *testing.T) {
	e := newAPI(t)
	c := e.adminCookie(t)

	for _, body := range []string{
		`{"host":"smtp.example.com","port":587,"tls":"none","from":"panel@example.com"}`,
		`{"host":"smtp.example.com","port":25,"tls":"","from":"panel@example.com"}`,
		`{"host":"unix:///run/mail.sock","port":587,"tls":"starttls","from":"panel@example.com"}`,
		`{"host":"smtp.example.com","port":587,"tls":"starttls","from":"Panel <panel@example.com>"}`,
		`{"host":"smtp.example.com","port":587,"tls":"starttls","from":"panel@example.com","from_name":"x\r\nBcc: a@example.com"}`,
		`{"host":"smtp.example.com","port":587,"tls":"starttls","from":"panel@example.com","password":"pw"}`,
		`{"host":"smtp.example.com","port":587,"tls":"starttls","from":"panel@example.com","ca_pem":"nope"}`,
	} {
		rec := e.do(t, reqOpts{method: "PUT", path: "/api/v1/settings/mail", body: body, cookie: c})
		if rec.Code != http.StatusUnprocessableEntity {
			t.Errorf("%s: %d %s", body, rec.Code, rec.Body)
		}
	}
}

func TestMailTest(t *testing.T) {
	e := newAPI(t)
	c := e.adminCookie(t)

	test := func(body string) (int, string) {
		rec := e.do(t, reqOpts{method: "POST", path: "/api/v1/settings/mail/test", body: body, cookie: c})

		return rec.Code, rec.Body.String()
	}

	if code, body := test(`{"to":"kim@example.com"}`); code != http.StatusConflict || !strings.Contains(body, "mail_not_configured") {
		t.Fatalf("unconfigured: %d %s", code, body)
	}

	e.putMail(t, c, mailBody)

	for _, to := range []string{"", "Kim <kim@example.com>", "a@example.com, b@example.com", `kim@example.com\r\nBcc: x@example.com`} {
		if code, body := test(`{"to":"` + to + `"}`); code != http.StatusUnprocessableEntity {
			t.Errorf("%q: %d %s", to, code, body)
		}
	}

	if code, body := test(`{"to":"kim@example.com"}`); code != http.StatusOK || body != `{"sent":true}`+"\n" {
		t.Fatalf("send: %d %s", code, body)
	}

	if len(e.mailSent.sent) != 1 || e.mailSent.sent[0].To != "kim@example.com" || !strings.Contains(e.mailSent.sent[0].Body, "smtp.example.com:587") {
		t.Fatalf("sent: %+v", e.mailSent.sent)
	}

	e.mailSent.err = errors.New("dial failed: SMTP AUTH failed: 535 5.7.8 authentication failed\nmore")

	code, body := test(`{"to":"kim@example.com"}`)
	if code != http.StatusBadGateway || !strings.Contains(body, "535 5.7.8") || strings.Contains(body, "more") {
		t.Fatalf("failure: %d %s", code, body)
	}

	// Ten per hour, failures included.
	e.mailSent.err = nil

	for i := 2; i < mailTestLimit; i++ {
		if code, body := test(`{"to":"kim@example.com"}`); code != http.StatusOK {
			t.Fatalf("send %d: %d %s", i, code, body)
		}
	}

	if code, body := test(`{"to":"kim@example.com"}`); code != http.StatusTooManyRequests {
		t.Fatalf("limit: %d %s", code, body)
	}

	var n int
	if err := e.st.RawCount(t.Context(), `SELECT count(*) FROM audit_log WHERE action = ?`, "settings.mail.test", &n); err != nil || n != mailTestLimit {
		t.Fatalf("audit: %d %v", n, err)
	}
}

func TestMailTestThroughRelay(t *testing.T) {
	e := newAPI(t)
	c := e.adminCookie(t)
	e.mailSent.real = true

	s := smtptest.New(t, smtptest.StartTLS)
	s.Users = map[string]string{"relay": "relay password"}

	body, err := json.Marshal(map[string]any{
		"host": s.Host, "port": s.Port, "tls": "starttls", "username": "relay", "password": "relay password",
		"from": "panel@example.com", "ca_pem": s.CAPEM,
	})
	if err != nil {
		t.Fatal(err)
	}

	e.putMail(t, c, string(body))

	rec := e.do(t, reqOpts{method: "POST", path: "/api/v1/settings/mail/test", body: `{"to":"kim@example.com"}`, cookie: c})
	if rec.Code != http.StatusOK {
		t.Fatalf("%d %s", rec.Code, rec.Body)
	}

	got := s.Messages()
	if len(got) != 1 || got[0].User != "relay" || !strings.Contains(got[0].Data, "Subject: Test email from Hostashi") {
		t.Fatalf("%+v", got)
	}

	// A wrong password is reported with the server's status line.
	s.Users["relay"] = "changed at the provider"

	rec = e.do(t, reqOpts{method: "POST", path: "/api/v1/settings/mail/test", body: `{"to":"kim@example.com"}`, cookie: c})
	if rec.Code != http.StatusBadGateway || !strings.Contains(rec.Body.String(), "535") || strings.Contains(rec.Body.String(), "relay password") {
		t.Fatalf("%d %s", rec.Code, rec.Body)
	}
}

func TestMailSettingsAccess(t *testing.T) {
	e := newAPI(t)
	c := e.adminCookie(t)
	tok := e.createToken(t, c, `{"name":"all","scopes":["sites:read"]}`)
	manager, _ := matrixLogin(t, e, c, "mona", "manager")

	for _, r := range []reqOpts{
		{method: "GET", path: "/api/v1/settings/mail"},
		{method: "PUT", path: "/api/v1/settings/mail", body: mailBody},
		{method: "DELETE", path: "/api/v1/settings/mail"},
		{method: "POST", path: "/api/v1/settings/mail/test", body: `{"to":"kim@example.com"}`},
	} {
		r.header = bearerHeader(tok.Token)
		if rec := e.do(t, r); rec.Code < 400 {
			t.Errorf("token %s %s: %d", r.method, r.path, rec.Code)
		}

		r.header, r.cookie = nil, manager
		if rec := e.do(t, r); rec.Code != http.StatusForbidden {
			t.Errorf("manager %s %s: %d %s", r.method, r.path, rec.Code, rec.Body)
		}
	}

	if len(e.mailSent.sent) != 0 {
		t.Fatal("sent " + strconv.Itoa(len(e.mailSent.sent)))
	}
}
