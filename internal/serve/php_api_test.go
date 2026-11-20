// SPDX-License-Identifier: AGPL-3.0-or-later

package serve

import (
	"context"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/maki-it/hostashi/internal/agentapi"
	"github.com/maki-it/hostashi/internal/agentrpc"
)

type stubPHP struct {
	mu      sync.Mutex
	started []string
	status  agentapi.PHPInstallStatus
	refuse  bool
}

func (s *stubPHP) Versions(context.Context) (agentapi.PHPVersionsResponse, error) {
	return agentapi.PHPVersionsResponse{Installed: []string{"8.4"}, Repository: true, Newer: []string{"9.9"}}, nil
}

func (s *stubPHP) Install(_ context.Context, v string) (agentapi.PHPInstallStatus, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.refuse {
		return agentapi.PHPInstallStatus{}, &agentrpc.Error{Code: agentrpc.CodePrecondition, Message: "phpinstall: an installation is already running"}
	}

	s.started = append(s.started, v)
	s.status = agentapi.PHPInstallStatus{Version: v, State: agentapi.PHPInstallRunning}

	return s.status, nil
}

func (s *stubPHP) Status(context.Context) (agentapi.PHPInstallStatus, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.status.State == "" {
		return agentapi.PHPInstallStatus{State: agentapi.PHPInstallIdle}, nil
	}

	return s.status, nil
}

func TestPHPInstallAPI(t *testing.T) {
	e := newAPI(t)
	c := e.adminCookie(t)
	js := map[string]string{"Content-Type": "application/json"}
	post := func(body string) (int, string) {
		rec := e.do(t, reqOpts{method: "POST", path: "/api/v1/php/install", body: body, cookie: c, header: js})
		if rec.Code >= 400 {
			return rec.Code, errCode(t, rec)
		}

		return rec.Code, ""
	}

	rec := e.do(t, reqOpts{method: "GET", path: "/api/v1/php/install", cookie: c})
	if rec.Code != http.StatusOK {
		t.Fatalf("status: %d %s", rec.Code, rec.Body)
	}

	var got phpInstallJSON
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil || got.State != agentapi.PHPInstallIdle || !got.Repository ||
		!slices.Equal(got.Newer, []string{"9.9"}) || got.Log == nil {
		t.Fatalf("status: %+v %v", got, err)
	}

	for _, body := range []string{`{"version":"8.0"}`, `{"version":"9.9"}`, `{"version":"../8.1"}`, `{}`} {
		if code, _ := post(body); code != http.StatusUnprocessableEntity {
			t.Errorf("%s: %d", body, code)
		}
	}

	// 8.1 has no security fixes since 2025-12-31: only with confirmation.
	if code, ec := post(`{"version":"8.1"}`); code != http.StatusConflict || ec != "php_eol" {
		t.Errorf("EOL without confirmation: %d %s", code, ec)
	}

	if code, _ := post(`{"version":"8.1","confirmEol":true}`); code != http.StatusAccepted {
		t.Errorf("EOL with confirmation: %d", code)
	}

	if code, _ := post(`{"version":"8.3"}`); code != http.StatusAccepted {
		t.Errorf("supported version: %d", code)
	}

	if !slices.Equal(e.php.started, []string{"8.1", "8.3"}) {
		t.Errorf("started %v", e.php.started)
	}

	e.php.refuse = true

	if code, ec := post(`{"version":"8.2"}`); code != http.StatusConflict || ec != "php_install_refused" {
		t.Errorf("busy: %d %s", code, ec)
	}

	var n int
	if err := e.st.RawCount(t.Context(), `SELECT count(*) FROM audit_log WHERE action = ?`, "php.install", &n); err != nil || n != 2 {
		t.Errorf("audit: %d %v", n, err)
	}

	// Tokens need php:install to install; sites:read reads the status.
	read := e.createToken(t, c, `{"name":"r","scopes":["sites:read"],"expiresInDays":1}`)
	if rec := e.do(t, reqOpts{method: "GET", path: "/api/v1/php/install", header: bearerHeader(read.Token)}); rec.Code != http.StatusOK {
		t.Errorf("status with sites:read: %d", rec.Code)
	}

	h := bearerHeader(read.Token)
	h["Content-Type"] = "application/json"

	if rec := e.do(t, reqOpts{method: "POST", path: "/api/v1/php/install", body: `{"version":"8.3"}`, header: h}); rec.Code != http.StatusForbidden {
		t.Errorf("install with sites:read: %d", rec.Code)
	}
}

func TestPHPEOL(t *testing.T) {
	day := func(s string) time.Time {
		d, err := time.Parse(time.DateOnly, s)
		if err != nil {
			t.Fatal(err)
		}

		return d.Add(12 * time.Hour)
	}

	for _, tc := range []struct {
		v, day string
		eol    bool
	}{
		{"8.2", "2026-12-31", false},
		{"8.2", "2027-01-01", true},
		{"7.4", "2026-11-20", true},
		{"8.5", "2026-11-20", false},
		{"9.9", "2026-11-20", true},
	} {
		if got := phpEOL(tc.v, day(tc.day)); got != tc.eol {
			t.Errorf("%s on %s: %v", tc.v, tc.day, got)
		}
	}
}

func TestDistroPHP(t *testing.T) {
	dir := t.TempDir()
	old := osReleasePath

	t.Cleanup(func() { osReleasePath = old })

	for _, tc := range []struct{ release, want string }{
		{"ID=debian\nVERSION_ID=\"13\"\n", "8.4"},
		{"NAME=\"Ubuntu\"\nID=ubuntu\nVERSION_ID=\"26.04\"\n", "8.5"},
		{"ID=ubuntu\nVERSION_ID=\"24.04\"\n", ""},
		{"", ""},
	} {
		osReleasePath = filepath.Join(dir, "os-release")
		if err := os.WriteFile(osReleasePath, []byte(tc.release), 0o600); err != nil {
			t.Fatal(err)
		}

		if got := distroPHP(); got != tc.want {
			t.Errorf("%q: got %q, want %q", tc.release, got, tc.want)
		}
	}

	osReleasePath = filepath.Join(dir, "missing")

	if got := distroPHP(); got != "" {
		t.Errorf("missing file: got %q", got)
	}
}
