// SPDX-License-Identifier: AGPL-3.0-or-later

package serve

import (
	"bytes"
	"context"
	"encoding/base32"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/maki-it/hostashi/internal/auth"
	"github.com/maki-it/hostashi/internal/cpmigrate"
	"github.com/maki-it/hostashi/internal/hosting"
	"github.com/maki-it/hostashi/internal/mail"
	"github.com/maki-it/hostashi/internal/metrics"
	"github.com/maki-it/hostashi/internal/secrets"
	"github.com/maki-it/hostashi/internal/servermon"
	"github.com/maki-it/hostashi/internal/store"
)

const apiPassword = "a long enough password"

type apiEnv struct {
	h         http.Handler
	svc       *auth.Service
	sites     *hosting.Service
	tokenPath string
	metrics   *metrics.Metrics
	settings  Settings
	st        *store.Store
	imports   *stubImports
	cp        *cpmigrate.Service
	doctor    *stubDoctor
	pools     *stubPools
	firewall  *stubFirewall
	php       *stubPHP
	pgclient  *stubPGClient
	node      *stubNode
	mail      MailRelay
	mailSent  *stubMailSend
	outbox    *mail.Outbox
	files     *stubFiles
	monitor   *servermon.Monitor
	srvAgent  *stubServerAgent
	logs      *stubLogs
	system    *stubSystem
}

func newAPI(t *testing.T) *apiEnv {
	t.Helper()

	return newAPIWithAgent(t, nil)
}

// newAPIWithAgent also serves the site routes against ag.
func newAPIWithAgent(t *testing.T, ag hosting.Agent) *apiEnv {
	t.Helper()

	dir := filepath.Join(t.TempDir(), "lib")
	if err := os.Mkdir(dir, 0o750); err != nil {
		t.Fatal(err)
	}

	ctx := context.Background()

	st, err := store.Open(ctx, filepath.Join(dir, "h.db"))
	if err != nil {
		t.Fatal(err)
	}

	t.Cleanup(func() { _ = st.Close() })

	tokenPath := SetupTokenPath(filepath.Join(dir, "h.db"))
	if err := auth.EnsureSetupToken(ctx, st, tokenPath, time.Now()); err != nil {
		t.Fatal(err)
	}

	ok := func(context.Context) error { return nil }

	box, err := secrets.New(bytes.Repeat([]byte{3}, 32))
	if err != nil {
		t.Fatal(err)
	}

	svc := auth.NewService(auth.Config{Store: st, SetupTokenPath: tokenPath, Box: box})

	deps := Deps{PingAgent: ok, PingDB: ok, Auth: svc, Metrics: metrics.New(), Settings: st}
	imports := &stubImports{}
	doc := &stubDoctor{}
	deps.Doctor = doc.run
	pl := &stubPools{}
	deps.Pools = pl.list
	fw := &stubFirewall{enabled: true}
	deps.Firewall = fw
	php := &stubPHP{}
	deps.PHP = php
	pgc := &stubPGClient{}
	deps.PGClient = pgc
	nd := &stubNode{}
	deps.Node = nd
	deps.Mail = mail.NewStore(st, box)
	sent := &stubMailSend{}
	deps.MailSend = sent.send
	outbox := mail.NewOutbox(st, deps.Mail, box, nil)
	outbox.Send = sent.deliver
	deps.Outbox = outbox
	fm := &stubFiles{t: t}
	deps.Files = fm.open
	lg := &stubLogs{days: -1}
	deps.Logs = lg
	deps.NotifyPrefs = st
	deps.MailHistory = st
	sa := &stubServerAgent{}
	mon := &servermon.Monitor{Agent: sa, Store: st}
	deps.Monitor = mon
	sys := newStubSystem()
	deps.System = sys
	deps.AuditLog = st

	if ag != nil {
		deps.Sites = hosting.New(st, ag, nil)
		deps.CloudPanel = cpmigrate.New(st, box, migrationTarget(deps.Sites), nil)
		deps.CloudPanel.Connect(siteCreator(deps.Sites, nil), imports)
		deps.CloudPanel.ConnectPools(cpmigrate.PoolLister(pl.list))
		deps.CloudPanel.EnableLoads(t.Context(), deps.Sites, time.Hour)
		deps.CloudPanelSource = newStubSource(t)
	}

	return &apiEnv{
		h: Handler(Config{PublicURL: testPublicURL}, deps), svc: svc, sites: deps.Sites, tokenPath: tokenPath, metrics: deps.Metrics, settings: st, st: st, cp: deps.CloudPanel,
		imports: imports, doctor: doc, pools: pl, firewall: fw, php: php, pgclient: pgc, node: nd, mail: deps.Mail, mailSent: sent, outbox: outbox, files: fm,
		monitor: mon, srvAgent: sa, logs: lg, system: sys,
	}
}

type reqOpts struct {
	method, path, body string
	header             map[string]string
	cookie             *http.Cookie
}

func (e *apiEnv) do(t *testing.T, o reqOpts) *httptest.ResponseRecorder {
	t.Helper()

	var body io.Reader
	if o.body != "" {
		body = strings.NewReader(o.body)
	}

	r := httptest.NewRequest(o.method, o.path, body)
	r.RemoteAddr = "192.0.2.10:5555"

	if o.body != "" {
		r.Header.Set("Content-Type", "application/json")
	}

	for k, v := range o.header {
		r.Header.Set(k, v)
	}

	if o.cookie != nil {
		r.AddCookie(o.cookie)
	}

	rec := httptest.NewRecorder()
	e.h.ServeHTTP(rec, r)
	checkResponse(t, r, rec)

	return rec
}

func errCode(t *testing.T, rec *httptest.ResponseRecorder) string {
	t.Helper()

	var e apiError
	if err := json.Unmarshal(rec.Body.Bytes(), &e); err != nil {
		t.Fatalf("not an error body: %s", rec.Body)
	}

	return e.Error.Code
}

func (e *apiEnv) setupAdmin(t *testing.T) {
	t.Helper()

	tok, err := auth.ReadSetupToken(e.tokenPath, time.Now())
	if err != nil {
		t.Fatal(err)
	}

	rec := e.do(t, reqOpts{
		method: "POST", path: "/api/v1/setup",
		body: `{"token":"` + tok + `","username":"kim","password":"` + apiPassword + `"}`,
	})
	if rec.Code != http.StatusCreated {
		t.Fatalf("setup: %d %s", rec.Code, rec.Body)
	}
}

func sessionCookieOf(t *testing.T, rec *httptest.ResponseRecorder) *http.Cookie {
	t.Helper()

	for _, c := range rec.Result().Cookies() {
		if c.Name == sessionCookie {
			return c
		}
	}

	t.Fatal("no session cookie")

	return nil
}

func TestSetupAndLoginFlow(t *testing.T) {
	e := newAPI(t)

	rec := e.do(t, reqOpts{method: "GET", path: "/api/v1/setup"})
	if rec.Code != 200 || !strings.Contains(rec.Body.String(), `"required":true`) {
		t.Fatalf("%d %s", rec.Code, rec.Body)
	}

	// Setup with a wrong token.
	rec = e.do(t, reqOpts{
		method: "POST", path: "/api/v1/setup",
		body: `{"token":"nope","username":"kim","password":"` + apiPassword + `"}`,
	})
	if rec.Code != http.StatusForbidden || errCode(t, rec) != "setup_not_allowed" {
		t.Fatalf("%d %s", rec.Code, rec.Body)
	}

	// Setup with invalid input is reported as such.
	tok, _ := auth.ReadSetupToken(e.tokenPath, time.Now())

	rec = e.do(t, reqOpts{
		method: "POST", path: "/api/v1/setup",
		body: `{"token":"` + tok + `","username":"kim","password":"short"}`,
	})
	if rec.Code != http.StatusUnprocessableEntity || errCode(t, rec) != "invalid_argument" {
		t.Fatalf("%d %s", rec.Code, rec.Body)
	}

	e.setupAdmin(t)

	rec = e.do(t, reqOpts{method: "GET", path: "/api/v1/setup"})
	if !strings.Contains(rec.Body.String(), `"required":false`) {
		t.Fatalf("%s", rec.Body)
	}

	// Not logged in yet.
	rec = e.do(t, reqOpts{method: "GET", path: "/api/v1/auth/me"})
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("%d", rec.Code)
	}

	rec = e.do(t, reqOpts{
		method: "POST", path: "/api/v1/auth/login",
		body: `{"username":"kim","password":"` + apiPassword + `"}`,
	})
	if rec.Code != 200 {
		t.Fatalf("login: %d %s", rec.Code, rec.Body)
	}

	c := sessionCookieOf(t, rec)
	if !c.HttpOnly || !c.Secure || c.SameSite != http.SameSiteStrictMode || c.Path != "/" || c.MaxAge <= 0 {
		t.Fatalf("weak cookie: %+v", c)
	}

	if strings.Contains(rec.Body.String(), "token") || strings.Contains(rec.Body.String(), "$argon2") {
		t.Fatalf("secret in login response: %s", rec.Body)
	}

	rec = e.do(t, reqOpts{method: "GET", path: "/api/v1/auth/me", cookie: c})
	if rec.Code != 200 || !strings.Contains(rec.Body.String(), `"username":"kim"`) {
		t.Fatalf("me: %d %s", rec.Code, rec.Body)
	}

	rec = e.do(t, reqOpts{method: "POST", path: "/api/v1/auth/logout", cookie: c})
	if rec.Code != http.StatusNoContent {
		t.Fatalf("logout: %d", rec.Code)
	}

	if cleared := sessionCookieOf(t, rec); cleared.MaxAge >= 0 || cleared.Value != "" {
		t.Fatalf("cookie not cleared: %+v", cleared)
	}

	rec = e.do(t, reqOpts{method: "GET", path: "/api/v1/auth/me", cookie: c})
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("session alive after logout: %d", rec.Code)
	}
}

func TestLoginErrors(t *testing.T) {
	e := newAPI(t)
	e.setupAdmin(t)

	rec := e.do(t, reqOpts{
		method: "POST", path: "/api/v1/auth/login",
		body: `{"username":"kim","password":"wrong password!!"}`,
	})
	if rec.Code != http.StatusUnauthorized || errCode(t, rec) != "invalid_credentials" {
		t.Fatalf("%d %s", rec.Code, rec.Body)
	}

	if len(rec.Result().Cookies()) != 0 {
		t.Fatal("cookie set on a failed login")
	}

	// The same body for an unknown user, apart from the request ID.
	rec2 := e.do(t, reqOpts{
		method: "POST", path: "/api/v1/auth/login",
		body: `{"username":"nobody","password":"wrong password!!"}`,
	})

	withoutID := func(r *httptest.ResponseRecorder) string {
		return strings.Replace(r.Body.String(), r.Header().Get(RequestIDHeader), "", 1)
	}
	if rec2.Code != rec.Code || withoutID(rec2) != withoutID(rec) {
		t.Fatalf("login errors differ: %s vs %s", rec.Body, rec2.Body)
	}

	for i := 0; i < 10; i++ {
		e.do(t, reqOpts{
			method: "POST", path: "/api/v1/auth/login",
			body: `{"username":"kim","password":"wrong password!!"}`,
		})
	}

	rec = e.do(t, reqOpts{
		method: "POST", path: "/api/v1/auth/login",
		body: `{"username":"kim","password":"` + apiPassword + `"}`,
	})
	if rec.Code != http.StatusTooManyRequests || rec.Header().Get("Retry-After") == "" {
		t.Fatalf("%d %v", rec.Code, rec.Header())
	}
}

func TestRequestHardening(t *testing.T) {
	e := newAPI(t)
	e.setupAdmin(t)

	login := `{"username":"kim","password":"` + apiPassword + `"}`

	cases := []struct {
		name string
		o    reqOpts
		code int
	}{
		{"wrong content type", reqOpts{
			method: "POST", path: "/api/v1/auth/login", body: login,
			header: map[string]string{"Content-Type": "text/plain"},
		}, http.StatusUnsupportedMediaType},
		{"form content type", reqOpts{
			method: "POST", path: "/api/v1/auth/login", body: "username=kim&password=x",
			header: map[string]string{"Content-Type": "application/x-www-form-urlencoded"},
		}, http.StatusUnsupportedMediaType},
		{"unknown field", reqOpts{
			method: "POST", path: "/api/v1/auth/login",
			body: `{"username":"kim","password":"x","admin":true}`,
		}, http.StatusBadRequest},
		{"trailing data", reqOpts{method: "POST", path: "/api/v1/auth/login", body: login + `{}`}, http.StatusBadRequest},
		{"not json", reqOpts{method: "POST", path: "/api/v1/auth/login", body: `username=kim`}, http.StatusBadRequest},
		{"wrong field type", reqOpts{
			method: "POST", path: "/api/v1/auth/login",
			body: `{"username":1,"password":2}`,
		}, http.StatusBadRequest},
		{"oversized body", reqOpts{
			method: "POST", path: "/api/v1/auth/login",
			body: `{"username":"kim","password":"` + strings.Repeat("x", 5000) + `"}`,
		}, http.StatusBadRequest},
		{"cross-site origin", reqOpts{
			method: "POST", path: "/api/v1/auth/login", body: login,
			header: map[string]string{"Origin": "https://evil.example"},
		}, http.StatusForbidden},
		{"cross-site fetch metadata", reqOpts{
			method: "POST", path: "/api/v1/auth/login", body: login,
			header: map[string]string{"Sec-Fetch-Site": "cross-site"},
		}, http.StatusForbidden},
		{"same-site (sibling) fetch metadata", reqOpts{
			method: "POST", path: "/api/v1/auth/logout",
			header: map[string]string{"Sec-Fetch-Site": "same-site"},
		}, http.StatusForbidden},
		{"same origin ok", reqOpts{
			method: "POST", path: "/api/v1/auth/login", body: login,
			header: map[string]string{"Origin": "http://example.com", "Sec-Fetch-Site": "same-origin"},
		}, http.StatusOK},
		{"GET on POST route", reqOpts{method: "GET", path: "/api/v1/auth/login"}, http.StatusMethodNotAllowed},
		{"POST on me", reqOpts{method: "POST", path: "/api/v1/auth/me"}, http.StatusMethodNotAllowed},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if rec := e.do(t, tc.o); rec.Code != tc.code {
				t.Fatalf("got %d, want %d: %s", rec.Code, tc.code, rec.Body)
			}
		})
	}
}

func TestSecurityHeaders(t *testing.T) {
	e := newAPI(t)
	rec := e.do(t, reqOpts{method: "GET", path: "/api/v1/health"})

	for k, want := range map[string]string{
		"X-Content-Type-Options":  "nosniff",
		"X-Frame-Options":         "DENY",
		"Referrer-Policy":         "no-referrer",
		"Cache-Control":           "no-store",
		"Content-Security-Policy": "default-src 'none'; frame-ancestors 'none'",
	} {
		if got := rec.Header().Get(k); got != want {
			t.Errorf("%s = %q, want %q", k, got, want)
		}
	}
}

func TestForgedSessionCookies(t *testing.T) {
	e := newAPI(t)
	e.setupAdmin(t)

	for _, v := range []string{"", "x", "admin", strings.Repeat("A", 43), strings.Repeat("A", 500), "' OR 1=1 --"} {
		rec := e.do(t, reqOpts{method: "GET", path: "/api/v1/auth/me", cookie: &http.Cookie{Name: sessionCookie, Value: v}})
		if rec.Code != http.StatusUnauthorized {
			t.Errorf("cookie %q: %d", v, rec.Code)
		}
	}
}

func (e *apiEnv) login(t *testing.T, body string) *httptest.ResponseRecorder {
	t.Helper()

	return e.do(t, reqOpts{method: "POST", path: "/api/v1/auth/login", body: body})
}

func TestTOTPEndpoints(t *testing.T) {
	e := newAPI(t)
	e.setupAdmin(t)

	// All TOTP routes need a session.
	for _, p := range []string{"enroll", "confirm", "disable"} {
		rec := e.do(t, reqOpts{method: "POST", path: "/api/v1/auth/totp/" + p, body: `{"code":"123456","password":"x"}`})
		if rec.Code != http.StatusUnauthorized {
			t.Fatalf("%s without session: %d", p, rec.Code)
		}
	}

	rec := e.login(t, `{"username":"kim","password":"`+apiPassword+`"}`)
	c := sessionCookieOf(t, rec)

	if rec := e.do(t, reqOpts{method: "GET", path: "/api/v1/users", cookie: c}); rec.Code != http.StatusForbidden || errCode(t, rec) != "second_factor_required" {
		t.Fatalf("admin without a second factor: %d %s", rec.Code, rec.Body)
	}

	rec = e.do(t, reqOpts{method: "POST", path: "/api/v1/auth/totp/enroll", cookie: c})
	if rec.Code != 200 {
		t.Fatalf("enroll: %d %s", rec.Code, rec.Body)
	}

	var enr struct {
		Secret string `json:"secret"`
		URI    string `json:"uri"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &enr); err != nil || !strings.HasPrefix(enr.URI, "otpauth://") {
		t.Fatalf("%s %v", rec.Body, err)
	}

	seed, err := base32.StdEncoding.WithPadding(base32.NoPadding).DecodeString(enr.Secret)
	if err != nil {
		t.Fatal(err)
	}

	rec = e.do(t, reqOpts{method: "POST", path: "/api/v1/auth/totp/confirm", cookie: c, body: `{"code":"000000"}`})
	if rec.Code != http.StatusUnauthorized || errCode(t, rec) != "invalid_credentials" {
		t.Fatalf("wrong confirm code: %d %s", rec.Code, rec.Body)
	}

	rec = e.do(t, reqOpts{
		method: "POST", path: "/api/v1/auth/totp/confirm", cookie: c,
		body: `{"code":"` + auth.TOTPCode(seed, auth.TOTPStep(time.Now())) + `"}`,
	})
	if rec.Code != 200 {
		t.Fatalf("confirm: %d %s", rec.Code, rec.Body)
	}

	var conf struct {
		RecoveryCodes []string `json:"recoveryCodes"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &conf); err != nil || len(conf.RecoveryCodes) != 8 {
		t.Fatalf("%s %v", rec.Body, err)
	}

	// Before TOTP the admin could only set up a factor; now the session
	// works (ADR-0035).
	if rec := e.do(t, reqOpts{method: "GET", path: "/api/v1/users", cookie: c}); rec.Code != http.StatusOK {
		t.Fatalf("admin with TOTP: %d %s", rec.Code, rec.Body)
	}

	// Enrolling again while enabled is a conflict.
	rec = e.do(t, reqOpts{method: "POST", path: "/api/v1/auth/totp/enroll", cookie: c})
	if rec.Code != http.StatusConflict || errCode(t, rec) != "totp_state" {
		t.Fatalf("re-enroll: %d %s", rec.Code, rec.Body)
	}

	// Login now asks for the code and sets no cookie.
	rec = e.login(t, `{"username":"kim","password":"`+apiPassword+`"}`)
	if rec.Code != http.StatusUnauthorized || errCode(t, rec) != "totp_required" || len(rec.Result().Cookies()) != 0 {
		t.Fatalf("login without code: %d %s", rec.Code, rec.Body)
	}

	rec = e.login(t, `{"username":"kim","password":"`+apiPassword+`","totp":"`+conf.RecoveryCodes[0]+`"}`)
	if rec.Code != 200 {
		t.Fatalf("login with recovery code: %d %s", rec.Code, rec.Body)
	}

	c2 := sessionCookieOf(t, rec)

	// Disable needs password and a code.
	rec = e.do(t, reqOpts{
		method: "POST", path: "/api/v1/auth/totp/disable", cookie: c2,
		body: `{"password":"wrong password!!","code":"` + conf.RecoveryCodes[1] + `"}`,
	})
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("disable with wrong password: %d", rec.Code)
	}

	// An admin must keep a second factor (ADR-0035).
	rec = e.do(t, reqOpts{
		method: "POST", path: "/api/v1/auth/totp/disable", cookie: c2,
		body: `{"password":"` + apiPassword + `","code":"` + conf.RecoveryCodes[1] + `"}`,
	})
	if rec.Code != http.StatusConflict || errCode(t, rec) != "second_factor_required" {
		t.Fatalf("disable the last factor: %d %s", rec.Code, rec.Body)
	}

	e.giveSecondFactor(t, "kim")

	rec = e.do(t, reqOpts{
		method: "POST", path: "/api/v1/auth/totp/disable", cookie: c2,
		body: `{"password":"` + apiPassword + `","code":"` + conf.RecoveryCodes[2] + `"}`,
	})
	if rec.Code != http.StatusNoContent {
		t.Fatalf("disable: %d %s", rec.Code, rec.Body)
	}

	// With only a passkey left, the password alone is not enough.
	rec = e.login(t, `{"username":"kim","password":"`+apiPassword+`"}`)
	if rec.Code != http.StatusUnauthorized || errCode(t, rec) != "passkey_required" || len(rec.Result().Cookies()) != 0 {
		t.Fatalf("password login with only a passkey: %d %s", rec.Code, rec.Body)
	}
}
