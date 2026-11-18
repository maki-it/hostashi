// SPDX-License-Identifier: AGPL-3.0-or-later

package serve

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/maki-it/hostashi/internal/store"
)

func TestCheckEUID(t *testing.T) {
	if err := CheckEUID(0); !errors.Is(err, ErrRoot) {
		t.Fatalf("root accepted: %v", err)
	}

	if err := CheckEUID(998); err != nil {
		t.Fatal(err)
	}
}

func TestCheckListen(t *testing.T) {
	ok := []string{"127.0.0.1:8080", "[::1]:8080", "localhost:8080", "127.0.0.2:1"}
	bad := []string{"0.0.0.0:8080", ":8080", "[::]:8080", "192.168.1.10:8080", "example.com:80", "127.0.0.1", "127.0.0.1:", ""}

	for _, a := range ok {
		if err := CheckListen(a); err != nil {
			t.Errorf("%q rejected: %v", a, err)
		}
	}

	for _, a := range bad {
		if err := CheckListen(a); err == nil {
			t.Errorf("%q accepted", a)
		}
	}
}

func TestHealth(t *testing.T) {
	up := func(context.Context) error { return nil }
	agentDown := func(context.Context) error {
		return errors.New("dial unix /run/hostashi/agent.sock: connection refused")
	}
	dbDown := func(context.Context) error {
		return errors.New("open /var/lib/hostashi/hostashi.db: disk I/O error")
	}

	cases := []struct {
		name              string
		deps              Deps
		code              int
		status, agent, db string
	}{
		{"all up", Deps{up, up}, http.StatusOK, "ok", "ok", "ok"},
		{"agent down", Deps{agentDown, up}, http.StatusServiceUnavailable, "degraded", "unavailable", "ok"},
		{"db down", Deps{up, dbDown}, http.StatusServiceUnavailable, "degraded", "ok", "unavailable"},
		{"both down", Deps{agentDown, dbDown}, http.StatusServiceUnavailable, "degraded", "unavailable", "unavailable"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rec := httptest.NewRecorder()
			Handler(Config{Version: "dev"}, tc.deps).ServeHTTP(rec, httptest.NewRequest("GET", "/api/v1/health", nil))

			if rec.Code != tc.code {
				t.Fatalf("code %d", rec.Code)
			}

			if strings.Contains(rec.Body.String(), "/run/") || strings.Contains(rec.Body.String(), "/var/") {
				t.Fatalf("internal detail leaked: %s", rec.Body)
			}

			var body struct {
				Status string `json:"status"`
				Agent  string `json:"agent"`
				DB     string `json:"db"`
			}
			if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
				t.Fatalf("body %s, %v", rec.Body, err)
			}

			if body.Status != tc.status || body.Agent != tc.agent || body.DB != tc.db {
				t.Fatalf("got %+v", body)
			}
		})
	}
}

func TestUnknownRoutesAndMethods(t *testing.T) {
	ok := func(context.Context) error { return nil }
	h := Handler(Config{}, Deps{ok, ok})

	for _, tc := range []struct {
		method, path string
		code         int
	}{
		{"POST", "/api/v1/health", http.StatusMethodNotAllowed},
		{"GET", "/api/v1/agent/exec", http.StatusNotFound},
		{"GET", "/", http.StatusNotFound},
	} {
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, httptest.NewRequest(tc.method, tc.path, nil))

		if rec.Code != tc.code {
			t.Errorf("%s %s: %d, want %d", tc.method, tc.path, rec.Code, tc.code)
		}
	}
}

func TestRunRejectsPublicAddress(t *testing.T) {
	if err := Run(context.Background(), Config{Listen: "0.0.0.0:0"}); err == nil {
		t.Fatal("public address accepted")
	}
}

func TestRunRejectsBadDatabase(t *testing.T) {
	dir := t.TempDir()
	if err := os.Chmod(dir, 0o777); err != nil {
		t.Fatal(err)
	}

	err := Run(context.Background(), Config{Listen: "127.0.0.1:0", DBPath: filepath.Join(dir, "h.db")})
	if !errors.Is(err, store.ErrInsecurePermissions) {
		t.Fatalf("want ErrInsecurePermissions, got %v", err)
	}
}
