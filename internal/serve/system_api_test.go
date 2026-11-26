// SPDX-License-Identifier: AGPL-3.0-or-later

package serve

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"sync"
	"testing"

	"github.com/maki-it/hostashi/internal/agentapi"
	"github.com/maki-it/hostashi/internal/agentrpc"
	"github.com/maki-it/hostashi/internal/store"
)

type stubSystem struct {
	mu      sync.Mutex
	info    agentapi.SystemInfo
	err     error
	reboots int
	sets    []string
}

func newStubSystem() *stubSystem {
	return &stubSystem{info: agentapi.SystemInfo{
		Hostname: "web2", Transient: "web2", Timezone: "UTC", Time: "2026-11-22T12:00:00Z",
		UptimeSeconds: 3600, RebootRequired: true, RebootPackages: []string{"linux-image-6.12"},
	}}
}

func (s *stubSystem) Info(context.Context) (agentapi.SystemInfo, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	return s.info, nil
}

func (s *stubSystem) Timezones(context.Context) (agentapi.SystemTimezones, error) {
	return agentapi.SystemTimezones{Zones: []string{"Europe/Berlin", "UTC"}}, nil
}

func (s *stubSystem) SetTimezone(_ context.Context, zone string) (agentapi.SystemInfo, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.sets = append(s.sets, "tz "+zone)
	if s.err != nil {
		return agentapi.SystemInfo{}, s.err
	}

	s.info.Timezone = zone

	return s.info, nil
}

func (s *stubSystem) SetHostname(_ context.Context, name string) (agentapi.SystemInfo, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.sets = append(s.sets, "host "+name)
	if s.err != nil {
		return agentapi.SystemInfo{}, s.err
	}

	s.info.Hostname, s.info.Transient = name, name

	return s.info, nil
}

func (s *stubSystem) SetAutoUpdate(_ context.Context, on bool) (agentapi.SystemInfo, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.err != nil {
		return agentapi.SystemInfo{}, s.err
	}

	s.info.Updates = &agentapi.SystemUpdates{Installed: "0.13.0", Auto: on}

	return s.info, nil
}

func (s *stubSystem) Reboot(context.Context) (agentapi.SystemRebootResponse, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.err != nil {
		return agentapi.SystemRebootResponse{}, s.err
	}

	s.reboots++

	return agentapi.SystemRebootResponse{DelaySeconds: 2}, nil
}

func (e *apiEnv) auditCount(t *testing.T, action string) int {
	t.Helper()

	var n int
	if err := e.st.RawCount(t.Context(), `SELECT COUNT(*) FROM audit_log WHERE action = ?`, action, &n); err != nil {
		t.Fatal(err)
	}

	return n
}

// auditEntry is the newest entry of action.
func (e *apiEnv) auditEntry(t *testing.T, action string) store.AuditEntry {
	t.Helper()

	entries, _, err := e.st.AuditEntries(t.Context(), store.AuditFilter{Action: action}, 0, 1)
	if err != nil || len(entries) != 1 {
		t.Fatalf("audit %s: %v %v", action, entries, err)
	}

	return entries[0]
}

func TestSystemAPI(t *testing.T) {
	e := newAPI(t)
	c := e.adminCookie(t)

	if rec := e.do(t, reqOpts{method: "GET", path: "/api/v1/system"}); rec.Code != http.StatusUnauthorized {
		t.Fatalf("anonymous: %d", rec.Code)
	}

	rec := e.do(t, reqOpts{method: "GET", path: "/api/v1/system", cookie: c})

	var info agentapi.SystemInfo
	if rec.Code != http.StatusOK || json.Unmarshal(rec.Body.Bytes(), &info) != nil || info.Hostname != "web2" || !info.RebootRequired {
		t.Fatalf("info: %d %s", rec.Code, rec.Body)
	}

	rec = e.do(t, reqOpts{method: "GET", path: "/api/v1/system/timezones", cookie: c})
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "Europe/Berlin") {
		t.Fatalf("zones: %d %s", rec.Code, rec.Body)
	}

	// Refused before the agent sees it.
	for _, tc := range []struct{ method, path, body string }{
		{"PUT", "/api/v1/system/timezone", `{"zone":"../../etc/passwd"}`},
		{"PUT", "/api/v1/system/timezone", `{"zone":""}`},
		{"PUT", "/api/v1/system/timezone", `{"zone":"UTC","x":1}`},
		{"PUT", "/api/v1/system/hostname", `{"hostname":"Web2"}`},
		{"PUT", "/api/v1/system/hostname", `{"hostname":"web2;reboot"}`},
		{"POST", "/api/v1/system/reboot", `{}`},
		{"POST", "/api/v1/system/reboot", `{"confirm":"WEB2"}`},
		{"POST", "/api/v1/system/reboot", `{"confirm":"web3"}`},
	} {
		if rec := e.do(t, reqOpts{method: tc.method, path: tc.path, body: tc.body, cookie: c}); rec.Code != http.StatusBadRequest {
			t.Errorf("%s %s: %d %s", tc.path, tc.body, rec.Code, rec.Body)
		}
	}

	if len(e.system.sets) != 0 || e.system.reboots != 0 {
		t.Fatalf("agent reached: %v, %d reboots", e.system.sets, e.system.reboots)
	}

	rec = e.do(t, reqOpts{method: "PUT", path: "/api/v1/system/timezone", body: `{"zone":"Europe/Berlin"}`, cookie: c})
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"timezone":"Europe/Berlin"`) {
		t.Fatalf("set zone: %d %s", rec.Code, rec.Body)
	}

	rec = e.do(t, reqOpts{method: "PUT", path: "/api/v1/system/hostname", body: `{"hostname":"web3.example.org"}`, cookie: c})
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"hostname":"web3.example.org"`) {
		t.Fatalf("set hostname: %d %s", rec.Code, rec.Body)
	}

	diff := string(e.auditEntry(t, "system.hostname").Diff)

	if !strings.Contains(diff, "web2") || !strings.Contains(diff, "web3.example.org") {
		t.Fatalf("hostname diff %s", diff)
	}

	rec = e.do(t, reqOpts{method: "PUT", path: "/api/v1/system/auto-update", body: `{"on":true}`, cookie: c})
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"auto":true`) {
		t.Fatalf("auto-update: %d %s", rec.Code, rec.Body)
	}

	if diff := string(e.auditEntry(t, "system.auto_update").Diff); !strings.Contains(diff, "off") || !strings.Contains(diff, "on") {
		t.Fatalf("auto-update diff %s", diff)
	}

	if rec := e.do(t, reqOpts{method: "PUT", path: "/api/v1/system/auto-update", body: `{"on":"yes"}`, cookie: c}); rec.Code != http.StatusBadRequest {
		t.Fatalf("auto-update with a string: %d", rec.Code)
	}

	// The confirmation is the hostname as it is now.
	if rec := e.do(t, reqOpts{method: "POST", path: "/api/v1/system/reboot", body: `{"confirm":"web2"}`, cookie: c}); rec.Code != http.StatusBadRequest {
		t.Fatalf("old hostname confirmed: %d", rec.Code)
	}

	rec = e.do(t, reqOpts{method: "POST", path: "/api/v1/system/reboot", body: `{"confirm":"web3.example.org"}`, cookie: c})
	if rec.Code != http.StatusAccepted || e.system.reboots != 1 {
		t.Fatalf("reboot: %d %s", rec.Code, rec.Body)
	}

	if n := e.auditCount(t, "system.reboot"); n != 1 {
		t.Fatalf("reboot audit rows %d", n)
	}

	detail := string(e.auditEntry(t, "system.reboot").Detail)

	if !strings.Contains(detail, `"reboot_required":"yes"`) || !strings.Contains(detail, "linux-image-6.12") {
		t.Fatalf("reboot detail %s", detail)
	}

	// Agent errors map to HTTP codes and are audited.
	e.system.err = &agentrpc.Error{Code: agentrpc.CodePrecondition, Message: "the D-Bus system bus is not running"}
	if rec := e.do(t, reqOpts{method: "PUT", path: "/api/v1/system/timezone", body: `{"zone":"UTC"}`, cookie: c}); rec.Code != http.StatusConflict {
		t.Fatalf("no bus: %d %s", rec.Code, rec.Body)
	}

	e.system.err = &agentrpc.Error{Code: agentrpc.CodeConflict, Message: "the server is already rebooting"}
	if rec := e.do(t, reqOpts{method: "POST", path: "/api/v1/system/reboot", body: `{"confirm":"web3.example.org"}`, cookie: c}); rec.Code != http.StatusConflict {
		t.Fatalf("second reboot: %d %s", rec.Code, rec.Body)
	}

	if n := e.auditCount(t, "system.timezone"); n != 2 {
		t.Fatalf("timezone audit rows %d", n)
	}

	if n := e.auditCount(t, "system.reboot"); n != 3 {
		t.Fatalf("reboot audit rows after the error %d", n)
	}
}
