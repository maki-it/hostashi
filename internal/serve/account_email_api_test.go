// SPDX-License-Identifier: AGPL-3.0-or-later

package serve

import (
	"encoding/json"
	"net/http"
	"regexp"
	"strings"
	"testing"
)

const testPublicURL = "https://panel.example.com"

var confirmLink = regexp.MustCompile(`https://panel\.example\.com/#/confirm-email/([A-Za-z0-9_-]+)`)

func (e *apiEnv) accountEmail(t *testing.T, c *http.Cookie) accountEmailJSON {
	t.Helper()

	rec := e.do(t, reqOpts{method: "GET", path: "/api/v1/account/email", cookie: c})
	if rec.Code != http.StatusOK {
		t.Fatalf("get email: %d %s", rec.Code, rec.Body)
	}

	var out accountEmailJSON
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}

	return out
}

// requestEmail asks for an address and returns the link's token from the
// email the outbox sent.
func (e *apiEnv) requestEmail(t *testing.T, c *http.Cookie, address string) string {
	t.Helper()

	rec := e.do(t, reqOpts{method: "PUT", path: "/api/v1/account/email", body: `{"email":"` + address + `"}`, cookie: c})
	if rec.Code != http.StatusOK {
		t.Fatalf("put email %s: %d %s", address, rec.Code, rec.Body)
	}

	if err := e.outbox.Process(t.Context()); err != nil {
		t.Fatal(err)
	}

	msgs := e.mailSent.messages()
	last := msgs[len(msgs)-1]

	m := confirmLink.FindStringSubmatch(last.Body)
	if m == nil || last.Subject != "Confirm your email address for Hostashi" {
		t.Fatalf("no confirmation email: %+v", last)
	}

	return m[1]
}

func (e *apiEnv) confirmEmail(t *testing.T, token string) (int, string) {
	t.Helper()

	rec := e.do(t, reqOpts{method: "POST", path: "/api/v1/account/email/confirm", body: `{"token":"` + token + `"}`})

	return rec.Code, rec.Body.String()
}

func TestAccountEmail(t *testing.T) {
	e := newAPI(t)
	c := e.adminCookie(t)

	if got := e.accountEmail(t, c); got.MailEnabled || got.Email != "" || got.Pending != "" {
		t.Fatalf("start: %+v", got)
	}

	rec := e.do(t, reqOpts{method: "PUT", path: "/api/v1/account/email", body: `{"email":"kim@example.com"}`, cookie: c})
	if rec.Code != http.StatusConflict || errCode(t, rec) != "mail_not_configured" {
		t.Fatalf("while off: %d %s", rec.Code, rec.Body)
	}

	e.putMail(t, c, mailBody)

	for _, bad := range []string{"", "Kim <kim@example.com>", "a@example.com, b@example.com", "nobody"} {
		rec := e.do(t, reqOpts{method: "PUT", path: "/api/v1/account/email", body: `{"email":"` + bad + `"}`, cookie: c})
		if rec.Code != http.StatusUnprocessableEntity {
			t.Errorf("%q: %d %s", bad, rec.Code, rec.Body)
		}
	}

	token := e.requestEmail(t, c, "Kim@Example.com")

	got := e.accountEmail(t, c)
	if !got.MailEnabled || got.Email != "" || got.Pending != "kim@example.com" || got.PendingExpires == nil {
		t.Fatalf("pending: %+v", got)
	}

	if sent := e.mailSent.messages(); sent[0].To != "kim@example.com" {
		t.Fatalf("sent to %s", sent[0].To)
	}

	if code, _ := e.confirmEmail(t, token+"x"); code != http.StatusNotFound {
		t.Fatalf("wrong token: %d", code)
	}

	// The link works without a session, once.
	if code, body := e.confirmEmail(t, token); code != http.StatusOK || !strings.Contains(body, `"email":"kim@example.com"`) {
		t.Fatalf("confirm: %d %s", code, body)
	}

	if code, _ := e.confirmEmail(t, token); code != http.StatusNotFound {
		t.Fatalf("second use: %d", code)
	}

	if got := e.accountEmail(t, c); got.Email != "kim@example.com" || got.Pending != "" {
		t.Fatalf("confirmed: %+v", got)
	}

	rec = e.do(t, reqOpts{method: "PUT", path: "/api/v1/account/email", body: `{"email":"kim@example.com"}`, cookie: c})
	if rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("same address: %d %s", rec.Code, rec.Body)
	}

	// A new address is used only after its link; the old one hears of it.
	token = e.requestEmail(t, c, "kim@new.example.com")

	if got := e.accountEmail(t, c); got.Email != "kim@example.com" || got.Pending != "kim@new.example.com" {
		t.Fatalf("change pending: %+v", got)
	}

	if code, _ := e.confirmEmail(t, token); code != http.StatusOK {
		t.Fatalf("confirm change: %d", code)
	}

	if err := e.outbox.Process(t.Context()); err != nil {
		t.Fatal(err)
	}

	msgs := e.mailSent.messages()
	if note := msgs[len(msgs)-1]; note.To != "kim@example.com" || !strings.Contains(note.Body, "New address: kim@new.example.com") {
		t.Fatalf("no note to the old address: %+v", note)
	}

	// The link's token never reaches the audit log.
	var n int
	if err := e.st.RawCount(t.Context(), `SELECT count(*) FROM audit_log WHERE detail LIKE ?`, "%"+token+"%", &n); err != nil || n != 0 {
		t.Fatalf("token in audit log: %d %v", n, err)
	}

	rec = e.do(t, reqOpts{method: "DELETE", path: "/api/v1/account/email", cookie: c})
	if rec.Code != http.StatusNoContent {
		t.Fatalf("delete: %d %s", rec.Code, rec.Body)
	}

	if got := e.accountEmail(t, c); got.Email != "" || got.Pending != "" {
		t.Fatalf("after delete: %+v", got)
	}
}

func TestAccountEmailRateLimit(t *testing.T) {
	e := newAPI(t)
	c := e.adminCookie(t)
	e.putMail(t, c, mailBody)

	for i := range 5 {
		rec := e.do(t, reqOpts{method: "PUT", path: "/api/v1/account/email", body: `{"email":"kim` + string(rune('a'+i)) + `@example.com"}`, cookie: c})
		if rec.Code != http.StatusOK {
			t.Fatalf("%d: %d %s", i, rec.Code, rec.Body)
		}
	}

	rec := e.do(t, reqOpts{method: "PUT", path: "/api/v1/account/email", body: `{"email":"kimz@example.com"}`, cookie: c})
	if rec.Code != http.StatusTooManyRequests {
		t.Fatalf("limit: %d %s", rec.Code, rec.Body)
	}

	// Another account has its own budget.
	other, _ := matrixLogin(t, e, c, "otto", "user")
	if got := e.accountEmail(t, other); !got.MailEnabled {
		t.Fatalf("%+v", got)
	}

	rec = e.do(t, reqOpts{method: "PUT", path: "/api/v1/account/email", body: `{"email":"otto@example.com"}`, cookie: other})
	if rec.Code != http.StatusOK {
		t.Fatalf("other: %d %s", rec.Code, rec.Body)
	}
}

func TestAccountEmailOnlyOwnSession(t *testing.T) {
	e := newAPI(t)
	c := e.adminCookie(t)
	tok := e.createToken(t, c, `{"name":"all","scopes":["sites:read"]}`)

	for _, r := range []reqOpts{
		{method: "GET", path: "/api/v1/account/email"},
		{method: "PUT", path: "/api/v1/account/email", body: `{"email":"kim@example.com"}`},
		{method: "DELETE", path: "/api/v1/account/email"},
	} {
		if rec := e.do(t, r); rec.Code != http.StatusUnauthorized {
			t.Errorf("anonymous %s: %d", r.method, rec.Code)
		}

		r.header = bearerHeader(tok.Token)
		if rec := e.do(t, r); rec.Code < 400 {
			t.Errorf("token %s: %d", r.method, rec.Code)
		}
	}
}

func TestPanelLink(t *testing.T) {
	for in, want := range map[string]string{
		"https://panel.example.com":       "https://panel.example.com/#/x",
		"https://panel.example.com/":      "https://panel.example.com/#/x",
		"https://panel.example.com:8443/": "https://panel.example.com:8443/#/x",
		"":                                "",
		"panel.example.com":               "",
		"javascript:alert(1)":             "",
	} {
		if got := panelLink(in, "x"); got != want {
			t.Errorf("%q: %q", in, got)
		}
	}
}

func TestInvitationByEmail(t *testing.T) {
	e := newAPI(t)
	c := e.adminCookie(t)

	body := `{"username":"anna","role":"user","email":"Anna@Example.com"}`

	rec := e.do(t, reqOpts{method: "POST", path: "/api/v1/users", body: body, cookie: c})
	if rec.Code != http.StatusConflict || errCode(t, rec) != "mail_not_configured" {
		t.Fatalf("while off: %d %s", rec.Code, rec.Body)
	}

	e.putMail(t, c, mailBody)

	rec = e.do(t, reqOpts{method: "POST", path: "/api/v1/users", body: `{"username":"bad","role":"user","email":"x"}`, cookie: c})
	if rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("bad address: %d %s", rec.Code, rec.Body)
	}

	rec = e.do(t, reqOpts{method: "POST", path: "/api/v1/users", body: body, cookie: c})
	if rec.Code != http.StatusCreated || !strings.Contains(rec.Body.String(), `"emailedTo":"anna@example.com"`) {
		t.Fatalf("create: %d %s", rec.Code, rec.Body)
	}

	var created struct {
		User struct {
			ID int64 `json:"id"`
		} `json:"user"`
		Invitation struct {
			Token string `json:"token"`
		} `json:"invitation"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &created); err != nil {
		t.Fatal(err)
	}

	if err := e.outbox.Process(t.Context()); err != nil {
		t.Fatal(err)
	}

	msgs := e.mailSent.messages()
	if len(msgs) != 1 || msgs[0].To != "anna@example.com" || !strings.Contains(msgs[0].Body, testPublicURL+"/#/invite/"+created.Invitation.Token) ||
		!strings.Contains(msgs[0].Body, "invited you") {
		t.Fatalf("invitation email: %+v", msgs)
	}

	// Accepting the emailed link confirms the address.
	rec = e.do(t, reqOpts{method: "POST", path: "/api/v1/invitations/accept", body: `{"token":"` + created.Invitation.Token + `","password":"` + apiPassword + `"}`})
	if rec.Code != http.StatusOK {
		t.Fatalf("accept: %d %s", rec.Code, rec.Body)
	}

	if _, addr, err := e.svc.ConfirmedEmail(t.Context(), created.User.ID); err != nil || addr != "anna@example.com" {
		t.Fatalf("confirmed: %q %v", addr, err)
	}

	// A reset link goes to the confirmed address.
	rec = e.do(t, reqOpts{method: "POST", path: userPath(created.User.ID, "/reset"), body: "{}", cookie: c})
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"emailedTo":"anna@example.com"`) {
		t.Fatalf("reset: %d %s", rec.Code, rec.Body)
	}

	if err := e.outbox.Process(t.Context()); err != nil {
		t.Fatal(err)
	}

	msgs = e.mailSent.messages()
	if last := msgs[len(msgs)-1]; last.Subject != "Your Hostashi account was reset" || !strings.Contains(last.Body, "/#/invite/") {
		t.Fatalf("reset email: %+v", last)
	}

	// Without an address the link is only shown.
	rec = e.do(t, reqOpts{method: "POST", path: "/api/v1/users", body: `{"username":"bert","role":"user"}`, cookie: c})
	if rec.Code != http.StatusCreated || strings.Contains(rec.Body.String(), "emailedTo") {
		t.Fatalf("no address: %d %s", rec.Code, rec.Body)
	}
}

func TestAccountSecurityEmail(t *testing.T) {
	e := newAPI(t)
	c := e.adminCookie(t)
	e.putMail(t, c, mailBody)

	// Nothing goes out while the account has no confirmed address.
	e.createToken(t, c, `{"name":"before","scopes":["sites:read"]}`)

	if err := e.outbox.Process(t.Context()); err != nil {
		t.Fatal(err)
	}

	if n := len(e.mailSent.messages()); n != 0 {
		t.Fatalf("sent %d without an address", n)
	}

	token := e.requestEmail(t, c, "kim@example.com")
	if code, _ := e.confirmEmail(t, token); code != http.StatusOK {
		t.Fatal(code)
	}

	e.createToken(t, c, `{"name":"deploy","scopes":["sites:read"]}`)

	if err := e.outbox.Process(t.Context()); err != nil {
		t.Fatal(err)
	}

	msgs := e.mailSent.messages()
	last := msgs[len(msgs)-1]

	if last.To != "kim@example.com" || last.Subject != "Security change in your Hostashi account" || !strings.Contains(last.Body, "A new API token was created: deploy.") {
		t.Fatalf("%+v", last)
	}

	if strings.Contains(last.Body, "hst_") {
		t.Fatal("token secret in the email")
	}
}
