// SPDX-License-Identifier: AGPL-3.0-or-later

package serve

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/maki-it/hostashi/internal/agentapi"
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
	up := func(context.Context) (agentapi.PingResponse, error) { return agentapi.PingResponse{}, nil }
	down := func(context.Context) (agentapi.PingResponse, error) {
		return agentapi.PingResponse{}, errors.New("dial unix /run/hostashi/agent.sock: connection refused")
	}

	cases := []struct {
		name   string
		ping   pinger
		code   int
		status string
	}{
		{"agent up", up, http.StatusOK, "ok"},
		{"agent down", down, http.StatusServiceUnavailable, "degraded"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rec := httptest.NewRecorder()
			Handler(Config{Version: "dev"}, tc.ping).ServeHTTP(rec, httptest.NewRequest("GET", "/api/v1/health", nil))

			if rec.Code != tc.code {
				t.Fatalf("code %d", rec.Code)
			}

			if strings.Contains(rec.Body.String(), "/run/") {
				t.Fatalf("internal detail leaked: %s", rec.Body)
			}

			var body struct {
				Status string `json:"status"`
			}
			if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil || body.Status != tc.status {
				t.Fatalf("body %s, %v", rec.Body, err)
			}
		})
	}
}

func TestUnknownRoutesAndMethods(t *testing.T) {
	h := Handler(Config{}, func(context.Context) (agentapi.PingResponse, error) { return agentapi.PingResponse{}, nil })

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
