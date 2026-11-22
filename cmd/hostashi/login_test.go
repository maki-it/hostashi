// SPDX-License-Identifier: AGPL-3.0-or-later

package main

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/maki-it/hostashi/internal/apiclient"
)

// fakePanel answers /auth/me for hst_good and runs a device login that is
// approved on the first poll.
func fakePanel(t *testing.T) *httptest.Server {
	t.Helper()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v1/auth/me":
			if r.Header.Get("Authorization") != "Bearer hst_good" {
				w.WriteHeader(http.StatusUnauthorized)
				_, _ = w.Write([]byte(`{"error":{"code":"unauthenticated","message":"login required"}}`))

				return
			}

			_, _ = w.Write([]byte(`{"username":"kim","role":"admin","auth":"token","scopes":["sites:read"]}`))
		case "/api/v1/auth/device":
			var in struct {
				Client string `json:"client"`
			}

			_ = json.NewDecoder(r.Body).Decode(&in)
			if in.Client != "laptop" {
				t.Errorf("client %q", in.Client)
			}

			w.WriteHeader(http.StatusCreated)
			_, _ = w.Write([]byte(`{"deviceCode":"dc","userCode":"WDJB-MJHT","verificationUri":"https://p/#/device","verificationUriComplete":"https://p/#/device?code=WDJB-MJHT","expiresIn":600,"interval":5}`))
		case "/api/v1/auth/device/token":
			_, _ = w.Write([]byte(`{"token":"hst_good","name":"laptop"}`))
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(srv.Close)

	return srv
}

func loginEnv(t *testing.T) string {
	t.Helper()

	cfg := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", cfg)
	t.Setenv("HOSTASHI_URL", "")
	t.Setenv("HOSTASHI_TOKEN", "")

	apiWait = func(context.Context, time.Duration) error { return nil }

	t.Cleanup(func() { apiWait = apiclient.Sleep })

	return filepath.Join(cfg, "hostashi", "credentials.json")
}

func runCLI(t *testing.T, in string, args ...string) (int, string, string) {
	t.Helper()

	stdin = strings.NewReader(in)

	t.Cleanup(func() { stdin = os.Stdin })

	var out, errb bytes.Buffer

	code := run(context.Background(), args, 1000, &out, &errb)

	return code, out.String(), errb.String()
}

func TestLoginWithBrowser(t *testing.T) {
	path := loginEnv(t)
	srv := fakePanel(t)

	code, out, errOut := runCLI(t, "", "login", "-name", "laptop", srv.URL)
	if code != 0 {
		t.Fatalf("login = %d: %s", code, errOut)
	}

	for _, want := range []string{"https://p/#/device?code=WDJB-MJHT", "WDJB-MJHT", "Logged in to " + srv.URL + " as kim (admin)"} {
		if !strings.Contains(out, want) {
			t.Errorf("output lacks %q:\n%s", want, out)
		}
	}

	b, err := os.ReadFile(path)
	if err != nil || !strings.Contains(string(b), "hst_good") {
		t.Fatalf("credentials: %s, %v", b, err)
	}

	code, out, _ = runCLI(t, "", "whoami")
	if code != 0 || !strings.Contains(out, "kim (admin) at "+srv.URL) || !strings.Contains(out, "scopes: sites:read") {
		t.Errorf("whoami = %d: %s", code, out)
	}

	code, out, _ = runCLI(t, "", "logout")
	if code != 0 || !strings.Contains(out, "Removed the stored token") {
		t.Errorf("logout = %d: %s", code, out)
	}

	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Errorf("credentials still there: %v", err)
	}

	code, _, errOut = runCLI(t, "", "whoami")
	if code != 1 || !strings.Contains(errOut, "not logged in") {
		t.Errorf("whoami after logout = %d: %s", code, errOut)
	}
}

func TestLoginWithToken(t *testing.T) {
	path := loginEnv(t)
	srv := fakePanel(t)

	// A wrong token is checked before it is stored.
	code, _, errOut := runCLI(t, "hst_bad\n", "login", "-token", srv.URL)
	if code != 1 || !strings.Contains(errOut, "does not work") {
		t.Errorf("bad token = %d: %s", code, errOut)
	}

	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Errorf("bad token stored: %v", err)
	}

	if code, _, errOut = runCLI(t, "\n", "login", "-token", srv.URL); code != 1 || !strings.Contains(errOut, "no token") {
		t.Errorf("empty stdin = %d: %s", code, errOut)
	}

	// HOSTASHI_URL picks the panel when no URL is given.
	t.Setenv("HOSTASHI_URL", srv.URL)

	if code, _, errOut = runCLI(t, "hst_good\n", "login", "-token"); code != 0 {
		t.Fatalf("good token = %d: %s", code, errOut)
	}

	if fi, err := os.Stat(path); err != nil || fi.Mode().Perm() != 0o600 {
		t.Errorf("credentials: %v, %v", fi, err)
	}
}

func TestLoginUsage(t *testing.T) {
	loginEnv(t)

	cases := [][]string{
		{"login", "a", "b"},
		{"login", "-nope"},
		{"logout", "x"},
		{"whoami", "x"},
	}
	for _, args := range cases {
		if code, _, _ := runCLI(t, "", args...); code != 2 {
			t.Errorf("%v = %d, want 2", args, code)
		}
	}

	// A token must not go to a remote panel over plain http.
	if code, _, errOut := runCLI(t, "hst_good\n", "login", "-token", "http://panel.example.com"); code != 1 || !strings.Contains(errOut, "plain http") {
		t.Errorf("plain http = %d: %s", code, errOut)
	}
}
