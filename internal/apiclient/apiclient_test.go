// SPDX-License-Identifier: AGPL-3.0-or-later

package apiclient

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestCheckURL(t *testing.T) {
	ok := map[string]string{
		"https://panel.example.com":       "https://panel.example.com",
		"https://panel.example.com:8443/": "https://panel.example.com:8443",
		"http://127.0.0.1:8080":           "http://127.0.0.1:8080",
		"http://localhost:8080":           "http://localhost:8080",
		"http://[::1]:8080":               "http://[::1]:8080",
	}
	for in, want := range ok {
		if got, err := CheckURL(in); err != nil || got != want {
			t.Errorf("CheckURL(%q) = %q, %v", in, got, err)
		}
	}

	for _, bad := range []string{"", "panel.example.com", "ftp://x", "http://panel.example.com", "https://u:p@x", "https://x/api", "https://x?a=1"} {
		if _, err := CheckURL(bad); err == nil {
			t.Errorf("CheckURL(%q) accepted", bad)
		}
	}
}

func TestResolve(t *testing.T) {
	stored := Credentials{URL: "https://a.example.com", Token: "hst_a"}
	env := func(m map[string]string) func(string) string { return func(k string) string { return m[k] } }

	c, err := Resolve(stored, env(nil))
	if err != nil || c != stored {
		t.Errorf("stored: %+v, %v", c, err)
	}

	// Another panel in the environment does not get the stored token.
	c, _ = Resolve(stored, env(map[string]string{EnvURL: "https://b.example.com"}))
	if c.URL != "https://b.example.com" || c.Token != "" {
		t.Errorf("other URL: %+v", c)
	}

	c, _ = Resolve(stored, env(map[string]string{EnvURL: "https://b.example.com", EnvToken: "hst_b"}))
	if c.Token != "hst_b" {
		t.Errorf("env token: %+v", c)
	}

	c, _ = Resolve(Credentials{}, env(nil))
	if c.URL != DefaultURL || c.Token != "" {
		t.Errorf("empty: %+v", c)
	}

	if _, err := Resolve(Credentials{}, env(map[string]string{EnvURL: "http://panel.example.com"})); err == nil {
		t.Error("plain http over the network accepted")
	}
}

func TestCredentialsFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "hostashi", "credentials.json")

	if c, err := LoadCredentials(path); err != nil || c != (Credentials{}) {
		t.Fatalf("missing file: %+v, %v", c, err)
	}

	want := Credentials{URL: "https://panel.example.com", Token: "hst_x"}
	if err := SaveCredentials(path, want); err != nil {
		t.Fatal(err)
	}

	fi, err := os.Stat(path)
	if err != nil || fi.Mode().Perm() != 0o600 {
		t.Fatalf("mode %v, %v", fi.Mode(), err)
	}

	if di, _ := os.Stat(filepath.Dir(path)); di.Mode().Perm() != 0o700 {
		t.Errorf("dir mode %v", di.Mode())
	}

	if c, err := LoadCredentials(path); err != nil || c != want {
		t.Errorf("load: %+v, %v", c, err)
	}

	if err := RemoveCredentials(path); err != nil {
		t.Fatal(err)
	}

	if err := RemoveCredentials(path); err != nil {
		t.Errorf("second remove: %v", err)
	}
}

func writeErr(w http.ResponseWriter, status int, code string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]any{"error": map[string]string{"code": code, "message": code + " message", "requestId": "r1"}})
}

func TestDo(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer hst_x" {
			writeErr(w, http.StatusUnauthorized, "unauthenticated")

			return
		}

		_ = json.NewEncoder(w).Encode(map[string]string{"path": r.URL.Path})
	}))
	defer srv.Close()

	var out struct{ Path string }
	if err := New(srv.URL, "hst_x", "test").Do(context.Background(), http.MethodGet, "/auth/me", nil, &out); err != nil || out.Path != "/api/v1/auth/me" {
		t.Errorf("Do = %+v, %v", out, err)
	}

	err := New(srv.URL, "hst_bad", "test").Do(context.Background(), http.MethodGet, "/auth/me", nil, &out)

	var e *Error
	if !errors.As(err, &e) || e.Status != 401 || e.Code != "unauthenticated" || e.RequestID != "r1" {
		t.Errorf("error = %#v", err)
	}
}

func TestDeviceLogin(t *testing.T) {
	polls := 0

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var in map[string]string

		_ = json.NewDecoder(r.Body).Decode(&in)

		switch r.URL.Path {
		case "/api/v1/auth/device":
			if in["client"] != "cli on test" {
				t.Errorf("client %q", in["client"])
			}

			w.WriteHeader(http.StatusCreated)
			_ = json.NewEncoder(w).Encode(DeviceStart{DeviceCode: "dc", UserCode: "WDJB-MJHT", VerificationURIComplete: "https://x/#/device?code=WDJB-MJHT", ExpiresIn: 600, Interval: 5})
		case "/api/v1/auth/device/token":
			if in["deviceCode"] != "dc" {
				t.Errorf("device code %q", in["deviceCode"])
			}

			polls++

			switch polls {
			case 1:
				writeErr(w, http.StatusBadRequest, "authorization_pending")
			case 2:
				writeErr(w, http.StatusBadRequest, "slow_down")
			default:
				_ = json.NewEncoder(w).Encode(NewToken{Token: "hst_new", Name: "cli on test"})
			}
		}
	}))
	defer srv.Close()

	var shown DeviceStart

	var waits []time.Duration

	nt, err := New(srv.URL, "", "test").DeviceLogin(context.Background(), "cli on test",
		func(st DeviceStart) { shown = st },
		func(_ context.Context, d time.Duration) error { waits = append(waits, d); return nil })
	if err != nil || nt.Token != "hst_new" {
		t.Fatalf("token = %+v, %v", nt, err)
	}

	if shown.UserCode != "WDJB-MJHT" {
		t.Errorf("shown %+v", shown)
	}

	// slow_down adds five seconds (RFC 8628 3.5).
	if len(waits) != 3 || waits[0] != 5*time.Second || waits[2] != 10*time.Second {
		t.Errorf("waits %v", waits)
	}
}

func TestDeviceLoginEnds(t *testing.T) {
	for code, want := range map[string]error{"access_denied": ErrDenied, "expired_token": ErrExpired} {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path == "/api/v1/auth/device" {
				_ = json.NewEncoder(w).Encode(DeviceStart{DeviceCode: "dc", Interval: 5})

				return
			}

			writeErr(w, http.StatusBadRequest, code)
		}))

		_, err := New(srv.URL, "", "test").DeviceLogin(context.Background(), "cli", func(DeviceStart) {},
			func(context.Context, time.Duration) error { return nil })
		if !errors.Is(err, want) {
			t.Errorf("%s: %v", code, err)
		}

		srv.Close()
	}

	// Ctrl-C while waiting ends the login.
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	if err := Sleep(ctx, time.Hour); !errors.Is(err, context.Canceled) {
		t.Errorf("sleep: %v", err)
	}
}
