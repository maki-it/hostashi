// SPDX-License-Identifier: AGPL-3.0-or-later

package serve

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/maki-it/hostashi/internal/apiclient"
)

func TestDeviceLoginOverHTTP(t *testing.T) {
	e := newAPI(t)
	c := e.adminCookie(t)

	rec := e.do(t, reqOpts{method: "POST", path: "/api/v1/auth/device", body: `{"client":"hostashi CLI on laptop"}`})
	if rec.Code != http.StatusCreated || rec.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("%d %s", rec.Code, rec.Body)
	}

	var st deviceStartJSON
	if err := json.Unmarshal(rec.Body.Bytes(), &st); err != nil {
		t.Fatal(err)
	}

	if !strings.HasSuffix(st.VerificationURI, "/#/device") || !strings.HasSuffix(st.VerificationURIComplete, "?code="+st.UserCode) || st.ExpiresIn != 600 || st.Interval != 5 {
		t.Errorf("start = %+v", st)
	}

	poll := func() *httptest.ResponseRecorder {
		return e.do(t, reqOpts{method: "POST", path: "/api/v1/auth/device/token", body: `{"deviceCode":"` + st.DeviceCode + `"}`})
	}

	if rec := poll(); rec.Code != http.StatusBadRequest || errCode(t, rec) != "authorization_pending" {
		t.Fatalf("pending: %d %s", rec.Code, rec.Body)
	}

	// Reading and approving need a browser session.
	rec = e.do(t, reqOpts{method: "GET", path: "/api/v1/auth/device/" + st.UserCode})
	if rec.Code != http.StatusUnauthorized {
		t.Errorf("without session: %d", rec.Code)
	}

	rec = e.do(t, reqOpts{method: "GET", path: "/api/v1/auth/device/" + st.UserCode, cookie: c})
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"client":"hostashi CLI on laptop"`) {
		t.Fatalf("read: %d %s", rec.Code, rec.Body)
	}

	rec = e.do(t, reqOpts{method: "GET", path: "/api/v1/auth/device/BCDF-GHJK", cookie: c})
	if rec.Code != http.StatusNotFound {
		t.Errorf("wrong code: %d %s", rec.Code, rec.Body)
	}

	rec = e.do(t, reqOpts{method: "POST", path: "/api/v1/auth/device/approve", cookie: c, body: `{"userCode":"` + st.UserCode + `","approve":true,"scopes":["sites:read"]}`})
	if rec.Code != http.StatusNoContent {
		t.Fatalf("approve: %d %s", rec.Code, rec.Body)
	}

	rec = poll()
	if rec.Code != http.StatusOK {
		t.Fatalf("token: %d %s", rec.Code, rec.Body)
	}

	var tok tokenCreatedJSON
	if err := json.Unmarshal(rec.Body.Bytes(), &tok); err != nil {
		t.Fatal(err)
	}

	if tok.Name != "hostashi CLI on laptop" || tok.ExpiresAt == nil {
		t.Errorf("token = %+v", tok.tokenJSON)
	}

	rec = e.do(t, reqOpts{method: "GET", path: "/api/v1/auth/me", header: bearerHeader(tok.Token)})
	if rec.Code != http.StatusOK {
		t.Errorf("token does not work: %d %s", rec.Code, rec.Body)
	}

	if rec := poll(); rec.Code != http.StatusBadRequest || errCode(t, rec) != "expired_token" {
		t.Errorf("second token: %d %s", rec.Code, rec.Body)
	}
}

func TestDeviceLoginDenied(t *testing.T) {
	e := newAPI(t)
	c := e.adminCookie(t)

	rec := e.do(t, reqOpts{method: "POST", path: "/api/v1/auth/device", body: `{"client":"cli"}`})

	var st deviceStartJSON
	if err := json.Unmarshal(rec.Body.Bytes(), &st); err != nil {
		t.Fatal(err)
	}

	rec = e.do(t, reqOpts{method: "POST", path: "/api/v1/auth/device/approve", cookie: c, body: `{"userCode":"` + st.UserCode + `","approve":false}`})
	if rec.Code != http.StatusNoContent {
		t.Fatalf("deny: %d %s", rec.Code, rec.Body)
	}

	rec = e.do(t, reqOpts{method: "POST", path: "/api/v1/auth/device/token", body: `{"deviceCode":"` + st.DeviceCode + `"}`})
	if rec.Code != http.StatusBadRequest || errCode(t, rec) != "access_denied" {
		t.Errorf("denied poll: %d %s", rec.Code, rec.Body)
	}

	rec = e.do(t, reqOpts{method: "POST", path: "/api/v1/auth/device", body: `{"client":""}`})
	if rec.Code != http.StatusUnprocessableEntity {
		t.Errorf("empty client: %d %s", rec.Code, rec.Body)
	}
}

// TestDeviceLoginWithCLIClient runs the CLI's client against the real
// handler, so that both sides agree on the wire format.
func TestDeviceLoginWithCLIClient(t *testing.T) {
	e := newAPI(t)
	c := e.adminCookie(t)

	srv := httptest.NewServer(e.h)
	defer srv.Close()

	cli := apiclient.New(srv.URL, "", "test")

	// The user approves in the panel as soon as the CLI shows the code.
	approve := func(st apiclient.DeviceStart) {
		rec := e.do(t, reqOpts{method: "POST", path: "/api/v1/auth/device/approve", cookie: c, body: `{"userCode":"` + st.UserCode + `","approve":true,"scopes":["sites:read"]}`})
		if rec.Code != http.StatusNoContent {
			t.Fatalf("approve: %d %s", rec.Code, rec.Body)
		}
	}

	nt, err := cli.DeviceLogin(context.Background(), "hostashi CLI on test", approve, func(context.Context, time.Duration) error { return nil })
	if err != nil {
		t.Fatal(err)
	}

	var me struct {
		Username string   `json:"username"`
		Scopes   []string `json:"scopes"`
	}

	if err := apiclient.New(srv.URL, nt.Token, "test").Do(context.Background(), http.MethodGet, "/auth/me", nil, &me); err != nil || me.Username != "kim" || len(me.Scopes) != 1 {
		t.Errorf("me = %+v, %v", me, err)
	}
}
